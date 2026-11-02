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
	if err := ValidateChainIDs(chainID, evmID); err != nil {
		return err
	}
	file, err := os.Open(filepath.Join(home, "config", "genesis.json"))
	if err != nil {
		return fmt.Errorf("read node genesis: %w", err)
	}
	defer file.Close()
	var genesis struct {
		ChainID string `json:"chain_id"`
	}
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&genesis); err != nil {
		return fmt.Errorf("decode node genesis: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("node genesis must contain exactly one JSON document")
	}
	if err := ValidateChainIDs(genesis.ChainID, evmID); err != nil {
		return fmt.Errorf("genesis signing domain: %w", err)
	}
	if genesis.ChainID != chainID {
		return fmt.Errorf("configured chain ID %q differs from genesis chain ID %q", chainID, genesis.ChainID)
	}
	return nil
}
