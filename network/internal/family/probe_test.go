package family

import "testing"

func TestPatchedFamilyConstructsUpstreamApplication(t *testing.T) {
	if err := CheckWiring(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
