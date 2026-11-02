package types

import (
	"fmt"
	"strings"

	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/ethereum/go-ethereum/common"
	coinconfig "github.com/maxwell2010/go-smart-node/network/decimal/coin/config"

	"github.com/maxwell2010/go-smart-node/network/decimal/nft/config"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/errors"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func (c *Collection) Validate() error {
	if c == nil {
		return fmt.Errorf("NFT collection is missing")
	}
	if !config.CollectionDenomValidator.MatchString(c.Denom) {
		return errors.InvalidDenom
	}

	_, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).StringToBytes(c.Creator)
	if err != nil {
		return errors.InvalidSender
	}
	if c.TypeNft != NftType_Unspecified && c.TypeNft != NftType_NFT721 && c.TypeNft != NftType_NFT1155 {
		return fmt.Errorf("unknown NFT collection type")
	}
	if c.AddressDRC != "" && (c.TypeNft == NftType_Unspecified || len(c.AddressDRC) != 42 || !strings.HasPrefix(c.AddressDRC, "0x") || !common.IsHexAddress(c.AddressDRC) || common.HexToAddress(c.AddressDRC) == (common.Address{})) {
		return fmt.Errorf("invalid NFT contract assignment")
	}

	return nil
}

func (t *Token) Validate() error {
	if t == nil {
		return fmt.Errorf("NFT token is missing")
	}
	_, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).StringToBytes(t.Creator)
	if err != nil {
		return errors.InvalidSender
	}
	if strings.TrimSpace(t.Denom) == "" {
		return errors.InvalidDenom
	}
	if strings.TrimSpace(t.ID) == "" {
		return errors.InvalidNFT
	}
	if strings.TrimSpace(t.URI) == "" {
		return errors.InvalidNFT
	}
	if t.Reserve.Amount.IsNil() || t.Reserve.Validate() != nil || !t.Reserve.IsPositive() || t.Reserve.Amount.GT(coinconfig.MaxCoinSupply) {
		return errors.InvalidReserve
	}
	if !config.CollectionDenomValidator.MatchString(t.Denom) {
		return errors.InvalidDenom
	}
	if !config.CollectionDenomValidator.MatchString(t.ID) {
		return errors.InvalidTokenID
	}
	return nil
}

func (st *SubToken) Validate() error {
	if st == nil {
		return fmt.Errorf("NFT sub-token is missing")
	}
	if _, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).StringToBytes(st.Owner); err != nil {
		return errors.InvalidSender
	}
	// Nil inherits the token's default reserve. A slashed stored sub-token may
	// have an explicit zero reserve; mint admission still requires positive value.
	if st.Reserve != nil && (st.Reserve.Amount.IsNil() || st.Reserve.Validate() != nil || st.Reserve.Amount.GT(coinconfig.MaxCoinSupply)) {
		return errors.InvalidReserve
	}

	return nil
}
