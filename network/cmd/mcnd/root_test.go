package main

import (
	"os"
	"testing"

	evmtypes "github.com/cosmos/evm/x/vm/types"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/stretchr/testify/require"
)

func TestCommandTreeDoesNotConstructApplication(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.Nil(t, evmtypes.GetChainConfig())
	for range 2 {
		root := NewRootCmd()
		require.Nil(t, evmtypes.GetChainConfig(), "static CLI must not reserve the process-global EVM keeper configuration")
		for _, path := range [][]string{{"init"}, {"genesis", "gentx"}, {"genesis", "collect-gentxs"}, {"start"}, {"keys", "add"}} {
			command, _, err := root.Find(path)
			require.NoError(t, err)
			require.Equal(t, path[len(path)-1], command.Name())
		}
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
