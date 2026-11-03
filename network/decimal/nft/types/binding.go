package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/config"
)

// MaxNextSubTokenID includes the terminal cursor after uint32 exhaustion.
const MaxNextSubTokenID uint64 = uint64(math.MaxUint32) + 1

func ValidateExternalDomain(chainID string, evmChainID uint64) error {
	if chainID == "" || len(chainID) > 128 || strings.TrimSpace(chainID) != chainID || strings.ContainsAny(chainID, "\x00\r\n") || evmChainID == 0 {
		return fmt.Errorf("invalid external NFT domain")
	}
	return nil
}

func (b *ExternalNFTBinding) Validate() error {
	if b == nil {
		return fmt.Errorf("external NFT binding is missing")
	}
	if err := ValidateExternalDomain(b.ChainID, b.EVMChainID); err != nil {
		return err
	}
	if len(b.Contract) != common.AddressLength || bytes.Equal(b.Contract, make([]byte, common.AddressLength)) || len(b.TokenID) != 32 {
		return fmt.Errorf("external NFT identity must retain a nonzero address and full uint256 ID")
	}
	if !config.CollectionDenomValidator.MatchString(b.NativeTokenID) || (b.NFTType != NftType_NFT721 && b.NFTType != NftType_NFT1155) {
		return fmt.Errorf("invalid external NFT native identity or standard")
	}
	if b.NextSubTokenID == 0 || b.NextSubTokenID > MaxNextSubTokenID {
		return fmt.Errorf("invalid external NFT sub-token cursor")
	}
	return nil
}

// FreshExternalTokenID is versioned and domain-separated; legacy IDs are stored
// explicitly in ExternalNFTBinding and never rewritten to this fresh format.
func FreshExternalTokenID(chainID string, evmChainID uint64, contract, tokenID []byte) (string, error) {
	b := ExternalNFTBinding{ChainID: chainID, EVMChainID: evmChainID, Contract: contract, TokenID: tokenID, NativeTokenID: "placeholder", NFTType: NftType_NFT1155, NextSubTokenID: 1}
	if err := b.Validate(); err != nil {
		return "", err
	}
	payload := append([]byte("mintcandy:nft-binding:v1\x00"), byte(len(chainID)))
	payload = append(payload, []byte(chainID)...)
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], evmChainID)
	payload = append(payload, encoded[:]...)
	payload = append(payload, contract...)
	payload = append(payload, tokenID...)
	hash := sha256.Sum256(payload)
	return "evm1_" + hex.EncodeToString(hash[:]), nil
}

// ExternalIdentityKey is an unambiguous full identity, without lossy integer casts.
func ExternalIdentityKey(b ExternalNFTBinding) string {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], b.EVMChainID)
	return string(append(append(append(append([]byte{byte(len(b.ChainID))}, []byte(b.ChainID)...), encoded[:]...), b.Contract...), b.TokenID...))
}

func ValidateBindingToken(b ExternalNFTBinding, token Token, collection Collection) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if token.Burnt > token.Minted {
		return fmt.Errorf("external NFT burnt count exceeds minted history")
	}
	if token.ID != b.NativeTokenID || token.Creator != collection.Creator || token.Denom != collection.Denom || collection.TypeNft != b.NFTType || !common.IsHexAddress(collection.AddressDRC) || !bytes.Equal(common.HexToAddress(collection.AddressDRC).Bytes(), b.Contract) {
		return fmt.Errorf("external NFT binding differs from its token or collection")
	}
	if b.NextSubTokenID != uint64(token.Minted)+1 {
		return fmt.Errorf("external NFT cursor differs from lifetime minted count")
	}
	if token.ReserveLess != collection.ReserveLess {
		return fmt.Errorf("external NFT reserve profile mismatch")
	}
	if b.NFTType == NftType_NFT721 && uint64(token.Minted)-uint64(token.Burnt) > 1 {
		return fmt.Errorf("ERC721 external token has more than one active unit")
	}
	return nil
}
