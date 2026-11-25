package systemcontracts_test

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

func assetPermitFixture(t *testing.T, artifact, name string, target, spender common.Address, owner *ecdsa.PrivateKey, id, nonce, deadline *big.Int) []byte {
	t.Helper()
	token, err := contracts.CompiledABI(artifact)
	require.NoError(t, err)
	encode := func(types []string, args ...interface{}) []byte {
		t.Helper()
		fields := make(abi.Arguments, len(types))
		for i, typ := range types {
			fields[i].Type, err = abi.NewType(typ, "", nil)
			require.NoError(t, err)
		}
		out, err := fields.Pack(args...)
		require.NoError(t, err)
		return out
	}
	domain := crypto.Keccak256(encode([]string{"bytes32", "bytes32", "bytes32", "uint256", "address"},
		crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")),
		crypto.Keccak256Hash([]byte(name)), crypto.Keccak256Hash([]byte("1")), big.NewInt(identity.TestnetEVMChainID), target))
	from := crypto.PubkeyToAddress(owner.PublicKey)
	var message []byte
	var call []interface{}
	switch artifact {
	case "DRC20", "DRC20Reserveless":
		amount := big.NewInt(7)
		message = encode([]string{"bytes32", "address", "address", "uint256", "uint256", "uint256"},
			crypto.Keccak256Hash([]byte("Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)")), from, spender, amount, nonce, deadline)
		call = []interface{}{from, spender, amount, deadline}
	case "NewNetworkDRC721", "DRC721Reserveless":
		message = encode([]string{"bytes32", "address", "uint256", "uint256", "uint256"},
			crypto.Keccak256Hash([]byte("Permit(address spender,uint256 tokenId,uint256 nonce,uint256 deadline)")), spender, id, nonce, deadline)
		call = []interface{}{spender, id, deadline}
	case "MintCandyIsolatedDRC1155", "DRC1155Reserveless":
		message = encode([]string{"bytes32", "address", "uint256", "uint256"},
			crypto.Keccak256Hash([]byte("Permit(address spender,uint256 nonce,uint256 deadline)")), spender, nonce, deadline)
		call = []interface{}{from, spender, deadline}
	default:
		t.Fatalf("unexpected permit artifact %q", artifact)
	}
	digest := crypto.Keccak256([]byte{0x19, 0x01}, domain, crypto.Keccak256(message))
	sig, err := crypto.Sign(digest, owner)
	require.NoError(t, err)
	call = append(call, sig[64]+27, [32]byte(sig[:32]), [32]byte(sig[32:64]))
	data, err := token.Pack("permit", call...)
	require.NoError(t, err)
	return data
}
