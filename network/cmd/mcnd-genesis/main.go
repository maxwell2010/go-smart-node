// mcnd-genesis assembles and verifies unsigned launch candidates offline.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/maxwell2010/go-smart-node/network/launchgenesis"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{Use: "mcnd-genesis", Short: "Work with unsigned MintCandy launch candidates offline", SilenceUsage: true}
	root.AddCommand(addressesCommand(), candidateCommand(false), candidateCommand(true), launchConsentCommand())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func addressesCommand() *cobra.Command {
	var chainID string
	var evmID uint64
	cmd := &cobra.Command{Use: "addresses", Short: "Discover the Gateway and wrapper addresses for a signing domain",
		Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := launchgenesis.DiscoverAddresses(chainID, evmID)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}}
	cmd.Flags().StringVar(&chainID, "chain-id", "", "MintCandy Cosmos chain ID")
	cmd.Flags().Uint64Var(&evmID, "evm-chain-id", 0, "MintCandy EVM chain ID")
	return cmd
}

func candidateCommand(verify bool) *cobra.Command {
	var ledgerPath, paramsPath, genesisPath string
	name, short := "build", "Write an unsigned genesis candidate"
	genesisHelp := "New genesis output file"
	if verify {
		name, short = "verify", "Run InitChain and commit block one in an independent memory database"
		genesisHelp = "Genesis candidate to verify"
	}
	cmd := &cobra.Command{Use: name, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if ledgerPath == "" || paramsPath == "" || genesisPath == "" {
			return fmt.Errorf("--ledger, --parameters and --genesis are required")
		}
		ledger, err := readInput(ledgerPath, launchgenesis.ReadLedger)
		if err != nil {
			return fmt.Errorf("read founding ledger: %w", err)
		}
		params, err := readInput(paramsPath, launchgenesis.ReadLaunchParameters)
		if err != nil {
			return fmt.Errorf("read launch parameters: %w", err)
		}
		if verify {
			body, err := readInput(genesisPath, func(r io.Reader) ([]byte, error) {
				return io.ReadAll(io.LimitReader(r, launchgenesis.MaxCandidateBytes+1))
			})
			if err != nil {
				return err
			}
			report, err := launchgenesis.VerifyCold(body, ledger, params)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
		}
		body, err := launchgenesis.Build(ledger, params)
		if err != nil {
			return err
		}
		return writeNewFile(genesisPath, append(body, '\n'))
	}}
	cmd.Flags().StringVar(&ledgerPath, "ledger", "", "Prepared founding ledger JSON")
	cmd.Flags().StringVar(&paramsPath, "parameters", "", "Explicit launch parameters JSON")
	cmd.Flags().StringVar(&genesisPath, "genesis", "", genesisHelp)
	return cmd
}

func readInput[T any](path string, read func(io.Reader) (T, error)) (result T, err error) {
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, fmt.Errorf("expected a regular input file")
	}
	return read(file)
}

func writeNewFile(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcnd-genesis-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(body)
	if err := errors.Join(writeErr, tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	// Link publishes the fully written file without replacing existing paths.
	return os.Link(tmp.Name(), path)
}
