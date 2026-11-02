package main

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"
	"cosmossdk.io/client/v2/autocli"
	"cosmossdk.io/x/evidence"
	feegrantcli "cosmossdk.io/x/feegrant/client/cli"
	feegrantmodule "cosmossdk.io/x/feegrant/module"
	"cosmossdk.io/x/upgrade"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/x/auth"
	vestingcli "github.com/cosmos/cosmos-sdk/x/auth/vesting/client/cli"
	authzcli "github.com/cosmos/cosmos-sdk/x/authz/client/cli"
	authzmodule "github.com/cosmos/cosmos-sdk/x/authz/module"
	"github.com/cosmos/cosmos-sdk/x/bank"
	bankcli "github.com/cosmos/cosmos-sdk/x/bank/client/cli"
	"github.com/cosmos/cosmos-sdk/x/consensus"
	distr "github.com/cosmos/cosmos-sdk/x/distribution"
	distrcli "github.com/cosmos/cosmos-sdk/x/distribution/client/cli"
	"github.com/cosmos/cosmos-sdk/x/gov"
	"github.com/cosmos/cosmos-sdk/x/mint"
	"github.com/cosmos/cosmos-sdk/x/slashing"
	"github.com/cosmos/cosmos-sdk/x/staking"
	stakingcli "github.com/cosmos/cosmos-sdk/x/staking/client/cli"
	evmaddress "github.com/cosmos/evm/encoding/address"
	"github.com/cosmos/evm/x/erc20"
	"github.com/cosmos/evm/x/feemarket"
	"github.com/cosmos/evm/x/vm"
	vmcli "github.com/cosmos/evm/x/vm/client/cli"
	transfer "github.com/cosmos/ibc-go/v10/modules/apps/transfer"
	ibc "github.com/cosmos/ibc-go/v10/modules/core"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
)

// Only stateless command descriptors and BasicManager commands are consulted.
// None of these zero-value metadata providers constructs a keeper or database.
func enhanceModuleCommands(root *cobra.Command, ctx client.Context, basics module.BasicManager) error {
	query, _, err := root.Find([]string{"query"})
	if err != nil {
		return err
	}
	tx, _, err := root.Find([]string{"tx"})
	if err != nil {
		return err
	}
	basics.AddQueryCommands(query)
	txBasics := module.BasicManager{}
	for name, basic := range basics {
		switch name {
		case "upgrade", "staking", "distribution", "bank", "vesting", "authz", "feegrant", "evm":
		default:
			txBasics[name] = basic
		}
	}
	txBasics.AddTxCommands(tx)
	accountCodec := ctx.InterfaceRegistry.SigningContext().AddressCodec()
	validatorCodec := ctx.InterfaceRegistry.SigningContext().ValidatorAddressCodec()
	// These public SDK command constructors need only client address codecs.
	// Their AppModuleBasic zero values hold unexported codec fields, so do not
	// call those Basic GetTxCmd methods or fabricate an account keeper for CLI.
	tx.AddCommand(bankcli.NewTxCmd(accountCodec), stakingcli.NewTxCmd(validatorCodec, accountCodec), distrcli.NewTxCmd(validatorCodec, accountCodec), vestingcli.GetTxCmd(accountCodec), authzcli.GetTxCmd(accountCodec), feegrantcli.GetTxCmd(accountCodec), vmcli.NewTxCmd(accountCodec))
	providers := map[string]any{
		"auth": auth.AppModule{}, "bank": bank.AppModule{}, "staking": staking.AppModule{},
		"distribution": distr.AppModule{}, "gov": gov.AppModule{}, "mint": mint.AppModule{},
		"slashing": slashing.AppModule{}, "authz": authzmodule.AppModule{}, "consensus": consensus.AppModule{},
		"evidence": evidence.AppModule{}, "feegrant": feegrantmodule.AppModule{}, "upgrade": upgrade.AppModule{},
		"evm": vm.AppModule{}, "erc20": erc20.AppModule{}, "feemarket": feemarket.AppModule{},
		"ibc": ibc.AppModule{}, "transfer": transfer.AppModule{},
	}
	options := map[string]*autocliv1.ModuleOptions{}
	for name, provider := range providers {
		if descriptor, ok := provider.(interface {
			AutoCLIOptions() *autocliv1.ModuleOptions
		}); ok {
			if metadata := descriptor.AutoCLIOptions(); metadata != nil {
				copied := proto.Clone(metadata).(*autocliv1.ModuleOptions)
				// Retain upgrade queries and on-chain state, while omitting the SDK's
				// software-upgrade proposal helper that downloads external binaries.
				if name == "upgrade" {
					copied.Tx = nil
				}
				options[name] = copied
			}
		}
	}
	auto := autocli.AppOptions{
		ModuleOptions: options, ClientCtx: ctx,
		AddressCodec:          evmaddress.NewEvmCodec(identity.Bech32PrefixAccAddr),
		ValidatorAddressCodec: address.NewBech32Codec(identity.Bech32PrefixValAddr),
		ConsensusAddressCodec: address.NewBech32Codec(identity.Bech32PrefixConsAddr),
	}
	return auto.EnhanceRootCommand(root)
}
