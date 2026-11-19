package types

import (
	"strings"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func nativeAvatarSchemaFixture(t *testing.T) *GenesisState {
	t.Helper()
	g := DefaultGenesisState()
	g.Validators = []Validator{fixture(t, 1, 0), fixture(t, 2, 0)}
	g.ValidatorAvatars = []ValidatorAvatar{
		{OperatorAddress: g.Validators[0].OperatorAddress, AvatarURI: "ipfs://bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku/avatar.png"},
		{OperatorAddress: g.Validators[1].OperatorAddress, AvatarURI: "https://cdn.example.org/second.png"},
	}
	require.NoError(t, g.Validate())
	return g
}

func TestNativeValidatorAvatarSchemaRoundTripAndLegacyWire(t *testing.T) {
	g := nativeAvatarSchemaFixture(t)
	raw, err := proto.Marshal(g)
	require.NoError(t, err)
	var decoded GenesisState
	require.NoError(t, proto.Unmarshal(raw, &decoded))
	require.NoError(t, decoded.Validate())
	require.Equal(t, g.ValidatorAvatars, decoded.ValidatorAvatars)
	cdc := NewCodec()
	encoded, err := cdc.MarshalJSON(&decoded)
	require.NoError(t, err)
	var jsonDecoded GenesisState
	require.NoError(t, cdc.UnmarshalJSON(encoded, &jsonDecoded))
	require.NoError(t, jsonDecoded.Validate())
	require.Equal(t, g.ValidatorAvatars, jsonDecoded.ValidatorAvatars)
	// A protobuf produced before field 15 was introduced has no avatar rows.
	// The new schema must not invent an empty row or require this optional field.
	g.ValidatorAvatars = nil
	legacyWire, err := proto.Marshal(g)
	require.NoError(t, err)
	var legacy GenesisState
	require.NoError(t, proto.Unmarshal(legacyWire, &legacy))
	require.Empty(t, legacy.ValidatorAvatars)
	require.NoError(t, legacy.Validate())
}

func TestNativeValidatorAvatarGenesisValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*GenesisState)
	}{
		{"duplicate operator", func(g *GenesisState) { g.ValidatorAvatars = append(g.ValidatorAvatars, g.ValidatorAvatars[0]) }},
		{"unknown operator", func(g *GenesisState) { g.ValidatorAvatars[0].OperatorAddress = fixture(t, 9, 0).OperatorAddress }},
		{"account instead of operator", func(g *GenesisState) {
			g.ValidatorAvatars[0].OperatorAddress = testAddress(t, identity.Bech32PrefixAccAddr, 1)
		}},
		{"malformed operator", func(g *GenesisState) { g.ValidatorAvatars[0].OperatorAddress = "not-an-operator" }},
		{"empty avatar record", func(g *GenesisState) { g.ValidatorAvatars[0].AvatarURI = "" }},
		{"invalid UTF8", func(g *GenesisState) { g.ValidatorAvatars[0].AvatarURI = "https://example.org/" + string([]byte{0xff}) }},
		{"unsafe URI", func(g *GenesisState) { g.ValidatorAvatars[0].AvatarURI = "file:///etc/passwd" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := nativeAvatarSchemaFixture(t)
			tc.mutate(g)
			require.Error(t, g.Validate())
		})
	}
}

func TestNativeValidatorAvatarURISharedPolicy(t *testing.T) {
	for _, value := range []string{"", "https://cdn.example.org/avatar.png", "ipfs://bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku/avatar.png",
		"https://example.org/" + strings.Repeat("x", 512-len("https://example.org/"))} {
		require.NoError(t, ValidateValidatorAvatarURI(value), "valid stored pointer or explicit clear: %q", value)
	}
	for _, value := range []string{
		"http://example.org/a.png", "ftp://example.org/a.png", "file:///tmp/avatar.png", "data:image/png;base64,AAAA", "javascript:alert(1)",
		"https:///missing-host.png", "ipfs:///missing-host", "https://user:password@example.org/avatar.png", "https://example.org/avatar.png#fragment",
		"https://example.org/%zz", "https://example.org/\x00avatar", "https://example.org/\n", "https://example.org/\x7favatar",
		"https://example.org/" + string([]byte{0xff}), "https://example.org/" + strings.Repeat("x", 513-len("https://example.org/")),
	} {
		require.Error(t, ValidateValidatorAvatarURI(value), "invalid stored pointer: %q", value)
	}
}
