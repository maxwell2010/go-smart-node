package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func needsDomain(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		switch current.Name() {
		case "init", "start", "export", "genesis", "tx", "pruning", "snapshot", "comet", "tendermint", "rollback":
			return true
		}
	}
	return false
}

// Read existing files without creating defaults. A rejected production home or
// mismatched signing domain must not acquire new client/application config.
func configuredDomains(cmd *cobra.Command, ctx client.Context) (string, uint64, bool, error) {
	if !needsDomain(cmd) {
		return "", 990001, false, nil // codec-only domain, never used to sign or construct an app
	}
	chainID := ctx.ChainID
	if chainID == "" {
		config := viper.New()
		config.SetConfigFile(filepath.Join(ctx.HomeDir, "config", "client.toml"))
		if err := config.ReadInConfig(); err == nil {
			chainID = config.GetString(flags.FlagChainID)
		} else if !os.IsNotExist(err) {
			return "", 0, true, fmt.Errorf("read client configuration: %w", err)
		}
	}
	if chainID == "" && cmd.Name() != "init" {
		file, err := os.Open(filepath.Join(ctx.HomeDir, "config", "genesis.json"))
		if err != nil {
			return "", 0, true, fmt.Errorf("read genesis signing domain: %w", err)
		}
		var genesis struct {
			ChainID string `json:"chain_id"`
		}
		err = json.NewDecoder(file).Decode(&genesis)
		_ = file.Close()
		if err != nil {
			return "", 0, true, fmt.Errorf("decode genesis signing domain: %w", err)
		}
		chainID = genesis.ChainID
	}
	var evmID uint64
	if flag := cmd.Flag(srvflags.EVMChainID); flag != nil && flag.Changed {
		value, err := strconv.ParseUint(flag.Value.String(), 10, 64)
		if err != nil {
			return "", 0, true, fmt.Errorf("numeric EVM chain ID: %w", err)
		}
		evmID = value
	} else {
		config := viper.New()
		config.SetConfigFile(filepath.Join(ctx.HomeDir, "config", "app.toml"))
		if err := config.ReadInConfig(); err == nil {
			evmID = config.GetUint64(srvflags.EVMChainID)
		} else if !os.IsNotExist(err) {
			return "", 0, true, fmt.Errorf("read application configuration: %w", err)
		}
	}
	if err := identity.ValidateChainIDs(chainID, evmID); err != nil {
		return "", 0, true, err
	}
	if cmd.Name() != "init" {
		if err := identity.ValidateHomeGenesis(ctx.HomeDir, chainID, evmID); err != nil {
			return "", 0, true, err
		}
	} else {
		if _, err := os.Stat(filepath.Join(ctx.HomeDir, "config", "genesis.json")); err == nil {
			if err := identity.ValidateHomeGenesis(ctx.HomeDir, chainID, evmID); err != nil {
				return "", 0, true, err
			}
		} else if !os.IsNotExist(err) {
			return "", 0, true, err
		}
		if flag := cmd.Flag("default-denom"); flag != nil && flag.Value.String() != identity.BaseDenom {
			return "", 0, true, fmt.Errorf("this network requires native denomination %q", identity.BaseDenom)
		}
	}
	return chainID, evmID, true, nil
}

func addDomainFlags(cmd *cobra.Command) {
	if cmd.Flag(srvflags.EVMChainID) == nil {
		cmd.Flags().Uint64(srvflags.EVMChainID, 0, "Numeric EVM signing domain matching the fresh network chain ID")
	}
	if needsDomain(cmd) && cmd.Flag(flags.FlagChainID) == nil {
		cmd.Flags().String(flags.FlagChainID, "", "Fresh network Cosmos signing domain")
	}
	for _, sub := range cmd.Commands() {
		addDomainFlags(sub)
	}
}
