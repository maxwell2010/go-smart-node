package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeGenesisCommandIsStateless(t *testing.T) {
	root := NewRootCmd()
	command, rest, err := root.Find([]string{"genesis", "reconcile-native-allocation"})
	require.NoError(t, err)
	require.Empty(t, rest)
	require.Equal(t, "reconcile-native-allocation", command.Name())
	require.NotNil(t, command.RunE)
}

func TestNativeGenesisBuilderPreservesSDKGenesisAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "config"), 0700))
	cfg := ownedapp.MakeEncodingConfig(990001)
	genesis := ownedapp.DefaultGenesis(cfg.Codec)
	owner, err := address.NewBech32Codec("d0").BytesToString(bytes.Repeat([]byte{3}, 20))
	require.NoError(t, err)
	var bank banktypes.GenesisState
	cfg.Codec.MustUnmarshalJSON(genesis["bank"], &bank)
	bank.Balances = []banktypes.Balance{{Address: owner, Coins: sdk.NewCoins(sdk.NewInt64Coin("del", 123))}}
	bank.Supply = nil
	genesis["bank"] = cfg.Codec.MustMarshalJSON(&bank)
	data, err := json.Marshal(genesis)
	require.NoError(t, err)
	doc := genutiltypes.NewAppGenesisWithVersion("mintcandy_990001-1", data)
	doc.AppName = "fixture-app"
	doc.AppVersion = "fixture-version"
	path := filepath.Join(home, "config", "genesis.json")
	require.NoError(t, doc.SaveAs(path))
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	command := nativeAllocationCmd()
	command.SetContext(context.Background())
	require.NoError(t, client.SetCmdClientContext(command, client.Context{Codec: cfg.Codec, HomeDir: home}))
	require.NoError(t, command.RunE(command, nil))
	updated, state, err := readNativeGenesisAt(path)
	require.NoError(t, err)
	require.Equal(t, "fixture-app", updated.AppName)
	require.Equal(t, "fixture-version", updated.AppVersion)
	require.NoError(t, ownedapp.ValidateNativeGenesisAllocation(cfg.Codec, state))
	backup, err := os.ReadFile(path + ".before-native-allocation")
	require.NoError(t, err)
	require.Equal(t, original, backup)
	first, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, command.RunE(command, nil))
	second, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, first, second)
	// Validation must inspect the supplied file, not the valid default home.
	alternate := filepath.Join(home, "invalid-allocation.json")
	require.NoError(t, os.WriteFile(alternate, original, 0600))
	root := NewRootCmd()
	validate, _, err := root.Find([]string{"genesis", "validate"})
	require.NoError(t, err)
	validate.SetContext(context.Background())
	require.NoError(t, client.SetCmdClientContext(validate, client.Context{Codec: cfg.Codec, HomeDir: home}))
	require.ErrorContains(t, validate.RunE(validate, []string{alternate}), "explicitly run")
	require.NoError(t, os.MkdirAll(filepath.Join(home, "data", "application.db"), 0700))
	require.ErrorContains(t, command.RunE(command, nil), "existing chain data")
}
