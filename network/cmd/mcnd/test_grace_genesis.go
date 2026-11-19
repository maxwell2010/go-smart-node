package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"

	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/spf13/cobra"
)

// Prepare a new offline document; never rewrite a node home or running state.
func prepareTestGraceGenesis(raw []byte, evmID uint64, until int64) ([]byte, error) {
	if until <= 0 || evmID < 990001 || evmID > 990099 {
		return nil, errors.New("finite positive grace height and isolated EVM chain ID 990001..990099 required")
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	var chain string
	if err := json.Unmarshal(doc["chain_id"], &chain); err != nil {
		return nil, err
	}
	if err := identity.ValidateChainIDs(chain, evmID); err != nil {
		return nil, err
	}
	var initial string
	if err := json.Unmarshal(doc["initial_height"], &initial); err != nil {
		return nil, err
	}
	height, err := strconv.ParseInt(initial, 10, 64)
	if err != nil || height < 0 || height > until {
		return nil, errors.New("grace must cover the new genesis initial height")
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(doc["app_state"], &state); err != nil {
		return nil, err
	}
	var profile ownedapp.WithdrawalProfile
	if err := json.Unmarshal(state[ownedapp.WithdrawalGenesisKey], &profile); err != nil {
		return nil, err
	}
	if !profile.EnableNativeConsensus || !profile.EnableNativeOrdinaryDELDowntime || !profile.EnableNativeDuplicateVote {
		return nil, errors.New("native consensus and normal penalty profiles must remain enabled for resumption")
	}
	profile.TestGraceUntilHeight = until
	state[ownedapp.WithdrawalGenesisKey], err = json.Marshal(profile)
	if err != nil {
		return nil, err
	}
	doc["app_state"], err = json.Marshal(state)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(doc, "", "  ")
}

func newTestGraceGenesisCmd() *cobra.Command {
	var input, output string
	var evmID uint64
	var until int64
	cmd := &cobra.Command{Use: "test-grace-genesis", Short: "Write a separate isolated-test genesis with a finite penalty grace period", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		f, err := os.Open(input)
		if err != nil {
			return err
		}
		defer f.Close()
		const limit = 32 << 20
		raw, err := io.ReadAll(io.LimitReader(f, limit+1))
		if err != nil {
			return err
		}
		if len(raw) > limit {
			return errors.New("test genesis exceeds 32 MiB limit")
		}
		data, err := prepareTestGraceGenesis(raw, evmID, until)
		if err != nil {
			return err
		}
		if output == "" {
			return errors.New("a separate output file is required")
		}
		return outputFeeEnvelope(cmd, output, append(data, '\n'))
	}}
	cmd.Flags().StringVar(&input, "input-file", "", "Existing new-chain test genesis; read only")
	cmd.Flags().StringVar(&output, "output-file", "", "New file, must not already exist")
	cmd.Flags().Uint64Var(&evmID, "evm-chain-id", 0, "Isolated test domain 990001..990099")
	cmd.Flags().Int64Var(&until, "until-height", 0, "Last grace block, inclusive; ordinary rules resume afterwards")
	for _, name := range []string{"input-file", "output-file", "evm-chain-id", "until-height"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}
