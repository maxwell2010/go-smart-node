package app

import (
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/auth/vesting"
	authzmodule "github.com/cosmos/cosmos-sdk/x/authz/module"
	"github.com/cosmos/cosmos-sdk/x/bank"
	"github.com/cosmos/cosmos-sdk/x/consensus"
	distr "github.com/cosmos/cosmos-sdk/x/distribution"
	"github.com/cosmos/cosmos-sdk/x/evidence"
	feegrantmodule "github.com/cosmos/cosmos-sdk/x/feegrant/module"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/cosmos/cosmos-sdk/x/gov"
	"github.com/cosmos/cosmos-sdk/x/mint"
	"github.com/cosmos/cosmos-sdk/x/slashing"
	"github.com/cosmos/cosmos-sdk/x/staking"
	"github.com/cosmos/cosmos-sdk/x/upgrade"
	evmencoding "github.com/cosmos/evm/encoding"
	"github.com/cosmos/evm/x/erc20"
	"github.com/cosmos/evm/x/feemarket"
	"github.com/cosmos/evm/x/vm"
	transfer "github.com/cosmos/ibc-go/v11/modules/apps/transfer"
	ibc "github.com/cosmos/ibc-go/v11/modules/core"
	ibctm "github.com/cosmos/ibc-go/v11/modules/light-clients/07-tendermint"
)

// NewBasicModuleManager registers the owned application's stateless module set.
// It opens no database and never constructs an EVM keeper or chain singleton.
func NewBasicModuleManager() module.BasicManager {
	return module.NewBasicManager(
		auth.AppModuleBasic{}, bank.AppModuleBasic{}, staking.AppModuleBasic{},
		mint.AppModuleBasic{}, distr.AppModuleBasic{}, slashing.AppModuleBasic{},
		gov.NewAppModuleBasic(nil), consensus.AppModuleBasic{}, upgrade.AppModuleBasic{},
		feegrantmodule.AppModuleBasic{}, evidence.NewAppModuleBasic(), authzmodule.AppModuleBasic{},
		genutil.NewAppModuleBasic(genutiltypes.DefaultMessageValidator), vesting.AppModuleBasic{},
		ibc.AppModuleBasic{}, ibctm.AppModuleBasic{}, transfer.AppModuleBasic{},
		vm.AppModuleBasic{}, feemarket.AppModuleBasic{}, erc20.AppModuleBasic{},
	)
}

// MakeEncodingConfig creates a stateless client codec/signing configuration.
// Launch and signing entry points must also validate both chain IDs; this
// function does not instantiate or reserve an EVM chain configuration.
func MakeEncodingConfig(evmChainID uint64) evmencoding.Config {
	config := evmencoding.MakeConfig(evmChainID)
	basics := NewBasicModuleManager()
	basics.RegisterLegacyAminoCodec(config.Amino)
	basics.RegisterInterfaces(config.InterfaceRegistry)
	return config
}
