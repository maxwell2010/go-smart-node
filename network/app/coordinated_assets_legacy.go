//go:build !mintcandy_coordinated_assets || (!mintcandy_mainnet && !mintcandy_testnet)

package app

import sdk "github.com/cosmos/cosmos-sdk/types"

func (*App) applyCoordinatedAssets(sdk.Context) error { return nil }
