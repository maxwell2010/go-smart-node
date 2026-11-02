// ibcreference is disposable interoperability test tooling, not a release daemon.
// It intentionally never imports network/app: its SDK address prefix is cosmos.
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"cosmossdk.io/log/v2"
	"cosmossdk.io/math"
	cmtconfig "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/p2p"
	"github.com/cometbft/cometbft/privval"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/server"
	servercmd "github.com/cosmos/cosmos-sdk/server/cmd"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	"github.com/cosmos/go-bip39"
	"github.com/cosmos/ibc-go/v11/testing/simapp"
	"github.com/spf13/cobra"
)

const chainID = "ibc-reference-1"
const manifestName = "disposable-ibc-reference.json"

type options map[string]any

func (o options) Get(key string) any { return o[key] }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount("cosmos", "cosmospub")
	cfg.SetBech32PrefixForValidator("cosmosvaloper", "cosmosvaloperpub")
	cfg.SetBech32PrefixForConsensusNode("cosmosvalcons", "cosmosvalconspub")
	cfg.SetCoinType(118)
	cfg.Seal()
	temp, err := os.MkdirTemp("", "ibc-reference-codec-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	probe := simapp.NewSimApp(log.NewNopLogger(), dbm.NewMemDB(), nil, true, options{flags.FlagHome: temp})
	defer probe.Close()
	clientCtx := client.Context{}.WithCodec(probe.AppCodec()).WithInterfaceRegistry(probe.InterfaceRegistry()).WithTxConfig(probe.GetTxConfig()).WithLegacyAmino(probe.LegacyAmino()).WithInput(os.Stdin).WithOutput(os.Stdout).WithChainID(chainID)
	root := &cobra.Command{Use: "ibcreference", Short: "Disposable loopback-only SDK IBC counterparty", SilenceUsage: true}
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		home, _ := cmd.Flags().GetString(flags.FlagHome)
		if !filepath.IsAbs(home) {
			return fmt.Errorf("an absolute disposable test home is required")
		}
		if cmd.Name() == "prepare" {
			return nil
		}
		ctx := clientCtx.WithHomeDir(home)
		if err := client.SetCmdClientContextHandler(ctx, cmd); err != nil {
			return err
		}
		if err := server.InterceptConfigsPreRunHandler(cmd, "", nil, cmtconfig.DefaultConfig()); err != nil {
			return err
		}
		return validateFixture(home, server.GetServerContextFromCmd(cmd))
	}
	prepare := &cobra.Command{Use: "prepare", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		home, _ := cmd.Flags().GetString(flags.FlagHome)
		rpc, _ := cmd.Flags().GetInt("rpc-port")
		p2p, _ := cmd.Flags().GetInt("p2p-port")
		grpc, _ := cmd.Flags().GetInt("grpc-port")
		return prepareFixture(home, rpc, p2p, grpc)
	}}
	prepare.Flags().Int("rpc-port", 28757, "Private loopback RPC port")
	prepare.Flags().Int("p2p-port", 28756, "Private loopback P2P port")
	prepare.Flags().Int("grpc-port", 28790, "Private loopback gRPC port")
	root.AddCommand(prepare)
	root.AddCommand(server.StartCmd(func(logger log.Logger, db dbm.DB, opts servertypes.AppOptions) servertypes.Application {
		return simapp.NewSimApp(logger, db, nil, true, opts, append(server.DefaultBaseappOptions(opts), baseapp.SetChainID(chainID))...)
	}, ""))
	return servercmd.Execute(root, "IBCREFERENCE", "")
}

func validateFixture(home string, ctx *server.Context) error {
	data, err := os.ReadFile(filepath.Join(home, manifestName))
	if err != nil {
		return fmt.Errorf("not a prepared disposable test fixture: %w", err)
	}
	var marker struct {
		Disposable bool   `json:"disposable"`
		ChainID    string `json:"chain_id"`
	}
	if err = json.Unmarshal(data, &marker); err != nil {
		return err
	}
	if !marker.Disposable || marker.ChainID != chainID {
		return fmt.Errorf("only the dedicated disposable reference chain is allowed")
	}
	genesis, err := cmttypes.GenesisDocFromFile(filepath.Join(home, "config", "genesis.json"))
	if err != nil {
		return err
	}
	if genesis.ChainID != chainID {
		return fmt.Errorf("wrong reference signing domain")
	}
	if ctx.Config.P2P.Seeds != "" || ctx.Config.P2P.PersistentPeers != "" || ctx.Config.P2P.PexReactor {
		return fmt.Errorf("reference peers and PEX must be disabled")
	}
	for _, address := range []string{ctx.Config.RPC.ListenAddress, ctx.Config.P2P.ListenAddress} {
		u, parseErr := url.Parse(address)
		if parseErr != nil || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
			return fmt.Errorf("reference listeners must be loopback: %s", address)
		}
	}
	appCfg, err := serverconfig.GetConfig(ctx.Viper)
	if err != nil {
		return err
	}
	for _, listener := range []struct {
		enabled bool
		address string
	}{{appCfg.GRPC.Enable, appCfg.GRPC.Address}, {appCfg.API.Enable, appCfg.API.Address}} {
		if !listener.enabled {
			continue
		}
		address := listener.address
		if u, parseErr := url.Parse(address); parseErr == nil && u.Host != "" {
			address = u.Host
		}
		host, _, splitErr := net.SplitHostPort(address)
		if splitErr != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return fmt.Errorf("reference app listeners must be loopback")
		}
	}
	return nil
}

func prepareFixture(home string, rpcPort, p2pPort, grpcPort int) error {
	if !filepath.IsAbs(home) {
		return fmt.Errorf("absolute NEW disposable home required")
	}
	ports := map[int]bool{}
	for _, port := range []int{rpcPort, p2pPort, grpcPort} {
		if port < 1024 || port > 65535 || ports[port] {
			return fmt.Errorf("three distinct unprivileged ports required")
		}
		ports[port] = true
	}
	if err := os.Mkdir(home, 0700); err != nil {
		return fmt.Errorf("refuse existing output: %w", err)
	}
	for _, dir := range []string{"config", "data"} {
		if err := os.Mkdir(filepath.Join(home, dir), 0700); err != nil {
			return err
		}
	}
	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		return err
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return err
	}
	derived, err := hd.Secp256k1.Derive()(mnemonic, "", "m/44'/118'/0'/0/0")
	if err != nil {
		return err
	}
	key := hd.Secp256k1.Generate()(derived)
	account := authtypes.NewBaseAccount(key.PubKey().Address().Bytes(), key.PubKey(), 0, 0)
	pv := privval.GenFilePV(filepath.Join(home, "config", "priv_validator_key.json"), filepath.Join(home, "data", "priv_validator_state.json"))
	pv.Save()
	pub, err := pv.GetPubKey()
	if err != nil {
		return err
	}
	vals := cmttypes.NewValidatorSet([]*cmttypes.Validator{cmttypes.NewValidator(pub, 1)})
	app := simapp.NewSimApp(log.NewNopLogger(), dbm.NewMemDB(), nil, true, options{flags.FlagHome: home})
	defer app.Close()
	genesis, err := simtestutil.GenesisStateWithValSet(app.AppCodec(), app.DefaultGenesis(), vals, []authtypes.GenesisAccount{account}, banktypes.Balance{Address: account.GetAddress().String(), Coins: sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, math.NewInt(1_000_000_000_000_000_000)))})
	if err != nil {
		return err
	}
	var slashing slashingtypes.GenesisState
	app.AppCodec().MustUnmarshalJSON(genesis[slashingtypes.ModuleName], &slashing)
	consAddr := sdk.ConsAddress(pub.Address())
	slashing.SigningInfos = []slashingtypes.SigningInfo{{Address: consAddr.String(), ValidatorSigningInfo: slashingtypes.NewValidatorSigningInfo(consAddr, 0, 0, time.Unix(0, 0).UTC(), false, 0)}}
	genesis[slashingtypes.ModuleName] = app.AppCodec().MustMarshalJSON(&slashing)
	state, err := json.Marshal(genesis)
	if err != nil {
		return err
	}
	doc := cmttypes.GenesisDoc{ChainID: chainID, GenesisTime: time.Now().UTC().Add(-time.Second), ConsensusParams: cmttypes.DefaultConsensusParams(), AppState: state}
	if err = doc.SaveAs(filepath.Join(home, "config", "genesis.json")); err != nil {
		return err
	}
	cfg := cmtconfig.DefaultConfig().SetRoot(home)
	cfg.Moniker = "disposable-ibc-reference"
	cfg.RPC.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", rpcPort)
	cfg.P2P.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", p2pPort)
	cfg.P2P.Seeds = ""
	cfg.P2P.PersistentPeers = ""
	cfg.P2P.PexReactor = false
	cfg.Consensus.TimeoutCommit = 500 * time.Millisecond
	cmtconfig.WriteConfigFile(filepath.Join(home, "config", "config.toml"), cfg)
	if _, err = p2p.LoadOrGenNodeKey(filepath.Join(home, "config", "node_key.json")); err != nil {
		return err
	}
	appCfg := serverconfig.DefaultConfig()
	appCfg.MinGasPrices = "0.025stake"
	appCfg.GRPC.Enable = true
	appCfg.GRPC.Address = fmt.Sprintf("127.0.0.1:%d", grpcPort)
	appCfg.GRPCWeb.Enable = false
	appCfg.API.Enable = false
	serverconfig.WriteConfigFile(filepath.Join(home, "config", "app.toml"), appCfg)
	if err = os.WriteFile(filepath.Join(home, "mnemonic.txt"), []byte(mnemonic+"\n"), 0600); err != nil {
		return err
	}
	marker := map[string]any{"disposable": true, "chain_id": chainID, "address": account.GetAddress().String(), "rpc_port": rpcPort, "p2p_port": p2pPort, "grpc_port": grpcPort, "warning": "EPHEMERAL TEST KEYS: never publish or fund on public networks"}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, manifestName), data, 0600)
}
