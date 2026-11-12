package app

import (
	"errors"
	"fmt"
	"strconv"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func (app *App) usesNativeDuplicateVote() bool {
	return app.withdrawals != nil && app.withdrawals.profile.EnableNativeDuplicateVote
}

func (app *App) verifyNativeDuplicateVoteState(ctx sdk.Context, enabled bool) error {
	keeper := app.NativeValidatorKeeper
	active, err := keeper.NativeStakeProvenanceEnabled(ctx)
	if err != nil {
		return err
	}
	if enabled != active {
		return errors.New("native duplicate-vote profile differs from persisted stake history")
	}
	if !enabled {
		return nil
	}
	if err := keeper.VerifyNativeEvidenceLimits(ctx, app.GetConsensusParams(ctx).Evidence); err != nil {
		return err
	}
	if _, err := keeper.NativeLiveValidatorOperators(ctx); err != nil {
		return err
	}
	return keeper.VerifyNativeEvidenceClockHeight(ctx)
}

func (app *App) preBlockNativeDuplicateVote(ctx sdk.Context) (*sdk.ResponsePreBlock, error) {
	child, write := ctx.CacheContext()
	keeper := app.NativeValidatorKeeper
	if err := keeper.VerifyNativeEvidenceLimits(child, child.ConsensusParams().Evidence); err != nil {
		return nil, err
	}
	if _, err := keeper.EnqueueNativeDuplicateVotePenalties(child); err != nil {
		return nil, fmt.Errorf("enqueue native duplicate-vote liabilities: %w", err)
	}
	result, err := app.ModuleManager.PreBlock(child)
	if err != nil {
		return nil, err
	}
	if err := keeper.VerifyNativeEvidenceLimits(child, app.GetConsensusParams(child).Evidence); err != nil {
		return nil, err
	}
	write()
	return result, nil
}

// One financial attempt per block shares the existing penalty work budget.
// Pending history takes precedence for its operator. Alternating priority
// allows other operators' downtime to progress even while a job is deferred.
func (app *App) applyNativePenalties(ctx sdk.Context) error {
	if !app.usesNativeDuplicateVote() {
		_, err := app.applyOrdinaryDELDowntime(ctx)
		return err
	}
	ordinaryFirst := ctx.BlockHeight()%2 == 0
	if ordinaryFirst {
		attempted, err := app.applyOrdinaryDELDowntime(ctx)
		if err != nil || attempted {
			return err
		}
	}
	attempted, err := app.applyNativeDuplicateVotePenalty(ctx)
	if err != nil || attempted || ordinaryFirst {
		return err
	}
	_, err = app.applyOrdinaryDELDowntime(ctx)
	return err
}

func (app *App) applyNativeDuplicateVotePenalty(ctx sdk.Context) (bool, error) {
	job, err := app.NativeValidatorKeeper.NextNativeDuplicateVotePenalty(ctx)
	if errors.Is(err, validatorkeeper.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := app.executeNativeDuplicateVotePenalty(ctx, job); err != nil {
		ctx.Logger().Error("native duplicate-vote settlement deferred", "operator", job.Operator,
			"infraction_height", job.InfractionHeight, "err", err)
		ctx.EventManager().EmitEvent(sdk.NewEvent("decimal_native_duplicate_vote_deferred",
			sdk.NewAttribute("operator", job.Operator), sdk.NewAttribute("infraction_height", strconv.FormatInt(job.InfractionHeight, 10))))
	}
	return true, nil
}

func (app *App) executeNativeDuplicateVotePenalty(ctx sdk.Context, job validatorkeeper.NativeDuplicateVoteJob) (err error) {
	child, write := ctx.CacheContext()
	meter := storetypes.NewGasMeter(withdrawalAttemptGas)
	child = child.WithGasMeter(meter)
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(storetypes.ErrorOutOfGas); !ok {
				panic(recovered)
			}
			err = errors.New("native duplicate-vote penalty exceeded its block work budget")
		}
		ctx.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), "native duplicate-vote settlement")
	}()
	c := app.withdrawals
	operator, err := c.keeper.ValidatorAddressCodec().StringToBytes(job.Operator)
	if err != nil {
		return err
	}
	supply := func(read sdk.Context) sdkmath.Int {
		return app.BankKeeper.GetSupply(read, identity.BaseDenom).Amount
	}
	before := supply(child)
	err = c.executor.AppendMasterHistoricalPenalty(child, common.BytesToAddress(operator), job.InfractionHeight, supply,
		func(next sdk.Context, master settlement.MasterPenaltyProof) error {
			if master.Percentage != job.PenaltyPercent {
				return errors.New("typed Master penalty differs from the admitted rate")
			}
			if err := c.reconcileNativeDuplicateVoteStakes(next, master, supply); err != nil {
				return err
			}
			if err := c.keeper.FinalizeNativeDuplicateVotePenalty(next, job); err != nil {
				return err
			}
			eligible, err := consensusOperators(next, c.keeper)
			if err != nil {
				return err
			}
			return c.keeper.ValidateStakeConsensusSelection(next, eligible)
		})
	if err != nil {
		return err
	}
	burned := before.Sub(supply(child))
	if burned.IsNegative() {
		return errors.New("native duplicate-vote settlement increased DEL supply")
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_native_duplicate_vote_applied",
		sdk.NewAttribute("operator", job.Operator), sdk.NewAttribute("infraction_height", strconv.FormatInt(job.InfractionHeight, 10)),
		sdk.NewAttribute("burned", burned.String()), sdk.NewAttribute("gas_used", strconv.FormatUint(meter.GasConsumed(), 10))))
	write()
	return nil
}
