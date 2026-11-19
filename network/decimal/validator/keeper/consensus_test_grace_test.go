package keeper

import (
	"bytes"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestNativeGraceStillAuthenticatesAndCheckpointsCommits(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	g.Params.SignedBlocksWindow = 2
	require.NoError(t, k.InitGenesis(ctx, g))
	set, err := ConsensusSet(g.Validators)
	require.NoError(t, err)
	require.NoError(t, k.BootstrapABCIConsensus(ctx, 1, g.Validators, initialABCIUpdates(t, set)))
	eligible := []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress}
	for height := int64(1); height <= 5; height++ {
		commit := cometPreviousCommit(height, 1, set)
		for i := range commit.Votes {
			commit.Votes[i].BlockIdFlag = cmtproto.BlockIDFlagAbsent
		}
		block := ctx.WithExecMode(sdk.ExecModeFinalize).WithBlockHeader(cmtproto.Header{
			ChainID: "native-grace-test", Height: height, Time: time.Unix(2000+height, 0).UTC(),
			NextValidatorsHash: set.Hash(), ProposerAddress: set.Validators[0].Address,
		}).WithHeaderHash(bytes.Repeat([]byte{byte(height)}, 32)).WithVoteInfos(commit.Votes)
		if height == 2 {
			wrong := append(commit.Votes[:0:0], commit.Votes...)
			wrong[0].Validator.Power++
			_, err = k.RecordABCICommitWithGrace(block.WithVoteInfos(wrong), 2)
			require.ErrorContains(t, err, "prior validator address or power")
		}
		_, err = k.RecordABCICommitWithGrace(block, 2)
		require.NoError(t, err)
		_, err = k.RecordABCICommitWithGrace(block, 2)
		require.ErrorContains(t, err, "replayed or skipped")
		if height <= 3 {
			_, err := k.GetSigningWindow(block, eligible[0])
			require.ErrorIs(t, err, ErrNotFound)
		} else {
			window, err := k.GetSigningWindow(block, eligible[0])
			require.NoError(t, err)
			require.EqualValues(t, height-3, window.Samples)
			require.EqualValues(t, height-3, window.Missed)
		}
		plan, err := k.PlanNativeDowntimePenalties(block)
		require.NoError(t, err)
		if height < 5 {
			require.Empty(t, plan)
		} else {
			require.Len(t, plan, 2, "only fresh post-grace misses form a penalty window")
		}
		_, err = k.ScheduledABCIEndBlock(block, eligible)
		require.NoError(t, err)
		if height == 3 {
			k, err = NewStateKeeper(service)
			require.NoError(t, err, "reopen keeper without resetting the checkpoint")
		}
	}
}

func TestNativeGraceVerifiedEvidenceNeverCreatesRetroactiveLiability(t *testing.T) {
	ctx, k, input := finalizedDuplicateAdmission(t)
	old := input[0]
	late := input[1]
	late.Height = 4
	late.Time = late.Time.Add(2 * time.Second)
	proofs := ctx.WithCometInfo(duplicateVoteBlockInfo{metadata: duplicateVoteMetadataList{old, late}})
	added, err := k.EnqueueNativeDuplicateVotePenaltiesWithGrace(proofs, 7)
	require.NoError(t, err)
	require.Zero(t, added)
	added, err = k.EnqueueNativeDuplicateVotePenaltiesWithGrace(proofs, 2)
	require.NoError(t, err)
	require.Equal(t, 1, added)
	jobs, err := k.PendingNativeDuplicateVotePenalties(ctx, 8)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, int64(4), jobs[0].InfractionHeight)
	v, err := k.GetValidatorByConsAddr(ctx, old.Validator.Address)
	require.NoError(t, err)
	require.False(t, v.Jailed)
	_, err = k.GetNativeDuplicateVoteExclusion(ctx, old.Validator.Address)
	require.ErrorIs(t, err, ErrNotFound)
	v, err = k.GetValidatorByConsAddr(ctx, late.Validator.Address)
	require.NoError(t, err)
	require.True(t, v.Jailed, "post-grace infraction permanently excludes the old key")
	invalid := old
	invalid.Validator.Power = invalid.TotalVotingPower + 1
	_, err = k.EnqueueNativeDuplicateVotePenaltiesWithGrace(ctx.WithCometInfo(duplicateVoteBlockInfo{
		metadata: duplicateVoteMetadataList{invalid}}), 7)
	require.ErrorContains(t, err, "invalid Comet duplicate-vote metadata")
	_, err = k.EnqueueNativeDuplicateVotePenaltiesWithGrace(proofs.WithExecMode(sdk.ExecModeCheck), 7)
	require.ErrorContains(t, err, "FinalizeBlock")
}
