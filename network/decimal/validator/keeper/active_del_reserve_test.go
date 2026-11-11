package keeper

import (
	"math/big"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func assertActiveDELReserve(t *testing.T, ctx sdk.Context, k *StateKeeper, operator string, expected int64) {
	t.Helper()
	got, err := k.GetActiveDELReserve(ctx, operator)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(expected), got)
}

func TestActiveDELReserveTracksDirectHoldsAndNFTWithoutCountingCompanion(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	position, nft := seedGatewayDELNFTCompanion(t, ctx, k)
	_, operator, err := position.nativePair()
	require.NoError(t, err)
	assertActiveDELReserve(t, ctx, k, operator, 20)
	require.Equal(t, sdkmath.NewInt(20), nft.ReportedReserveAmount)

	position.Amount = sdkmath.NewInt(12)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, position.Amount))
	held := gatewayDELHoldFixture(2000, 2020, 8)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	assertActiveDELReserve(t, ctx, k, operator, 40)
	position.Amount = sdkmath.NewInt(7)
	due := time.Unix(2010, 0).UTC()
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, common.Hash{}, sdkmath.NewInt(5), due))
	assertActiveDELReserve(t, ctx, k, operator, 35)
	ctx = ctx.WithBlockHeight(11).WithBlockTime(due)
	frozen, err := k.GetEVMFrozenBinding(ctx, position.Gateway[:], common.Hash{}.Bytes())
	require.NoError(t, err)
	require.NoError(t, k.ApplyWithdrawalCompletion(ctx, frozen))
	assertActiveDELReserve(t, ctx, k, operator, 35)

	newValidator := inactiveValidator(t, 17)
	require.NoError(t, k.SetValidator(ctx, newValidator))
	assertActiveDELReserve(t, ctx, k, newValidator.OperatorAddress, 0)
	hot, err := NewStateKeeperWithDomain(pointRegistryService{service}, bindingTestChain, 990001)
	require.NoError(t, err)
	assertActiveDELReserve(t, ctx, hot, operator, 35)
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
}

func TestActiveDELReserveOverflowCorruptionAndAtomicRollback(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	first, second := g.Validators[0], g.Validators[1]
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	large := types.Delegation{Delegator: first.RewardAddress, Validator: second.OperatorAddress,
		Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntFromBigInt(maximum)))}
	added := types.Delegation{Delegator: second.RewardAddress, Validator: second.OperatorAddress,
		Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, sdkmath.OneInt()))}
	initial := *g
	initial.Delegations = []types.Delegation{large, added}
	require.ErrorContains(t, k.InitGenesis(ctx, &initial), "active DEL reserve exceeds uint256")
	require.NoError(t, k.InitGenesis(ctx, g), "overflow must leave the validator store empty")
	require.NoError(t, k.SetDelegation(ctx, large))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.ErrorContains(t, k.SetDelegation(ctx, added), "outside uint256 bounds")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	got, err := k.GetActiveDELReserve(ctx, second.OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, maximum, got.BigInt())
	// A full-amount replacement has a safe final total despite both legs being large.
	require.NoError(t, k.ApplyDelegationBatch(ctx, []DelegationChange{{Expected: &large, Next: &large}}))

	broken, err := NewStateKeeper(failingService{service: service, prefix: activeDELReservePrefix})
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyDelegationBatch(ctx, []DelegationChange{{Expected: &large}}), "injected write failure")
	after, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)

	key, err := operatorKey(activeDELReservePrefix, second.OperatorAddress)
	require.NoError(t, err)
	state := service.OpenKVStore(ctx)
	require.NoError(t, state.Set(key, make([]byte, activeDELReserveBytes)))
	require.ErrorContains(t, k.ApplyDelegationBatch(ctx, []DelegationChange{{Expected: &large, Next: &large}}), "underflows the native prestate")
	_, err = k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "differs from native delegations")
	require.NoError(t, state.Delete(key))
	_, err = k.GetActiveDELReserve(ctx, second.OperatorAddress)
	require.ErrorContains(t, err, "missing or invalid active DEL reserve index row")
	_, err = k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "missing or invalid active DEL reserve index row")
}

func TestActiveDELReserveExplicitLegacyBuildSurvivesLevelDBRestart(t *testing.T) {
	directory := t.TempDir()
	open := func() (sdk.Context, store.CommitMultiStore, dbm.DB, *StateKeeper) {
		database, err := dbm.NewDB("active-del-reserve", dbm.GoLevelDBBackend, directory)
		require.NoError(t, err)
		cms := store.NewCommitMultiStore(database, log.NewNopLogger(), metrics.NewNoOpMetrics())
		key := storetypes.NewKVStoreKey("native-validator")
		cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, database)
		require.NoError(t, cms.LoadLatestVersion())
		k, err := NewStateKeeper(runtime.NewKVStoreService(key))
		require.NoError(t, err)
		return sdk.NewContext(cms, cmtproto.Header{}, false, log.NewNopLogger()), cms, database, k
	}
	ctx, cms, db, k := open()
	g := genesis(t) // Active DEL NFT total 100 includes a 40 DEL hold; queues are separate.
	operator := g.Validators[0].OperatorAddress
	custom := types.Delegation{Delegator: g.Validators[1].RewardAddress, Validator: operator,
		Stake: types.NewStakeCoin(sdk.NewCoin("custom", sdkmath.NewInt(80)))}
	g.Delegations = append(g.Delegations, custom)
	require.NoError(t, k.InitGenesis(ctx, g))
	assertActiveDELReserve(t, ctx, k, operator, 100)

	// Recreate a prior initialized store without the new marker and rows.
	state := k.service.OpenKVStore(ctx)
	require.NoError(t, state.Delete([]byte{activeDELReserveMarker}))
	_, err := k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "rows exist without the index marker")
	for _, v := range g.Validators {
		key, err := operatorKey(activeDELReservePrefix, v.OperatorAddress)
		require.NoError(t, err)
		require.NoError(t, state.Delete(key))
	}
	_, err = k.GetActiveDELReserve(ctx, operator)
	require.ErrorIs(t, err, ErrActiveDELReserveIndexUnavailable)
	updated := g.Delegations[0]
	updated.Stake.Stake.Amount = sdkmath.NewInt(120)
	require.NoError(t, k.SetDelegation(ctx, updated), "legacy financial transitions remain usable")
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err, "legacy export remains usable")
	cms.Commit()
	require.NoError(t, db.Close())

	ctx, cms, db, k = open()
	_, err = k.GetActiveDELReserve(ctx, operator)
	require.ErrorIs(t, err, ErrActiveDELReserveIndexUnavailable)
	require.NoError(t, k.BuildActiveDELReserveIndex(ctx))
	require.ErrorContains(t, k.BuildActiveDELReserveIndex(ctx), "already initialized")
	assertActiveDELReserve(t, ctx, k, operator, 120)
	cms.Commit()
	require.NoError(t, db.Close())

	ctx, _, db, k = open()
	defer func() { require.NoError(t, db.Close()) }()
	assertActiveDELReserve(t, ctx, k, operator, 120)
	assertActiveDELReserve(t, ctx, k, g.Validators[1].OperatorAddress, 0)
	updated.Stake.Stake.Amount = sdkmath.NewInt(125)
	require.NoError(t, k.SetDelegation(ctx, updated))
	assertActiveDELReserve(t, ctx, k, operator, 125)
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
}
