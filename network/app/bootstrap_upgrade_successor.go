//go:build mintcandy_bootstrap_unanimity && (mintcandy_mainnet || mintcandy_testnet)

package app

import (
	"context"
	"errors"
	"strconv"

	upgradetypes "cosmossdk.io/x/upgrade/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func (app *App) registerBootstrapUnanimityHandler() {
	app.UpgradeKeeper.SetUpgradeHandler(bootstrapUnanimityPlan, app.activateBootstrapUnanimity)
}

func (app *App) activateBootstrapUnanimity(ctx context.Context, plan upgradetypes.Plan, from module.VersionMap) (module.VersionMap, error) {
	s := sdk.UnwrapSDKContext(ctx)
	if s.ChainID() != identity.ProfileChainID {
		return nil, errors.New("bootstrap voting plan requires the matching MintCandy network")
	}
	if err := app.verifyNativeUpgradeApproval(s, plan, bootstrapUnanimityPlan); err != nil {
		return nil, err
	}
	_, eligible, err := app.bootstrapOperators(s)
	if err != nil {
		return nil, err
	}
	var minimum uint64
	if eligible {
		minimum, err = app.GovKeeper.ProposalID.Peek(s)
		if err != nil {
			return nil, err
		}
		if minimum == 0 {
			return nil, errors.New("invalid next governance proposal ID")
		}
	}
	if err := app.NativeValidatorKeeper.InitializeBootstrapUnanimity(s, minimum); err != nil {
		return nil, err
	}
	s.EventManager().EmitEvent(sdk.NewEvent("mintcandy_bootstrap_unanimity",
		sdk.NewAttribute("enabled", strconv.FormatBool(eligible)), sdk.NewAttribute("minimum_proposal_id", strconv.FormatUint(minimum, 10))))
	return from, nil
}
