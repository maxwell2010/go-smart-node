package keeper

import (
	"bytes"
	"encoding/json"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func penaltyProgressFixture(t *testing.T, ctx sdk.Context, operator byte) NativePenaltyProgress {
	t.Helper()
	return NativePenaltyProgress{Version: 1, Reason: NativePenaltyDowntime,
		Operator: addr(t, identity.Bech32PrefixValAddr, operator), ConsensusAddress: stateAddress(operator),
		InfractionHeight: ctx.BlockHeight() - 1, AdmittedHeight: ctx.BlockHeight(), Percentage: 1,
		BeforeCursor: stateWord(0), AfterCursor: stateWord(1), Phase: NativePenaltyActiveNFT, Burned: sdkmath.ZeroInt()}
}

func TestNativePenaltyProgressDurableRollbackCASAndCompletion(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize)
	p := penaltyProgressFixture(t, ctx, 1)
	raw := stateAddress(1)
	outer, _ := ctx.CacheContext()
	require.NoError(t, k.StartNativePenaltyProgress(outer, p))
	pending, err := k.PendingNativePenalty(ctx, raw)
	require.NoError(t, err)
	require.False(t, pending)
	require.NoError(t, k.StartNativePenaltyProgress(ctx, p))
	require.Error(t, k.StartNativePenaltyProgress(ctx, p))
	next := p
	next.Phase = NativePenaltyActiveCoin
	next.Burned = sdkmath.NewInt(3)
	discarded, _ := ctx.CacheContext()
	require.NoError(t, k.AdvanceNativePenaltyProgress(discarded, p, next))
	actual, err := k.GetNativePenaltyProgress(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, p, actual)
	require.NoError(t, k.AdvanceNativePenaltyProgress(ctx, p, next))
	require.ErrorContains(t, k.AdvanceNativePenaltyProgress(ctx, p, next), "prestate changed")
	forged := next
	forged.Percentage = 2
	require.ErrorContains(t, k.AdvanceNativePenaltyProgress(ctx, next, forged), "invalid penalty progress transition")
	require.Error(t, k.CompleteNativePenaltyProgress(ctx, next))
	reopened, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	actual, err = reopened.GetNativePenaltyProgress(ctx.WithBlockHeight(100000), raw)
	require.NoError(t, err)
	require.Equal(t, next, actual, "a historical liability remains resumable after its evidence admission window")
	for next.Phase < NativePenaltyFinished {
		after := next
		after.Phase++
		require.NoError(t, reopened.AdvanceNativePenaltyProgress(ctx, next, after))
		next = after
	}
	require.NoError(t, reopened.CompleteNativePenaltyProgress(ctx, next))
	pending, err = reopened.PendingNativePenalty(ctx, raw)
	require.NoError(t, err)
	require.False(t, pending)
	require.ErrorIs(t, reopened.CompleteNativePenaltyProgress(ctx, next), ErrNotFound)
}

func TestNativePenaltyProgressRotationAndMalformedBarrier(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize)
	for _, id := range []byte{1, 2, 3} {
		require.NoError(t, k.StartNativePenaltyProgress(ctx, penaltyProgressFixture(t, ctx, id)))
	}
	for _, id := range []byte{1, 2, 3, 1, 2, 3} {
		p, err := k.NextNativePenaltyProgress(ctx, NativePenaltyDowntime)
		require.NoError(t, err)
		require.Equal(t, addr(t, identity.Bech32PrefixValAddr, id), p.Operator)
	}
	key, err := nativePenaltyProgressKey(NativePenaltyDowntime, stateAddress(2))
	require.NoError(t, err)
	require.NoError(t, service.OpenKVStore(ctx).Set(key, []byte("corrupt")))
	pending, err := k.PendingNativePenalty(ctx, stateAddress(2))
	require.NoError(t, err)
	require.True(t, pending, "corruption must not unlock custody")
	_, err = k.GetNativePenaltyProgress(ctx, stateAddress(2))
	require.Error(t, err)
	for _, mutate := range []func(*NativePenaltyProgress){
		func(p *NativePenaltyProgress) { p.AfterCursor = stateWord(2) },
		func(p *NativePenaltyProgress) { p.ConsensusAddress = make([]byte, 20) },
		func(p *NativePenaltyProgress) { p.FrozenAfter = bytes.Repeat([]byte{1}, 74) },
	} {
		p := penaltyProgressFixture(t, ctx, 9)
		mutate(&p)
		require.Error(t, k.StartNativePenaltyProgress(ctx, p))
	}
	good := penaltyProgressFixture(t, ctx, 9)
	b, err := json.Marshal(good)
	require.NoError(t, err)
	key, err = nativePenaltyProgressKey(NativePenaltyDowntime, stateAddress(9))
	require.NoError(t, err)
	require.NoError(t, service.OpenKVStore(ctx).Set(key, append(b, ' ')))
	_, err = k.GetNativePenaltyProgress(ctx, stateAddress(9))
	require.ErrorContains(t, err, "noncanonical")
}

func TestNativeDuplicateVotePagedJailPreservesLiability(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize)
	offender := validator(t, 2, 0)
	consensus, err := offender.GetConsAddr()
	require.NoError(t, err)
	candidate := queuedDuplicateVote(t, 2, 2, 4)
	candidate.ConsensusAddress = consensus
	_, err = k.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{candidate})
	require.NoError(t, err)
	jobs, err := k.PendingNativeDuplicateVotePenalties(ctx, 1)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	discarded, _ := ctx.CacheContext()
	require.NoError(t, k.JailNativeDuplicateVotePenalty(discarded, jobs[0]))
	_, err = k.GetNativeDuplicateVoteExclusion(ctx, consensus)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, k.JailNativeDuplicateVotePenalty(ctx, jobs[0]))
	require.NoError(t, k.JailNativeDuplicateVotePenalty(ctx, jobs[0]))
	jailed, err := k.GetValidator(ctx, candidate.Operator)
	require.NoError(t, err)
	require.True(t, jailed.Jailed)
	pending, err := k.HasPendingNativeDuplicateVotePenalty(ctx, candidate.Operator)
	require.NoError(t, err)
	require.True(t, pending)
	require.NoError(t, k.FinalizeNativeDuplicateVotePenalty(ctx, jobs[0]))
	pending, err = k.HasPendingNativeDuplicateVotePenalty(ctx, candidate.Operator)
	require.NoError(t, err)
	require.False(t, pending)
	_, err = k.GetNativeDuplicateVoteExclusion(ctx, consensus)
	require.NoError(t, err)
}

// Cosmos cacheMergeIterator.Error reports invalid when no key exists. An
// empty durable queue is routine BeginBlock state, including nested caches.
func TestNativePenaltyEmptyQueueInNestedFinalizeCache(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize)
	outer, _ := ctx.CacheContext()
	inner, _ := outer.CacheContext()
	for _, read := range []sdk.Context{ctx, outer, inner} {
		_, err := k.NextNativePenaltyProgress(read, NativePenaltyDowntime)
		require.ErrorIs(t, err, ErrNotFound)
	}
}
