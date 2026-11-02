package types

import (
	"fmt"
	"strings"

	sdkmath "cosmossdk.io/math"

	cmdconfig "github.com/maxwell2010/go-smart-node/network/identity"

	"github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/errors"
)

// Coin params default values.
var (
	DefaultBaseDenom  string      = cmdconfig.BaseDenom
	DefaultBaseTitle  string      = "MintCandy native coin"
	DefaultBaseVolume sdkmath.Int = sdkmath.ZeroInt()
)

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return Params{
		BaseDenom:  DefaultBaseDenom,
		BaseTitle:  DefaultBaseTitle,
		BaseVolume: DefaultBaseVolume,
	}
}

// NewParams creates a new Params instance.
func NewParams(baseDenom string, baseTitle string, baseVolume sdkmath.Int) Params {
	return Params{
		BaseDenom:  baseDenom,
		BaseTitle:  baseTitle,
		BaseVolume: baseVolume,
	}
}

// Validate validates the set of params.
func (p *Params) Validate() (err error) {
	if p == nil {
		return fmt.Errorf("coin params are missing")
	}
	if err = validateBaseDenom(p.BaseDenom); err != nil {
		return
	}
	if err = validateBaseTitle(p.BaseTitle); err != nil {
		return
	}
	if err = validateBaseVolume(p.BaseVolume); err != nil {
		return
	}
	return
}

func validateBaseDenom(i interface{}) error {
	denom, ok := i.(string)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}
	if !config.CoinDenomValidator.MatchString(denom) {
		return errors.InvalidCoinDenom
	}
	return nil
}

func validateBaseTitle(i interface{}) error {
	title, ok := i.(string)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}
	if strings.TrimSpace(title) == "" || len(title) > config.MaxCoinTitleLength {
		return errors.InvalidCoinTitle
	}
	return nil
}

func validateBaseVolume(i interface{}) error {
	volume, ok := i.(sdkmath.Int)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}
	if volume.IsNil() || volume.IsNegative() || volume.GT(config.MaxCoinSupply) {
		return errors.InvalidCoinInitialVolume
	}
	return nil
}
