package keeper

import (
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func TestNativeConsensusScheduleSurvivesRealLevelDBRestart(t *testing.T) {
	directory := t.TempDir()
	open := func() (sdk.Context, store.CommitMultiStore, dbm.DB, *StateKeeper) {
		database, err := dbm.NewDB("native-consensus", dbm.GoLevelDBBackend, directory)
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
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators))
	require.ErrorContains(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators), "already initialized")
	first, second := g.Validators[0], g.Validators[1]
	first.Stake = 150
	second.Jailed = true
	require.NoError(t, k.SetValidator(ctx, first))
	require.NoError(t, k.SetValidator(ctx, second))
	eligible := []string{first.OperatorAddress, second.OperatorAddress}
	ctx = transitionContext(t, ctx, 1, g.Validators)
	updates, err := k.ScheduledEndBlock(ctx, eligible)
	require.NoError(t, err)
	require.Len(t, updates, 2)
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, before.ScheduledValidatorSets[0].EffectiveHeight)
	require.EqualValues(t, 3, before.ScheduledValidatorSets[1].EffectiveHeight)
	cms.Commit()
	require.NoError(t, database.Close())
	ctx, cms, database, k = open()
	restored, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, restored)
	ctx = transitionContext(t, ctx, 2, g.Validators)
	updates, err = k.ScheduledEndBlock(ctx, eligible)
	require.NoError(t, err)
	require.Empty(t, updates)
	cms.Commit()
	require.NoError(t, database.Close())
	ctx, cms, database, k = open()
	defer func() { require.NoError(t, database.Close()) }()
	// H3 uses saved new power even though H2 still used the old effective set.
	h2, err := k.GetHistoricalInfo(ctx, 2)
	require.NoError(t, err)
	old, err := ConsensusSet(g.Validators)
	require.NoError(t, err)
	require.Equal(t, old.Hash(), h2.Header.ValidatorsHash)
	next := []types.Validator{first}
	for height := int64(3); height <= 8; height++ {
		ctx = transitionContext(t, ctx, height, next)
		updates, err = k.ScheduledEndBlock(ctx, eligible)
		require.NoError(t, err)
		require.Empty(t, updates)
		cms.Commit()
	}
	final, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, final.ScheduledValidatorSets, 2)
	require.EqualValues(t, 9, final.ScheduledValidatorSets[0].EffectiveHeight)
	require.EqualValues(t, 10, final.ScheduledValidatorSets[1].EffectiveHeight)
	historyCount := 0
	require.NoError(t, k.scan(ctx, historyPrefix, func([]byte, []byte) error { historyCount++; return nil }))
	require.EqualValues(t, g.Params.HistoricalEntries, historyCount)
	// A genesis export/import carries the delayed pipeline, not merely current RS.
	fresh, _, freshKeeper := environment(t)
	require.NoError(t, freshKeeper.InitGenesis(fresh, final))
	again, err := freshKeeper.ExportGenesis(fresh)
	require.NoError(t, err)
	assertGenesisBytes(t, final, again)
}

func TestNativeScheduledTransitionFailureDiscardsPowerHistoryAndQueue(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators))
	changed := g.Validators[0]
	changed.Stake = 150
	require.NoError(t, k.SetValidator(ctx, changed))
	ctx = transitionContext(t, ctx, 1, g.Validators)
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	bad, err := NewStateKeeper(failingService{service: service, prefix: schedulePrefix})
	require.NoError(t, err)
	_, err = bad.ScheduledEndBlock(ctx, []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress})
	require.ErrorContains(t, err, "injected write failure")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	count := 0
	require.NoError(t, k.scan(ctx, historyPrefix, func([]byte, []byte) error { count++; return nil }))
	require.Zero(t, count)
	_, err = k.ScheduledEndBlock(transitionContext(t, ctx, 2, g.Validators), nil)
	require.ErrorContains(t, err, "current height")
}

func TestNativeScheduleGenesisRejectsMalformedPipelineAtomically(t *testing.T) {
	ctx, _, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators))
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	for _, mode := range []string{"gap", "single", "empty", "identity", "order"} {
		t.Run(mode, func(t *testing.T) {
			data, err := proto.Marshal(exported)
			require.NoError(t, err)
			var bad types.GenesisState
			require.NoError(t, proto.Unmarshal(data, &bad))
			switch mode {
			case "gap":
				bad.ScheduledValidatorSets[1].EffectiveHeight++
			case "single":
				bad.ScheduledValidatorSets = bad.ScheduledValidatorSets[:1]
			case "empty":
				bad.ScheduledValidatorSets[0].Validators = nil
			case "identity":
				bad.ScheduledValidatorSets[0].Validators[0].ConsensusPubkey = validator(t, 77, 1).ConsensusPubkey
			case "order":
				vals := bad.ScheduledValidatorSets[0].Validators
				vals[0], vals[1] = vals[1], vals[0]
			}
			fresh, service, native := environment(t)
			require.Error(t, native.InitGenesis(fresh, &bad))
			it, err := service.OpenKVStore(fresh).Iterator(nil, nil)
			require.NoError(t, err)
			require.False(t, it.Valid())
			require.NoError(t, it.Close())
		})
	}
	fresh, service, _ := environment(t)
	bad, err := NewStateKeeper(failingService{service: service, prefix: schedulePrefix})
	require.NoError(t, err)
	require.ErrorContains(t, bad.InitGenesis(fresh, exported), "injected write failure")
	it, err := service.OpenKVStore(fresh).Iterator(nil, nil)
	require.NoError(t, err)
	require.False(t, it.Valid())
	require.NoError(t, it.Close())
}

func assertGenesisBytes(t *testing.T, want, actual *types.GenesisState) {
	t.Helper()
	a, err := proto.Marshal(want)
	require.NoError(t, err)
	b, err := proto.Marshal(actual)
	require.NoError(t, err)
	require.Equal(t, a, b)
}
