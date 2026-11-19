package keeper

import (
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func finalizedDuplicateAdmission(t *testing.T) (sdk.Context, *StateKeeper, duplicateVoteMetadataList) {
	t.Helper()
	ctx, k, _ := delProvenanceGenesis(t)
	start := ctx.BlockTime()
	for h := int64(1); h <= 6; h++ {
		ctx = ctx.WithBlockHeight(h).WithBlockTime(start.Add(time.Duration(h-1) * time.Second))
		require.NoError(t, k.RecordNativeEvidenceBlock(ctx))
	}
	ctx = ctx.WithBlockHeight(7).WithBlockTime(start.Add(10 * time.Second)).WithExecMode(sdk.ExecModeFinalize)
	ctx = ctx.WithConsensusParams(cmtproto.ConsensusParams{Evidence: &cmtproto.EvidenceParams{MaxAgeNumBlocks: 100, MaxAgeDuration: time.Hour, MaxBytes: 100000}})
	var input duplicateVoteMetadataList
	for _, id := range []byte{1, 2} {
		v := validator(t, id, 0)
		cons, err := v.GetConsAddr()
		require.NoError(t, err)
		input = append(input, abci.Misbehavior{Type: abci.MisbehaviorType_DUPLICATE_VOTE, Height: 2, Time: start.Add(time.Second), Validator: abci.Validator{Address: cons, Power: 1}, TotalVotingPower: 10})
	}
	return ctx, k, input
}

func TestNativeDuplicateVoteAdmissionExcludesAllKeysBeforeFinancialWork(t *testing.T) {
	ctx, k, input := finalizedDuplicateAdmission(t)
	evidence := func(base sdk.Context, items duplicateVoteMetadataList) sdk.Context {
		return base.WithCometInfo(duplicateVoteBlockInfo{metadata: items})
	}
	branch, _ := ctx.CacheContext()
	added, err := k.EnqueueNativeDuplicateVotePenalties(evidence(branch, input))
	require.NoError(t, err)
	require.Equal(t, 2, added)
	for _, item := range input {
		v, err := k.GetValidatorByConsAddr(branch, item.Validator.Address)
		require.NoError(t, err)
		require.True(t, v.Jailed)
		exclusion, err := k.GetNativeDuplicateVoteExclusion(branch, item.Validator.Address)
		require.NoError(t, err)
		require.Equal(t, int64(2), exclusion.InfractionHeight)
		require.Equal(t, int64(7), exclusion.SettledHeight)
		original, err := k.GetValidatorByConsAddr(ctx, item.Validator.Address)
		require.NoError(t, err)
		require.False(t, original.Jailed, "whole-block rollback reverts early exclusion too")
	}
	added, err = k.EnqueueNativeDuplicateVotePenalties(evidence(ctx, input))
	require.NoError(t, err)
	require.Equal(t, 2, added)
	added, err = k.EnqueueNativeDuplicateVotePenalties(evidence(ctx, input))
	require.NoError(t, err)
	require.Zero(t, added)
	jobs, err := k.PendingNativeDuplicateVotePenalties(ctx, 8)
	require.NoError(t, err)
	require.Len(t, jobs, 2, "admission must not mark any financial liability complete")
	// A later distinct proof for the same key adds a liability, but never
	// replaces the earliest permanent exclusion or makes that key eligible.
	later := input[0]
	later.Height = 3
	later.Time = later.Time.Add(time.Second)
	added, err = k.EnqueueNativeDuplicateVotePenalties(evidence(ctx, duplicateVoteMetadataList{later}))
	require.NoError(t, err)
	require.Equal(t, 1, added)
	exclusion, err := k.GetNativeDuplicateVoteExclusion(ctx, later.Validator.Address)
	require.NoError(t, err)
	require.Equal(t, int64(2), exclusion.InfractionHeight)
	jobs, err = k.PendingNativeDuplicateVotePenalties(ctx, 8)
	require.NoError(t, err)
	require.Len(t, jobs, 3)
	// A failed financial page rolls back its writes, while a previously
	// committed evidence admission keeps all offenders out of consensus.
	failed, _ := ctx.CacheContext()
	require.NoError(t, k.FinalizeNativeDuplicateVotePenalty(failed, jobs[0]))
	left, err := k.PendingNativeDuplicateVotePenalties(ctx, 8)
	require.NoError(t, err)
	require.Equal(t, jobs, left)
	for _, item := range input {
		v, err := k.GetValidatorByConsAddr(ctx, item.Validator.Address)
		require.NoError(t, err)
		require.True(t, v.Jailed)
	}
	require.NoError(t, k.FinalizeNativeDuplicateVotePenalty(ctx, jobs[0]))
	require.ErrorIs(t, k.FinalizeNativeDuplicateVotePenalty(ctx, jobs[0]), ErrNativeDuplicateVoteAlreadyCompleted)
}

func TestNativeDuplicateVoteAdmissionJailFailureRollsBackQueue(t *testing.T) {
	ctx, k, input := finalizedDuplicateAdmission(t)
	broken, err := NewStateKeeperWithDomain(failingService{service: k.service, prefix: validatorPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	_, err = broken.EnqueueNativeDuplicateVotePenalties(ctx.WithCometInfo(duplicateVoteBlockInfo{metadata: input}))
	require.ErrorContains(t, err, "injected write failure")
	jobs, err := k.PendingNativeDuplicateVotePenalties(ctx, 8)
	require.NoError(t, err)
	require.Empty(t, jobs)
	for _, item := range input {
		v, err := k.GetValidatorByConsAddr(ctx, item.Validator.Address)
		require.NoError(t, err)
		require.False(t, v.Jailed)
		_, err = k.GetNativeDuplicateVoteExclusion(ctx, item.Validator.Address)
		require.ErrorIs(t, err, ErrNotFound)
	}
	_, err = k.EnqueueNativeDuplicateVotePenalties(ctx.WithExecMode(sdk.ExecModeCheck).WithCometInfo(duplicateVoteBlockInfo{metadata: input}))
	require.ErrorContains(t, err, "FinalizeBlock")
}
