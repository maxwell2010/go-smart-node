package app

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestValidatorControlMetadataBoundaries(t *testing.T) {
	value, err := CanonicalValidatorControlMetadata([]byte(`{"commission":"0.05","avatar_uri":"ipfs://bafyfixture/image.png","name":"Operator A","description":"Public description"}`))
	require.NoError(t, err)
	require.Contains(t, string(value), `"commission":"0.050000000000000000"`)
	for _, input := range []string{`{}`, `{"operator":"0x1234"}`, `{"commission":"-0.1"}`, `{"commission":"1.01"}`, `{"name":""}`, `{"avatar_uri":"javascript:alert(1)"}`, `{"avatar_uri":"http://example.invalid/avatar.png"}`, `{"avatar_uri":"https://user:password@example.invalid/x"}`, `{"name":"A\u0000B"}`, `{"name":"A"} {"name":"B"}`} {
		_, err := CanonicalValidatorControlMetadata([]byte(input))
		require.Error(t, err, input)
	}
}
