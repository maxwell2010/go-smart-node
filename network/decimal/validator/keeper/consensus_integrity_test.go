package keeper

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func TestConsensusMembershipIntegrityFailsClosedWithoutHotRepair(t *testing.T) {
	for _, mode := range []string{"bonded count", "bonded total", "bonded overflow", "bonded payload", "missing bonded", "positive value", "missing positive", "missing total", "raw power identity", "extra positive"} {
		t.Run(mode, func(t *testing.T) {
			ctx, service, k := environment(t)
			g := genesis(t)
			require.NoError(t, k.InitGenesis(ctx, g))
			require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators))
			canonical, err := k.ExportGenesis(ctx)
			require.NoError(t, err)
			state := service.OpenKVStore(ctx)
			bondedKey, _ := operatorKey(bondedIndexPrefix, g.Validators[0].OperatorAddress)
			positiveKey, _ := operatorKey(positivePowerPrefix, g.Validators[0].OperatorAddress)
			rebuildable := true
			switch mode {
			case "bonded count":
				require.NoError(t, state.Set([]byte{bondedTotalsKey}, bondedTotalsData(3, 300)))
			case "bonded total":
				require.NoError(t, state.Set([]byte{bondedTotalsKey}, bondedTotalsData(2, 301)))
			case "bonded overflow":
				require.NoError(t, state.Set([]byte{bondedTotalsKey}, bondedTotalsData(math.MaxUint64, 0)))
			case "bonded payload":
				require.NoError(t, state.Set(bondedKey, []byte("invalid operator text")))
			case "missing bonded":
				require.NoError(t, state.Delete(bondedKey))
			case "positive value":
				value := make([]byte, 8)
				binary.BigEndian.PutUint64(value, 101)
				require.NoError(t, state.Set(positiveKey, value))
			case "missing positive":
				require.NoError(t, state.Delete(positiveKey))
			case "missing total":
				require.NoError(t, state.Delete([]byte{3}))
				rebuildable = false
			case "raw power identity":
				key, _ := operatorKey(powerPrefix, g.Validators[0].OperatorAddress)
				require.NoError(t, k.put(ctx, key, &types.LastValidatorPower{Address: g.Validators[1].OperatorAddress, Power: 100}))
				rebuildable = false
			case "extra positive":
				key, _ := operatorKey(positivePowerPrefix, validator(t, 99, 1).OperatorAddress)
				value := make([]byte, 8)
				binary.BigEndian.PutUint64(value, 1)
				require.NoError(t, state.Set(key, value))
			}
			before := nativeStoreSnapshot(t, service, ctx)
			_, err = k.ExportGenesis(ctx)
			require.Error(t, err, "cold audit must reject corrupted derived or source state")
			hot, err := NewStateKeeper(pointRegistryService{service})
			require.NoError(t, err)
			_, err = hot.ScheduledEndBlock(transitionContext(t, ctx, 1, g.Validators), []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress})
			require.Error(t, err, "block execution must not repair indexes implicitly")
			require.Equal(t, before, nativeStoreSnapshot(t, service, ctx))
			err = k.RebuildConsensusIndexes(ctx)
			if rebuildable {
				require.NoError(t, err)
				after, err := k.ExportGenesis(ctx)
				require.NoError(t, err)
				assertGenesisBytes(t, canonical, after)
			} else {
				require.Error(t, err, "invalid source ledger cannot be repaired by an index rebuild")
				require.Equal(t, before, nativeStoreSnapshot(t, service, ctx))
			}
		})
	}
}

func TestConsensusMembershipAndUppercaseIdentitySurviveIAVLRestart(t *testing.T) {
	directory := t.TempDir()
	open := func() (sdk.Context, store.CommitMultiStore, dbm.DB, *StateKeeper) {
		database, err := dbm.NewDB("membership-restart", dbm.GoLevelDBBackend, directory)
		require.NoError(t, err)
		cms := store.NewCommitMultiStore(database, log.NewNopLogger(), metrics.NewNoOpMetrics())
		key := storetypes.NewKVStoreKey("native-validator")
		cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, database)
		require.NoError(t, cms.LoadLatestVersion())
		k, err := NewStateKeeper(runtime.NewKVStoreService(key))
		require.NoError(t, err)
		return sdk.NewContext(cms, cmtproto.Header{}, false, log.NewNopLogger()), cms, database, k
	}
	ctx, cms, database, k := open()
	v := validator(t, 1, 100)
	v.OperatorAddress = strings.ToUpper(v.OperatorAddress)
	g := types.DefaultGenesisState()
	g.Validators = []types.Validator{v}
	g.LastValidatorPowers = []types.LastValidatorPower{{Address: v.OperatorAddress, Power: 100}}
	g.LastTotalPower = 100
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators))
	v.Stake = 150
	require.NoError(t, k.SetValidator(ctx, v))
	before := nativeStoreSnapshot(t, k.service, ctx)
	cms.Commit()
	require.NoError(t, database.Close())
	ctx, cms, database, k = open()
	require.Equal(t, before, nativeStoreSnapshot(t, k.service, ctx))
	previous, err := k.previousPositivePowers(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 100, previous[v.OperatorAddress], "persisted previous power can differ from current RS150")
	updates, err := k.ScheduledEndBlock(transitionContext(t, ctx, 1, g.Validators), []string{v.OperatorAddress})
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.EqualValues(t, 150, updates[0].Power)
	canonical, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	before = nativeStoreSnapshot(t, k.service, ctx)
	cms.Commit()
	require.NoError(t, database.Close())
	ctx, _, database, k = open()
	defer func() { require.NoError(t, database.Close()) }()
	require.Equal(t, before, nativeStoreSnapshot(t, k.service, ctx))
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, canonical, after)
	previous, err = k.previousPositivePowers(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 150, previous[v.OperatorAddress])
	bonded, err := k.bondedValidators(ctx)
	require.NoError(t, err)
	require.Len(t, bonded, 1)
	require.Equal(t, v.OperatorAddress, bonded[0].OperatorAddress)
}
