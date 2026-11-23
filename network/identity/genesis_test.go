package identity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestHomeGenesisSigningDomain(t *testing.T) {
	if ProfileChainID != "" {
		t.Skip("legacy genesis signing domains")
	}
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

func TestCompiledNativeProfile(t *testing.T) {
	for _, tc := range []struct {
		chainID, denom, symbol, wrapper string
		evmID                           uint64
	}{
		{MainnetChainID, "msp", "MSP", "WMSP", MainnetEVMChainID},
		{TestnetChainID, "tmsp", "tMSP", "WtMSP", TestnetEVMChainID},
	} {
		t.Run(tc.chainID, func(t *testing.T) {
			domainErr := ValidateProfileChainIDs(tc.chainID, tc.evmID)
			if ProfileChainID == "" {
				if domainErr != nil || BaseDenom != "del" {
					t.Fatalf("legacy profile changed: %s %v", BaseDenom, domainErr)
				}
				state := map[string]json.RawMessage{
					"coin": json.RawMessage(fmt.Sprintf(`{"params":{"base_denom":%q}}`, tc.denom)),
				}
				if err := ValidateNativeDenomGenesis(state); err == nil {
					t.Fatal("legacy runtime accepted dedicated native denomination")
				}
				return
			}
			if ProfileChainID != tc.chainID {
				if domainErr == nil {
					t.Fatal("foreign signing domain accepted")
				}
				return
			}
			if domainErr != nil || BaseDenom != tc.denom || NativeTokenSymbol != tc.symbol || WrappedMSPSymbol != tc.wrapper {
				t.Fatalf("invalid compiled profile: %s %s %s %v", BaseDenom, NativeTokenSymbol, WrappedMSPSymbol, domainErr)
			}
			home := t.TempDir()
			if err := os.Mkdir(filepath.Join(home, "config"), 0700); err != nil {
				t.Fatal(err)
			}
			var state map[string]json.RawMessage
			template := fmt.Sprintf(`{
				"coin":{"params":{"base_denom":%[1]q}},
				"validator":{"params":{"base_denom":%[1]q}},
				"evm":{"params":{"evm_denom":%[1]q,"extended_denom_options":{"extended_denom":%[1]q}}},
				"staking":{"params":{"bond_denom":%[1]q}},
				"mint":{"params":{"mint_denom":%[1]q}},
				"bank":{"denom_metadata":[{"base":%[1]q,"display":%[2]q,"symbol":%[2]q,"name":%[3]q,
					"denom_units":[{"denom":%[1]q,"exponent":0},{"denom":%[2]q,"exponent":18}]}]}
			}`, tc.denom, tc.symbol, NativeTokenName)
			if err := json.Unmarshal([]byte(template), &state); err != nil {
				t.Fatal(err)
			}
			writeGenesis := func() {
				t.Helper()
				raw, err := json.Marshal(map[string]any{"chain_id": tc.chainID, "app_state": state})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, "config", "genesis.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeGenesis()
			if err := ValidateHomeGenesis(home, tc.chainID, tc.evmID); err != nil {
				t.Fatal(err)
			}
			state["staking"] = json.RawMessage(`{"params":{"bond_denom":"del"}}`)
			writeGenesis()
			if err := ValidateHomeGenesis(home, tc.chainID, tc.evmID); err == nil {
				t.Fatal("legacy staking denomination accepted in tagged genesis")
			}
		})
	}
}
