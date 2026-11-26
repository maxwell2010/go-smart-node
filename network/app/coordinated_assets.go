//go:build mintcandy_coordinated_assets && (mintcandy_mainnet || mintcandy_testnet)

package app

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	upgradetypes "cosmossdk.io/x/upgrade/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const coordinatedAssetsMarker byte = 0x88

// Chain identity and height are compiled into the candidate signed by operators.
// No RPC, clock, local file or environment value participates in activation.
func (app *App) applyCoordinatedAssets(s sdk.Context) error {
	if s.BlockHeight() < coordinatedAssetsHeight {
		return nil
	}
	if s.ExecMode() != sdk.ExecModeFinalize || s.ChainID() != identity.ProfileChainID || app.chainID != s.ChainID() {
		return errors.New("coordinated asset upgrade requires FinalizeBlock on its compiled network")
	}
	store := s.KVStore(app.keys[validatortypes.StoreKey])
	marker := store.Get([]byte{coordinatedAssetsMarker})
	if s.BlockHeight() > coordinatedAssetsHeight {
		if !bytes.Equal(marker, []byte{1}) {
			return errors.New("missing committed coordinated asset upgrade at its compiled height")
		}
		return nil
	}
	if marker != nil {
		return errors.New("coordinated asset upgrade already applied")
	}
	if app.withdrawals == nil || app.withdrawals.customCollateral == nil || !app.usesNativeUpgradeGovernance() {
		return errors.New("coordinated asset upgrade requires the native launch profile")
	}
	if _, err := app.UpgradeKeeper.GetUpgradePlan(s); !errors.Is(err, upgradetypes.ErrNoUpgradePlanFound) {
		if err != nil {
			return err
		}
		return errors.New("another SDK upgrade plan is pending")
	}
	if s.ChainID() == identity.MainnetChainID {
		done, err := app.UpgradeKeeper.GetDoneHeight(s, nativeSDKMigrationPlan)
		if err != nil {
			return err
		}
		if done != 60000 || !bytes.Equal(store.Get([]byte{userNFTRefundActivationKey}), []byte{1}) {
			return errors.New("coordinated mainnet asset upgrade requires the completed height 60000 SDK and NFT refund upgrade")
		}
	}
	child, commit := s.CacheContext()
	if err := app.activateDRC20IssuanceTariff(child); err != nil {
		return fmt.Errorf("coordinated issuance activation: %w", err)
	}
	pin := coordinatedPoolPin(child, app.withdrawals.customCollateral)
	if err := app.installNativeLiquidityPoolUpgrade(child, pin); err != nil {
		return fmt.Errorf("coordinated pool activation: %w", err)
	}
	child.KVStore(app.keys[validatortypes.StoreKey]).Set([]byte{coordinatedAssetsMarker}, []byte{1})
	child.EventManager().EmitEvent(sdk.NewEvent("mintcandy_coordinated_assets_activated",
		sdk.NewAttribute("upgrade", "mintcandy-coordinated-assets-v1"), sdk.NewAttribute("height", strconv.FormatInt(s.BlockHeight(), 10))))
	commit()
	return nil
}
