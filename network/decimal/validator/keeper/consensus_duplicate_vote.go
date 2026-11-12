package keeper

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"cosmossdk.io/core/comet"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// NativeDuplicateVoteCandidate is one validated duplicate-vote liability
// from the finalized block's Comet metadata.
type NativeDuplicateVoteCandidate struct {
	Operator         string
	ConsensusAddress sdk.ConsAddress
	InfractionHeight int64
	InfractionTime   time.Time
	ObservedPower    int64
	TotalVotingPower int64
	PenaltyPercent   uint16
}

// PlanNativeDuplicateVotePenalties reads the Comet-authenticated evidence in
// the FinalizeBlock context. Use the incoming consensus parameters, before
// any PreBlock upgrade changes them. Older metadata remains valid when the
// app has already pruned its copy of the historical validator set.
func (k *StateKeeper) PlanNativeDuplicateVotePenalties(ctx sdk.Context) ([]NativeDuplicateVoteCandidate, error) {
	if ctx.ExecMode() != sdk.ExecModeFinalize || ctx.CometInfo() == nil {
		return nil, errors.New("native duplicate-vote planning requires Comet FinalizeBlock metadata")
	}
	evidence := ctx.CometInfo().GetEvidence()
	if evidence == nil {
		return nil, nil
	}
	var previous CommittedEvidenceBlock
	var candidates []NativeDuplicateVoteCandidate
	limits := ctx.ConsensusParams().Evidence
	for i := range evidence.Len() {
		item := evidence.Get(i)
		if item == nil {
			return nil, errors.New("missing Comet misbehavior metadata")
		}
		if item.Type() != comet.DuplicateVote {
			continue
		}
		if previous.Height == 0 {
			var err error
			previous, err = k.PreviousNativeEvidenceBlock(ctx)
			if err != nil {
				return nil, err
			}
			if limits == nil {
				return nil, errors.New("missing Comet evidence age limits")
			}
		}
		subject := CommittedEvidenceBlock{Height: item.Height(), Time: item.Time().UTC()}
		observed := item.Validator()
		if observed == nil || len(observed.Address()) != 20 || observed.Power() <= 0 ||
			observed.Power() > item.TotalVotingPower() || subject.Time.Unix() <= 0 {
			return nil, errors.New("invalid Comet duplicate-vote metadata")
		}
		expired, err := nativeEvidenceWindowElapsed(ctx.BlockHeight(), previous, subject, limits.MaxAgeNumBlocks, limits.MaxAgeDuration)
		if err != nil {
			return nil, fmt.Errorf("check Comet duplicate-vote age: %w", err)
		}
		if expired {
			continue
		}
		consensus := sdk.ConsAddress(bytes.Clone(observed.Address()))
		validator, err := k.GetValidatorByConsAddr(ctx, consensus)
		if err != nil {
			return nil, fmt.Errorf("resolve duplicate-vote native consensus identity: %w", err)
		}
		candidates = append(candidates, NativeDuplicateVoteCandidate{
			Operator: validator.OperatorAddress, ConsensusAddress: consensus,
			InfractionHeight: subject.Height, InfractionTime: subject.Time,
			ObservedPower: observed.Power(), TotalVotingPower: item.TotalVotingPower(), PenaltyPercent: HistoricalDELPenaltyPercent,
		})
	}

	slices.SortFunc(candidates, func(a, b NativeDuplicateVoteCandidate) int {
		if order := cmp.Compare(a.InfractionHeight, b.InfractionHeight); order != 0 {
			return order
		}
		return bytes.Compare(a.ConsensusAddress, b.ConsensusAddress)
	})
	unique := candidates[:0]
	for _, candidate := range candidates {
		if len(unique) > 0 {
			last := unique[len(unique)-1]
			if last.InfractionHeight == candidate.InfractionHeight {
				if !last.InfractionTime.Equal(candidate.InfractionTime) || last.TotalVotingPower != candidate.TotalVotingPower {
					return nil, errors.New("conflicting duplicate-vote metadata for the same block")
				}
				if bytes.Equal(last.ConsensusAddress, candidate.ConsensusAddress) {
					if last.ObservedPower != candidate.ObservedPower {
						return nil, errors.New("conflicting duplicate-vote power for the same validator and block")
					}
					continue
				}
			}
		}
		unique = append(unique, candidate)
	}
	return unique, nil
}
