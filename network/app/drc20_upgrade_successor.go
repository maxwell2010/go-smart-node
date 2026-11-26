//go:build mintcandy_mainnet && mintcandy_drc20_issuance

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

func (app *App) registerDRC20IssuanceHandler() {
	app.UpgradeKeeper.SetUpgradeHandler(drc20IssuancePlan, app.runDRC20IssuanceUpgrade)
}

func (app *App) runDRC20IssuanceUpgrade(ctx context.Context, plan upgradetypes.Plan, from module.VersionMap) (module.VersionMap, error) {
	s := sdk.UnwrapSDKContext(ctx)
	if s.ChainID() != identity.MainnetChainID {
		return nil, errors.New("DRC20 issuance plan requires MintCandy mainnet")
	}
	if err := app.verifyNativeUpgradeApproval(s, plan, drc20IssuancePlan); err != nil {
		return nil, err
	}
	if err := app.activateDRC20IssuanceTariff(s); err != nil {
		return nil, err
	}
	s.EventManager().EmitEvent(sdk.NewEvent("mintcandy_drc20_issuance_activated",
		sdk.NewAttribute("height", strconv.FormatInt(s.BlockHeight(), 10))))
	return from, nil
}
