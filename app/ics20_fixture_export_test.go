//go:build experimental_ibc

package app

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	ibctesting "github.com/cosmos/ibc-go/v6/testing"
	ibcmock "github.com/cosmos/ibc-go/v6/testing/mock"
	"github.com/stretchr/testify/require"
	tmconfig "github.com/tendermint/tendermint/config"
	tmed25519 "github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/privval"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicit opt-in exports NEW disposable keys, never imports production material.
// Output contains private test keys and MUST NOT be committed or published.
func TestExportIsolatedICS20Fixtures(t *testing.T) {
	dest := os.Getenv("DSC_IBC_FIXTURE_DIR")
	if dest == "" {
		t.Skip("set DSC_IBC_FIXTURE_DIR to a NEW absolute directory to export disposable fixtures")
	}
	if dest == "@test-temp" {
		dest = filepath.Join(t.TempDir(), "export")
	}
	exportIsolatedICS20Fixtures(t, dest)
}

func exportIsolatedICS20Fixtures(t *testing.T, dest string) {
	require.True(t, filepath.IsAbs(dest), "fixture directory must be absolute")
	require.NoError(t, os.Mkdir(dest, 0700), "refusing to overwrite any existing output")
	c := &ibctesting.Coordinator{T: t, CurrentTime: time.Now().UTC().Add(-time.Minute), Chains: map[string]*ibctesting.TestChain{}}
	for i, id := range []string{"decimal_900001-1", "decimal_900002-1"} {
		chain := newDSCChain(t, c, id)
		app := chain.App.(dscIBCApp)
		home := filepath.Join(dest, id)
		require.NoError(t, os.MkdirAll(filepath.Join(home, "config"), 0700))
		require.NoError(t, os.MkdirAll(filepath.Join(home, "data"), 0700))
		genesis, err := os.ReadFile(filepath.Join(app.home, "config", "genesis.json"))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(home, "config", "genesis.json"), genesis, 0600))
		cfg := tmconfig.DefaultConfig().SetRoot(home)
		cfg.Moniker = id
		cfg.RPC.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", 26757+i*100)
		cfg.P2P.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", 26756+i*100)
		cfg.P2P.PexReactor = false
		cfg.P2P.Seeds = ""
		cfg.P2P.PersistentPeers = ""
		tmconfig.WriteConfigFile(filepath.Join(home, "config", "config.toml"), cfg)
		for _, signer := range chain.Signers {
			key := signer.(ibcmock.PV).PrivKey.Bytes()
			pv := privval.NewFilePV(tmed25519.PrivKey(key), filepath.Join(home, "config", "priv_validator_key.json"), filepath.Join(home, "data", "priv_validator_state.json"))
			pv.Save()
		}
		metadata := map[string]any{"warning": "DISPOSABLE ISOLATED TEST KEY - NEVER FUND ON PUBLIC NETWORKS OR PUBLISH", "chain_id": id, "address": chain.SenderAccount.GetAddress().String(), "private_key_hex": hex.EncodeToString(chain.SenderPrivKey.Bytes())}
		keyJSON, err := json.MarshalIndent(metadata, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(home, "relayer-test-key.json"), keyJSON, 0600))
	}
	// No processes, external RPC, relayers or transactions are launched by export.
	t.Log("Created fresh isolated fixtures; private keys were not printed. Initialize DSC app config and review ports before starting.")
}
