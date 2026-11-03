package main

import (
	"encoding/json"
	"errors"
	nativecointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"os"
	"path/filepath"
	"strings"

	"github.com/cosmos/cosmos-sdk/client"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/spf13/cobra"
)

func readNativeGenesis(cmd *cobra.Command) (*genutiltypes.AppGenesis, map[string]json.RawMessage, error) {
	ctx := client.GetClientContextFromCmd(cmd)
	return readNativeGenesisAt(filepath.Join(ctx.HomeDir, "config", "genesis.json"))
}
func readNativeGenesisAt(path string) (*genutiltypes.AppGenesis, map[string]json.RawMessage, error) {
	doc, err := genutiltypes.AppGenesisFromFile(path)
	if err != nil {
		return nil, nil, err
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(doc.AppState, &state); err != nil {
		return nil, nil, err
	}
	return doc, state, nil
}

func nativeAllocationCmd() *cobra.Command {
	return &cobra.Command{Use: "reconcile-native-allocation", Short: "Explicitly declare native genesis allocation from bank DEL balances", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := client.GetClientContextFromCmd(cmd)
		for _, name := range []string{"config", "data"} {
			info, err := os.Lstat(filepath.Join(ctx.HomeDir, name))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				return errors.New("genesis builder rejects configuration/data directory symlinks")
			}
		}
		entries, err := os.ReadDir(filepath.Join(ctx.HomeDir, "data"))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".db") || entry.Name() == "cs.wal" {
				return errors.New("existing chain data: genesis builder requires a fresh home")
			}
		}
		path := filepath.Join(ctx.HomeDir, "config", "genesis.json")
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("genesis symlink rejected")
		}
		doc, state, err := readNativeGenesis(cmd)
		if err != nil {
			return err
		}
		var before nativecointypes.GenesisState
		if err := ctx.Codec.UnmarshalJSON(state["coin"], &before); err != nil {
			return err
		}
		if err := ownedapp.ReconcileNativeGenesisAllocation(ctx.Codec, state); err != nil {
			return err
		}
		var after nativecointypes.GenesisState
		if err := ctx.Codec.UnmarshalJSON(state["coin"], &after); err != nil {
			return err
		}
		if !before.Params.BaseVolume.IsNil() && before.Params.BaseVolume.Equal(after.Params.BaseVolume) {
			return nil
		}
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		doc.AppState = data
		original, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		backup, err := os.OpenFile(path+".before-native-allocation", os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, writeErr := backup.Write(original)
		closeErr := backup.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return err
		}
		temporary, err := os.CreateTemp(filepath.Dir(path), "genesis.native-*.json")
		if err != nil {
			return err
		}
		tempName := temporary.Name()
		if err := temporary.Close(); err != nil {
			return err
		}
		defer os.Remove(tempName)
		if err := doc.SaveAs(tempName); err != nil {
			return err
		}
		if err := os.Chmod(tempName, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Rename(tempName, path)
	}}
}

func addNativeGenesisCommands(genesis *cobra.Command) {
	genesis.AddCommand(nativeAllocationCmd())
	for _, cmd := range genesis.Commands() {
		if cmd.Name() == "validate" {
			original := cmd.RunE
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				path := filepath.Join(client.GetClientContextFromCmd(cmd).HomeDir, "config", "genesis.json")
				if len(args) > 0 {
					path = args[0]
				}
				_, state, err := readNativeGenesisAt(path)
				if err != nil {
					return err
				}
				if err := ownedapp.ValidateNativeGenesisAllocation(client.GetClientContextFromCmd(cmd).Codec, state); err != nil {
					return err
				}
				return original(cmd, args)
			}
		}
	}
}
