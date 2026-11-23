package launchgenesis

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// These are the _name and _symbol words of the pinned ERC20Upgradeable
// layout in sources/DecimalTokenCenter.sol. The wrapper still uses the
// original runtime and registry key in every network.
var wrapperNameSlot = common.HexToHash("0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace03")
var wrapperSymbolSlot = common.HexToHash("0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace04")

const legacyWrapperName = "MintCandy Wrapped DEL"
const legacyWrapperSymbol = "WDEL"

func shortStorageString(value string) common.Hash {
	var word common.Hash
	copy(word[:], value)
	word[len(word)-1] = byte(len(value) * 2)
	return word
}

func brandFoundingWrapper(a *app.App, ctx sdk.Context, wrapper common.Address) error {
	keeper := a.EVMKeeper
	if keeper.GetState(ctx, wrapper, wrapperNameSlot) != shortStorageString(legacyWrapperName) ||
		keeper.GetState(ctx, wrapper, wrapperSymbolSlot) != shortStorageString(legacyWrapperSymbol) {
		return fmt.Errorf("unexpected initial wrapper metadata")
	}
	name, symbol := shortStorageString(identity.WrappedMSPName), shortStorageString(identity.WrappedMSPSymbol)
	keeper.SetState(ctx, wrapper, wrapperNameSlot, name[:])
	keeper.SetState(ctx, wrapper, wrapperSymbolSlot, symbol[:])
	return nil
}

func foundingWrapperSymbol(a *app.App, ctx sdk.Context, wrapper common.Address) (string, error) {
	name := a.EVMKeeper.GetState(ctx, wrapper, wrapperNameSlot)
	symbol := a.EVMKeeper.GetState(ctx, wrapper, wrapperSymbolSlot)
	switch {
	case name == shortStorageString(identity.WrappedMSPName) && symbol == shortStorageString(identity.WrappedMSPSymbol):
		return identity.WrappedMSPSymbol, nil
	case identity.ProfileChainID == "" && name == shortStorageString(legacyWrapperName) && symbol == shortStorageString(legacyWrapperSymbol):
		return legacyWrapperSymbol, nil
	default:
		return "", fmt.Errorf("invalid committed wrapper name or symbol")
	}
}
