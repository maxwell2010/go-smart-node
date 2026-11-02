package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeGenesisSigningDomain(t *testing.T) {
	for _, test := range []struct {
		name, content, configured string
		evmID                     uint64
		wantError                 bool
	}{
		{"matching", `{"chain_id":"mintcandy_990001-1","app_state":{}}`, "mintcandy_990001-1", 990001, false},
		{"different_revision", `{"chain_id":"mintcandy_990001-2"}`, "mintcandy_990001-1", 990001, true},
		{"different_EVM_ID", `{"chain_id":"mintcandy_990002-1"}`, "mintcandy_990001-1", 990001, true},
		{"legacy_genesis", `{"chain_id":"decimal_75-1"}`, "mintcandy_990001-1", 990001, true},
		{"missing_ID", `{"app_state":{}}`, "mintcandy_990001-1", 990001, true},
		{"truncated", `{"chain_id":"mintcandy_990001-1"`, "mintcandy_990001-1", 990001, true},
		{"trailing_document", `{"chain_id":"mintcandy_990001-1"} {}`, "mintcandy_990001-1", 990001, true},
		{"wrong_type", `{"chain_id":990001}`, "mintcandy_990001-1", 990001, true},
		{"invalid_configuration", `{"chain_id":"mintcandy_990001-1"}`, "mintcandy_990001-1", 75, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.Mkdir(filepath.Join(home, "config"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "config", "genesis.json"), []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			err := ValidateHomeGenesis(home, test.configured, test.evmID)
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v, wantError=%v", err, test.wantError)
			}
		})
	}
	if err := ValidateHomeGenesis(t.TempDir(), "mintcandy_990001-1", 990001); err == nil {
		t.Fatal("missing genesis accepted")
	}
}
