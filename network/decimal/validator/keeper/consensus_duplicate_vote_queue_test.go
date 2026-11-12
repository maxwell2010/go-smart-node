package keeper

import (
	"bytes"
	"testing"
	"time"

	cmtcrypto "github.com/cometbft/cometbft/crypto"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func queuedDuplicateVote(t *testing.T, operator, consensus byte, height int64) NativeDuplicateVoteCandidate {
	t.Helper()
	return NativeDuplicateVoteCandidate{
		Operator:         addr(t, identity.Bech32PrefixValAddr, operator),
		ConsensusAddress: sdk.ConsAddress(bytes.Repeat([]byte{consensus}, cmtcrypto.AddressSize)),
		InfractionHeight: height, InfractionTime: time.Unix(1900+height, height).UTC(),
		ObservedPower: 20, TotalVotingPower: 60, PenaltyPercent: HistoricalDELPenaltyPercent,
	}
}

func TestNativeDuplicateVoteQueueOrderAndLimit(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	first := queuedDuplicateVote(t, 2, 2, 4)
	second := queuedDuplicateVote(t, 1, 1, 5)
	third := queuedDuplicateVote(t, 2, 2, 5)
	added, err := k.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{third, second, first})
	require.NoError(t, err)
	require.Equal(t, 3, added)

	later := ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(ctx.BlockTime().Add(time.Second))
	fourth := queuedDuplicateVote(t, 3, 4, 1)
	added, err = k.enqueueNativeDuplicateVoteCandidates(later, []NativeDuplicateVoteCandidate{second, fourth})
	require.NoError(t, err)
	require.Equal(t, 1, added)
	jobs, err := k.PendingNativeDuplicateVotePenalties(later, 2)
	require.NoError(t, err)
	require.Equal(t, []NativeDuplicateVoteJob{
		{NativeDuplicateVoteCandidate: first, AdmittedHeight: ctx.BlockHeight()},
		{NativeDuplicateVoteCandidate: second, AdmittedHeight: ctx.BlockHeight()},
	}, jobs)

	read, _ := later.CacheContext()
	jobs, err = k.PendingNativeDuplicateVotePenalties(read, MaxNativeDuplicateVoteQueuePage)
	require.NoError(t, err)
	require.Len(t, jobs, 4)
	require.Equal(t, third, jobs[2].NativeDuplicateVoteCandidate)
	require.Equal(t, fourth, jobs[3].NativeDuplicateVoteCandidate)
	require.Equal(t, later.BlockHeight(), jobs[3].AdmittedHeight)
	_, err = k.PendingNativeDuplicateVotePenalties(read, MaxNativeDuplicateVoteQueuePage+1)
	require.ErrorContains(t, err, "queue page size")
}

func TestNativeDuplicateVoteQueueCompletesOnceWithOuterCache(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	first := queuedDuplicateVote(t, 2, 1, 4)
	second := queuedDuplicateVote(t, 2, 1, 5)
	added, err := k.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{first, second})
	require.NoError(t, err)
	require.Equal(t, 2, added)
	jobs, err := k.PendingNativeDuplicateVotePenalties(ctx, 2)
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	has, err := k.HasPendingNativeDuplicateVotePenalty(ctx, first.Operator)
	require.NoError(t, err)
	require.True(t, has)

	outer, commit := ctx.CacheContext()
	forged := jobs[0]
	forged.InfractionTime = forged.InfractionTime.Add(time.Nanosecond)
	require.ErrorContains(t, k.CompleteNativeDuplicateVotePenalty(outer, forged), "differs from the queued liability")
	require.NoError(t, k.CompleteNativeDuplicateVotePenalty(outer, jobs[0]))
	has, err = k.HasPendingNativeDuplicateVotePenalty(outer, first.Operator)
	require.NoError(t, err)
	require.True(t, has, "the operator still has a pending job at a different infraction height")
	require.ErrorIs(t, k.CompleteNativeDuplicateVotePenalty(outer, jobs[0]), ErrNativeDuplicateVoteAlreadyCompleted)
	require.NoError(t, k.CompleteNativeDuplicateVotePenalty(outer, jobs[1]))
	has, err = k.HasPendingNativeDuplicateVotePenalty(outer, first.Operator)
	require.NoError(t, err)
	require.False(t, has)

	stillPending, err := k.PendingNativeDuplicateVotePenalties(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, jobs, stillPending, "completing in an outer cache cannot change committed state")
	commit()
	reopened, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	added, err = reopened.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{first, second})
	require.NoError(t, err)
	require.Zero(t, added)
	left, err := reopened.PendingNativeDuplicateVotePenalties(ctx, MaxNativeDuplicateVoteQueuePage)
	require.NoError(t, err)
	require.Empty(t, left)
	require.ErrorIs(t, reopened.CompleteNativeDuplicateVotePenalty(ctx, jobs[1]), ErrNativeDuplicateVoteAlreadyCompleted)
}

func TestNativeDuplicateVoteQueueAtomicWrites(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	first := queuedDuplicateVote(t, 2, 1, 4)
	conflict := first
	conflict.ObservedPower++
	added, err := k.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{first, conflict})
	require.ErrorContains(t, err, "conflicting native duplicate-vote liability")
	require.Zero(t, added)
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: nativeDuplicateVoteOperatorPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	_, err = broken.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{first})
	require.ErrorContains(t, err, "injected write failure")
	read, _ := ctx.CacheContext()
	jobs, err := k.PendingNativeDuplicateVotePenalties(read, 1)
	require.NoError(t, err)
	require.Empty(t, jobs)
	has, err := k.HasPendingNativeDuplicateVotePenalty(read, first.Operator)
	require.NoError(t, err)
	require.False(t, has)

	added, err = k.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{first})
	require.NoError(t, err)
	require.Equal(t, 1, added)
	jobs, err = k.PendingNativeDuplicateVotePenalties(ctx, 1)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	broken, err = NewStateKeeperWithDomain(failingService{service: service, prefix: nativeDuplicateVoteOperatorPrefix, deleteOnly: true}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.CompleteNativeDuplicateVotePenalty(ctx, jobs[0]), "injected delete failure")
	retained, err := k.PendingNativeDuplicateVotePenalties(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, jobs, retained)
	has, err = k.HasPendingNativeDuplicateVotePenalty(ctx, first.Operator)
	require.NoError(t, err)
	require.True(t, has)
	require.NoError(t, k.CompleteNativeDuplicateVotePenalty(ctx, jobs[0]))
}
