package integrationconfig

import "testing"

func TestExactIsolatedChainAllowlist(t *testing.T) {
	for _, id := range []string{"decimal_75-1", "decimal_202020-1", "", "decimal_900001-2", "decimal_900001-1-extra"} {
		if AllowedChainID(id) {
			t.Fatalf("unexpected isolated chain: %q", id)
		}
	}
	if !AllowedChainID("decimal_900001-1") || !AllowedChainID("decimal_900002-1") {
		t.Fatal("isolated chain not allowed")
	}
}
