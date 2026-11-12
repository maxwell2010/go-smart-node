package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func delProvenanceGenesis(t *testing.T) (sdk.Context, *StateKeeper, GatewayDELStake) {
	t.Helper()
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain).WithBlockTime(time.Unix(2000, 0).UTC())
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	position := gatewayDELFixture()
	row, err := position.binding(position.Amount, 1, 1)
	require.NoError(t, err)
	owner, operator, err := position.nativePair()
	require.NoError(t, err)
	genesis := bindingGenesisState(t, false)
	genesis.EVMStakeBindings = []types.EVMStakeBinding{row}
	genesis.Delegations = []types.Delegation{{Delegator: owner, Validator: operator, Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, position.Amount))}}
	require.NoError(t, k.InitGenesis(ctx, genesis))
	require.ErrorContains(t, k.InitializeDELProvenance(ctx.WithBlockHeight(10), position.Gateway, position.WrappedDEL), "fresh height-one genesis")
	require.NoError(t, k.InitializeDELProvenance(ctx, position.Gateway, position.WrappedDEL))
	require.ErrorContains(t, k.InitializeDELProvenance(ctx, position.Gateway, position.WrappedDEL), "already initialized")
	return ctx.WithBlockHeight(1), k, position
}

func requireDELProvenance(t *testing.T, lots []DELProvenanceTranche, heights, amounts []int64) {
	t.Helper()
	require.Len(t, lots, len(heights))
	for i, lot := range lots {
		require.Equal(t, heights[i], lot.FundedHeight)
		require.Equal(t, sdkmath.NewInt(amounts[i]), lot.Amount)
	}
}

func activeDELProvenanceForTest(t *testing.T, ctx sdk.Context, k *StateKeeper, position GatewayDELStake) []DELProvenanceTranche {
	t.Helper()
	row, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	lots, err := k.GetActiveDELProvenance(ctx, row)
	require.NoError(t, err)
	return lots
}

func TestDELProvenanceGenesisPartialFreezePenaltyAndCompletion(t *testing.T) {
	ctx, k, position := delProvenanceGenesis(t)
	position.Amount = position.Amount.AddRaw(6)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(6)))
	ctx = ctx.WithBlockHeight(2).WithBlockTime(time.Unix(2001, 300).UTC())
	for _, amount := range []int64{8, 2} {
		position.Amount = position.Amount.AddRaw(amount)
		require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(amount)))
	}
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, position), []int64{0, 1, 2}, []int64{12, 6, 10})
	ctx = ctx.WithBlockHeight(3).WithBlockTime(time.Unix(2002, 400).UTC())
	position.Amount = position.Amount.SubRaw(15)
	index := common.Hash{}
	due := time.Unix(2010, 0).UTC()
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, index, sdkmath.NewInt(15), due))
	claim, err := k.GetEVMFrozenBinding(ctx, position.Gateway[:], index[:])
	require.NoError(t, err)
	queued, frozenAt, err := k.GetFrozenDELProvenance(ctx, claim)
	require.NoError(t, err)
	require.Equal(t, ctx.BlockTime(), frozenAt)
	requireDELProvenance(t, queued, []int64{0, 1}, []int64{12, 3})
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, position), []int64{1, 2}, []int64{3, 10})

	ctx = ctx.WithBlockHeight(4).WithBlockTime(time.Unix(2003, 0).UTC())
	cursor := common.Hash{31: 1}
	require.NoError(t, k.ApplyGatewayDELPenalty(ctx, position, sdkmath.NewInt(8), cursor))
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, position), []int64{1, 2}, []int64{2, 6})
	require.NoError(t, k.ApplyGatewayFrozenDELPenalty(ctx, claim, 3, common.Hash{}, cursor, sdkmath.NewInt(12)))
	claim, err = k.GetEVMFrozenBinding(ctx, position.Gateway[:], index[:])
	require.NoError(t, err)
	queued, retainedTime, err := k.GetFrozenDELProvenance(ctx, claim)
	require.NoError(t, err)
	require.Equal(t, frozenAt, retainedTime)
	requireDELProvenance(t, queued, []int64{0, 1}, []int64{10, 2})

	ctx = ctx.WithBlockHeight(5).WithBlockTime(due)
	result, err := k.SettleMatureWithdrawals(ctx, 1_000_000, k.ApplyWithdrawalCompletion)
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Settled: 1}, result)
	key := delProvenanceKey(frozenDELProvenancePrefix, position.Gateway[:], index[:])
	value, err := k.service.OpenKVStore(ctx).Get(key)
	require.NoError(t, err)
	require.Nil(t, value)
	require.ErrorIs(t, k.ApplyWithdrawalCompletion(ctx, claim), ErrWithdrawalAlreadyCompleted)
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, position), []int64{1, 2}, []int64{2, 6})
}

func TestDELProvenanceHeldSameSecondPenaltyThenReset(t *testing.T) {
	ctx, k, ordinary := delProvenanceGenesis(t)
	held := gatewayDELHoldFixture(2000, 2010, 4)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	ctx = ctx.WithBlockHeight(2).WithBlockTime(time.Unix(2000, 500).UTC())
	held.Amount = held.Amount.AddRaw(6)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, sdkmath.NewInt(6)))
	ctx = ctx.WithBlockHeight(3).WithBlockTime(time.Unix(2001, 0).UTC())
	held.Amount = held.Amount.AddRaw(7)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, sdkmath.NewInt(7)))
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, held), []int64{1, 2, 3}, []int64{4, 6, 7})
	owner, operator, err := held.nativePair()
	require.NoError(t, err)
	native, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Len(t, native.Stake.Holds, 2, "same-second deposits share a native hold lot only")

	ctx = ctx.WithBlockHeight(4).WithBlockTime(time.Unix(2002, 0).UTC())
	require.NoError(t, k.ApplyGatewayDELHoldPenalty(ctx, held, sdkmath.NewInt(10), common.Hash{31: 1}))
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, held), []int64{1, 2, 3}, []int64{3, 3, 4})
	// A hold reset uses a caught-up zero-cursor position. Exercise the reset
	// independently below because the current Gateway rejects a penalized reset.
	ctx, k, ordinary = delProvenanceGenesis(t)
	held = gatewayDELHoldFixture(2000, 2010, 4)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	ordinary.Amount = ordinary.Amount.AddRaw(2)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, ordinary, sdkmath.NewInt(2)))
	ctx = ctx.WithBlockHeight(2).WithBlockTime(time.Unix(2001, 0).UTC())
	held.Amount = held.Amount.AddRaw(6)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, sdkmath.NewInt(6)))
	ctx = ctx.WithBlockHeight(3).WithBlockTime(time.Unix(2010, 0).UTC())
	ordinary.Amount = ordinary.Amount.Add(held.Amount)
	moved := held.Amount
	held.Amount = sdkmath.ZeroInt()
	require.NoError(t, k.ApplyGatewayDELHoldReset(ctx, held, ordinary, moved))
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, ordinary), []int64{0, 1, 2}, []int64{12, 6, 6})
	key := delProvenanceKey(activeDELProvenancePrefix, held.Gateway[:], held.StakeID[:])
	value, err := k.service.OpenKVStore(ctx).Get(key)
	require.NoError(t, err)
	require.Nil(t, value)
}

func TestDELProvenanceHeldFIFOAndFrozenPenalty(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	require.NoError(t, k.service.OpenKVStore(ctx).Set([]byte{nativeStakeProvenanceMarker}, []byte{1}))
	held := gatewayDELHoldFixture(2000, 2010, 4)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2000, 1).UTC())
	held.Amount = held.Amount.AddRaw(6)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, sdkmath.NewInt(6)))
	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2011, 0).UTC())
	held.Amount = held.Amount.SubRaw(8)
	index := common.Hash{31: 3}
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, held, index, sdkmath.NewInt(8), time.Unix(2020, 0).UTC()))
	claim, err := k.GetEVMFrozenBinding(ctx, held.Gateway[:], index[:])
	require.NoError(t, err)
	queued, _, err := k.GetFrozenDELProvenance(ctx, claim)
	require.NoError(t, err)
	requireDELProvenance(t, queued, []int64{10, 11}, []int64{4, 4})
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, held), []int64{11}, []int64{2})
	proof := GatewayFrozenDELHold{WrappedDEL: held.WrappedDEL, HoldDeadline: held.HoldDeadline, HoldStartTime: held.HoldStartTime}
	require.NoError(t, k.ApplyGatewayFrozenDELHoldPenalty(ctx, claim, proof, 12, common.Hash{}, common.Hash{31: 1}, sdkmath.NewInt(3)))
	claim, err = k.GetEVMFrozenBinding(ctx, held.Gateway[:], index[:])
	require.NoError(t, err)
	queued, _, err = k.GetFrozenDELProvenance(ctx, claim)
	require.NoError(t, err)
	requireDELProvenance(t, queued, []int64{10, 11}, []int64{2, 1})
	require.NoError(t, k.ApplyGatewayFrozenDELHoldPenalty(ctx, claim, proof, 12, common.Hash{31: 1}, common.Hash{31: 2}, sdkmath.ZeroInt()))
	key := delProvenanceKey(frozenDELProvenancePrefix, held.Gateway[:], index[:])
	value, err := k.service.OpenKVStore(ctx).Get(key)
	require.NoError(t, err)
	require.Nil(t, value)
}

func TestDELProvenanceLimitPreservesTopupAndSplitHeadroom(t *testing.T) {
	ctx, k, position := delProvenanceGenesis(t)
	for height := int64(1); height < maxDELProvenanceTranches-1; height++ {
		ctx = ctx.WithBlockHeight(height).WithBlockTime(time.Unix(2000+height, 0).UTC())
		position.Amount = position.Amount.AddRaw(1)
		require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.OneInt()))
	}
	later := ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(ctx.BlockTime().Add(time.Second))
	position.Amount = position.Amount.AddRaw(1)
	require.ErrorContains(t, k.ApplyGatewayDELDelegation(later, position, sdkmath.OneInt()), "provenance tranche limit")
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.OneInt()), "same funding block coalesces at the limit")
	position.Amount = position.Amount.SubRaw(1)
	require.NoError(t, k.ApplyGatewayDELWithdrawal(later, position, common.Hash{}, sdkmath.OneInt(), later.BlockTime().Add(time.Minute)))
	count, err := k.countDELProvenanceTranches(later, position.Validator)
	require.NoError(t, err)
	require.Equal(t, maxDELProvenanceTranches, count, "partial FIFO freeze adds one fragment")
}

func TestDELProvenanceBindingFailureRollsBackAndRetrySucceeds(t *testing.T) {
	ctx, k, position := delProvenanceGenesis(t)
	broken := *k
	broken.service = failingService{service: k.service, prefix: bindingRevisionKey}
	index := common.Hash{31: 6}
	due := ctx.BlockTime().Add(time.Minute)
	position.Amount = position.Amount.SubRaw(3)
	require.ErrorContains(t, broken.ApplyGatewayDELWithdrawal(ctx, position, index, sdkmath.NewInt(3), due), "injected write failure")
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, position), []int64{0}, []int64{12})
	key := delProvenanceKey(frozenDELProvenancePrefix, position.Gateway[:], index[:])
	value, err := k.service.OpenKVStore(ctx).Get(key)
	require.NoError(t, err)
	require.Nil(t, value)
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, index, sdkmath.NewInt(3), due))
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, position), []int64{0}, []int64{9})
}
