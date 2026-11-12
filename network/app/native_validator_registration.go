package app

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/ethereum/go-ethereum/common"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func (app *App) usesNativeValidatorRegistration() bool {
	return app.usesNativeDuplicateVote() && app.usesNativeGovernance()
}

func (app *App) nativeValidatorRegistration(ctx sdk.Context, metadata, proposer, title string) (*nativeValidatorRegistration, error) {
	if !strings.HasPrefix(metadata, nativeValidatorRegistrationNamespace) {
		return nil, nil
	}
	if !app.usesNativeValidatorRegistration() {
		return nil, errors.New("native validator registration governance is disabled")
	}
	moniker := strings.TrimSpace(title)
	if len(moniker) == 0 || len(moniker) > 70 {
		return nil, errors.New("native validator registration title must be a valid validator moniker")
	}
	operator, err := app.AccountKeeper.AddressCodec().StringToBytes(proposer)
	if err != nil {
		return nil, err
	}
	return validateNativeValidatorRegistrationProposal(metadata, app.chainID, app.evmChainID, operator, ctx.BlockHeight())
}

func (app *App) applyNativeValidatorAction(ctx sdk.Context, expected govv1.Proposal) error {
	proposal, err := app.GovKeeper.Proposals.Get(ctx, expected.Id)
	if err != nil {
		return err
	}
	if proposal.Status != govv1.StatusPassed {
		return nil
	}
	if proposal.Id != expected.Id || proposal.Metadata != expected.Metadata || proposal.Proposer != expected.Proposer ||
		proposal.Title != expected.Title || len(proposal.Messages) != 0 || proposal.FinalTallyResult == nil {
		return errors.New("approved native validator action differs from its tallied proposal")
	}
	action, failureEvent := app.registerNativeValidator, "decimal_native_validator_registration_failed"
	if strings.HasPrefix(proposal.Metadata, nativeValidatorRetirementNamespace) {
		action, failureEvent = app.retireNativeValidator, "decimal_native_validator_retirement_failed"
	}
	if err := action(ctx, proposal); err != nil {
		proposal.Status, proposal.FailedReason = govv1.StatusFailed, err.Error()
		if err := app.GovKeeper.SetProposal(ctx, proposal); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent(failureEvent,
			sdk.NewAttribute("proposal_id", strconv.FormatUint(proposal.Id, 10)), sdk.NewAttribute("reason", proposal.FailedReason)))
	}
	return nil
}

func (app *App) registerNativeValidator(ctx sdk.Context, proposal govv1.Proposal) error {
	registration, err := app.nativeValidatorRegistration(ctx, proposal.Metadata, proposal.Proposer, proposal.Title)
	if err != nil {
		return err
	}
	if registration == nil {
		return errors.New("passed proposal does not contain a native validator registration")
	}
	keeper := app.NativeValidatorKeeper
	operator, err := keeper.ValidatorAddressCodec().BytesToString(registration.Operator[:])
	if err != nil {
		return err
	}
	key := &ed25519.PubKey{Key: registration.ConsensusPubKey[:]}
	if _, err := keeper.GetValidator(ctx, operator); !errors.Is(err, validatorkeeper.ErrNotFound) {
		if err == nil {
			return errors.New("native validator registration requires an unused operator address")
		}
		return err
	}
	if _, err := keeper.GetValidatorByConsAddr(ctx, sdk.ConsAddress(key.Address())); !errors.Is(err, validatorkeeper.ErrNotFound) {
		if err == nil {
			return errors.New("native validator registration requires an unused consensus key")
		}
		return err
	}
	validator, err := validatortypes.NewValidator(operator, proposal.Proposer, key,
		validatortypes.Description{Moniker: strings.TrimSpace(proposal.Title)},
		sdkmath.LegacyNewDecWithPrec(int64(registration.CommissionPercent), 2))
	if err != nil {
		return err
	}
	validator.Online = true
	address := common.BytesToAddress(registration.Operator[:])
	meta, err := json.Marshal(struct {
		OperatorAddress string                     `json:"operator_address"`
		RewardAddress   string                     `json:"reward_address"`
		ConsensusPubkey []byte                     `json:"consensus_pubkey"`
		Description     validatortypes.Description `json:"description"`
		Commission      string                     `json:"commission"`
	}{operator, address.Hex(), key.Bytes(), validator.Description, strconv.FormatUint(uint64(registration.CommissionPercent), 10)})
	if err != nil {
		return err
	}
	supply := func(read sdk.Context) sdkmath.Int {
		return app.BankKeeper.GetSupply(read, identity.BaseDenom).Amount
	}
	return app.withdrawals.executor.RegisterNativeValidator(ctx, address, string(meta), supply, func(next sdk.Context) error {
		if err := keeper.SetValidator(next, validator); err != nil {
			return err
		}
		next.EventManager().EmitEvent(sdk.NewEvent("decimal_native_validator_registered",
			sdk.NewAttribute("proposal_id", strconv.FormatUint(proposal.Id, 10)), sdk.NewAttribute("operator", operator)))
		return nil
	})
}
