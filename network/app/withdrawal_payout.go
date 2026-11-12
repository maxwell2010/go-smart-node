package app

import (
	"errors"
	"fmt"
	"math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

func (c *withdrawalController) checkWithdrawalPayout(ctx sdk.Context, row validatortypes.EVMFrozenBinding) error {
	enabled, err := c.keeper.NativeStakeProvenanceEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	executionHeight, previous, err := c.withdrawalEvidenceClock(ctx)
	if err != nil {
		return fmt.Errorf("read committed withdrawal evidence clock: %w", err)
	}
	limits := ctx.ConsensusParams().Evidence
	// The gRPC estimator runs the signed hook in a discarded query cache.
	// Its snapshot context can lack a cached copy of the consensus parameters.
	if limits == nil && ctx.ExecMode() != sdk.ExecModeFinalize && c.consensusParams != nil {
		limits = c.consensusParams(ctx).Evidence
	}
	if limits == nil {
		return errors.New("missing consensus limits for withdrawal evidence finality")
	}
	creation, err := c.keeper.GetFrozenWithdrawalCreationBlock(ctx, row)
	if err != nil {
		return fmt.Errorf("read authenticated withdrawal creation block: %w", err)
	}
	if creation.Height > previous.Height {
		return fmt.Errorf("%w: request is awaiting its first commitment", validatorkeeper.ErrWithdrawalAwaitingEvidenceFinality)
	}
	elapsed, err := validatorkeeper.WithdrawalEvidenceWindowElapsed(executionHeight, previous, creation, limits.MaxAgeNumBlocks, limits.MaxAgeDuration)
	if err != nil {
		return err
	}
	if !elapsed {
		return fmt.Errorf("%w: evidence age limit remains open", validatorkeeper.ErrWithdrawalAwaitingEvidenceFinality)
	}
	operator, err := c.keeper.ValidatorAddressCodec().BytesToString(row.SourceValidator)
	if err != nil {
		return err
	}
	pending, err := c.keeper.HasPendingNativeDuplicateVotePenalty(ctx, operator)
	if err != nil {
		return err
	}
	if pending {
		return fmt.Errorf("%w: validator has an unsettled penalty", validatorkeeper.ErrWithdrawalAwaitingEvidenceFinality)
	}
	return nil
}

func (c *withdrawalController) withdrawalEvidenceClock(ctx sdk.Context) (int64, validatorkeeper.CommittedEvidenceBlock, error) {
	if ctx.ExecMode() == sdk.ExecModeFinalize {
		previous, err := c.keeper.PreviousNativeEvidenceBlock(ctx)
		return ctx.BlockHeight(), previous, err
	}
	// Simulation evaluates eligibility for the block following the committed
	// snapshot, with the snapshot itself as the authenticated previous block.
	if ctx.ExecMode() != sdk.ExecModeSimulate && (ctx.ExecMode() != sdk.ExecModeCheck || !ctx.IsCheckTx()) {
		return 0, validatorkeeper.CommittedEvidenceBlock{}, errors.New("unsupported withdrawal evidence execution mode")
	}
	if ctx.BlockHeight() == math.MaxInt64 {
		return 0, validatorkeeper.CommittedEvidenceBlock{}, errors.New("withdrawal evidence execution height exceeds its range")
	}
	previous, err := c.keeper.NativeEvidenceBlockAtSnapshot(ctx)
	return ctx.BlockHeight() + 1, previous, err
}
