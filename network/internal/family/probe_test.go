package family

import "testing"

func TestPatchedFamilyConstructsOwnedApplication(t *testing.T) {
	if err := CheckWiring(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
