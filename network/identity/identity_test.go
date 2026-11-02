package identity

import "testing"

func TestSeparateSigningDomains(t *testing.T) {
	tests := []struct {
		chain string
		evm   uint64
		valid bool
	}{
		{"mintcandy_990001-1", 990001, true},
		{"mintcandy_990002-2", 990002, true},
		{"decimal_75-1", 75, false},
		{"decimal_202020-1", 202020, false},
		{"mintcandy_75-1", 75, false},
		{"mintcandy_202020-1", 202020, false},
		{"mintcandy_20202020-1", 20202020, false},
		{"mintcandy_990001-1", 990002, false},
		{"mintcandy_0990001-1", 990001, false},
		{"mintcandy_990001-0", 990001, false},
		{"mintcandy_0-1", 0, false},
		{"mintcandy_18446744073709551616-1", 1, false},
		{"mintcandy_990001-18446744073709551616", 990001, false},
		{"mintcandy_990001-1/../../decimal", 990001, false},
	}
	for _, tt := range tests {
		t.Run(tt.chain, func(t *testing.T) {
			if err := ValidateChainIDs(tt.chain, tt.evm); (err == nil) != tt.valid {
				t.Fatalf("valid=%v error=%v", tt.valid, err)
			}
		})
	}
}
