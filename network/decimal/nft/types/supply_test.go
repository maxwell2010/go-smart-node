package types

import "testing"

func TestSupplyInvariantRejectsCorruptMaterializedState(t *testing.T) {
	valid := func() []Collection {
		return []Collection{{Denom: "collection", Supply: 1, Tokens: Tokens{&Token{ID: "token", Minted: 3, Burnt: 1, SubTokens: SubTokens{&SubToken{ID: 2}, &SubToken{ID: 3}}}}}}
	}
	if message, broken := SupplyInvariantCheck(valid()); broken {
		t.Fatal(message)
	}
	tests := []struct {
		name    string
		corrupt func([]Collection)
	}{
		{"collection count", func(s []Collection) { s[0].Supply++ }},
		{"nil token", func(s []Collection) { s[0].Tokens[0] = nil }},
		{"counter underflow", func(s []Collection) { s[0].Tokens[0].Burnt = 4 }},
		{"missing active token", func(s []Collection) { s[0].Tokens[0].SubTokens = s[0].Tokens[0].SubTokens[:1] }},
		{"duplicate sub-token", func(s []Collection) { s[0].Tokens[0].SubTokens[1].ID = 2 }},
		{"nil sub-token", func(s []Collection) { s[0].Tokens[0].SubTokens[0] = nil }},
		{"ID beyond minted range", func(s []Collection) { s[0].Tokens[0].SubTokens[1].ID = 4 }},
		{"zero ID", func(s []Collection) { s[0].Tokens[0].SubTokens[0].ID = 0 }},
		{"duplicate token", func(s []Collection) { s[0].Tokens = append(s[0].Tokens, s[0].Tokens[0]); s[0].Supply++ }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := valid()
			tt.corrupt(state)
			if message, broken := SupplyInvariantCheck(state); !broken {
				t.Fatalf("corruption was accepted: %s", message)
			}
		})
	}
}
