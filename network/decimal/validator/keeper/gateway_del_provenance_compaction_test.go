package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func activeDELCompactionAtLimit(t *testing.T, maxAgeNumBlocks int64, maxAgeDuration time.Duration, held bool) (sdk.Context, *StateKeeper, GatewayDELStake) {
	t.Helper()
	ctx, k, position := delProvenanceGenesis(t)
	require.NoError(t, k.InitializeNativeEvidenceLimits(ctx.WithBlockHeight(0), &cmtproto.EvidenceParams{
		MaxAgeNumBlocks: maxAgeNumBlocks, MaxAgeDuration: maxAgeDuration, MaxBytes: 1024,
	}))
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize)
	if held {
		position = gatewayDELHoldFixture(2000, 3000, 0)
	}
	for h := int64(1); h <= 126; h++ {
		ctx = ctx.WithBlockHeight(h).WithBlockTime(time.Unix(1999+h, 0).UTC())
		position.Amount = position.Amount.AddRaw(20)
		require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(20)))
		require.NoError(t, k.RecordNativeEvidenceBlock(ctx))
	}
	count, err := k.countDELProvenanceTranches(ctx, position.Validator)
	require.NoError(t, err)
	require.Equal(t, maxDELProvenanceTranches-1, count, "no compaction occurs below the deposit cap")
	return ctx.WithBlockHeight(127).WithBlockTime(time.Unix(2126, 0).UTC()), k, position
}

func TestActiveDELProvenanceCompactionRequiresBothExpiredLimits(t *testing.T) {
	for _, test := range []struct {
		name    string
		blocks  int64
		elapsed time.Duration
		held    bool
	}{
		{name: "second funding block equals height limit", blocks: 124, elapsed: time.Second},
		{name: "second funding block equals time limit", blocks: 1, elapsed: 124 * time.Second},
		{name: "hold retains every funding block", blocks: 2, elapsed: 2 * time.Second, held: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, k, position := activeDELCompactionAtLimit(t, test.blocks, test.elapsed, test.held)
			before := activeDELProvenanceForTest(t, ctx, k, position)
			position.Amount = position.Amount.AddRaw(20)
			require.ErrorContains(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(20)), "provenance tranche limit")
			require.Equal(t, before, activeDELProvenanceForTest(t, ctx, k, position))
		})
	}
}

func TestActiveDELProvenanceCompactsExpiredPrefixAtDepositCap(t *testing.T) {
	ctx, k, position := activeDELCompactionAtLimit(t, 2, 2*time.Second, false)
	owner, operator, err := position.nativePair()
	require.NoError(t, err)
	row, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	plan, err := k.PlanActiveDELHistoricalPenalty(ctx, row, 124)
	require.NoError(t, err)
	beforeLots := activeDELProvenanceForTest(t, ctx, k, position)
	key := delProvenanceKey(activeDELProvenancePrefix, row.Gateway, row.StakeID)
	beforeBytes, err := k.service.OpenKVStore(ctx).Get(key)
	require.NoError(t, err)
	deposit := sdkmath.NewInt(20)
	next := position
	next.Amount = next.Amount.Add(deposit)

	validator, err := k.GetValidator(ctx, operator)
	require.NoError(t, err)
	consensus, err := validator.GetConsAddr()
	require.NoError(t, err)
	pending, _ := ctx.CacheContext()
	count, err := k.enqueueNativeDuplicateVoteCandidates(pending, []NativeDuplicateVoteCandidate{{
		Operator: operator, ConsensusAddress: consensus, InfractionHeight: 124, InfractionTime: time.Unix(2123, 0).UTC(),
		ObservedPower: 1, TotalVotingPower: 2, PenaltyPercent: HistoricalDELPenaltyPercent,
	}})
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.ErrorContains(t, k.ApplyGatewayDELDelegation(pending, next, deposit), "provenance tranche limit")
	require.Equal(t, beforeLots, activeDELProvenanceForTest(t, pending, k, position))

	broken, err := NewStateKeeperWithDomain(failingService{service: k.service, prefix: bindingRevisionKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyGatewayDELDelegation(ctx, next, deposit), "injected write failure")
	afterFailure, err := k.service.OpenKVStore(ctx).Get(key)
	require.NoError(t, err)
	require.Equal(t, beforeBytes, afterFailure)
	unchanged, err := k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.True(t, unchanged.Stake.Stake.Amount.Equal(position.Amount))

	// A partial withdrawal at the cap can split one tranche before the next
	// deposit. The compaction must still leave the reserve slot available.
	split, _ := ctx.CacheContext()
	active := position
	active.Amount = active.Amount.SubRaw(1)
	index := common.Hash{31: 7}
	require.NoError(t, k.ApplyGatewayDELWithdrawal(split, active, index, sdkmath.OneInt(), ctx.BlockTime().Add(time.Second)))
	count, err = k.countDELProvenanceTranches(split, position.Validator)
	require.NoError(t, err)
	require.Equal(t, maxDELProvenanceTranches, count)
	active.Amount = active.Amount.Add(deposit)
	require.NoError(t, k.ApplyGatewayDELDelegation(split, active, deposit))
	count, err = k.countDELProvenanceTranches(split, position.Validator)
	require.NoError(t, err)
	require.LessOrEqual(t, count, maxDELProvenanceTranches-1)
	frozen, err := k.GetEVMFrozenBinding(split, active.Gateway[:], index[:])
	require.NoError(t, err)
	frozenLots, _, err := k.GetFrozenDELProvenance(split, frozen)
	require.NoError(t, err)
	requireDELProvenance(t, frozenLots, []int64{0}, []int64{1})
	require.Equal(t, beforeLots, activeDELProvenanceForTest(t, ctx, k, position))

	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, next, deposit))
	lots := activeDELProvenanceForTest(t, ctx, k, next)
	requireDELProvenance(t, lots, []int64{0, 123, 124, 125, 126, 127}, []int64{12, 2460, 20, 20, 20, 20})
	require.Equal(t, beforeLots[0], lots[0], "genesis principal retains its own funding record")
	require.Equal(t, beforeLots[123].FundedAt, lots[1].FundedAt)
	require.Equal(t, beforeLots[124:], lots[2:5], "recent records retain exact amounts and timestamps")
	current, err := k.GetEVMStakeBinding(ctx, row.Kind, row.Gateway, row.StakeID)
	require.NoError(t, err)
	compactedPlan, err := k.PlanActiveDELHistoricalPenalty(ctx, current, 124)
	require.NoError(t, err)
	require.True(t, plan.Eligible.Equal(compactedPlan.Eligible))
	require.True(t, plan.Loss.Equal(compactedPlan.Loss))
	require.True(t, plan.Remaining.Add(deposit).Equal(compactedPlan.Remaining))
	delegation, err := k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.True(t, delegation.Stake.Stake.Amount.Equal(next.Amount))
	reserve, err := k.GetActiveDELReserve(ctx, operator)
	require.NoError(t, err)
	require.True(t, reserve.Equal(next.Amount))
}
