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

func markWithdrawalSettled(ctx sdk.Context, k *StateKeeper, row types.EVMFrozenBinding) error {
	row.Active = false
	row.UpdatedHeight = ctx.BlockHeight()
	plan, err := k.PrepareBindingPlan(ctx, []BindingMutation{{Frozen: &row}})
	if err != nil {
		return err
	}
	return k.ApplyBindingPlan(ctx, plan)
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
		require.NoError(t, markWithdrawalSettled(child, k, row))
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
		return markWithdrawalSettled(child, k, row)
	})
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Settled: maxWithdrawalAttempts}, next)
	require.Equal(t, wideID, seen[0])
	require.Equal(t, stateWord(0), seen[1])
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, exported.Undelegations, 1)
	require.Len(t, exported.Undelegations[0].Entries, 2)

	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2002, 0))
	last, err := k.SettleMatureWithdrawals(ctx, 1_000_000, func(child sdk.Context, row types.EVMFrozenBinding) error {
		require.NotEqual(t, wideID, row.FrozenIndex, "committed payout must not be tried again")
		return markWithdrawalSettled(child, k, row)
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

func TestWithdrawalSettlementRequiresMatchingCompletedBinding(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain).WithBlockHeight(10).WithBlockTime(time.Unix(1999, 0))
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	g := bindingGenesisState(t, false)
	b := withdrawalGenesis(t, g, stateWord(0), 2000)
	require.NoError(t, k.InitGenesis(ctx, g))
	before, err := k.SettleMatureWithdrawals(ctx, 1_000_000, func(sdk.Context, types.EVMFrozenBinding) error {
		t.Fatal("settlement must not run before the deadline")
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, before)
	ctx = ctx.WithBlockTime(time.Unix(2000, 0))
	marker := []byte{0xfa}
	result, err := k.SettleMatureWithdrawals(ctx, 1_000_000, func(child sdk.Context, _ types.EVMFrozenBinding) error {
		return service.OpenKVStore(child).Set(marker, []byte{1})
	})
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Retried: 1}, result)
	value, err := service.OpenKVStore(ctx).Get(marker)
	require.NoError(t, err)
	require.Nil(t, value)
	current, err := k.GetEVMFrozenBinding(ctx, b.Gateway, b.FrozenIndex)
	require.NoError(t, err)
	require.True(t, current.Active)
}
