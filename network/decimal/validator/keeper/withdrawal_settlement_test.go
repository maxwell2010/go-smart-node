package keeper

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	multistore "cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func withdrawalGenesis(t *testing.T, g *types.GenesisState, id []byte, deadline int64) types.EVMFrozenBinding {
	t.Helper()
	owner, operator := addr(t, identity.Bech32PrefixAccAddr, 2), addr(t, identity.Bech32PrefixValAddr, 1)
	entry := types.UndelegationEntry{CreationHeight: 7, CompletionTime: time.Unix(deadline, 0).UTC(),
		Stake: types.NewStakeCoin(sdk.NewInt64Coin(identity.BaseDenom, 1))}
	hash, err := types.NativeQueueEntryHash(1, owner, operator, "", &entry)
	require.NoError(t, err)
	if len(g.Undelegations) == 0 {
		g.Undelegations = []types.Undelegation{{Delegator: owner, Validator: operator}}
	}
	g.Undelegations[0].Entries = append(g.Undelegations[0].Entries, entry)
	row := types.EVMFrozenBinding{
		Gateway: stateAddress(3), FrozenIndex: id, SourceKind: types.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN,
		SourceStakeID: stateWord(30), SourceValidator: stateAddress(1), Beneficiary: stateAddress(2),
		FreezeType: 1, CompletionTimestamp: stateWord(uint64(deadline)), RawAmount: sdkmath.OneInt(),
		ReportedReserveAmount: sdkmath.ZeroInt(), ReserveAsset: &types.EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom},
		NativeTarget:    &types.NativeStakeTarget{StakeType: types.StakeType_Coin, ID: identity.BaseDenom},
		NativeEntryHash: hash, Active: true,
	}
	g.EVMFrozenBindings = append(g.EVMFrozenBindings, row)
	return row
}

func TestWithdrawalSettlementRetriesWithoutBlockingOthersAcrossIAVLReopen(t *testing.T) {
	dir := t.TempDir()
	open := func() (sdk.Context, multistore.CommitMultiStore, dbm.DB, *StateKeeper) {
		db, err := dbm.NewDB("withdrawal-settlement", dbm.GoLevelDBBackend, dir)
		require.NoError(t, err)
		cms := multistore.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
		key := storetypes.NewKVStoreKey("native-validator")
		cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, db)
		require.NoError(t, cms.LoadLatestVersion())
		k, err := NewStateKeeperWithDomain(runtime.NewKVStoreService(key), bindingTestChain, 990001)
		require.NoError(t, err)
		ctx := sdk.NewContext(cms, cmtproto.Header{ChainID: bindingTestChain, Height: 10, Time: time.Unix(2000, 0)}, false, log.NewNopLogger())
		return ctx, cms, db, k
	}
	ctx, cms, db, k := open()
	g := bindingGenesisState(t, false)
	// The first batch is all failures. The full-width last ID must still
	// run after reopening the committed store.
	for i := 0; i < maxWithdrawalAttempts; i++ {
		withdrawalGenesis(t, g, stateWord(uint64(i)), 2000)
	}
	wideID := stateWord(0)
	wideID[0] = 1
	withdrawalGenesis(t, g, wideID, 2000)
	future := withdrawalGenesis(t, g, stateWord(20), 2002)
	require.NoError(t, k.InitGenesis(ctx, g))

	failMarker := []byte{0xfa}
	attempts := 0
	first, err := k.SettleMatureWithdrawals(ctx, 1_000_000, func(child sdk.Context, row types.EVMFrozenBinding) error {
		attempts++
		require.Equal(t, stateWord(uint64(attempts-1)), row.FrozenIndex)
		require.NoError(t, k.service.OpenKVStore(child).Set(failMarker, []byte{1}))
		child.EventManager().EmitEvent(sdk.NewEvent("failed_withdrawal"))
		require.NoError(t, k.ApplyWithdrawalCompletion(child, row))
		switch attempts {
		case 1:
			// Explicit VM or projection failure after tentative writes.
			return errors.New("withdrawal execution failed")
		case 2:
			child.GasMeter().ConsumeGas(1_000_000, "attempt budget")
		}
		return errors.New("withdrawal projection failed")
	})
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Retried: maxWithdrawalAttempts}, first)
	value, err := k.service.OpenKVStore(ctx).Get(failMarker)
	require.NoError(t, err)
	require.Nil(t, value)
	require.Empty(t, ctx.EventManager().Events())
	cms.Commit()
	require.NoError(t, db.Close())

	ctx, cms, db, k = open()
	defer func() { require.NoError(t, db.Close()) }()
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2001, 0))
	var seen [][]byte
	next, err := k.SettleMatureWithdrawals(ctx, 1_000_000, func(child sdk.Context, row types.EVMFrozenBinding) error {
		seen = append(seen, bytes.Clone(row.FrozenIndex))
		return k.ApplyWithdrawalCompletion(child, row)
	})
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Settled: maxWithdrawalAttempts}, next)
	require.Equal(t, wideID, seen[0])
	require.Equal(t, stateWord(0), seen[1])
	require.Len(t, ctx.EventManager().Events(), maxWithdrawalAttempts)
	require.Equal(t, "decimal_withdrawal_completed", ctx.EventManager().Events()[0].Type)
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, exported.Undelegations, 1)
	require.Len(t, exported.Undelegations[0].Entries, 2)

	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2002, 0))
	last, err := k.SettleMatureWithdrawals(ctx, 1_000_000, func(child sdk.Context, row types.EVMFrozenBinding) error {
		require.NotEqual(t, wideID, row.FrozenIndex, "committed payout must not be tried again")
		return k.ApplyWithdrawalCompletion(child, row)
	})
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Settled: 2}, last)
	row, err := k.GetEVMFrozenBinding(ctx, future.Gateway, future.FrozenIndex)
	require.NoError(t, err)
	require.False(t, row.Active)
	exported, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, exported.Undelegations)
	cms.Commit()
}

func TestWithdrawalSettlementRequiresExactPairDelta(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain).WithBlockHeight(10).WithBlockTime(time.Unix(1999, 0))
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	g := bindingGenesisState(t, false)
	b := withdrawalGenesis(t, g, stateWord(0), 2000)
	pair := &g.Undelegations[0]
	pair.Entries[0].CompletionTime = time.Unix(2000, 500)
	g.EVMFrozenBindings[0].NativeEntryHash, err = types.NativeQueueEntryHash(1, pair.Delegator, pair.Validator, "", &pair.Entries[0])
	require.NoError(t, err)
	require.NoError(t, k.InitGenesis(ctx, g))
	unreachable := func(sdk.Context, types.EVMFrozenBinding) error {
		t.Fatal("the executor must not run before the full native deadline")
		return nil
	}
	before, err := k.SettleMatureWithdrawals(ctx, 1_000_000, unreachable)
	require.NoError(t, err)
	require.Empty(t, before)
	ctx = ctx.WithBlockTime(time.Unix(2000, 0))
	before, err = k.SettleMatureWithdrawals(ctx, 1_000_000, unreachable)
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Retried: 1}, before)

	ctx = ctx.WithBlockHeight(11).WithBlockTime(pair.Entries[0].CompletionTime)
	marker := []byte{0xfa}
	result, err := k.SettleMatureWithdrawals(ctx, 1_000_000, func(child sdk.Context, row types.EVMFrozenBinding) error {
		require.NoError(t, service.OpenKVStore(child).Set(marker, []byte{1}))
		// Metadata-only retirement is insufficient even if the frozen row and
		// all its indexes are otherwise exactly the expected poststate.
		row.Active, row.UpdatedHeight = false, child.BlockHeight()
		plan, err := k.PrepareBindingPlan(child, []BindingMutation{{Frozen: &row}})
		require.NoError(t, err)
		return k.ApplyBindingPlan(child, plan)
	})
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Retried: 1}, result)
	value, err := service.OpenKVStore(ctx).Get(marker)
	require.NoError(t, err)
	require.Nil(t, value)
	current, err := k.GetEVMFrozenBinding(ctx, b.Gateway, b.FrozenIndex)
	require.NoError(t, err)
	require.True(t, current.Active)
	require.Empty(t, ctx.EventManager().Events())
	result, err = k.SettleMatureWithdrawals(ctx, 1_000_000, k.ApplyWithdrawalCompletion)
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Settled: 1}, result)
	require.Len(t, ctx.EventManager().Events(), 1)
}

func TestApplyWithdrawalCompletionKeepsEqualEntriesSeparateAndRejectsReplay(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain).WithBlockHeight(10).WithBlockTime(time.Unix(1999, 0))
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	g := bindingGenesisState(t, false)
	first := withdrawalGenesis(t, g, stateWord(0), 2000)
	wideID := stateWord(0)
	wideID[0] = 1
	second := withdrawalGenesis(t, g, wideID, 2000)
	require.Equal(t, first.NativeEntryHash, second.NativeEntryHash)
	require.NoError(t, k.InitGenesis(ctx, g))
	require.ErrorContains(t, k.ApplyWithdrawalCompletion(ctx, first), "has not matured")
	ctx = ctx.WithBlockTime(time.Unix(2000, 0))
	mismatched := first
	mismatched.SourceStakeID = stateWord(31)
	require.ErrorContains(t, k.ApplyWithdrawalCompletion(ctx, mismatched), "expected preimage")
	require.Empty(t, ctx.EventManager().Events())

	require.NoError(t, k.ApplyWithdrawalCompletion(ctx, first))
	require.ErrorIs(t, k.ApplyWithdrawalCompletion(ctx, first), ErrWithdrawalAlreadyCompleted)
	require.ErrorContains(t, k.ApplyWithdrawalCompletion(ctx, mismatched), "expected identity")
	revision, err := k.BindingRevision(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, revision)
	require.Len(t, ctx.EventManager().Events(), 1)
	remaining, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, remaining.Undelegations, 1)
	require.Len(t, remaining.Undelegations[0].Entries, 1)
	active, err := k.GetEVMFrozenBinding(ctx, second.Gateway, second.FrozenIndex)
	require.NoError(t, err)
	require.True(t, active.Active)

	// Fail the final queue deletion after the tentative binding retirement.
	fault, err := NewStateKeeperWithDomain(failingService{service: service, prefix: undelegationPrefix, deleteOnly: true}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, fault.ApplyWithdrawalCompletion(ctx, second), "injected delete failure")
	unchanged, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, remaining, unchanged)
	require.Len(t, ctx.EventManager().Events(), 1)
	require.NoError(t, k.ApplyWithdrawalCompletion(ctx, second))
	finished, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, finished.Undelegations)
	require.Len(t, ctx.EventManager().Events(), 2)
}

func TestWithdrawalSettlementLowerLimitLeavesLaterDueEntries(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain).WithBlockHeight(10).WithBlockTime(time.Unix(2000, 0))
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	g := bindingGenesisState(t, false)
	for i := range 3 {
		withdrawalGenesis(t, g, stateWord(uint64(i)), 2000)
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	first, err := k.SettleMatureWithdrawalsLimit(ctx, 2, 1_000_000, k.ApplyWithdrawalCompletion)
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Settled: 2}, first)
	remaining, err := k.GetEVMFrozenBinding(ctx, stateAddress(3), stateWord(2))
	require.NoError(t, err)
	require.True(t, remaining.Active)
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2001, 0))
	last, err := k.SettleMatureWithdrawalsLimit(ctx, 2, 1_000_000, k.ApplyWithdrawalCompletion)
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Settled: 1}, last)
}
