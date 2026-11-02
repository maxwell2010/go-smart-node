package config

import (
	"regexp"

	sdkmath "cosmossdk.io/math"

	helpers "github.com/maxwell2010/go-smart-node/network/decimal/compat"
)

var (
	CoinDenomRegExp    = "^[a-z][a-z0-9]{2,9}$"
	CoinDenomValidator = regexp.MustCompile(CoinDenomRegExp)
	MaxCoinTitleLength = 64
	MinCoinSupply      = helpers.EtherToWei(sdkmath.NewInt(1))
	MaxCoinSupply      = helpers.EtherToWei(sdkmath.NewInt(1_000_000_000_000_000))
	MinCoinReserve     = helpers.EtherToWei(sdkmath.NewInt(1))
)
