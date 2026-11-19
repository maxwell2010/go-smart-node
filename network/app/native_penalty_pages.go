package app

import (
	"errors"
	"strconv"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

var errPagedPenaltyRequired = errors.New("validator penalty requires durable pages")

func (app *App) startPagedOrdinaryPenalty(ctx sdk.Context, candidate validatorkeeper.NativeDowntimeCandidate) error {
	percentage, err := ordinaryDowntimePercentage(candidate.SlashFraction)
	if err != nil {
		return err
	}
	return app.startPagedPenalty(ctx, validatorkeeper.NativePenaltyDowntime, candidate.Operator, candidate.ConsensusAddress,
		candidate.InfractionHeight, percentage, 0, func(child sdk.Context) error {
			return app.NativeValidatorKeeper.ApplyNativeDowntimeOutcome(child, candidate)
		})
}

func (app *App) startPagedDuplicatePenalty(ctx sdk.Context, job validatorkeeper.NativeDuplicateVoteJob) error {
	return app.startPagedPenalty(ctx, validatorkeeper.NativePenaltyDuplicateVote, job.Operator, job.ConsensusAddress,
		job.InfractionHeight, job.PenaltyPercent, job.InfractionHeight, func(child sdk.Context) error {
			return app.NativeValidatorKeeper.JailNativeDuplicateVotePenalty(child, job)
		})
}

func (app *App) boundedPenaltyWork(ctx sdk.Context, label string, work func(sdk.Context) error) (err error) {
	child, write := ctx.CacheContext()
	meter := storetypes.NewGasMeter(withdrawalAttemptGas)
	child = child.WithGasMeter(meter)
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(storetypes.ErrorOutOfGas); !ok {
				panic(recovered)
			}
			err = errors.New("paged penalty exceeded its block work budget")
		}
		ctx.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), label)
	}()
	if err := work(child); err != nil {
		return err
	}
	write()
	return nil
}

func (app *App) startPagedPenalty(ctx sdk.Context, reason byte, operator string, consensus []byte, infraction int64, percentage uint16, masterHeight int64, jail func(sdk.Context) error) error {
	return app.boundedPenaltyWork(ctx, "announce paged validator penalty", func(child sdk.Context) error {
		validator, err := app.NativeValidatorKeeper.ValidatorAddressCodec().StringToBytes(operator)
		if err != nil {
			return err
		}
		supply := func(read sdk.Context) sdkmath.Int { return app.BankKeeper.GetSupply(read, identity.BaseDenom).Amount }
		return app.withdrawals.executor.BeginPagedMasterPenalty(child, common.BytesToAddress(validator), percentage, masterHeight, supply, func(next sdk.Context, proof settlement.MasterPenaltyProof) error {
			// Announce only from a uniform authenticated pre-cursor. The two
			// ordered endpoint seeks remain constant work at any census size.
			if err := app.NativeValidatorKeeper.RequireActiveStakePenaltyCatchup(next, validator, proof.BeforeCursor.Bytes()); err != nil {
				return err
			}
			progress := validatorkeeper.NativePenaltyProgress{Version: 1, Reason: reason, Operator: operator, ConsensusAddress: consensus,
				InfractionHeight: infraction, AdmittedHeight: next.BlockHeight(), Percentage: percentage, BeforeCursor: proof.BeforeCursor.Bytes(), AfterCursor: proof.AfterCursor.Bytes(), Phase: validatorkeeper.NativePenaltyActiveNFT, Burned: sdkmath.ZeroInt()}
			if err := app.NativeValidatorKeeper.StartNativePenaltyProgress(next, progress); err != nil {
				return err
			}
			if err := jail(next); err != nil {
				return err
			}
			eligible, err := consensusOperators(next, app.NativeValidatorKeeper)
			if err != nil {
				return err
			}
			if err := app.NativeValidatorKeeper.ValidateStakeConsensusSelection(next, eligible); err != nil {
				return err
			}
			next.EventManager().EmitEvent(sdk.NewEvent("decimal_native_penalty_started", sdk.NewAttribute("operator", operator), sdk.NewAttribute("reason", strconv.Itoa(int(reason))), sdk.NewAttribute("infraction_height", strconv.FormatInt(infraction, 10))))
			return nil
		})
	})
}

func (app *App) executePagedPenalty(ctx sdk.Context, progress validatorkeeper.NativePenaltyProgress, duplicate *validatorkeeper.NativeDuplicateVoteJob) error {
	return app.boundedPenaltyWork(ctx, "realize paged validator penalty", func(child sdk.Context) error {
		c := app.withdrawals
		validator, err := c.keeper.ValidatorAddressCodec().StringToBytes(progress.Operator)
		if err != nil {
			return err
		}
		current, err := c.keeper.GetNativePenaltyProgress(child, validator)
		if err != nil {
			return err
		}
		if current.InfractionHeight != progress.InfractionHeight || current.Reason != progress.Reason {
			return errors.New("paged penalty identity changed")
		}
		progress = current
		if progress.Reason == validatorkeeper.NativePenaltyDuplicateVote && (duplicate == nil || duplicate.Operator != progress.Operator || duplicate.InfractionHeight != progress.InfractionHeight) {
			return errors.New("historical page requires the exact pending evidence")
		}
		master := settlement.MasterPenaltyProof{Validator: common.BytesToAddress(validator), Percentage: progress.Percentage, BeforeCursor: common.BytesToHash(progress.BeforeCursor), AfterCursor: common.BytesToHash(progress.AfterCursor)}
		if progress.Reason == validatorkeeper.NativePenaltyDuplicateVote {
			master.InfractionHeight = progress.InfractionHeight
		}
		supply := func(read sdk.Context) sdkmath.Int { return app.BankKeeper.GetSupply(read, identity.BaseDenom).Amount }
		beforeSupply := supply(child)
		return c.executor.ResumeMasterPenalty(child, master, func(next sdk.Context) error {
			after := progress
			var active []validatortypes.EVMStakeBinding
			var frozen []validatortypes.EVMFrozenBinding
			switch progress.Phase {
			case validatorkeeper.NativePenaltyActiveNFT, validatorkeeper.NativePenaltyActiveCoin:
				kind := validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT
				if progress.Phase == validatorkeeper.NativePenaltyActiveCoin {
					kind = validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN
				}
				active, err = c.keeper.GetPendingActivePenaltyKindPage(next, validator, progress.BeforeCursor, kind, 1)
				if err != nil {
					return err
				}
				if len(active) == 0 {
					after.Phase++
				}
			case validatorkeeper.NativePenaltyFrozen:
				var cursor []byte
				frozen, cursor, err = c.keeper.GetValidatorFrozenBindingsPage(next, validator, progress.FrozenAfter, 1)
				if err != nil {
					return err
				}
				if cursor == nil {
					after.Phase = validatorkeeper.NativePenaltyFinished
					after.FrozenAfter = nil
				} else {
					after.FrozenAfter = cursor
				}
			case validatorkeeper.NativePenaltyFinished:
			default:
				return errors.New("invalid durable penalty phase")
			}
			if err := c.validateNativePenaltyRows(active, frozen); err != nil {
				return err
			}
			if progress.Reason == validatorkeeper.NativePenaltyDuplicateVote {
				if err := c.reconcileHistoricalPenaltyRows(next, active, frozen, master, supply); err != nil {
					return err
				}
			} else if err := c.reconcileOrdinaryDowntimeRows(next, active, frozen, master, progress.InfractionHeight, progress.Percentage, supply); err != nil {
				return err
			}
			burned := beforeSupply.Sub(supply(next))
			if burned.IsNegative() {
				return errors.New("paged penalty increased native supply")
			}
			after.Burned = after.Burned.Add(burned)
			if err := c.keeper.AdvanceNativePenaltyProgress(next, progress, after); err != nil {
				return err
			}
			if after.Phase == validatorkeeper.NativePenaltyFinished {
				if err := c.keeper.RequireActiveStakePenaltyCatchup(next, validator, progress.AfterCursor); err != nil {
					return err
				}
				if duplicate != nil {
					if err := c.keeper.FinalizeNativeDuplicateVotePenalty(next, *duplicate); err != nil {
						return err
					}
				}
				if err := c.keeper.CompleteNativePenaltyProgress(next, after); err != nil {
					return err
				}
				appliedEvent := "decimal_native_downtime_applied"
				if after.Reason == validatorkeeper.NativePenaltyDuplicateVote {
					appliedEvent = "decimal_native_duplicate_vote_applied"
				}
				next.EventManager().EmitEvent(sdk.NewEvent(appliedEvent,
					sdk.NewAttribute("operator", after.Operator),
					sdk.NewAttribute("infraction_height", strconv.FormatInt(after.InfractionHeight, 10)),
					sdk.NewAttribute("burned", after.Burned.String()),
					sdk.NewAttribute("paged", "true")))
			}
			eligible, err := consensusOperators(next, c.keeper)
			if err != nil {
				return err
			}
			if err := c.keeper.ValidateStakeConsensusSelection(next, eligible); err != nil {
				return err
			}
			next.EventManager().EmitEvent(sdk.NewEvent("decimal_native_penalty_page", sdk.NewAttribute("operator", progress.Operator), sdk.NewAttribute("phase", strconv.Itoa(int(after.Phase))), sdk.NewAttribute("burned", burned.String()), sdk.NewAttribute("total_burned", after.Burned.String())))
			return nil
		})
	})
}
