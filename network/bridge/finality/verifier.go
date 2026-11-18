// Package finality verifies CometBFT light-client updates against an explicitly
// trusted checkpoint. It does not fetch RPC data, initialize trust from an RPC,
// persist state, authorize transfers, or prove application-state membership.
//
// This initial profile accepts Ed25519 consensus validators only. Production
// callers must provision and authenticate the initial checkpoint and policy out
// of band, then persist successful updates atomically in trusted storage. They
// must also provide independent fork/misbehaviour detection: a single successful
// update does not exclude a conflicting validly signed header.
package finality

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	"github.com/cometbft/cometbft/light"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
)

const (
	// HardMaxValidators bounds CPU and memory before library verification.
	HardMaxValidators  = 1000
	maxLightBlockBytes = 1024 * 1024
)

var chainIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,49}$`)

// Checkpoint is an external trust assertion, not a claim made by RPC data.
// Hash is the SHA256 Comet header hash, not an EVM block or transaction hash.
// Constructing this value does not establish trust: callers must authenticate
// its chain, height and hash through their explicit bootstrap procedure.
type Checkpoint struct {
	ChainID string
	Height  int64
	Hash    [32]byte
}

// VerifiedCheckpoint is a successful transition from the supplied trust anchor.
// AppHash is a commitment only; a state-proof layer must check membership and
// the correct ABCI/header height relationship before acting on application data.
type VerifiedCheckpoint struct {
	Checkpoint
	Time               time.Time
	AppHash            [32]byte
	NextValidatorsHash [32]byte
}

// Policy is externally authenticated chain configuration. UnbondingPeriod must
// come from trusted chain rules; this verifier does not discover it from RPC.
// TrustingPeriod must be strictly shorter than UnbondingPeriod. Validator-set
// skipping uses the fixed Comet trust level 1/3, never a caller-lowered threshold.
type Policy struct {
	ChainID         string
	TrustingPeriod  time.Duration
	UnbondingPeriod time.Duration
	MaxClockDrift   time.Duration
	MaxValidators   int
}

func (p Policy) validate() error {
	if !chainIDPattern.MatchString(p.ChainID) {
		return errors.New("invalid policy chain ID")
	}
	if p.TrustingPeriod <= 0 || p.UnbondingPeriod <= p.TrustingPeriod ||
		p.MaxClockDrift <= 0 || p.MaxClockDrift >= p.TrustingPeriod {
		return errors.New("require positive clock drift < trusting period < unbonding period")
	}
	if p.MaxValidators < 1 || p.MaxValidators > HardMaxValidators {
		return errors.New("validator bound exceeds the supported profile")
	}
	return nil
}

func boundedValidator(v *types.Validator) error {
	if v == nil || v.PubKey == nil {
		return errors.New("nil validator or public key")
	}
	key, ok := v.PubKey.(ed25519.PubKey)
	if !ok || len(key) != ed25519.PubKeySize {
		return errors.New("only 32-byte Ed25519 consensus keys are supported")
	}
	if v.VotingPower <= 0 || v.VotingPower > types.MaxTotalVotingPower ||
		len(v.Address) != 20 || !bytes.Equal(v.Address, key.Address()) {
		return errors.New("invalid validator power or address")
	}
	return nil
}

// checkedCopy validates sizes and identities before calling code which hashes
// validator sets. The protobuf roundtrip deep-copies the input and recomputes
// validator power, rather than trusting mutable/cached totals in supplied Go
// objects. The caller must not concurrently mutate input during this function.
func checkedCopy(block *types.LightBlock, policy Policy) (*types.LightBlock, error) {
	if block == nil || block.SignedHeader == nil || block.Header == nil ||
		block.Commit == nil || block.ValidatorSet == nil {
		return nil, errors.New("incomplete light block")
	}
	h, commit, vals := block.Header, block.Commit, block.ValidatorSet
	if len(vals.Validators) < 1 || len(vals.Validators) > policy.MaxValidators ||
		len(commit.Signatures) != len(vals.Validators) {
		return nil, errors.New("validator or commit-signature count is outside the bounded profile")
	}
	if h.ChainID != policy.ChainID || h.Height <= 0 || h.Time.IsZero() ||
		h.Time.UTC().Year() < 1 || h.Time.UTC().Year() > 9999 || len(h.ProposerAddress) != 20 {
		return nil, errors.New("invalid light-block chain, height, time or proposer")
	}
	for _, hash := range [][]byte{h.ValidatorsHash, h.NextValidatorsHash, h.ConsensusHash, h.AppHash} {
		if len(hash) != 32 {
			return nil, errors.New("required SHA256 header commitment must have 32 bytes")
		}
	}
	for _, hash := range [][]byte{h.LastBlockID.Hash, h.LastBlockID.PartSetHeader.Hash,
		h.LastCommitHash, h.DataHash, h.LastResultsHash, h.EvidenceHash,
		commit.BlockID.Hash, commit.BlockID.PartSetHeader.Hash} {
		if len(hash) != 0 && len(hash) != 32 {
			return nil, errors.New("invalid header or commit hash length")
		}
	}
	seen := make(map[string]bool, len(vals.Validators))
	var total int64
	for _, validator := range vals.Validators {
		if err := boundedValidator(validator); err != nil {
			return nil, err
		}
		identity := string(validator.Address)
		if seen[identity] || total > types.MaxTotalVotingPower-validator.VotingPower {
			return nil, errors.New("duplicate validator or excessive total voting power")
		}
		seen[identity] = true
		total += validator.VotingPower
	}
	if err := boundedValidator(vals.Proposer); err != nil || !seen[string(vals.Proposer.Address)] {
		return nil, errors.New("invalid validator-set proposer")
	}
	for _, signature := range commit.Signatures {
		if len(signature.ValidatorAddress) > 20 || len(signature.Signature) > ed25519.SignatureSize {
			return nil, errors.New("oversized commit signature")
		}
		// Canonical vote encoding expects protobuf-representable UTC times.
		// ValidateBasic intentionally leaves timestamp policy to the caller.
		if signature.BlockIDFlag != types.BlockIDFlagAbsent &&
			(signature.Timestamp.IsZero() || signature.Timestamp.UTC().Year() < 1 || signature.Timestamp.UTC().Year() > 9999) {
			return nil, errors.New("invalid canonical vote timestamp")
		}
	}
	if err := block.ValidateBasic(policy.ChainID); err != nil {
		return nil, fmt.Errorf("light-block structure: %w", err)
	}
	pb, err := block.ToProto()
	if err != nil {
		return nil, err
	}
	if pb.Size() > maxLightBlockBytes {
		return nil, errors.New("oversized light block")
	}
	raw, err := pb.Marshal()
	if err != nil {
		return nil, err
	}
	var copied cmtproto.LightBlock
	if err := copied.Unmarshal(raw); err != nil {
		return nil, err
	}
	return types.LightBlockFromProto(&copied)
}

// VerifyUpdate verifies one adjacent or skipping update using CometBFT 0.38.26.
// Adjacent updates must follow trusted.NextValidatorsHash. Skipping updates
// additionally require >1/3 of trusted voting power, and always >2/3 of the
// candidate voting power. A skipped update without sufficient overlap fails;
// callers may supply authenticated intermediate headers via repeated calls.
// No network fetching, automatic re-bootstrap after expiry or fallback trust is
// performed. On error the returned checkpoint is zero and must not be persisted.
func VerifyUpdate(anchor Checkpoint, trustedBlock, candidate *types.LightBlock,
	now time.Time, policy Policy) (VerifiedCheckpoint, error) {
	var zero VerifiedCheckpoint
	if err := policy.validate(); err != nil {
		return zero, err
	}
	if anchor.ChainID != policy.ChainID || anchor.Height <= 0 || anchor.Hash == ([32]byte{}) ||
		now.IsZero() || now.UTC().Year() < 1 || now.UTC().Year() > 9999 {
		return zero, errors.New("invalid external checkpoint or evaluation time")
	}
	trusted, err := checkedCopy(trustedBlock, policy)
	if err != nil {
		return zero, fmt.Errorf("trusted block: %w", err)
	}
	if trusted.Height != anchor.Height || !bytes.Equal(trusted.Hash(), anchor.Hash[:]) {
		return zero, errors.New("trusted block does not match the external checkpoint")
	}
	untrusted, err := checkedCopy(candidate, policy)
	if err != nil {
		return zero, fmt.Errorf("candidate block: %w", err)
	}
	if untrusted.Height <= trusted.Height {
		return zero, errors.New("candidate height must increase")
	}
	if untrusted.Height == trusted.Height+1 &&
		(!bytes.Equal(untrusted.LastBlockID.Hash, trusted.Commit.BlockID.Hash) ||
			untrusted.LastBlockID.PartSetHeader.Total != trusted.Commit.BlockID.PartSetHeader.Total ||
			!bytes.Equal(untrusted.LastBlockID.PartSetHeader.Hash, trusted.Commit.BlockID.PartSetHeader.Hash)) {
		return zero, errors.New("adjacent candidate does not extend the trusted block ID")
	}
	// A bootstrap block fetched from RPC must still agree with its externally
	// trusted header and include a real canonical commit, not invented metadata.
	if err := trusted.ValidatorSet.VerifyCommit(policy.ChainID, trusted.Commit.BlockID,
		trusted.Height, trusted.Commit); err != nil {
		return zero, fmt.Errorf("trusted commit: %w", err)
	}
	if err := light.Verify(trusted.SignedHeader, trusted.ValidatorSet,
		untrusted.SignedHeader, untrusted.ValidatorSet, policy.TrustingPeriod, now,
		policy.MaxClockDrift, cmtmath.Fraction{Numerator: 1, Denominator: 3}); err != nil {
		return zero, fmt.Errorf("light update: %w", err)
	}
	// Verify every present signature, including nil-block precommits, rather
	// than accepting a forged suffix after an early +2/3 light-verifier exit.
	if err := untrusted.ValidatorSet.VerifyCommit(policy.ChainID, untrusted.Commit.BlockID,
		untrusted.Height, untrusted.Commit); err != nil {
		return zero, fmt.Errorf("candidate commit: %w", err)
	}
	verified := VerifiedCheckpoint{Checkpoint: Checkpoint{ChainID: policy.ChainID, Height: untrusted.Height},
		Time: untrusted.Time}
	copy(verified.Hash[:], untrusted.Hash())
	copy(verified.AppHash[:], untrusted.AppHash)
	copy(verified.NextValidatorsHash[:], untrusted.NextValidatorsHash)
	return verified, nil
}
