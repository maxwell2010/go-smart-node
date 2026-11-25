package app

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"math/big"
	"testing"
)

func TestAssetPermitSignature(t *testing.T) {
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	owner := crypto.PubkeyToAddress(key.PublicKey)
	domain := crypto.Keccak256Hash([]byte("fixture domain"))
	fields := []common.Hash{crypto.Keccak256Hash([]byte(erc20PermitType)), customAddressWord(owner), common.BytesToHash([]byte{55}), common.BigToHash(big.NewInt(7)), {}, common.BigToHash(big.NewInt(1000))}
	var words []byte
	for _, f := range fields {
		words = append(words, f[:]...)
	}
	sig, err := crypto.Sign(crypto.Keccak256([]byte{0x19, 1}, domain[:], crypto.Keccak256(words)), key)
	require.NoError(t, err)
	v, r, s := sig[64]+27, [32]byte(sig[:32]), [32]byte(sig[32:64])
	require.NoError(t, verifyAssetPermitSignature(owner, domain, fields, v, r, s))
	require.Error(t, verifyAssetPermitSignature(common.Address{}, domain, fields, v, r, s))
	require.Error(t, verifyAssetPermitSignature(owner, common.Hash{}, fields, v, r, s))
	require.Error(t, verifyAssetPermitSignature(owner, domain, fields, sig[64], r, s))
	malleable := new(big.Int).Sub(crypto.S256().Params().N, new(big.Int).SetBytes(s[:]))
	require.Error(t, verifyAssetPermitSignature(owner, domain, fields, (sig[64]^1)+27, r, common.BigToHash(malleable)))
	for idx := range fields {
		tampered := append([]common.Hash(nil), fields...)
		tampered[idx][31] ^= 1
		require.Error(t, verifyAssetPermitSignature(owner, domain, tampered, v, r, s))
	}
}
