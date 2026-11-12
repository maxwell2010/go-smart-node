package keeper

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"cosmossdk.io/core/comet"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

type duplicateVoteMetadata struct{ abci.Misbehavior }

func (m duplicateVoteMetadata) Type() comet.MisbehaviorType {
	return comet.MisbehaviorType(m.Misbehavior.Type)
}
func (m duplicateVoteMetadata) Validator() comet.Validator {
	return duplicateVoteValidator{m.Misbehavior.Validator}
}
func (m duplicateVoteMetadata) Height() int64           { return m.Misbehavior.Height }
func (m duplicateVoteMetadata) Time() time.Time         { return m.Misbehavior.Time }
func (m duplicateVoteMetadata) TotalVotingPower() int64 { return m.Misbehavior.TotalVotingPower }

type duplicateVoteValidator struct{ abci.Validator }

func (v duplicateVoteValidator) Address() []byte { return v.Validator.Address }
func (v duplicateVoteValidator) Power() int64    { return v.Validator.Power }

type duplicateVoteMetadataList []abci.Misbehavior

func (l duplicateVoteMetadataList) Len() int                 { return len(l) }
func (l duplicateVoteMetadataList) Get(i int) comet.Evidence { return duplicateVoteMetadata{l[i]} }

type duplicateVoteBlockInfo struct {
	comet.BlockInfo
	metadata duplicateVoteMetadataList
}

func (b duplicateVoteBlockInfo) GetEvidence() comet.EvidenceList { return b.metadata }

func TestPlanNativeDuplicateVotePenalties(t *testing.T) {
	ctx, k, _ := delProvenanceGenesis(t)
	genesisTime := ctx.BlockTime()
	blockOffsets := []time.Duration{0, 1, 2, 14, 15, 16}
	for i, seconds := range blockOffsets {
		ctx = ctx.WithBlockHeight(int64(i + 1)).WithBlockTime(genesisTime.Add(seconds * time.Second))
		require.NoError(t, k.RecordNativeEvidenceBlock(ctx))
	}
	ctx = ctx.WithBlockHeight(7).WithBlockTime(genesisTime.Add(30 * time.Second)).WithExecMode(sdk.ExecModeFinalize)
	limits := &cmtproto.EvidenceParams{MaxAgeNumBlocks: 3, MaxAgeDuration: 15 * time.Second}
	ctx = ctx.WithConsensusParams(cmtproto.ConsensusParams{Evidence: limits})
	first := validator(t, 1, 0)
	firstAddr, err := first.GetConsAddr()
	require.NoError(t, err)
	second := validator(t, 2, 0)
	secondAddr, err := second.GetConsAddr()
	require.NoError(t, err)
	_, err = k.GetHistoricalInfo(ctx, 2)
	require.ErrorIs(t, err, ErrNotFound, "authenticated evidence survives missing native history")
	makeEvidence := func(height int64, address sdk.ConsAddress, power int64) abci.Misbehavior {
		return abci.Misbehavior{Type: abci.MisbehaviorType_DUPLICATE_VOTE, Height: height,
			Time: genesisTime.Add(blockOffsets[height-1] * time.Second), Validator: abci.Validator{Address: address, Power: power}, TotalVotingPower: 10}
	}
	oldFirst := makeEvidence(2, firstAddr, 4)
	oldSecond := makeEvidence(2, secondAddr, 6)
	blockEdge := makeEvidence(3, firstAddr, 4)
	latest := makeEvidence(6, firstAddr, 4)
	expired := makeEvidence(1, sdk.ConsAddress(bytes.Repeat([]byte{0xff}, 20)), 4)
	ignored := abci.Misbehavior{Type: abci.MisbehaviorType_LIGHT_CLIENT_ATTACK}
	input := duplicateVoteMetadataList{latest, oldSecond, expired, oldFirst, blockEdge, ignored, oldSecond}
	plan := func(input duplicateVoteMetadataList) ([]NativeDuplicateVoteCandidate, error) {
		return k.PlanNativeDuplicateVotePenalties(ctx.WithCometInfo(duplicateVoteBlockInfo{metadata: input}))
	}
	got, err := plan(input)
	require.NoError(t, err)
	want := []NativeDuplicateVoteCandidate{
		{Operator: first.OperatorAddress, ConsensusAddress: firstAddr, InfractionHeight: 2, InfractionTime: oldFirst.Time.UTC(), ObservedPower: 4, TotalVotingPower: 10, PenaltyPercent: 5},
		{Operator: second.OperatorAddress, ConsensusAddress: secondAddr, InfractionHeight: 2, InfractionTime: oldSecond.Time.UTC(), ObservedPower: 6, TotalVotingPower: 10, PenaltyPercent: 5},
		{Operator: first.OperatorAddress, ConsensusAddress: firstAddr, InfractionHeight: 3, InfractionTime: blockEdge.Time.UTC(), ObservedPower: 4, TotalVotingPower: 10, PenaltyPercent: 5},
		{Operator: first.OperatorAddress, ConsensusAddress: firstAddr, InfractionHeight: 6, InfractionTime: latest.Time.UTC(), ObservedPower: 4, TotalVotingPower: 10, PenaltyPercent: 5},
	}
	if bytes.Compare(firstAddr, secondAddr) > 0 {
		want[0], want[1] = want[1], want[0]
	}
	require.Equal(t, want, got)
	reversed := slices.Clone(input)
	slices.Reverse(reversed)
	got, err = plan(reversed)
	require.NoError(t, err)
	require.Equal(t, want, got)

	shorter := ctx.WithConsensusParams(cmtproto.ConsensusParams{Evidence: &cmtproto.EvidenceParams{MaxAgeNumBlocks: 3, MaxAgeDuration: 13 * time.Second}})
	got, err = k.PlanNativeDuplicateVotePenalties(shorter.WithCometInfo(duplicateVoteBlockInfo{metadata: input}))
	require.NoError(t, err)
	require.Equal(t, want[2:], got, "height equality remains valid even after the time limit")

	t.Run("same previous height requires exact committed time", func(t *testing.T) {
		bad := latest
		bad.Time = bad.Time.Add(time.Nanosecond)
		got, err := plan(duplicateVoteMetadataList{bad})
		require.ErrorContains(t, err, "conflicting times for the same committed block")
		require.Empty(t, got)
	})
	t.Run("execution height is still uncommitted", func(t *testing.T) {
		bad := latest
		bad.Height, bad.Time = ctx.BlockHeight(), ctx.BlockTime()
		_, err := plan(duplicateVoteMetadataList{bad})
		require.ErrorContains(t, err, "committed subject block required")
	})
	t.Run("conflicting duplicates fail closed", func(t *testing.T) {
		bad := oldFirst
		bad.Validator.Power++
		got, err := plan(duplicateVoteMetadataList{oldFirst, bad})
		require.ErrorContains(t, err, "conflicting duplicate-vote power")
		require.Empty(t, got)
		bad = oldSecond
		bad.Time = bad.Time.Add(time.Nanosecond)
		_, err = plan(duplicateVoteMetadataList{oldFirst, bad})
		require.ErrorContains(t, err, "conflicting duplicate-vote metadata for the same block")
	})
	t.Run("valid age still requires native identity and voting power", func(t *testing.T) {
		bad := oldFirst
		bad.Validator.Address = expired.Validator.Address
		_, err := plan(duplicateVoteMetadataList{bad})
		require.ErrorIs(t, err, ErrNotFound)
		bad = oldFirst
		bad.Validator.Power = bad.TotalVotingPower + 1
		_, err = plan(duplicateVoteMetadataList{bad})
		require.ErrorContains(t, err, "invalid Comet duplicate-vote metadata")
	})
}
