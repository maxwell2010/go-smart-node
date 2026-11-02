package types

import (
	"fmt"
	"strings"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// ValidContractAddress accepts an assigned, nonzero Ethereum contract address.
// An empty optional assignment must be handled separately by the caller.
func ValidContractAddress(value string) bool {
	return len(value) == 42 && strings.HasPrefix(value, "0x") && common.IsHexAddress(value) && common.HexToAddress(value) != (common.Address{})
}

// Validate checks a complete stored custom coin, rather than creation admission.
// Burns and reserve reductions may leave balances below creation minima or zero.
func (c *Coin) Validate() error {
	if c == nil {
		return fmt.Errorf("coin is missing")
	}
	if !config.CoinDenomValidator.MatchString(c.Denom) {
		return fmt.Errorf("invalid coin denomination")
	}
	if strings.TrimSpace(c.Title) == "" || len(c.Title) > config.MaxCoinTitleLength {
		return fmt.Errorf("invalid coin title")
	}
	if _, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).StringToBytes(c.Creator); err != nil {
		return fmt.Errorf("invalid coin creator: %w", err)
	}
	if c.CRR < 10 || c.CRR > 100 {
		return fmt.Errorf("coin CRR must be between 10 and 100")
	}
	for _, field := range []struct {
		name  string
		value sdkmath.Int
	}{{"volume", c.Volume}, {"reserve", c.Reserve}, {"minimum volume", c.MinVolume}, {"volume limit", c.LimitVolume}} {
		if field.value.IsNil() || field.value.IsNegative() || field.value.GT(config.MaxCoinSupply) {
			return fmt.Errorf("coin %s must be initialized, nonnegative and within maximum supply", field.name)
		}
	}
	if c.MinVolume.GT(c.Volume) || c.Volume.GT(c.LimitVolume) {
		return fmt.Errorf("coin minimum volume <= volume <= volume limit is required")
	}
	if c.DRC20Contract != "" && !ValidContractAddress(c.DRC20Contract) {
		return fmt.Errorf("invalid coin contract address")
	}
	return nil
}

// Validate checks a redeemed-check record before hashing its financial fields.
func (c *Check) Validate() error {
	if c == nil {
		return fmt.Errorf("check is missing")
	}
	if c.ChainID == "" || strings.TrimSpace(c.ChainID) != c.ChainID || len(c.ChainID) > 50 {
		return fmt.Errorf("invalid check chain ID")
	}
	if !config.CoinDenomValidator.MatchString(c.Coin.Denom) || c.Coin.Amount.IsNil() || !c.Coin.Amount.IsPositive() || c.Coin.Amount.GT(config.MaxCoinSupply) {
		return fmt.Errorf("invalid check coin or amount")
	}
	// An empty byte string represents nonce zero in the check's RLP encoding.
	if len(c.Nonce) > 16 || c.DueBlock == 0 {
		return fmt.Errorf("invalid check nonce or expiry")
	}
	if c.V.IsNil() || c.R.IsNil() || c.S.IsNil() || (!c.V.Equal(sdkmath.NewInt(27)) && !c.V.Equal(sdkmath.NewInt(28))) {
		return fmt.Errorf("invalid check signature")
	}
	if !crypto.ValidateSignatureValues(byte(c.V.Uint64()-27), c.R.BigInt(), c.S.BigInt(), true) {
		return fmt.Errorf("invalid check signature values")
	}
	if len(c.Lock) != crypto.SignatureLength {
		return fmt.Errorf("invalid check lock signature length")
	}
	if _, err := c.Sender(); err != nil {
		return fmt.Errorf("invalid check signer: %w", err)
	}
	if _, err := c.LockPubKey(); err != nil {
		return fmt.Errorf("invalid check lock: %w", err)
	}
	return nil
}
