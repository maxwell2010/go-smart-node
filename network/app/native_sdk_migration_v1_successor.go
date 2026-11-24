//go:build mcnd_native_sdk_migration_v1

package app

import (
	"context"

	upgradetypes "cosmossdk.io/x/upgrade/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

func (app *App) registerNativeGovernanceUpgradeHandlers() {
	app.UpgradeKeeper.SetUpgradeHandler(nativeSDKMigrationPlan, func(ctx context.Context, plan upgradetypes.Plan, from module.VersionMap) (module.VersionMap, error) {
		versions, err := app.runNativeSDKMigration(ctx, plan, from)
		if err != nil {
			return nil, err
		}
		if app.withdrawals.profile.EnableNFTDelegation && app.withdrawals.customCollateral != nil {
			sdk.UnwrapSDKContext(ctx).KVStore(app.keys[validatortypes.StoreKey]).Set(
				[]byte{userNFTRefundActivationKey}, []byte{1},
			)
		}
		return versions, nil
	})
}
