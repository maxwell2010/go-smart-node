package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func TestFrozenDELHoldPenaltyPreservesTimingAndExactQueueMultiplicity(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain).WithBlockHeight(10).WithBlockTime(time.Unix(2025, 0))
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	g := bindingGenesisState(t, false)
	const completion = 2040
	withdrawalGenesis(t, g, stateWord(4), completion)
	withdrawalGenesis(t, g, stateWord(5), completion)
	source := gatewayDELHoldFixture(2000, 2020, 12)
	hold := GatewayFrozenDELHold{WrappedDEL: source.WrappedDEL, HoldDeadline: source.HoldDeadline, HoldStartTime: source.HoldStartTime}
	pair := &g.Undelegations[0]
	for i := range pair.Entries {
		entry := &pair.Entries[i]
		entry.Stake = gatewayDELHoldStake(source.Amount, []*types.StakeHold{
			{Amount: sdkmath.NewInt(3), HoldStartTime: 2001, HoldEndTime: 2020},
			{Amount: sdkmath.NewInt(5), HoldStartTime: 2002, HoldEndTime: 2020},
			{Amount: sdkmath.NewInt(4), HoldStartTime: 2003, HoldEndTime: 2020},
		})
		row := &g.EVMFrozenBindings[i]
		row.SourceStakeID, row.RawAmount = source.StakeID.Bytes(), source.Amount
		row.NativeTarget.StakeType = types.StakeType_CoinHold
		row.CreatedHeight, row.UpdatedHeight = entry.CreationHeight, entry.CreationHeight
		row.NativeEntryHash, err = types.NativeQueueEntryHash(1, pair.Delegator, pair.Validator, "", entry)
		require.NoError(t, err)
	}
	before, other := g.EVMFrozenBindings[0], g.EVMFrozenBindings[1]
	require.Equal(t, before.NativeEntryHash, other.NativeEntryHash)
	require.NoError(t, k.InitGenesis(ctx, g))
	plain := gatewayDELFixture()
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, plain, plain.Amount))
	cursor := func(n uint64) common.Hash { return common.BytesToHash(stateWord(n)) }
	load := func(id []byte) types.EVMFrozenBinding {
		row, err := k.GetEVMFrozenBinding(ctx, before.Gateway, id)
		require.NoError(t, err)
		return row
	}
	reduced := sdkmath.NewInt(7)
	wrong := hold
	wrong.HoldDeadline++
	require.ErrorContains(t, k.ApplyGatewayFrozenDELHoldPenalty(ctx, before, wrong, 7, cursor(0), cursor(1), reduced), "source stake identity")
	wrong = hold
	wrong.HoldStartTime = 2002
	require.ErrorContains(t, k.ApplyGatewayFrozenDELHoldPenalty(ctx, before, wrong, 7, cursor(0), cursor(1), reduced), "lot differs from the source hold timing")
	require.ErrorContains(t, k.ApplyGatewayFrozenDELPenalty(ctx, before, 7, cursor(0), cursor(1), reduced), "invalid ordinary")
	require.ErrorContains(t, k.ApplyGatewayFrozenDELHoldPenalty(ctx, before, hold, 8, cursor(0), cursor(1), reduced), "predates")
	require.NoError(t, k.ApplyGatewayFrozenDELHoldPenalty(ctx, before, hold, 8, cursor(0), cursor(1), before.RawAmount))
	checkpoint := load(before.FrozenIndex)
	require.Equal(t, before.NativeEntryHash, checkpoint.NativeEntryHash)
	require.ErrorContains(t, k.ApplyGatewayFrozenDELHoldPenalty(ctx, checkpoint, hold, 7, cursor(0), cursor(1), reduced), "cursor prestate changed")
	untouched, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, g.Undelegations, untouched.Undelegations)
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: frozenDELPenaltyCursorPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyGatewayFrozenDELHoldPenalty(ctx, checkpoint, hold, 7, cursor(1), cursor(2), reduced), "injected write failure")
	afterFailure, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, untouched, afterFailure)

	require.NoError(t, k.ApplyGatewayFrozenDELHoldPenalty(ctx, checkpoint, hold, 7, cursor(1), cursor(2), reduced))
	partial := load(before.FrozenIndex)
	require.NotEqual(t, before.NativeEntryHash, partial.NativeEntryHash)
	require.Equal(t, reduced, partial.RawAmount)
	require.Equal(t, before.SourceStakeID, partial.SourceStakeID)
	require.Equal(t, before.CompletionTimestamp, partial.CompletionTimestamp)
	require.Equal(t, other, load(other.FrozenIndex))
	pending, err := k.GetUndelegation(ctx, pair.Delegator, pair.Validator)
	require.NoError(t, err)
	expected := pair.Entries[0]
	expected.Stake = gatewayDELHoldStake(reduced, []*types.StakeHold{
		{Amount: sdkmath.NewInt(2), HoldStartTime: 2001, HoldEndTime: 2020},
		{Amount: sdkmath.NewInt(3), HoldStartTime: 2002, HoldEndTime: 2020},
		{Amount: sdkmath.NewInt(2), HoldStartTime: 2003, HoldEndTime: 2020},
	})
	require.Equal(t, []types.UndelegationEntry{expected, pair.Entries[1]}, pending.Entries)
	exactHash, err := types.NativeQueueEntryHash(1, pair.Delegator, pair.Validator, "", &expected)
	require.NoError(t, err)
	require.Equal(t, exactHash, partial.NativeEntryHash)
	for _, row := range []types.EVMFrozenBinding{other, partial} {
		claims, next, err := k.GetNativeQueueBindingsPage(ctx, row.NativeEntryHash, nil, 2)
		require.NoError(t, err)
		require.Nil(t, next)
		require.Equal(t, []types.EVMFrozenBinding{row}, claims)
	}
	mature := ctx.WithBlockTime(time.Unix(completion, 0))
	require.ErrorContains(t, k.ApplyGatewayFrozenDELHoldPenalty(mature, partial, hold, 7, cursor(2), cursor(3), sdkmath.ZeroInt()), "unmatured")
	completionCache, _ := mature.CacheContext()
	require.NoError(t, k.ApplyWithdrawalCompletion(completionCache, partial))
	cursorKey := frozenDELPenaltyCursorKey(partial.Gateway, partial.FrozenIndex)
	rawCursor, err := service.OpenKVStore(completionCache).Get(cursorKey)
	require.NoError(t, err)
	require.Nil(t, rawCursor)
	rawCursor, err = service.OpenKVStore(ctx).Get(cursorKey)
	require.NoError(t, err)
	require.Equal(t, cursor(2).Bytes(), rawCursor)
	completedPair, err := k.GetUndelegation(completionCache, pair.Delegator, pair.Validator)
	require.NoError(t, err)
	require.Equal(t, []types.UndelegationEntry{pair.Entries[1]}, completedPair.Entries)
	require.NoError(t, k.ApplyGatewayFrozenDELHoldPenalty(ctx, partial, hold, 7, cursor(2), cursor(3), sdkmath.ZeroInt()))
	closed := load(before.FrozenIndex)
	require.False(t, closed.Active)
	require.True(t, closed.RawAmount.IsZero())
	require.Equal(t, partial.NativeEntryHash, closed.NativeEntryHash)
	rawCursor, err = service.OpenKVStore(ctx).Get(cursorKey)
	require.NoError(t, err)
	require.Nil(t, rawCursor)
	require.ErrorContains(t, k.ApplyGatewayFrozenDELHoldPenalty(ctx, partial, hold, 7, cursor(2), cursor(3), sdkmath.ZeroInt()), "binding prestate changed")
	claims, next, err := k.GetNativeQueueBindingsPage(ctx, exactHash, nil, 2)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Empty(t, claims)
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, untouched.EVMStakeBindings, after.EVMStakeBindings)
	require.Equal(t, []types.UndelegationEntry{pair.Entries[1]}, after.Undelegations[0].Entries)
	require.Equal(t, other, load(other.FrozenIndex))
}
