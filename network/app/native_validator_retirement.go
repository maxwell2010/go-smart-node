package app

import (
	"errors"
	"strconv"
	"strings"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const nativeValidatorRetirementNamespace = "mintcandy-validator-retirement/"
const nativeValidatorRetirementSchema = nativeValidatorRetirementNamespace + "v1"

func (app *App) nativeValidatorRetirement(ctx sdk.Context, metadata, proposer string) (common.Address, string, error) {
	if !strings.HasPrefix(metadata, nativeValidatorRetirementNamespace) {
		return common.Address{}, "", nil
	}
	if metadata != nativeValidatorRetirementSchema {
		return common.Address{}, "", errors.New("unsupported native validator retirement proposal schema")
	}
	if !app.usesNativeValidatorRegistration() {
		return common.Address{}, "", errors.New("native validator retirement governance is disabled")
	}
	raw, err := app.AccountKeeper.AddressCodec().StringToBytes(proposer)
	if err != nil || len(raw) != common.AddressLength || common.BytesToAddress(raw) == (common.Address{}) {
		return common.Address{}, "", errors.New("native validator retirement requires the operator's 20-byte proposer account")
	}
	keeper := app.NativeValidatorKeeper
	operator, err := keeper.ValidatorAddressCodec().BytesToString(raw)
	if err != nil {
		return common.Address{}, "", err
	}
	if _, err := keeper.GetValidator(ctx, operator); err != nil {
		return common.Address{}, "", err
	}
	retired, err := keeper.IsNativeValidatorRetired(ctx, operator)
	if err != nil {
		return common.Address{}, "", err
	}
	if retired {
		return common.Address{}, "", errors.New("native validator has already retired")
	}
	return common.BytesToAddress(raw), operator, nil
}

func (app *App) retireNativeValidator(ctx sdk.Context, proposal govv1.Proposal) error {
	address, operator, err := app.nativeValidatorRetirement(ctx, proposal.Metadata, proposal.Proposer)
	if err != nil {
		return err
	}
	keeper := app.NativeValidatorKeeper
	supply := func(read sdk.Context) sdkmath.Int {
		return app.BankKeeper.GetSupply(read, identity.BaseDenom).Amount
	}
	return app.withdrawals.executor.RetireNativeValidator(ctx, address, supply, func(next sdk.Context) error {
		if err := keeper.RetireNativeValidator(next, operator); err != nil {
			return err
		}
		eligible, err := consensusOperators(next, keeper)
		if err != nil {
			return err
		}
		if err := keeper.ValidateStakeConsensusSelection(next, eligible); err != nil {
			return err
		}
		next.EventManager().EmitEvent(sdk.NewEvent("decimal_native_validator_retired",
			sdk.NewAttribute("proposal_id", strconv.FormatUint(proposal.Id, 10)), sdk.NewAttribute("operator", operator)))
		return nil
	})
}
