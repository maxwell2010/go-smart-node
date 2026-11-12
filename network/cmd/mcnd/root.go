// Adapted from cosmos/evm/evmd v0.6.3 (Apache-2.0).
// Uses stateless client codecs and exactly one lazily constructed application.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cast"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	cmtcfg "github.com/cometbft/cometbft/config"
	cmtcli "github.com/cometbft/cometbft/libs/cli"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/evm/config"
	"github.com/cosmos/evm/crypto/hd"
	evmencoding "github.com/cosmos/evm/encoding"
	cosmosevmserver "github.com/cosmos/evm/server"
	srvflags "github.com/cosmos/evm/server/flags"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"path/filepath"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	snapshottypes "cosmossdk.io/store/snapshots/types"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	clientcfg "github.com/cosmos/cosmos-sdk/client/config"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/pruning"
	"github.com/cosmos/cosmos-sdk/client/rpc"
	"github.com/cosmos/cosmos-sdk/client/snapshot"
	sdkserver "github.com/cosmos/cosmos-sdk/server"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	authcmd "github.com/cosmos/cosmos-sdk/x/auth/client/cli"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"
)

// Only the start command may pass this typed option to the app constructor.
type stateSyncGenesisPath string

const stateSyncGenesisOption = "mcnd-internal-state-sync-genesis"

// NewRootCmd creates a stateless root command. It is called once in the
// main function.
func NewRootCmd() *cobra.Command {
	encodingConfig := ownedapp.MakeEncodingConfig(990001)
	basics := ownedapp.NewBasicModuleManager()
	initClientCtx := client.Context{}.
		WithCodec(encodingConfig.Codec).
		WithInterfaceRegistry(encodingConfig.InterfaceRegistry).
		WithTxConfig(encodingConfig.TxConfig).
		WithLegacyAmino(encodingConfig.Amino).
		WithInput(os.Stdin).
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithBroadcastMode("sync").
		WithHomeDir(defaultNodeHome()).
		WithViper("MCND").
		// Cosmos EVM specific setup
		WithKeyringOptions(hd.EthSecp256k1Option()).
		WithLedgerHasProtobuf(true)

	rootCmd := &cobra.Command{
		Use:   "mcnd",
		Short: "Separate MintCandy network candidate",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// set the default command outputs
			cmd.SetOut(cmd.OutOrStdout())
			cmd.SetErr(cmd.ErrOrStderr())
			// Stateless commands require no client config, keyring or RPC client.
			if statelessCommand(cmd) {
				return nil
			}

			initClientCtx = initClientCtx.WithCmdContext(cmd.Context())
			initClientCtx, err := client.ReadPersistentCommandFlags(initClientCtx, cmd.Flags())
			if err != nil {
				return err
			}

			chainID, evmID, guarded, err := configuredDomains(cmd, initClientCtx)
			if err != nil {
				return err
			}
			if guarded {
				if err := bindResolvedDomainFlags(cmd, chainID, evmID); err != nil {
					return err
				}
				initClientCtx = initClientCtx.WithChainID(chainID)
			}
			initClientCtx, err = clientcfg.ReadFromClientConfig(initClientCtx)
			if err != nil {
				return err
			}
			if guarded {
				enc := ownedapp.MakeEncodingConfig(evmID)
				initClientCtx = initClientCtx.WithChainID(chainID).WithCodec(enc.Codec).WithInterfaceRegistry(enc.InterfaceRegistry).WithTxConfig(enc.TxConfig).WithLegacyAmino(enc.Amino)
			}
			if err := client.SetCmdClientContextHandler(initClientCtx, cmd); err != nil {
				return err
			}

			if !guarded {
				return nil
			}
			customAppTemplate, rawAppConfig := config.InitAppConfig(identity.BaseDenom, evmID)
			customAppConfig := rawAppConfig.(config.EVMAppConfig)
			customAppConfig.API.Address = "tcp://127.0.0.1:1317"
			customAppConfig.GRPC.Address = "127.0.0.1:9090"
			customAppConfig.JSONRPC.Address = "127.0.0.1:8545"
			customAppConfig.JSONRPC.WsAddress = "127.0.0.1:8546"
			customTMConfig := initCometConfig()

			if err := sdkserver.InterceptConfigsPreRunHandler(cmd, customAppTemplate, customAppConfig, customTMConfig); err != nil {
				return err
			}
			if guarded {
				serverCtx := sdkserver.GetServerContextFromCmd(cmd)
				serverCtx.Viper.Set(flags.FlagChainID, chainID)
				serverCtx.Viper.Set(srvflags.EVMChainID, evmID)
				if cmd.Name() == "start" && serverCtx.Config.StateSync.Enable {
					withComet, err := cmd.Flags().GetBool(srvflags.WithCometBFT)
					if err != nil {
						return err
					}
					queryOnly, err := cmd.Flags().GetBool(srvflags.GRPCOnly)
					if err != nil {
						return err
					}
					if withComet && !queryOnly {
						serverCtx.Viper.Set(stateSyncGenesisOption, stateSyncGenesisPath(serverCtx.Config.GenesisFile()))
					}
				}
			}
			return nil
		},
	}

	initRootCmd(rootCmd, encodingConfig, basics)
	if err := enhanceModuleCommands(rootCmd, initClientCtx, basics); err != nil {
		panic(err)
	}
	addDomainFlags(rootCmd)
	rootCmd.AddCommand(nativeValidatorRegistrationMetadataCmd())

	return rootCmd
}

func statelessCommand(cmd *cobra.Command) bool {
	for cmd.Parent() != nil && cmd.Parent() != cmd.Root() {
		cmd = cmd.Parent()
	}
	switch cmd.Name() {
	case "version", "completion", "help", nativeValidatorRegistrationMetadataCommandName:
		return true
	default:
		return false
	}
}

// initCometConfig helps to override default CometBFT Config values.
// return cmtcfg.DefaultConfig if no custom configuration is required for the application.
func initCometConfig() *cmtcfg.Config {
	cfg := cmtcfg.DefaultConfig()
	cfg.RPC.ListenAddress = "tcp://127.0.0.1:26657"
	cfg.P2P.ListenAddress = "tcp://127.0.0.1:26656"
	cfg.P2P.PexReactor = false
	cfg.P2P.Seeds = ""
	cfg.P2P.PersistentPeers = ""

	// these values put a higher strain on node memory
	// cfg.P2P.MaxNumInboundPeers = 100
	// cfg.P2P.MaxNumOutboundPeers = 40

	return cfg
}

func initRootCmd(rootCmd *cobra.Command, encodingConfig evmencoding.Config, basics module.BasicManager) {
	home := defaultNodeHome()
	sdkAppCreator := func(l log.Logger, d dbm.DB, w io.Writer, ao servertypes.AppOptions) servertypes.Application {
		return newApp(l, d, w, ao)
	}
	initCmd := genutilcli.InitCmd(initializationBasics(encodingConfig.Codec), home)
	_ = initCmd.Flags().Set(genutilcli.FlagDefaultBondDenom, identity.BaseDenom)
	initCmd.Flags().Lookup(genutilcli.FlagDefaultBondDenom).DefValue = identity.BaseDenom
	genesisCommands := genutilcli.Commands(encodingConfig.TxConfig, basics, home)
	addNativeGenesisCommands(genesisCommands)
	rootCmd.AddCommand(initCmd, genesisCommands, cmtcli.NewCompletionCmd(rootCmd, true), pruning.Cmd(sdkAppCreator, home), snapshot.Cmd(sdkAppCreator))

	// add Cosmos EVM' flavored TM commands to start server, etc.
	cosmosevmserver.AddCommands(
		rootCmd,
		cosmosevmserver.NewDefaultStartOptions(newApp, home),
		appExport,
		addModuleInitFlags,
	)

	// add Cosmos EVM key commands
	rootCmd.AddCommand(
		safeKeyCommands(),
	)

	// add keybase, auxiliary RPC, query, genesis, and tx child commands
	rootCmd.AddCommand(
		sdkserver.StatusCommand(),
		queryCommand(),
		txCommand(),
	)

	// add general tx flags to the root command
	var err error
	_, err = srvflags.AddTxFlags(rootCmd)
	if err != nil {
		panic(err)
	}
}

func addModuleInitFlags(_ *cobra.Command) {}

func queryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "query",
		Aliases:                    []string{"q"},
		Short:                      "Querying subcommands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		rpc.QueryEventForTxCmd(),
		rpc.ValidatorCommand(),
		authcmd.QueryTxsByEventsCmd(),
		authcmd.QueryTxCmd(),
		sdkserver.QueryBlockCmd(),
		sdkserver.QueryBlockResultsCmd(),
	)

	cmd.PersistentFlags().String(flags.FlagChainID, "", "The network chain ID")

	return cmd
}

func txCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "tx",
		Short:                      "Transactions subcommands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		authcmd.GetSignCommand(),
		authcmd.GetSignBatchCommand(),
		authcmd.GetMultiSignCommand(),
		authcmd.GetMultiSignBatchCmd(),
		authcmd.GetValidateSignaturesCommand(),
		authcmd.GetBroadcastCommand(),
		authcmd.GetEncodeCommand(),
		authcmd.GetDecodeCommand(),
		authcmd.GetSimulateCmd(),
	)

	cmd.PersistentFlags().String(flags.FlagChainID, "", "The network chain ID")

	return cmd
}

// newApp creates the application
func newApp(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	appOpts servertypes.AppOptions,
) cosmosevmserver.Application {
	if err := validateApplicationHome(appOpts); err != nil {
		panic(err)
	}
	var cache storetypes.MultiStorePersistentCache

	path, stateSync := appOpts.Get(stateSyncGenesisOption).(stateSyncGenesisPath)
	// The SDK reloads stores after restoring the snapshot but retains its
	// inter-block cache, including misses from the pre-restore database.
	if !stateSync && cast.ToBool(appOpts.Get(sdkserver.FlagInterBlockCache)) {
		cache = store.NewCommitKVStoreCacheManager()
	}

	pruningOpts, err := sdkserver.GetPruningOptionsFromFlags(appOpts)
	if err != nil {
		panic(err)
	}

	// get the chain id
	chainID, err := getChainIDFromOpts(appOpts)
	if err != nil {
		panic(err)
	}

	snapshotStore, err := sdkserver.GetSnapshotStore(appOpts)
	if err != nil {
		panic(err)
	}

	snapshotOptions := snapshottypes.NewSnapshotOptions(
		cast.ToUint64(appOpts.Get(sdkserver.FlagStateSyncSnapshotInterval)),
		cast.ToUint32(appOpts.Get(sdkserver.FlagStateSyncSnapshotKeepRecent)),
	)

	baseappOptions := []func(*baseapp.BaseApp){
		baseapp.SetPruning(pruningOpts),
		baseapp.SetMinGasPrices(cast.ToString(appOpts.Get(sdkserver.FlagMinGasPrices))),
		baseapp.SetQueryGasLimit(cast.ToUint64(appOpts.Get(sdkserver.FlagQueryGasLimit))),
		baseapp.SetHaltHeight(cast.ToUint64(appOpts.Get(sdkserver.FlagHaltHeight))),
		baseapp.SetHaltTime(cast.ToUint64(appOpts.Get(sdkserver.FlagHaltTime))),
		baseapp.SetMinRetainBlocks(cast.ToUint64(appOpts.Get(sdkserver.FlagMinRetainBlocks))),
		baseapp.SetInterBlockCache(cache),
		baseapp.SetTrace(cast.ToBool(appOpts.Get(sdkserver.FlagTrace))),
		baseapp.SetIndexEvents(cast.ToStringSlice(appOpts.Get(sdkserver.FlagIndexEvents))),
		baseapp.SetSnapshot(snapshotStore, snapshotOptions),
		baseapp.SetIAVLCacheSize(cast.ToInt(appOpts.Get(sdkserver.FlagIAVLCacheSize))),
		baseapp.SetIAVLDisableFastNode(cast.ToBool(appOpts.Get(sdkserver.FlagDisableIAVLFastNode))),
		baseapp.SetChainID(chainID),
	}

	app := ownedapp.NewApp(logger, db, true, appOpts, baseappOptions...)
	if stateSync && app.LastBlockHeight() == 0 {
		doc, state, err := readNativeGenesisAt(string(path))
		if err != nil {
			panic(fmt.Errorf("read state sync genesis: %w", err))
		}
		if doc.ChainID != chainID {
			panic(errors.New("state sync genesis differs from the application chain"))
		}
		if err := app.PrepareStateSyncGenesis(state); err != nil {
			panic(fmt.Errorf("prepare state sync genesis: %w", err))
		}
	}
	app.SetCommitMultiStoreTracer(traceStore)
	return app
}

// appExport creates a new application (optionally at a given height) and exports state.
func appExport(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	height int64,
	forZeroHeight bool,
	jailAllowedAddrs []string,
	appOpts servertypes.AppOptions,
	modulesToExport []string,
) (servertypes.ExportedApp, error) {
	if err := validateApplicationHome(appOpts); err != nil {
		return servertypes.ExportedApp{}, err
	}
	var exampleApp *ownedapp.App

	// this check is necessary as we use the flag in x/upgrade.
	// we can exit more gracefully by checking the flag here.
	homePath, ok := appOpts.Get(flags.FlagHome).(string)
	if !ok || homePath == "" {
		return servertypes.ExportedApp{}, errors.New("application home not set")
	}

	viperAppOpts, ok := appOpts.(*viper.Viper)
	if !ok {
		return servertypes.ExportedApp{}, errors.New("appOpts is not viper.Viper")
	}

	// overwrite the FlagInvCheckPeriod
	viperAppOpts.Set(sdkserver.FlagInvCheckPeriod, 1)
	appOpts = viperAppOpts

	// get the chain id
	chainID, err := getChainIDFromOpts(appOpts)
	if err != nil {
		return servertypes.ExportedApp{}, err
	}

	if height != -1 {
		exampleApp = ownedapp.NewApp(logger, db, false, appOpts, baseapp.SetChainID(chainID))

		if err := exampleApp.LoadHeight(height); err != nil {
			return servertypes.ExportedApp{}, err
		}
	} else {
		exampleApp = ownedapp.NewApp(logger, db, true, appOpts, baseapp.SetChainID(chainID)) // TODO:VLAD - Remove // TODO:VLAD - Remove appoptions and evmchainid
	}

	defer exampleApp.Close()
	return exampleApp.ExportAppStateAndValidators(forZeroHeight, jailAllowedAddrs, modulesToExport)
}

// getChainIDFromOpts returns the chain Id from app Opts
// It first tries to get from the chainId flag, if not available
// it will load from home
func getChainIDFromOpts(appOpts servertypes.AppOptions) (chainID string, err error) {
	// Get the chain Id from appOpts
	chainID = cast.ToString(appOpts.Get(flags.FlagChainID))
	if chainID == "" {
		// If not available load from home
		homeDir := cast.ToString(appOpts.Get(flags.FlagHome))
		chainID, err = config.GetChainIDFromHome(homeDir)
		if err != nil {
			return "", err
		}
	}

	return
}

func defaultNodeHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	return filepath.Join(home, ".mcnd")
}
func safeKeyCommands() *cobra.Command {
	cmd := keys.Commands()
	add, _, err := cmd.Find([]string{"add"})
	if err != nil {
		panic(err)
	}
	// Operators use Ethereum account bytes by default. Native Cosmos relayers
	// explicitly request --key-type secp256k1 in their separate fixture.
	keyType := add.Flags().Lookup("key-type")
	if keyType == nil {
		panic("SDK keys add has no key-type flag")
	}
	if err := keyType.Value.Set("eth_secp256k1"); err != nil {
		panic(err)
	}
	keyType.DefValue = "eth_secp256k1"
	for _, sub := range cmd.Commands() {
		switch sub.Name() {
		case "export", "import", "rename", "unsafe-export-eth-key":
			cmd.RemoveCommand(sub)
		}
	}
	return cmd
}
func validateApplicationHome(opts servertypes.AppOptions) error {
	return identity.ValidateHomeGenesis(cast.ToString(opts.Get(flags.FlagHome)), cast.ToString(opts.Get(flags.FlagChainID)), cast.ToUint64(opts.Get(srvflags.EVMChainID)))
}
