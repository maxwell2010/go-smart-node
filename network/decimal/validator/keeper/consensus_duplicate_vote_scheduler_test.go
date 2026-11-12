package keeper

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativeDuplicateVoteSchedulerReachesBeyondOriginalPage(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	_, err := k.NextNativeDuplicateVotePenalty(ctx)
	require.ErrorIs(t, err, ErrNotFound)
	var candidates []NativeDuplicateVoteCandidate
	for n := byte(1); n <= MaxNativeDuplicateVoteQueuePage+3; n++ {
		candidates = append(candidates, queuedDuplicateVote(t, n, n, 4))
	}
	added, err := k.enqueueNativeDuplicateVoteCandidates(ctx, candidates)
	require.NoError(t, err)
	require.Equal(t, len(candidates), added)
	for i, candidate := range candidates {
		job, err := k.NextNativeDuplicateVotePenalty(ctx)
		require.NoError(t, err)
		require.Equal(t, candidate, job.NativeDuplicateVoteCandidate)
		if i == MaxNativeDuplicateVoteQueuePage {
			require.NoError(t, k.CompleteNativeDuplicateVotePenalty(ctx, job))
		}
		// Failed or deferred work stays queued. Every subsequent head still
		// becomes reachable, including beyond the original first page.
	}
	reopened, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	wrapped, err := reopened.NextNativeDuplicateVotePenalty(ctx)
	require.NoError(t, err)
	require.Equal(t, candidates[0], wrapped.NativeDuplicateVoteCandidate)
}

func TestNativeDuplicateVoteSchedulerKeepsOperatorAdmissionOrder(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	oldest := queuedDuplicateVote(t, 1, 1, 7)
	_, err := k.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{oldest})
	require.NoError(t, err)
	later := ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(ctx.BlockTime().Add(time.Second))
	backdated := queuedDuplicateVote(t, 1, 1, 2)
	other := queuedDuplicateVote(t, 2, 2, 8)
	_, err = k.enqueueNativeDuplicateVoteCandidates(later, []NativeDuplicateVoteCandidate{other, backdated})
	require.NoError(t, err)
	for _, want := range []NativeDuplicateVoteCandidate{oldest, oldest, other} {
		job, err := k.NextNativeDuplicateVotePenalty(later)
		require.NoError(t, err)
		require.Equal(t, want, job.NativeDuplicateVoteCandidate)
	}
	jobs, err := k.PendingNativeDuplicateVotePenalties(later, 3)
	require.NoError(t, err)
	require.Len(t, jobs, 3)
	require.Equal(t, backdated, jobs[1].NativeDuplicateVoteCandidate)
	require.ErrorContains(t, k.CompleteNativeDuplicateVotePenalty(later, jobs[1]), "first pending liability")
	require.NoError(t, k.CompleteNativeDuplicateVotePenalty(later, jobs[2]))
	require.NoError(t, k.CompleteNativeDuplicateVotePenalty(later, jobs[0]))
	next, err := k.NextNativeDuplicateVotePenalty(later)
	require.NoError(t, err)
	require.Equal(t, backdated, next.NativeDuplicateVoteCandidate)
	require.NoError(t, k.CompleteNativeDuplicateVotePenalty(later, next))
	_, err = k.NextNativeDuplicateVotePenalty(later)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestNativeDuplicateVoteSchedulerCursorSurvivesAttemptRollbackOnly(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	first := queuedDuplicateVote(t, 1, 1, 4)
	second := queuedDuplicateVote(t, 2, 2, 4)
	_, err := k.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{first, second})
	require.NoError(t, err)
	discarded, _ := ctx.CacheContext()
	job, err := k.NextNativeDuplicateVotePenalty(discarded)
	require.NoError(t, err)
	require.Equal(t, first, job.NativeDuplicateVoteCandidate)
	cursor, err := service.OpenKVStore(ctx).Get([]byte{nativeDuplicateVoteCursorKey})
	require.NoError(t, err)
	require.Nil(t, cursor)

	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: nativeDuplicateVoteCursorKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	_, err = broken.NextNativeDuplicateVotePenalty(ctx)
	require.ErrorContains(t, err, "injected write failure")
	outer, commit := ctx.CacheContext()
	job, err = k.NextNativeDuplicateVotePenalty(outer)
	require.NoError(t, err)
	require.Equal(t, first, job.NativeDuplicateVoteCandidate)
	attempt, _ := outer.CacheContext()
	require.NoError(t, k.CompleteNativeDuplicateVotePenalty(attempt, job))
	stillPending, err := k.HasPendingNativeDuplicateVotePenalty(outer, first.Operator)
	require.NoError(t, err)
	require.True(t, stillPending)
	next, err := k.NextNativeDuplicateVotePenalty(outer)
	require.NoError(t, err)
	require.Equal(t, second, next.NativeDuplicateVoteCandidate)
	commit()
	wrapped, err := k.NextNativeDuplicateVotePenalty(ctx)
	require.NoError(t, err)
	require.Equal(t, first, wrapped.NativeDuplicateVoteCandidate)
}
