package finality

import (
	"bytes"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/crypto/tmhash"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmtversion "github.com/cometbft/cometbft/proto/tendermint/version"
	"github.com/cometbft/cometbft/types"
	"github.com/cometbft/cometbft/version"
)

type testKeys map[string]ed25519.PrivKey

func testValidators(t *testing.T, names []string, powers []int64) (*types.ValidatorSet, testKeys) {
	t.Helper()
	keys := make(testKeys)
	validators := make([]*types.Validator, len(names))
	for index, name := range names {
		private := ed25519.GenPrivKeyFromSecret([]byte("disposable-light-client-test/" + name))
		public := private.PubKey()
		keys[string(public.Address())] = private
		validators[index] = types.NewValidator(public, powers[index])
	}
	return types.NewValidatorSet(validators), keys
}

func testBlockID(text string) types.BlockID {
	return types.BlockID{Hash: tmhash.Sum([]byte(text)),
		PartSetHeader: types.PartSetHeader{Total: 1, Hash: tmhash.Sum([]byte("parts/" + text))}}
}

func signTestBlock(t *testing.T, block *types.LightBlock, keys testKeys, chainID string,
	absent, nilVotes map[int]bool) {
	t.Helper()
	block.Commit = &types.Commit{Height: block.Height, Round: 2,
		BlockID: types.BlockID{Hash: block.Header.Hash(),
			PartSetHeader: types.PartSetHeader{Total: 1, Hash: tmhash.Sum([]byte(fmt.Sprint("parts/", block.Height)))}}}
	for index, validator := range block.ValidatorSet.Validators {
		if absent[index] {
			block.Commit.Signatures = append(block.Commit.Signatures, types.NewCommitSigAbsent())
			continue
		}
		flag := types.BlockIDFlagCommit
		blockID := block.Commit.BlockID
		if nilVotes[index] {
			flag, blockID = types.BlockIDFlagNil, types.BlockID{}
		}
		stamp := block.Time.Add(time.Duration(index) * time.Millisecond)
		vote := cmtproto.Vote{Type: cmtproto.PrecommitType, Height: block.Height,
			Round: block.Commit.Round, BlockID: blockID.ToProto(), Timestamp: stamp}
		private := keys[string(validator.Address)]
		signature, err := private.Sign(types.VoteSignBytes(chainID, &vote))
		if err != nil {
			t.Fatal(err)
		}
		block.Commit.Signatures = append(block.Commit.Signatures, types.CommitSig{
			BlockIDFlag: flag, ValidatorAddress: validator.Address, Timestamp: stamp, Signature: signature})
	}
}

func testBlock(t *testing.T, height int64, stamp time.Time, vals, next *types.ValidatorSet,
	keys testKeys, previous types.BlockID) *types.LightBlock {
	t.Helper()
	block := &types.LightBlock{SignedHeader: &types.SignedHeader{Header: &types.Header{
		Version: cmtversion.Consensus{Block: version.BlockProtocol}, ChainID: "bridge_fixture-1",
		Height: height, Time: stamp, LastBlockID: previous,
		LastCommitHash: tmhash.Sum([]byte("last-commit")), DataHash: tmhash.Sum([]byte("data")),
		ValidatorsHash: vals.Hash(), NextValidatorsHash: next.Hash(), ConsensusHash: tmhash.Sum([]byte("params")),
		AppHash:         tmhash.Sum([]byte(fmt.Sprint("application-state/", height))),
		LastResultsHash: tmhash.Sum([]byte("results")), EvidenceHash: tmhash.Sum([]byte("evidence")),
		ProposerAddress: vals.Proposer.Address,
	}}, ValidatorSet: vals}
	signTestBlock(t, block, keys, block.ChainID, nil, nil)
	return block
}

func testFixture(t *testing.T) (Checkpoint, *types.LightBlock, *types.LightBlock, time.Time, Policy, testKeys) {
	t.Helper()
	vals, keys := testValidators(t, []string{"a", "b", "c", "d"}, []int64{1, 1, 1, 1})
	base := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	trusted := testBlock(t, 10, base, vals, vals, keys, testBlockID("block9"))
	candidateVals, _ := testValidators(t, []string{"a", "b", "c", "d"}, []int64{1, 1, 1, 1})
	candidate := testBlock(t, 11, base.Add(time.Minute), candidateVals, candidateVals, keys, trusted.Commit.BlockID)
	anchor := Checkpoint{ChainID: trusted.ChainID, Height: trusted.Height}
	copy(anchor.Hash[:], trusted.Hash())
	policy := Policy{ChainID: trusted.ChainID, TrustingPeriod: time.Hour,
		UnbondingPeriod: 7 * 24 * time.Hour, MaxClockDrift: 30 * time.Second, MaxValidators: 64}
	return anchor, trusted, candidate, base.Add(5 * time.Minute), policy, keys
}

func mustReject(t *testing.T, anchor Checkpoint, trusted, candidate *types.LightBlock,
	now time.Time, policy Policy) {
	t.Helper()
	got, err := VerifyUpdate(anchor, trusted, candidate, now, policy)
	if err == nil || got != (VerifiedCheckpoint{}) {
		t.Fatalf("expected error and zero checkpoint, got %+v, %v", got, err)
	}
}

func TestAdjacentAndSkippingUpdates(t *testing.T) {
	for _, height := range []int64{11, 12, 100} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			anchor, trusted, candidate, now, policy, keys := testFixture(t)
			candidate.Header.Height = height
			signTestBlock(t, candidate, keys, policy.ChainID, nil, nil)
			got, err := VerifyUpdate(anchor, trusted, candidate, now, policy)
			if err != nil || got.Height != height || !bytes.Equal(got.Hash[:], candidate.Hash()) ||
				!bytes.Equal(got.AppHash[:], candidate.AppHash) || got.Time != candidate.Time {
				t.Fatalf("valid light update failed: %+v, %v", got, err)
			}
			candidate.AppHash[0] ^= 1
			if bytes.Equal(got.AppHash[:], candidate.AppHash) {
				t.Fatal("verified checkpoint aliases mutable candidate")
			}
		})
	}
}

func TestRotatedValidatorSetAdjacentAndTrustedOverlap(t *testing.T) {
	for _, mode := range []string{"adjacent_rotation", "skip_with_overlap", "skip_without_overlap"} {
		t.Run(mode, func(t *testing.T) {
			anchor, trusted, _, now, policy, _ := testFixture(t)
			names := []string{"e", "f", "g", "h"}
			if mode == "skip_with_overlap" {
				names = []string{"a", "b", "g", "h"}
			}
			vals, keys := testValidators(t, names, []int64{1, 1, 1, 1})
			height := int64(12)
			if mode == "adjacent_rotation" {
				// The new keys are authenticated by the pinned old header's next hash.
				height = 11
				_, _, _, _, _, oldKeys := testFixture(t)
				trusted.NextValidatorsHash = vals.Hash()
				signTestBlock(t, trusted, oldKeys, policy.ChainID, nil, nil)
				copy(anchor.Hash[:], trusted.Hash())
			}
			candidate := testBlock(t, height, trusted.Time.Add(time.Minute), vals, vals, keys, trusted.Commit.BlockID)
			if mode == "skip_without_overlap" {
				mustReject(t, anchor, trusted, candidate, now, policy)
			} else if _, err := VerifyUpdate(anchor, trusted, candidate, now, policy); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIntermediateHeaderAuthenticatesCompleteRotation(t *testing.T) {
	anchor, trusted, _, now, policy, oldKeys := testFixture(t)
	newVals, newKeys := testValidators(t, []string{"e", "f", "g", "h"}, []int64{1, 1, 1, 1})
	middle := testBlock(t, 11, trusted.Time.Add(time.Minute), trusted.ValidatorSet, newVals, oldKeys, trusted.Commit.BlockID)
	verified, err := VerifyUpdate(anchor, trusted, middle, now, policy)
	if err != nil {
		t.Fatal(err)
	}
	last := testBlock(t, 12, middle.Time.Add(time.Minute), newVals, newVals, newKeys, middle.Commit.BlockID)
	if _, err := VerifyUpdate(verified.Checkpoint, middle, last, now, policy); err != nil {
		t.Fatal(err)
	}
}

func TestStaleCachedVotingPowerDoesNotAuthorizeMinority(t *testing.T) {
	anchor, trusted, candidate, now, policy, keys := testFixture(t)
	if candidate.ValidatorSet.TotalVotingPower() != 4 {
		t.Fatal("unexpected fixture cache")
	}
	// Public Go fields can change while Comet's private power cache stays old.
	// The verifier must reconstruct the actual total rather than trusting it.
	candidate.ValidatorSet.Validators[0].VotingPower = 10
	candidate.ValidatorsHash = candidate.ValidatorSet.Hash()
	candidate.NextValidatorsHash = candidate.ValidatorSet.Hash()
	trusted.NextValidatorsHash = candidate.ValidatorsHash
	signTestBlock(t, trusted, keys, policy.ChainID, nil, nil)
	copy(anchor.Hash[:], trusted.Hash())
	candidate.LastBlockID = trusted.Commit.BlockID
	signTestBlock(t, candidate, keys, policy.ChainID, map[int]bool{0: true}, nil)
	mustReject(t, anchor, trusted, candidate, now, policy)
}

func TestQuorumAndAllPresentSignatures(t *testing.T) {
	for _, mode := range []string{"three_of_four", "two_of_four", "forged_tail", "nil_valid", "nil_forged", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			anchor, trusted, candidate, now, policy, keys := testFixture(t)
			absent, nilVotes := map[int]bool{}, map[int]bool{}
			switch mode {
			case "three_of_four":
				absent[3] = true
			case "two_of_four":
				absent[2], absent[3] = true, true
			case "nil_valid", "nil_forged":
				nilVotes[3] = true
			}
			signTestBlock(t, candidate, keys, policy.ChainID, absent, nilVotes)
			if mode == "forged_tail" || mode == "nil_forged" {
				candidate.Commit.Signatures[3].Signature[0] ^= 1
			}
			if mode == "duplicate" {
				candidate.Commit.Signatures[1] = candidate.Commit.Signatures[0]
			}
			if mode == "three_of_four" || mode == "nil_valid" {
				if _, err := VerifyUpdate(anchor, trusted, candidate, now, policy); err != nil {
					t.Fatal(err)
				}
			} else {
				mustReject(t, anchor, trusted, candidate, now, policy)
			}
		})
	}
}

func TestExactlyTwoThirdsAndLowWeightMajorityFail(t *testing.T) {
	for _, powers := range [][]int64{{2, 2, 1, 1}, {91, 3, 3, 3}} {
		anchor, trusted, _, now, policy, _ := testFixture(t)
		vals, keys := testValidators(t, []string{"a", "b", "c", "d"}, powers)
		trusted.ValidatorSet = vals
		trusted.ValidatorsHash, trusted.NextValidatorsHash = vals.Hash(), vals.Hash()
		signTestBlock(t, trusted, keys, policy.ChainID, nil, nil)
		copy(anchor.Hash[:], trusted.Hash())
		candidate := testBlock(t, 11, trusted.Time.Add(time.Minute), vals, vals, keys, trusted.Commit.BlockID)
		absent := map[int]bool{2: true, 3: true}
		if powers[0] == 91 {
			absent = map[int]bool{0: true}
		}
		signTestBlock(t, candidate, keys, policy.ChainID, absent, nil)
		mustReject(t, anchor, trusted, candidate, now, policy)
	}
}

func TestRejectExpiredFutureAndNonMonotonicUpdates(t *testing.T) {
	for _, mode := range []string{"expiry_boundary", "future_boundary", "future", "same_time", "earlier_time", "same_height", "earlier_height"} {
		t.Run(mode, func(t *testing.T) {
			anchor, trusted, candidate, now, policy, keys := testFixture(t)
			switch mode {
			case "expiry_boundary":
				now = trusted.Time.Add(policy.TrustingPeriod)
			case "future_boundary":
				candidate.Time = now.Add(policy.MaxClockDrift)
			case "future":
				candidate.Time = now.Add(time.Hour)
			case "same_time":
				candidate.Time = trusted.Time
			case "earlier_time":
				candidate.Time = trusted.Time.Add(-time.Second)
			case "same_height":
				candidate.Header.Height = trusted.Height
			case "earlier_height":
				candidate.Header.Height = trusted.Height - 1
			}
			signTestBlock(t, candidate, keys, policy.ChainID, nil, nil)
			mustReject(t, anchor, trusted, candidate, now, policy)
		})
	}
}

func TestRejectTamperingAndUnpinnedBootstrap(t *testing.T) {
	for _, mode := range []string{"anchor_hash", "anchor_height", "anchor_chain", "zero_anchor", "trusted_signature", "header_apphash", "commit_block", "chain", "signature_domain", "next_set", "previous_block"} {
		t.Run(mode, func(t *testing.T) {
			anchor, trusted, candidate, now, policy, keys := testFixture(t)
			switch mode {
			case "anchor_hash":
				anchor.Hash[0] ^= 1
			case "anchor_height":
				anchor.Height++
			case "anchor_chain":
				anchor.ChainID = "wrong-1"
			case "zero_anchor":
				anchor = Checkpoint{}
			case "trusted_signature":
				trusted.Commit.Signatures[0].Signature[0] ^= 1
			case "header_apphash":
				candidate.AppHash[0] ^= 1
			case "commit_block":
				candidate.Commit.BlockID.Hash[0] ^= 1
			case "chain":
				candidate.Header.ChainID = "wrong-1"
			case "signature_domain":
				signTestBlock(t, candidate, keys, "wrong-1", nil, nil)
			case "next_set":
				vals, newKeys := testValidators(t, []string{"e", "f", "g", "h"}, []int64{1, 1, 1, 1})
				candidate = testBlock(t, 11, candidate.Time, vals, vals, newKeys, trusted.Commit.BlockID)
			case "previous_block":
				candidate.LastBlockID = testBlockID("other-history")
				signTestBlock(t, candidate, keys, policy.ChainID, nil, nil)
			}
			mustReject(t, anchor, trusted, candidate, now, policy)
		})
	}
}

func TestRejectNilMalformedAndUnboundedInputs(t *testing.T) {
	for _, mode := range []string{"nil", "nil_signed", "nil_header", "nil_commit", "nil_set", "nil_validator", "nil_key", "short_key", "nil_proposer", "duplicate", "address", "zero_power", "overflow", "too_many", "large_signature", "large_hash", "empty_apphash", "bad_count", "signature_time", "utc_time_overflow"} {
		t.Run(mode, func(t *testing.T) {
			anchor, trusted, candidate, now, policy, _ := testFixture(t)
			switch mode {
			case "nil":
				candidate = nil
			case "nil_signed":
				candidate.SignedHeader = nil
			case "nil_header":
				candidate.Header = nil
			case "nil_commit":
				candidate.Commit = nil
			case "nil_set":
				candidate.ValidatorSet = nil
			case "nil_validator":
				candidate.ValidatorSet.Validators[0] = nil
			case "nil_key":
				candidate.ValidatorSet.Validators[0].PubKey = nil
			case "short_key":
				candidate.ValidatorSet.Validators[0].PubKey = ed25519.PubKey{1}
			case "nil_proposer":
				candidate.ValidatorSet.Proposer = nil
			case "duplicate":
				candidate.ValidatorSet.Validators[1] = candidate.ValidatorSet.Validators[0]
			case "address":
				candidate.ValidatorSet.Validators[0].Address = bytes.Repeat([]byte{1}, 20)
			case "zero_power":
				candidate.ValidatorSet.Validators[0].VotingPower = 0
			case "overflow":
				candidate.ValidatorSet.Validators[0].VotingPower = math.MaxInt64
			case "too_many":
				policy.MaxValidators = 3
			case "large_signature":
				candidate.Commit.Signatures[0].Signature = make([]byte, 65)
			case "large_hash":
				candidate.NextValidatorsHash = make([]byte, 100)
			case "empty_apphash":
				candidate.AppHash = nil
			case "bad_count":
				candidate.Commit.Signatures = candidate.Commit.Signatures[:3]
			case "signature_time":
				candidate.Commit.Signatures[0].Timestamp = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
			case "utc_time_overflow":
				candidate.Header.Time = time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("offset", 14*60*60))
			}
			mustReject(t, anchor, trusted, candidate, now, policy)
		})
	}
}

func TestRejectInvalidPolicyAndTime(t *testing.T) {
	for _, mode := range []string{"chain", "zero_trust", "negative_trust", "trust_equals_unbonding", "negative_drift", "zero_drift", "unbounded_drift", "zero_limit", "excess_limit", "zero_now"} {
		t.Run(mode, func(t *testing.T) {
			anchor, trusted, candidate, now, policy, _ := testFixture(t)
			switch mode {
			case "chain":
				policy.ChainID = "wrong chain"
			case "zero_trust":
				policy.TrustingPeriod = 0
			case "negative_trust":
				policy.TrustingPeriod = -1
			case "trust_equals_unbonding":
				policy.TrustingPeriod = policy.UnbondingPeriod
			case "negative_drift":
				policy.MaxClockDrift = -1
			case "zero_drift":
				policy.MaxClockDrift = 0
			case "unbounded_drift":
				policy.MaxClockDrift = policy.TrustingPeriod
			case "zero_limit":
				policy.MaxValidators = 0
			case "excess_limit":
				policy.MaxValidators = HardMaxValidators + 1
			case "zero_now":
				now = time.Time{}
			}
			mustReject(t, anchor, trusted, candidate, now, policy)
		})
	}
}
