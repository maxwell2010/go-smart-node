package keeper

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestNativeDuplicateVotePermanentlyExcludesExactKey(t *testing.T) {
	ctx, service, keeper := bindingEnvironment(t, false)
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize)
	offender := validator(t, 2, 0)
	consensus, err := offender.GetConsAddr()
	require.NoError(t, err)
	first := queuedDuplicateVote(t, 2, 2, 4)
	first.ConsensusAddress = consensus
	second := queuedDuplicateVote(t, 2, 2, 5)
	second.ConsensusAddress = consensus
	added, err := keeper.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{first, second})
	require.NoError(t, err)
	require.Equal(t, 2, added)
	jobs, err := keeper.PendingNativeDuplicateVotePenalties(ctx, 2)
	require.NoError(t, err)
	require.Len(t, jobs, 2)

	failed, _ := ctx.CacheContext()
	require.NoError(t, keeper.FinalizeNativeDuplicateVotePenalty(failed, jobs[0]))
	_, err = keeper.GetNativeDuplicateVoteExclusion(ctx, consensus)
	require.ErrorIs(t, err, ErrNotFound)
	stillPending, err := keeper.PendingNativeDuplicateVotePenalties(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, jobs, stillPending)

	require.NoError(t, keeper.FinalizeNativeDuplicateVotePenalty(ctx, jobs[0]))
	require.ErrorIs(t, keeper.FinalizeNativeDuplicateVotePenalty(ctx, jobs[0]), ErrNativeDuplicateVoteAlreadyCompleted)
	require.NoError(t, keeper.FinalizeNativeDuplicateVotePenalty(ctx, jobs[1]))
	reopened, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	exclusion, err := reopened.GetNativeDuplicateVoteExclusion(ctx, consensus)
	require.NoError(t, err)
	require.Equal(t, NativeDuplicateVoteExclusion{InfractionHeight: first.InfractionHeight, SettledHeight: ctx.BlockHeight()}, exclusion)
	old, err := reopened.GetValidator(ctx, first.Operator)
	require.NoError(t, err)
	require.True(t, old.Jailed)
	old.Jailed = false
	require.ErrorIs(t, reopened.SetValidator(ctx, old), ErrNativeConsensusKeyPermanentlyExcluded)
	old.OperatorAddress = addr(t, identity.Bech32PrefixValAddr, 6)
	require.ErrorIs(t, reopened.SetValidator(ctx, old), ErrNativeConsensusKeyPermanentlyExcluded)
	require.NoError(t, reopened.SetValidator(ctx, validator(t, 6, 0)))
}
