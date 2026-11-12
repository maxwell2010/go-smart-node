package keeper

import (
	"errors"
	"time"
)

// CommittedEvidenceBlock identifies a block by height and canonical header time.
type CommittedEvidenceBlock struct {
	Height int64
	Time   time.Time
}

// WithdrawalEvidenceWindowElapsed tests the CometBFT evidence age limits using
// the committed block immediately before executionHeight. For a position removed
// by a transaction, requestCreatedBlock is the last slashable block when exposure
// is measured before block execution. The caller authenticates both headers and
// supplies the evidence limits effective for the block being executed.
// Withdrawal settlement also checks maturity and unresolved liabilities.
func WithdrawalEvidenceWindowElapsed(
	executionHeight int64,
	previousBlock, requestCreatedBlock CommittedEvidenceBlock,
	maxAgeNumBlocks int64,
	maxAgeDuration time.Duration,
) (bool, error) {
	return nativeEvidenceWindowElapsed(executionHeight, previousBlock, requestCreatedBlock, maxAgeNumBlocks, maxAgeDuration)
}

func nativeEvidenceWindowElapsed(
	executionHeight int64,
	previousBlock, subject CommittedEvidenceBlock,
	maxAgeNumBlocks int64,
	maxAgeDuration time.Duration,
) (bool, error) {
	if maxAgeNumBlocks <= 0 || maxAgeDuration <= 0 {
		return false, errors.New("evidence age limits must be positive")
	}
	if executionHeight <= 1 || previousBlock.Height != executionHeight-1 || previousBlock.Time.IsZero() {
		return false, errors.New("previous committed block required for native evidence window")
	}
	if subject.Height <= 0 || subject.Height > previousBlock.Height || subject.Time.IsZero() {
		return false, errors.New("committed subject block required for native evidence window")
	}

	previousTime := previousBlock.Time.UTC()
	subjectTime := subject.Time.UTC()
	if subject.Height == previousBlock.Height {
		if !previousTime.Equal(subjectTime) {
			return false, errors.New("conflicting times for the same committed block")
		}
		return false, nil
	}
	if !previousTime.After(subjectTime) {
		return false, errors.New("committed block times must increase with height")
	}

	// CometBFT v0.38 expires evidence only after both strict age limits.
	return previousBlock.Height-subject.Height > maxAgeNumBlocks &&
		previousTime.Sub(subjectTime) > maxAgeDuration, nil
}
