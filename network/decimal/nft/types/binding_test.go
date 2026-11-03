package types

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFreshExternalNFTIdentitySeparatesDomainsAndAllUint256Bits(t *testing.T) {
	contract := bytes.Repeat([]byte{1}, 20)
	id := make([]byte, 32)
	first, err := FreshExternalTokenID("network-1", 777, contract, id)
	require.NoError(t, err)
	seen := map[string]bool{first: true}
	for _, input := range []struct {
		chain        string
		evm          uint64
		contract, id []byte
	}{
		{"network-2", 777, contract, id},
		{"network-1", 778, contract, id},
		{"network-1", 777, bytes.Repeat([]byte{2}, 20), id},
		{"network-1", 777, contract, append([]byte{1}, make([]byte, 31)...)},
		{"network-1", 777, contract, append(make([]byte, 31), 1)},
	} {
		nativeID, err := FreshExternalTokenID(input.chain, input.evm, input.contract, input.id)
		require.NoError(t, err)
		require.False(t, seen[nativeID], "domain, contract and every uint256 bit are part of identity")
		seen[nativeID] = true
	}
	_, err = FreshExternalTokenID("network-1", 777, make([]byte, 20), id)
	require.Error(t, err)
	_, err = FreshExternalTokenID("network-1", 0, contract, id)
	require.Error(t, err)
	_, err = FreshExternalTokenID("network-1", 777, contract, id[1:])
	require.Error(t, err)
}
