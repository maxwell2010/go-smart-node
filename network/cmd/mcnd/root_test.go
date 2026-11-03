package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"
	sdkversion "github.com/cosmos/cosmos-sdk/version"
	srvflags "github.com/cosmos/evm/server/flags"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestCommandTreeDoesNotConstructApplication(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.Nil(t, evmtypes.GetChainConfig())
	for range 2 {
		root := NewRootCmd()
		require.Nil(t, evmtypes.GetChainConfig(), "static CLI must not reserve the process-global EVM keeper configuration")
		for _, path := range [][]string{{"init"}, {"genesis", "gentx"}, {"genesis", "collect-gentxs"}, {"start"}, {"keys", "add"}, {"query", "bank", "balances"}, {"tx", "bank", "send"}, {"tx", "ibc-transfer", "transfer"}} {
			command, _, err := root.Find(path)
			require.NoError(t, err)
			require.Equal(t, path[len(path)-1], command.Name())
		}
		var rejectDownloader func(*cobra.Command)
		rejectDownloader = func(cmd *cobra.Command) {
			require.NotEqual(t, "software-upgrade", cmd.Name())
			for _, child := range cmd.Commands() {
				rejectDownloader(child)
			}
		}
		rejectDownloader(root)
		keyAdd, _, err := root.Find([]string{"keys", "add"})
		require.NoError(t, err)
		require.Equal(t, "eth_secp256k1", keyAdd.Flag("key-type").Value.String())
		keys, _, err := root.Find([]string{"keys"})
		require.NoError(t, err)
		for _, command := range keys.Commands() {
			require.NotContains(t, []string{"export", "import", "rename", "unsafe-export-eth-key"}, command.Name())
		}
	}
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Empty(t, entries, "constructing a command tree must not initialize the user's home")
}

func TestInitUsesOwnedGenesisWithoutKeeper(t *testing.T) {
	encoding := ownedapp.MakeEncodingConfig(990001)
	basics := initializationBasics(encoding.Codec)
	require.Equal(t, ownedapp.DefaultGenesis(encoding.Codec), basics.DefaultGenesis(encoding.Codec))
	require.NoError(t, basics.ValidateGenesis(encoding.Codec, encoding.TxConfig, basics.DefaultGenesis(encoding.Codec)))
	require.Nil(t, evmtypes.GetChainConfig())
}

func TestDaemonUsesItsOwnApplicationIdentity(t *testing.T) {
	require.Equal(t, "mcnd", sdkversion.AppName)
	require.Equal(t, "MintCandy", sdkversion.Name)
	require.Equal(t, buildVersion, sdkversion.Version)
	require.Equal(t, buildCommit, sdkversion.Commit)
}

func TestCommandFlagsUseValidatedPersistedDomain(t *testing.T) {
	root := NewRootCmd()
	start, _, err := root.Find([]string{"start"})
	require.NoError(t, err)
	require.NoError(t, bindResolvedDomainFlags(start, "mintcandy_990002-1", 990002))
	require.Equal(t, "990002", start.Flag(srvflags.EVMChainID).Value.String())
	require.Equal(t, "mintcandy_990002-1", start.Flag("chain-id").Value.String())
	require.False(t, start.Flag(srvflags.EVMChainID).Changed)
}

func TestInformationalCommandsDoNotInitializeOrReadNodeHome(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	for _, args := range [][]string{{"version", "--long", "--output", "json"}, {"completion"}} {
		t.Run(args[0], func(t *testing.T) {
			home := filepath.Join(base, args[0]+"-new")
			run := func() []byte {
				root := NewRootCmd()
				var output bytes.Buffer
				root.SetOut(&output)
				root.SetErr(&output)
				root.SetArgs(append(append([]string(nil), args...), "--home", home))
				require.NoError(t, svrcmd.Execute(root, "MCND", defaultNodeHome()))
				return output.Bytes()
			}
			output := run()
			require.NotEmpty(t, output)
			_, err := os.Stat(home)
			require.True(t, os.IsNotExist(err), "informational command must not initialize home")
			if args[0] == "version" {
				var version map[string]interface{}
				require.NoError(t, json.Unmarshal(output, &version))
				require.Equal(t, "mcnd", version["server_name"])
				require.Equal(t, buildCommit, version["commit"])
			}
			// An unrelated existing home is not parsed or repaired by version or
			// completion. This is a private fixture, never an operator's home.
			require.NoError(t, os.MkdirAll(filepath.Join(home, "config"), 0o700))
			config := filepath.Join(home, "config", "client.toml")
			before := []byte("invalid fixture TOML [\n")
			require.NoError(t, os.WriteFile(config, before, 0o600))
			require.NotEmpty(t, run())
			after, err := os.ReadFile(config)
			require.NoError(t, err)
			require.Equal(t, before, after)
			entries, err := os.ReadDir(home)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, "config", entries[0].Name())
		})
	}
	require.Nil(t, evmtypes.GetChainConfig(), "informational commands must not construct EVM application")
}

func TestRegisteredSnapshotsRequireTheFreshNetworkDomain(t *testing.T) {
	root := NewRootCmd()
	snapshots, _, err := root.Find([]string{"snapshots"})
	require.NoError(t, err)
	require.Equal(t, "snapshots", snapshots.Name())
	for _, command := range snapshots.Commands() {
		require.True(t, needsDomain(command), command.CommandPath())
		require.NotNil(t, command.Flag("chain-id"), command.CommandPath())
	}
	for _, domainArgs := range [][]string{nil, {"--chain-id", "decimal_75-1", "--evm.evm-chain-id", "75"}} {
		root := NewRootCmd()
		home := filepath.Join(t.TempDir(), "unused-home")
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&output)
		args := append([]string{"snapshots", "list", "--home", home}, domainArgs...)
		root.SetArgs(args)
		require.Error(t, svrcmd.Execute(root, "MCND", defaultNodeHome()))
		_, err := os.Stat(home)
		require.True(t, os.IsNotExist(err), "rejected snapshot domain must not initialize the home")
	}
	require.Nil(t, evmtypes.GetChainConfig(), "snapshot admission rejection must precede application construction")
}
