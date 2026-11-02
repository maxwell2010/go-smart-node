package types_test

import (
	"strings"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/stretchr/testify/require"
)

func TestStoredCoinValidationRejectsMalformedFinancialFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*types.Coin)
	}{
		{"nil volume", func(c *types.Coin) { c.Volume = sdkmath.Int{} }},
		{"nil reserve", func(c *types.Coin) { c.Reserve = sdkmath.Int{} }},
		{"nil minimum", func(c *types.Coin) { c.MinVolume = sdkmath.Int{} }},
		{"nil limit", func(c *types.Coin) { c.LimitVolume = sdkmath.Int{} }},
		{"negative volume", func(c *types.Coin) { c.Volume = sdkmath.NewInt(-1) }},
		{"negative reserve", func(c *types.Coin) { c.Reserve = sdkmath.NewInt(-1) }},
		{"negative minimum", func(c *types.Coin) { c.MinVolume = sdkmath.NewInt(-1) }},
		{"negative limit", func(c *types.Coin) { c.LimitVolume = sdkmath.NewInt(-1) }},
		{"excess volume", func(c *types.Coin) { c.Volume = config.MaxCoinSupply.AddRaw(1) }},
		{"excess reserve", func(c *types.Coin) { c.Reserve = config.MaxCoinSupply.AddRaw(1) }},
		{"excess minimum", func(c *types.Coin) { c.MinVolume = config.MaxCoinSupply.AddRaw(1) }},
		{"excess limit", func(c *types.Coin) { c.LimitVolume = config.MaxCoinSupply.AddRaw(1) }},
		{"minimum exceeds volume", func(c *types.Coin) { c.MinVolume = c.Volume.AddRaw(1) }},
		{"volume exceeds limit", func(c *types.Coin) { c.LimitVolume = c.Volume.SubRaw(1) }},
		{"low CRR", func(c *types.Coin) { c.CRR = 9 }},
		{"high CRR", func(c *types.Coin) { c.CRR = 101 }},
		{"denomination", func(c *types.Coin) { c.Denom = "BAD" }},
		{"empty title", func(c *types.Coin) { c.Title = "  " }},
		{"long title", func(c *types.Coin) { c.Title = strings.Repeat("a", config.MaxCoinTitleLength+1) }},
		{"creator", func(c *types.Coin) { c.Creator = "not-an-address" }},
		{"zero contract", func(c *types.Coin) { c.DRC20Contract = "0x0000000000000000000000000000000000000000" }},
		{"malformed contract", func(c *types.Coin) { c.DRC20Contract = "0x1234" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coin := completeStoredCoin(t)
			tc.change(&coin)
			require.NotPanics(t, func() { require.Error(t, coin.Validate()) })
		})
	}
}

func TestStoredCoinZeroBalancesAndExplicitAddressCodec(t *testing.T) {
	coin := completeStoredCoin(t)
	// Balances below creation minima are valid stored/exported state.
	require.NoError(t, coin.Validate())
	coin.Volume, coin.Reserve, coin.MinVolume, coin.LimitVolume = sdkmath.ZeroInt(), sdkmath.ZeroInt(), sdkmath.ZeroInt(), sdkmath.ZeroInt()
	require.NoError(t, coin.Validate())
	require.True(t, strings.HasPrefix(coin.Creator, "d01"))
	require.False(t, types.ValidContractAddress(""))
	require.True(t, types.ValidContractAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"))
	var missing *types.Coin
	require.Error(t, missing.Validate())
}

func signedGenesisCheck(t *testing.T, denom string) types.Check {
	t.Helper()
	issuer, err := crypto.GenerateKey()
	require.NoError(t, err)
	lock, err := crypto.GenerateKey()
	require.NoError(t, err)
	check := types.Check{ChainID: "mintcandy_777777-1", Coin: sdk.NewCoin(denom, sdkmath.NewInt(1)), Nonce: []byte{1}, DueBlock: 100}
	hash := check.HashWithoutLock()
	check.Lock, err = crypto.Sign(hash[:], lock)
	require.NoError(t, err)
	require.NoError(t, check.Sign(issuer))
	return check
}

func TestGenesisValidatesChecksBeforeHashing(t *testing.T) {
	valid := signedGenesisCheck(t, types.DefaultParams().BaseDenom)
	require.NoError(t, valid.Validate())
	for _, tc := range []struct {
		name   string
		change func(*types.Check)
	}{
		{"nil amount", func(c *types.Check) { c.Coin.Amount = sdkmath.Int{} }},
		{"zero amount", func(c *types.Check) { c.Coin.Amount = sdkmath.ZeroInt() }},
		{"negative amount", func(c *types.Check) { c.Coin.Amount = sdkmath.NewInt(-1) }},
		{"excess amount", func(c *types.Check) { c.Coin.Amount = config.MaxCoinSupply.AddRaw(1) }},
		{"nil V", func(c *types.Check) { c.V = sdkmath.Int{} }},
		{"nil R", func(c *types.Check) { c.R = sdkmath.Int{} }},
		{"nil S", func(c *types.Check) { c.S = sdkmath.Int{} }},
		{"invalid V", func(c *types.Check) { c.V = sdkmath.ZeroInt() }},
		{"invalid R", func(c *types.Check) { c.R = sdkmath.ZeroInt() }},
		{"long nonce", func(c *types.Check) { c.Nonce = make([]byte, 17) }},
		{"empty chain", func(c *types.Check) { c.ChainID = "" }},
		{"zero expiry", func(c *types.Check) { c.DueBlock = 0 }},
		{"invalid lock", func(c *types.Check) { c.Lock = make([]byte, 65) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := valid
			tc.change(&check)
			genesis := types.NewGenesisState(types.DefaultParams(), nil, []types.Check{check})
			require.NotPanics(t, func() { require.Error(t, genesis.Validate()) })
		})
	}
	genesis := types.NewGenesisState(types.DefaultParams(), nil, []types.Check{valid})
	require.NoError(t, genesis.Validate())
	genesis.Checks = append(genesis.Checks, valid)
	require.Error(t, genesis.Validate())
	genesis.Checks = []types.Check{signedGenesisCheck(t, "unknown")}
	require.Error(t, genesis.Validate())
}

func TestGenesisAllocationAndMappings(t *testing.T) {
	genesis := types.DefaultGenesisState()
	require.True(t, genesis.Params.BaseVolume.IsZero())
	require.NoError(t, genesis.Validate())
	genesis = completeCoinGenesis(t)
	require.NoError(t, genesis.Validate())
	genesis.Params.BaseVolume = genesis.Coins[0].Reserve.SubRaw(1)
	require.ErrorContains(t, genesis.Validate(), "reserves exceed")
	genesis = completeCoinGenesis(t)
	genesis.Coins[0].Denom = genesis.Params.BaseDenom
	require.Error(t, genesis.Validate())
	genesis = completeCoinGenesis(t)
	other := genesis.Coins[0]
	other.Denom = "other"
	genesis.Coins[0].DRC20Contract = "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	other.DRC20Contract = "0xABCDEFABCDEFABCDEFABCDEFABCDEFABCDEFABCD"
	genesis.Coins = append(genesis.Coins, other)
	genesis.Params.BaseVolume = genesis.Params.BaseVolume.MulRaw(2)
	require.ErrorContains(t, genesis.Validate(), "duplicate coin contract")
	genesis = types.DefaultGenesisState()
	genesis.Params.BaseVolume = sdkmath.Int{}
	require.NotPanics(t, func() { require.Error(t, genesis.Validate()) })
	var missing *types.GenesisState
	require.Error(t, missing.Validate())
	var params *types.Params
	require.Error(t, params.Validate())
}
