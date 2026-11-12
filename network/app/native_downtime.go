package app

import (
	"errors"
	"math/big"
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
	if profile.EnableNativeHeldDELDowntime && (!profile.EnableNativeOrdinaryDELDowntime || !profile.EnableDELHold) {
		return errors.New("native held DEL downtime requires native DEL downtime and DEL hold admission")
	}
	if profile.EnableNativeNFTDowntime && (!profile.EnableNativeOrdinaryDELDowntime || !profile.EnableNFTDelegation) {
		return errors.New("native NFT downtime requires native DEL downtime and isolated NFT admission")
	}
	if profile.EnableNativeOrdinaryDELDowntime && (!profile.EnableNativeConsensus ||
		(profile.EnableNFTDelegation && !profile.EnableNativeNFTDowntime) ||
		(profile.EnableDELHold && !profile.EnableNativeHeldDELDowntime)) {
		return errors.New("native DEL downtime requires native consensus and supported DEL position types")
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
	nft := validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT
	supported := func(kind validatortypes.StakeType) bool {
		return kind == validatortypes.StakeType_Coin || (c.profile.EnableNativeHeldDELDowntime && kind == validatortypes.StakeType_CoinHold)
	}
	for _, row := range active {
		if common.BytesToAddress(row.Gateway) != c.profile.Gateway {
			return nil, nil, errors.New("native downtime encountered a position outside the approved Gateway")
		}
		if c.profile.EnableNativeNFTDowntime && row.Kind == nft && row.NativeTarget.StakeType == validatortypes.StakeType_NFT &&
			common.BytesToAddress(row.TokenContract) == c.profile.PositionVault {
			continue
		}
		if row.Kind != coin || common.BytesToAddress(row.TokenContract) != c.profile.WrappedDEL ||
			!supported(row.NativeTarget.StakeType) || (!c.profile.EnableNativeNFTDowntime && !row.RawNftBackedAmount.IsZero()) {
			return nil, nil, errors.New("native downtime encountered an unsupported active position")
		}
	}
	for _, row := range frozen {
		if common.BytesToAddress(row.Gateway) != c.profile.Gateway || row.FreezeType != 1 {
			return nil, nil, errors.New("native downtime encountered a claim outside the approved Gateway withdrawal route")
		}
		if c.profile.EnableNativeNFTDowntime && row.SourceKind == nft && row.NativeTarget.StakeType == validatortypes.StakeType_NFT {
			continue
		}
		if row.SourceKind != coin || !supported(row.NativeTarget.StakeType) {
			return nil, nil, errors.New("native downtime encountered an unsupported pending claim")
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
		// Resolve NFT backing first. It can reduce or remove a logical coin
		// companion before direct DEL is reconciled at the same Master cursor.
		for _, row := range active {
			if row.Kind == validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT {
				if err := c.applyActiveNFTDowntime(project, row, master, percent, supply); err != nil {
					return err
				}
			}
		}
		for _, row := range active {
			if row.Kind != validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN {
				continue
			}
			if !row.RawNftBackedAmount.IsZero() {
				current, err := c.keeper.GetEVMStakeBinding(project, row.Kind, row.Gateway, row.StakeID)
				if errors.Is(err, validatorkeeper.ErrNotFound) && row.RawAmount.Equal(row.RawNftBackedAmount) {
					continue
				}
				if err != nil {
					return err
				}
				row = current
			}
			if common.BytesToHash(row.PenaltyCursor) != master.BeforeCursor {
				return errors.New("DEL stake differs from the prior Master penalty cursor")
			}
			applyPenalty := c.executor.ApplyOrdinaryDELPenalty
			held := row.NativeTarget.StakeType == validatortypes.StakeType_CoinHold
			if held {
				applyPenalty = c.executor.ApplyHeldDELPenalty
			}
			err := applyPenalty(project, row, percent, supply, func(next sdk.Context, proof settlement.ActiveDELPenaltyProof) error {
				before := validatorkeeper.GatewayDELStake{
					Gateway: c.profile.Gateway, WrappedDEL: c.profile.WrappedDEL, Validator: common.BytesToAddress(row.Validator),
					Beneficiary: common.BytesToAddress(row.Beneficiary), StakeID: common.BytesToHash(row.StakeID),
					Amount: row.RawAmount.Sub(row.RawNftBackedAmount), NFTBacked: row.RawNftBackedAmount,
					PenaltyCursor: master.BeforeCursor,
				}
				remaining := proof.After.RawAmount.Sub(row.RawNftBackedAmount)
				if held {
					// The EVM proof has already checked both words fit Unix seconds.
					before.HoldDeadline = common.BytesToHash(proof.Before.HoldTimestamp).Big().Int64()
					before.HoldStartTime = proof.Before.NativeHoldStartTime
					return c.keeper.ApplyGatewayDELHoldPenalty(next, before, remaining, master.AfterCursor)
				}
				return c.keeper.ApplyGatewayDELPenalty(next, before, remaining, master.AfterCursor)
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
			if row.SourceKind == validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT {
				if err := c.applyFrozenNFTDowntime(project, row, master, candidate.InfractionHeight, percent, supply); err != nil {
					return err
				}
				continue
			}
			err := c.executor.ApplyFrozenDELPenalty(project, row, candidate.InfractionHeight, percent, supply, func(next sdk.Context, proof settlement.FrozenDELPenaltyProof) error {
				if proof.BeforeCursor != master.BeforeCursor || proof.AfterCursor != master.AfterCursor {
					return errors.New("frozen DEL claim differs from the Master penalty cursor")
				}
				if row.NativeTarget.StakeType == validatortypes.StakeType_CoinHold {
					hold := validatorkeeper.GatewayFrozenDELHold{
						WrappedDEL: c.profile.WrappedDEL, HoldDeadline: proof.HoldDeadline, HoldStartTime: proof.HoldStartTime,
					}
					return c.keeper.ApplyGatewayFrozenDELHoldPenalty(next, row, hold, candidate.InfractionHeight, proof.BeforeCursor, proof.AfterCursor, proof.Remaining)
				}
				return c.keeper.ApplyGatewayFrozenDELPenalty(next, row, candidate.InfractionHeight, proof.BeforeCursor, proof.AfterCursor, proof.Remaining)
			})
			if err != nil {
				return err
			}
		}
		if err := c.keeper.RequireActiveStakePenaltyCatchup(project, validator, master.AfterCursor[:]); err != nil {
			return err
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
	if err != nil {
		return false, nil
	}
	held := method.Sig == "withdrawHold(address,address,uint256,uint256)"
	if method.Sig != "withdraw(address,address,uint256)" && !(held && c.profile.EnableNativeHeldDELDowntime) {
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
	var tokenID, hold common.Hash
	if held {
		deadline := args[3].(*big.Int)
		if !deadline.IsInt64() || deadline.Sign() <= 0 {
			return true, errors.New("invalid penalized DEL withdrawal hold deadline")
		}
		hold = common.BigToHash(deadline)
	}
	id := crypto.Keccak256Hash(operator[:], msg.From[:], token[:], tokenID[:], hold[:])
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
