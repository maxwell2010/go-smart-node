package identity

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ValidateHomeGenesis binds a node's configured signing domains to its actual
// genesis file. Call before opening its application database, including restart
// paths on which InitChain will not execute.
func ValidateHomeGenesis(home, chainID string, evmID uint64) error {
	if home == "" {
		return fmt.Errorf("node home is required")
	}
	if err := ValidateProfileChainIDs(chainID, evmID); err != nil {
		return err
	}
	file, err := os.Open(filepath.Join(home, "config", "genesis.json"))
	if err != nil {
		return fmt.Errorf("read node genesis: %w", err)
	}
	defer file.Close()
	var genesis struct {
		ChainID  string                     `json:"chain_id"`
		AppState map[string]json.RawMessage `json:"app_state"`
	}
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&genesis); err != nil {
		return fmt.Errorf("decode node genesis: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("node genesis must contain exactly one JSON document")
	}
	if err := ValidateProfileChainIDs(genesis.ChainID, evmID); err != nil {
		return fmt.Errorf("genesis signing domain: %w", err)
	}
	if genesis.ChainID != chainID {
		return fmt.Errorf("configured chain ID %q differs from genesis chain ID %q", chainID, genesis.ChainID)
	}
	return ValidateNativeDenomGenesis(genesis.AppState)
}

// ValidateNativeDenomGenesis checks the native denomination declared by the
// modules before opening a database or applying a tagged network's genesis.
func ValidateNativeDenomGenesis(state map[string]json.RawMessage) error {
	if ProfileChainID == "" {
		// Legacy homes contain del. Do not open a tagged network's database
		// using the legacy runtime, even when InitChain will not run again.
		var coin struct {
			Params struct {
				Denom string `json:"base_denom"`
			} `json:"params"`
		}
		if json.Unmarshal(state["coin"], &coin) == nil &&
			(coin.Params.Denom == MainnetBaseDenom || coin.Params.Denom == TestnetBaseDenom) {
			return fmt.Errorf("genesis denomination %q requires its dedicated MintCandy binary", coin.Params.Denom)
		}
		return nil
	}
	for _, module := range []struct{ name, field string }{
		{"coin", "base_denom"},
		{"validator", "base_denom"},
		{"evm", "evm_denom"},
		{"staking", "bond_denom"},
		{"mint", "mint_denom"},
	} {
		var record struct {
			Params map[string]json.RawMessage `json:"params"`
		}
		var denom string
		if json.Unmarshal(state[module.name], &record) != nil ||
			json.Unmarshal(record.Params[module.field], &denom) != nil || denom != BaseDenom {
			return fmt.Errorf("genesis %s.%s must be %q", module.name, module.field, BaseDenom)
		}
	}
	var evm struct {
		Params struct {
			Extended struct {
				Denom string `json:"extended_denom"`
			} `json:"extended_denom_options"`
		} `json:"params"`
	}
	if json.Unmarshal(state["evm"], &evm) != nil || evm.Params.Extended.Denom != BaseDenom {
		return fmt.Errorf("genesis EVM extended denomination must be %q", BaseDenom)
	}
	var bank struct {
		Metadata []struct {
			Base, Display, Name, Symbol string
			Units                       []struct {
				Denom    string
				Exponent uint32
			} `json:"denom_units"`
		} `json:"denom_metadata"`
	}
	if err := json.Unmarshal(state["bank"], &bank); err != nil {
		return fmt.Errorf("decode native bank metadata: %w", err)
	}
	for _, meta := range bank.Metadata {
		if meta.Base == BaseDenom && meta.Display == NativeTokenSymbol && meta.Name == NativeTokenName &&
			meta.Symbol == NativeTokenSymbol && len(meta.Units) == 2 &&
			meta.Units[0].Denom == BaseDenom && meta.Units[0].Exponent == 0 &&
			meta.Units[1].Denom == NativeTokenSymbol && meta.Units[1].Exponent == 18 {
			return nil
		}
	}
	return fmt.Errorf("genesis bank metadata must identify %s (%s) with base denomination %q",
		NativeTokenName, NativeTokenSymbol, BaseDenom)
}
