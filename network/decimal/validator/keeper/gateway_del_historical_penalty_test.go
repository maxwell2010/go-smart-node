package keeper

import (
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func requireDELHistoricalPenalty(t *testing.T, plan DELHistoricalPenaltyPlan, before, eligible, loss int64) {
	t.Helper()
	require.Equal(t, sdkmath.NewInt(before), plan.Before)
	require.Equal(t, sdkmath.NewInt(eligible), plan.Eligible)
	require.Equal(t, sdkmath.NewInt(loss), plan.Loss)
	require.Equal(t, sdkmath.NewInt(before-loss), plan.Remaining)
}

func TestPlanActiveDELHistoricalPenaltyPreservesLateDeposits(t *testing.T) {
	ctx, k, position := delProvenanceGenesis(t)
	for i, deposit := range []int64{8, 1, 1000, 2} {
		height := int64(i + 1)
		ctx = ctx.WithBlockHeight(height).WithBlockTime(time.Unix(1999+height, 0).UTC())
		position.Amount = position.Amount.AddRaw(deposit)
		require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(deposit)))
	}
	row, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	original := activeDELProvenanceForTest(t, ctx, k, position)
	plan, err := k.PlanActiveDELHistoricalPenalty(ctx, row, 3)
	require.NoError(t, err)
	requireDELHistoricalPenalty(t, plan, 1023, 21, 1)
	require.True(t, plan.FrozenAt.IsZero())
	// The prefix ending at height one totals 20, so the rounding atom lands
	// there. Deposits in the infraction block and every later block survive.
	requireDELProvenance(t, plan.Tranches, []int64{0, 1, 2, 3, 4}, []int64{12, 7, 1, 1000, 2})
	require.Equal(t, original, activeDELProvenanceForTest(t, ctx, k, position))
	stored, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, row, stored)
}

func TestPlanFrozenDELHistoricalPenaltyUsesFIFOAndFreezeHeight(t *testing.T) {
	ctx, k, position := delProvenanceGenesis(t)
	position.Amount = position.Amount.AddRaw(9)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(9)))
	ctx = ctx.WithBlockHeight(2).WithBlockTime(time.Unix(2001, 100).UTC())
	position.Amount = position.Amount.AddRaw(1000)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(1000)))
	position.Amount = position.Amount.SubRaw(26)
	index := common.Hash{}
	due := time.Unix(2010, 0).UTC()
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, index, sdkmath.NewInt(26), due))
	claim, err := k.GetEVMFrozenBinding(ctx, position.Gateway[:], index[:])
	require.NoError(t, err)
	freezeTime := ctx.BlockTime()
	ctx = ctx.WithBlockHeight(3).WithBlockTime(due.Add(time.Second))
	plan, err := k.PlanFrozenDELHistoricalPenalty(ctx, claim, 2)
	require.NoError(t, err)
	requireDELHistoricalPenalty(t, plan, 26, 21, 1)
	require.Equal(t, freezeTime, plan.FrozenAt)
	requireDELProvenance(t, plan.Tranches, []int64{0, 1, 2}, []int64{12, 8, 5})
	plan, err = k.PlanFrozenDELHistoricalPenalty(ctx, claim, 3)
	require.NoError(t, err)
	requireDELHistoricalPenalty(t, plan, 26, 0, 0)
	requireDELProvenance(t, plan.Tranches, []int64{0, 1, 2}, []int64{12, 9, 5})
	stored, _, err := k.GetFrozenDELProvenance(ctx, claim)
	require.NoError(t, err)
	require.Equal(t, stored, plan.Tranches)
	row, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	activePlan, err := k.PlanActiveDELHistoricalPenalty(ctx, row, 2)
	require.NoError(t, err)
	requireDELHistoricalPenalty(t, activePlan, 995, 0, 0)
}

func TestPlanHeldDELHistoricalPenaltyKeepsSameSecondFundingHeights(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	require.NoError(t, k.service.OpenKVStore(ctx).Set([]byte{nativeStakeProvenanceMarker}, []byte{1}))
	held := gatewayDELHoldFixture(2000, 2010, 20)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2000, 100).UTC())
	held.Amount = held.Amount.AddRaw(50)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, sdkmath.NewInt(50)))
	row, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, held.Gateway[:], held.StakeID[:])
	require.NoError(t, err)
	plan, err := k.PlanActiveDELHistoricalPenalty(ctx, row, 11)
	require.NoError(t, err)
	requireDELHistoricalPenalty(t, plan, 70, 20, 1)
	requireDELProvenance(t, plan.Tranches, []int64{10, 11}, []int64{19, 50})
	require.EqualValues(t, held.HoldStartTime, plan.Tranches[0].FundedAt.Unix())
	require.EqualValues(t, held.HoldStartTime, plan.Tranches[1].FundedAt.Unix())
	require.True(t, plan.Tranches[1].FundedAt.After(plan.Tranches[0].FundedAt))

	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2011, 0).UTC())
	held.Amount = held.Amount.SubRaw(25)
	index := common.Hash{31: 3}
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, held, index, sdkmath.NewInt(25), time.Unix(2020, 0).UTC()))
	claim, err := k.GetEVMFrozenBinding(ctx, held.Gateway[:], index[:])
	require.NoError(t, err)
	plan, err = k.PlanFrozenDELHistoricalPenalty(ctx, claim, 11)
	require.NoError(t, err)
	requireDELHistoricalPenalty(t, plan, 25, 20, 1)
	requireDELProvenance(t, plan.Tranches, []int64{10, 11}, []int64{19, 5})
	require.EqualValues(t, held.HoldStartTime, plan.Tranches[1].FundedAt.Unix())
}

func TestApplyMatureFrozenHistoricalDELOnlyDebitsEligibleCustody(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	require.NoError(t, k.service.OpenKVStore(ctx).Set([]byte{nativeStakeProvenanceMarker}, []byte{1}))
	held := gatewayDELHoldFixture(2000, 2010, 20)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2001, 0).UTC())
	held.Amount = held.Amount.AddRaw(50)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, sdkmath.NewInt(50)))
	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2011, 0).UTC())
	held.Amount = held.Amount.SubRaw(25)
	index, next := common.Hash{31: 3}, common.Hash{31: 1}
	due := time.Unix(2020, 0).UTC()
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, held, index, sdkmath.NewInt(25), due))
	claim, err := k.GetEVMFrozenBinding(ctx, held.Gateway[:], index[:])
	require.NoError(t, err)
	hold := GatewayFrozenDELHold{WrappedDEL: held.WrappedDEL, HoldDeadline: held.HoldDeadline, HoldStartTime: held.HoldStartTime}
	ctx = ctx.WithBlockHeight(13).WithBlockTime(due.Add(time.Second))
	require.Error(t, k.ApplyGatewayFrozenDELHoldPenalty(ctx, claim, hold, 11, common.Hash{}, next, sdkmath.NewInt(24)))
	require.ErrorContains(t, k.ApplyGatewayFrozenHistoricalDELPenalty(ctx, claim, &hold, 11, common.Hash{}, next, sdkmath.NewInt(23)), "eligible historical principal")
	child, _ := ctx.CacheContext()
	require.NoError(t, k.ApplyGatewayFrozenHistoricalDELPenalty(child, claim, &hold, 11, common.Hash{}, next, sdkmath.NewInt(24)))
	current, err := k.GetEVMFrozenBinding(ctx, held.Gateway[:], index[:])
	require.NoError(t, err)
	require.Equal(t, claim, current)
	require.NoError(t, k.ApplyGatewayFrozenHistoricalDELPenalty(ctx, claim, &hold, 11, common.Hash{}, next, sdkmath.NewInt(24)))
	current, err = k.GetEVMFrozenBinding(ctx, held.Gateway[:], index[:])
	require.NoError(t, err)
	require.True(t, current.RawAmount.Equal(sdkmath.NewInt(24)))
	lots, _, err := k.GetFrozenDELProvenance(ctx, current)
	require.NoError(t, err)
	requireDELProvenance(t, lots, []int64{10, 11}, []int64{19, 5})
	require.Error(t, k.ApplyGatewayFrozenHistoricalDELPenalty(ctx, claim, &hold, 11, common.Hash{}, next, sdkmath.NewInt(24)))
}

func TestPlanDELHistoricalPenaltyTruncatesSurvivingPrincipalBelowOneAtom(t *testing.T) {
	ctx, k, position := delProvenanceGenesis(t)
	position.Amount = position.Amount.AddRaw(8)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(8)))
	ctx = ctx.WithBlockHeight(2).WithBlockTime(time.Unix(2001, 0).UTC())
	position.Amount = position.Amount.AddRaw(1000)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(1000)))
	ctx = ctx.WithBlockHeight(3).WithBlockTime(time.Unix(2002, 0).UTC())
	require.NoError(t, k.ApplyGatewayDELPenalty(ctx, position, sdkmath.NewInt(969), common.Hash{31: 1}))
	row, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	plan, err := k.PlanActiveDELHistoricalPenalty(ctx, row, 2)
	require.NoError(t, err)
	requireDELHistoricalPenalty(t, plan, 969, 19, 0)
	require.Equal(t, activeDELProvenanceForTest(t, ctx, k, position), plan.Tranches)
	_, err = k.PlanActiveDELHistoricalPenalty(ctx, row, 0)
	require.ErrorContains(t, err, "infraction height")
	_, err = k.PlanActiveDELHistoricalPenalty(ctx, row, 4)
	require.ErrorContains(t, err, "infraction height")
}

func TestPlanDELHistoricalPenaltyRequiresFreshProvenance(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	position := gatewayDELFixture()
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, position.Amount))
	row, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	_, err = k.PlanActiveDELHistoricalPenalty(ctx, row, ctx.BlockHeight())
	require.ErrorContains(t, err, "requires native stake provenance")
}

func TestPlanActiveDELHistoricalPenaltyExcludesNFTCompanion(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	require.NoError(t, k.service.OpenKVStore(ctx).Set([]byte{nativeStakeProvenanceMarker}, []byte{1}))
	position, _ := seedGatewayDELNFTCompanion(t, ctx, k)
	row, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	plan, err := k.PlanActiveDELHistoricalPenalty(ctx, row, ctx.BlockHeight())
	require.NoError(t, err)
	requireDELHistoricalPenalty(t, plan, 0, 0, 0)
	require.Empty(t, plan.Tranches)
	position.Amount = sdkmath.NewInt(20)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, position.Amount))
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2001, 0).UTC())
	row, err = k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	plan, err = k.PlanActiveDELHistoricalPenalty(ctx, row, 11)
	require.NoError(t, err)
	requireDELHistoricalPenalty(t, plan, 20, 20, 1)
	requireDELProvenance(t, plan.Tranches, []int64{10}, []int64{19})
}

func TestPlanDELHistoricalPenaltyPreservesWideUint256Arithmetic(t *testing.T) {
	maximum := new(big.Int).Lsh(big.NewInt(1), 256)
	maximum.Sub(maximum, big.NewInt(1))
	amount := sdkmath.NewIntFromBigInt(maximum)
	plan := planDELHistoricalPenalty([]DELProvenanceTranche{{FundedHeight: 1, Amount: amount}}, 2)
	want := sdkmath.NewIntFromBigInt(new(big.Int).Quo(maximum, big.NewInt(20)))
	require.Equal(t, amount, plan.Eligible)
	require.Equal(t, want, plan.Loss)
	require.Equal(t, amount.Sub(want), plan.Remaining)
	require.Equal(t, plan.Remaining, plan.Tranches[0].Amount)
}
