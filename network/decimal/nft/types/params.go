package types

import (
	"fmt"

	sdkmath "cosmossdk.io/math"

	coinconfig "github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	helpers "github.com/maxwell2010/go-smart-node/network/decimal/compat"
)

// NFT params default values.
var (
	DefaultMaxCollectionSize uint32      = 10000
	DefaultMaxTokenQuantity  uint32      = 10000
	DefaultMinReserveAmount  sdkmath.Int = helpers.EtherToWei(sdkmath.NewInt(1))
)

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return Params{
		MaxCollectionSize: DefaultMaxCollectionSize,
		MaxTokenQuantity:  DefaultMaxTokenQuantity,
		MinReserveAmount:  DefaultMinReserveAmount,
	}
}

// NewParams creates a new Params instance.
func NewParams(maxCollectionSize uint32, maxTokenQuantity uint32, minReserveAmount sdkmath.Int) Params {
	return Params{
		MaxCollectionSize: maxCollectionSize,
		MaxTokenQuantity:  maxTokenQuantity,
		MinReserveAmount:  minReserveAmount,
	}
}

// Validate validates the set of params.
func (p *Params) Validate() (err error) {
	if p == nil {
		return fmt.Errorf("NFT params are missing")
	}
	if err = validateMaxCollectionSize(p.MaxCollectionSize); err != nil {
		return
	}
	if err = validateMaxTokenQuantity(p.MaxTokenQuantity); err != nil {
		return
	}
	if err = validateMinReserveAmount(p.MinReserveAmount); err != nil {
		return
	}
	return
}

func validateMaxCollectionSize(i interface{}) error {
	v, ok := i.(uint32)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}
	if v == 0 {
		return fmt.Errorf("max collection size must be positive: %d", v)
	}
	return nil
}

func validateMaxTokenQuantity(i interface{}) error {
	v, ok := i.(uint32)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}
	if v == 0 {
		return fmt.Errorf("max token quantity must be positive: %d", v)
	}
	return nil
}

func validateMinReserveAmount(i interface{}) error {
	amount, ok := i.(sdkmath.Int)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}
	if amount.IsNil() || !amount.IsPositive() || amount.GT(coinconfig.MaxCoinSupply) {
		return fmt.Errorf("NFT minimum reserve must be initialized and positive")
	}
	return nil
}
