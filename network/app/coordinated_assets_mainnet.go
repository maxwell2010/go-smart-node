//go:build mintcandy_coordinated_assets && mintcandy_mainnet

package app

import sdk "github.com/cosmos/cosmos-sdk/types"

const coordinatedAssetsHeight int64 = 62000

func coordinatedPoolPin(sdk.Context, *customCollateralRegistry) NativeLiquidityPoolProfile {
	return mainnetDRC20PoolPin()
}
