package app

import (
	"errors"
	"strconv"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const ordinaryDowntimePositions uint32 = 2

func validateNativeOrdinaryDELDowntimeProfile(profile WithdrawalProfile) error {
	if profile.EnableNativeOrdinaryDELDowntime && (!profile.EnableNativeConsensus || profile.EnableDELHold || profile.EnableNFTDelegation) {
		return errors.New("native ordinary DEL downtime requires native consensus with ordinary DEL positions only")
	}
	return nil
}

func ordinaryDowntimePercentage(fraction sdkmath.LegacyDec) (uint16, error) {
	if fraction.IsNil() {
		return 0, errors.New("native ordinary downtime requires a slash fraction")
	}
	percent := fraction.MulInt64(100)
	if !percent.IsInteger() || !percent.IsPositive() || percent.GT(sdkmath.LegacyNewDec(100)) {
		return 0, errors.New("native ordinary downtime requires a whole percentage between 1 and 100")
	}
	return uint16(percent.TruncateInt64()), nil
}

func (c *withdrawalController) ordinaryDowntimePositions(ctx sdk.Context, validator []byte) ([]validatortypes.EVMStakeBinding, []validatortypes.EVMFrozenBinding, error) {
	active, nextActive, err := c.keeper.GetActiveStakePenaltyPage(ctx, validator, nil, ordinaryDowntimePositions)
	if err != nil {
		return nil, nil, err
	}
	frozen, nextFrozen, err := c.keeper.GetValidatorFrozenBindingsPage(ctx, validator, nil, ordinaryDowntimePositions)
	if err != nil {
		return nil, nil, err
	}
	if nextActive != nil || nextFrozen != nil || len(active)+len(frozen) > int(ordinaryDowntimePositions) {
		return nil, nil, errors.New("native ordinary downtime exceeds its per-validator position budget")
	}
	coin := validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN
	for _, row := range active {
		if row.Kind != coin || common.BytesToAddress(row.Gateway) != c.profile.Gateway ||
			common.BytesToAddress(row.TokenContract) != c.profile.WrappedDEL ||
			row.NativeTarget.StakeType != validatortypes.StakeType_Coin || !row.RawNftBackedAmount.IsZero() {
			return nil, nil, errors.New("native ordinary downtime encountered an unsupported active position")
		}
	}
	for _, row := range frozen {
		if row.SourceKind != coin || row.FreezeType != 1 || row.NativeTarget.StakeType != validatortypes.StakeType_Coin ||
			common.BytesToAddress(row.Gateway) != c.profile.Gateway {
			return nil, nil, errors.New("native ordinary downtime encountered an unsupported pending claim")
		}
	}
	return active, frozen, nil
}

func (c *withdrawalController) validateOrdinaryDowntimeAdmission(ctx sdk.Context) error {
	params, err := c.keeper.GetParams(ctx)
	if err != nil {
		return err
	}
	if _, err := ordinaryDowntimePercentage(params.SlashFractionDowntime); err != nil {
		return err
	}
	operators, err := consensusOperators(ctx, c.keeper)
	if err != nil {
		return err
	}
	for _, operator := range operators {
		raw, err := c.keeper.ValidatorAddressCodec().StringToBytes(operator)
		if err != nil {
			return err
		}
		if _, _, err := c.ordinaryDowntimePositions(ctx, raw); err != nil {
			return err
		}
	}
	return nil
}

func (app *App) applyOrdinaryDELDowntime(ctx sdk.Context) error {
	candidates, err := app.NativeValidatorKeeper.PlanNativeDowntimePenalties(ctx)
	if err != nil || len(candidates) == 0 {
		return err
	}
	// One attempt per block, rotating across candidates if an operation
	// cannot be completed within its bounded work budget.
	candidate := candidates[ctx.BlockHeight()%int64(len(candidates))]
	if err := app.executeOrdinaryDELDowntime(ctx, candidate); err != nil {
		ctx.Logger().Error("native ordinary DEL downtime deferred", "operator", candidate.Operator, "error", err)
		ctx.EventManager().EmitEvent(sdk.NewEvent("decimal_native_downtime_deferred", sdk.NewAttribute("operator", candidate.Operator)))
	}
	return nil
}

func (app *App) executeOrdinaryDELDowntime(ctx sdk.Context, candidate validatorkeeper.NativeDowntimeCandidate) (err error) {
	child, write := ctx.CacheContext()
	meter := storetypes.NewGasMeter(withdrawalAttemptGas)
	child = child.WithGasMeter(meter)
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(storetypes.ErrorOutOfGas); !ok {
				panic(recovered)
			}
			err = errors.New("native ordinary DEL downtime exceeded the block work budget")
		}
		ctx.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), "native ordinary DEL downtime")
	}()
	c := app.withdrawals
	percent, err := ordinaryDowntimePercentage(candidate.SlashFraction)
	if err != nil {
		return err
	}
	validator, err := c.keeper.ValidatorAddressCodec().StringToBytes(candidate.Operator)
	if err != nil {
		return err
	}
	active, frozen, err := c.ordinaryDowntimePositions(child, validator)
	if err != nil {
		return err
	}
	supply := func(read sdk.Context) sdkmath.Int {
		return app.BankKeeper.GetSupply(read, identity.BaseDenom).Amount
	}
	beforeSupply := supply(child)
	err = c.executor.AppendMasterPenalty(child, common.BytesToAddress(validator), percent, supply, func(project sdk.Context, master settlement.MasterPenaltyProof) error {
		for _, row := range active {
			if common.BytesToHash(row.PenaltyCursor) != master.BeforeCursor {
				return errors.New("ordinary DEL stake differs from the prior Master penalty cursor")
			}
			err := c.executor.ApplyOrdinaryDELPenalty(project, row, percent, supply, func(next sdk.Context, proof settlement.OrdinaryDELPenaltyProof) error {
				before := validatorkeeper.GatewayDELStake{
					Gateway: c.profile.Gateway, WrappedDEL: c.profile.WrappedDEL, Validator: common.BytesToAddress(row.Validator),
					Beneficiary: common.BytesToAddress(row.Beneficiary), StakeID: common.BytesToHash(row.StakeID),
					Amount: row.RawAmount, PenaltyCursor: master.BeforeCursor,
				}
				return c.keeper.ApplyGatewayDELPenalty(next, before, proof.After.RawAmount, master.AfterCursor)
			})
			if err != nil {
				return err
			}
		}
		for _, row := range frozen {
			deadline := common.BytesToHash(row.CompletionTimestamp).Big()
			if !deadline.IsInt64() || deadline.Sign() <= 0 {
				return errors.New("invalid frozen DEL penalty deadline")
			}
			if deadline.Int64() <= project.BlockTime().Unix() {
				continue
			}
			err := c.executor.ApplyFrozenDELPenalty(project, row, candidate.InfractionHeight, percent, supply, func(next sdk.Context, proof settlement.FrozenDELPenaltyProof) error {
				if proof.BeforeCursor != master.BeforeCursor || proof.AfterCursor != master.AfterCursor {
					return errors.New("frozen DEL claim differs from the Master penalty cursor")
				}
				return c.keeper.ApplyGatewayFrozenDELPenalty(next, row, candidate.InfractionHeight, proof.BeforeCursor, proof.AfterCursor, proof.Remaining)
			})
			if err != nil {
				return err
			}
		}
		if err := c.keeper.ApplyNativeDowntimeOutcome(project, candidate); err != nil {
			return err
		}
		eligible, err := consensusOperators(project, c.keeper)
		if err != nil {
			return err
		}
		return c.keeper.ValidateStakeConsensusSelection(project, eligible)
	})
	if err != nil {
		return err
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_native_downtime_applied",
		sdk.NewAttribute("operator", candidate.Operator),
		sdk.NewAttribute("infraction_height", strconv.FormatInt(candidate.InfractionHeight, 10)),
		sdk.NewAttribute("burned", beforeSupply.Sub(supply(child)).String())))
	write()
	return nil
}

func (c *withdrawalController) projectPenalizedDELWithdrawal(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) (bool, error) {
	method, err := c.abi.MethodById(msg.Data[:4])
	if err != nil || method.Sig != "withdraw(address,address,uint256)" {
		return false, nil
	}
	args, err := method.Inputs.Unpack(msg.Data[4:])
	if err != nil {
		return true, err
	}
	operator, token := args[0].(common.Address), args[1].(common.Address)
	if token != c.profile.WrappedDEL {
		return false, nil
	}
	var zero [2 * common.HashLength]byte
	id := crypto.Keccak256Hash(operator[:], msg.From[:], token[:], zero[:])
	before, err := c.keeper.GetEVMStakeBinding(ctx, validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, c.profile.Gateway[:], id[:])
	if errors.Is(err, validatorkeeper.ErrNotFound) || (err == nil && common.BytesToHash(before.PenaltyCursor) == (common.Hash{})) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	transition, err := c.executor.VerifySignedPenalizedDELWithdrawal(ctx, msg, receipt, before)
	if err != nil {
		return true, err
	}
	next := c.delStakePosition(transition)
	next.PenaltyCursor = transition.PenaltyCursor
	return true, c.keeper.ApplyPenalizedGatewayDELWithdrawal(ctx, next, transition.FrozenIndex, transition.Amount, transition.Deadline)
}

func (c *withdrawalController) projectDowntimeUnjail(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	cursor, err := c.executor.VerifySignedDowntimeUnjail(ctx, msg, receipt)
	if err != nil {
		return err
	}
	if err := c.keeper.RequireActiveStakePenaltyCatchup(ctx, msg.From[:], cursor[:]); err != nil {
		return err
	}
	operator, err := c.keeper.ValidatorAddressCodec().BytesToString(msg.From[:])
	if err != nil {
		return err
	}
	if err := c.keeper.ReleaseNativeDowntimeJail(ctx, operator); err != nil {
		return err
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("decimal_native_downtime_unjailed", sdk.NewAttribute("operator", operator)))
	return nil
}
