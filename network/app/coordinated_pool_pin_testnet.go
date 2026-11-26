//go:build mintcandy_testnet && mintcandy_coordinated_assets

package app

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// The isolated factory uses the issuance block timestamp in the CREATE2 salt.
// Resolve the audited ticker and verify the pair's actual runtime and owner at installation.
func coordinatedPoolPin(ctx sdk.Context, r *customCollateralRegistry) NativeLiquidityPoolProfile {
	issuer := common.HexToAddress("0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf")
	return NativeLiquidityPoolProfile{
		Address:     nativeLiquidityPoolUpgradeAddress(identity.TestnetChainID),
		CUSDT:       common.BytesToAddress(ctx.KVStore(r.store).Get(symbolKey("cusdt"))),
		CUSDTOwner:  issuer,
		Initializer: issuer,
	}
}
