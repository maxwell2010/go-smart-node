package keeper

import (
	"errors"
	"math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
)

// compactActiveDELProvenance retains the genesis contribution and all funding
// still inside the evidence window. An expired prefix can share its newest
// height and time because every future admissible infraction is newer than it.
func (k *StateKeeper) compactActiveDELProvenance(ctx sdk.Context, validator common.Address, lots []DELProvenanceTranche, needed int) ([]DELProvenanceTranche, error) {
	start := 0
	if len(lots) != 0 && lots[0].FundedHeight == 0 {
		start = 1
	}
	if len(lots)-start <= needed {
		return lots, nil
	}
	limits, err := k.NativeEvidenceLimits(ctx)
	if errors.Is(err, ErrNotFound) {
		return lots, nil
	}
	if err != nil {
		return nil, err
	}
	operator, err := k.ValidatorAddressCodec().BytesToString(validator[:])
	if err != nil {
		return nil, err
	}
	pending, err := k.HasPendingNativeDuplicateVotePenalty(ctx, operator)
	if err != nil || pending {
		return lots, err
	}
	var previous CommittedEvidenceBlock
	executionHeight := ctx.BlockHeight()
	switch {
	case ctx.ExecMode() == sdk.ExecModeFinalize:
		previous, err = k.PreviousNativeEvidenceBlock(ctx)
	case ctx.ExecMode() == sdk.ExecModeSimulate || (ctx.ExecMode() == sdk.ExecModeCheck && ctx.IsCheckTx()):
		if executionHeight == math.MaxInt64 {
			return nil, errors.New("DEL provenance compaction height exceeds its range")
		}
		previous, err = k.NativeEvidenceBlockAtSnapshot(ctx)
		executionHeight++
	default:
		return lots, nil
	}
	if err != nil {
		return nil, err
	}

	end := start
	for i := start; i < len(lots); i++ {
		lot := lots[i]
		if lot.FundedHeight >= ctx.BlockHeight() || !lot.FundedAt.Before(ctx.BlockTime()) ||
			(i > start && !lot.FundedAt.After(lots[i-1].FundedAt)) {
			return nil, errors.New("active DEL provenance funding blocks are inconsistent with the new deposit")
		}
		funded := CommittedEvidenceBlock{Height: lot.FundedHeight, Time: lot.FundedAt}
		elapsed, err := nativeEvidenceWindowElapsed(executionHeight, previous, funded, limits.MaxAgeNumBlocks, limits.MaxAgeDuration)
		if err != nil {
			return nil, err
		}
		if elapsed {
			end = i + 1
		}
	}
	if end-start <= needed {
		return lots, nil
	}

	merged := lots[end-1]
	for _, lot := range lots[start : end-1] {
		merged.Amount = merged.Amount.Add(lot.Amount)
	}
	compacted := make([]DELProvenanceTranche, 0, len(lots)-(end-start-1)+1)
	compacted = append(compacted, lots[:start]...)
	compacted = append(compacted, merged)
	return append(compacted, lots[end:]...), nil
}
