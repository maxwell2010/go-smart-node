package app

import (
	"bytes"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/stretchr/testify/require"
)

func TestNativeGenesisAllocationRequiresExplicitBuilder(t *testing.T) {
	cfg := MakeEncodingConfig(990001)
	genesis := DefaultGenesis(cfg.Codec)
	require.NoError(t, ValidateNativeGenesisAllocation(cfg.Codec, genesis))
	owner, err := address.NewBech32Codec("d0").BytesToString(bytes.Repeat([]byte{3}, 20))
	require.NoError(t, err)
	var bank banktypes.GenesisState
	cfg.Codec.MustUnmarshalJSON(genesis["bank"], &bank)
	bank.Balances = []banktypes.Balance{{Address: owner, Coins: sdk.NewCoins(sdk.NewInt64Coin("del", 123))}}
	bank.Supply = nil
	genesis["bank"] = cfg.Codec.MustMarshalJSON(&bank)
	before := bytes.Clone(genesis["coin"])
	require.ErrorContains(t, ValidateNativeGenesisAllocation(cfg.Codec, genesis), "explicitly run")
	require.True(t, bytes.Equal(before, genesis["coin"]), "startup validation must never silently repair allocation")
	require.NoError(t, ReconcileNativeGenesisAllocation(cfg.Codec, genesis))
	require.NoError(t, ValidateNativeGenesisAllocation(cfg.Codec, genesis))
	var coin cointypes.GenesisState
	cfg.Codec.MustUnmarshalJSON(genesis["coin"], &coin)
	require.Equal(t, sdkmath.NewInt(123), coin.Params.BaseVolume)
	bank.Supply = sdk.NewCoins(sdk.NewInt64Coin("del", 124))
	genesis["bank"] = cfg.Codec.MustMarshalJSON(&bank)
	before = bytes.Clone(genesis["coin"])
	require.Error(t, ReconcileNativeGenesisAllocation(cfg.Codec, genesis))
	require.True(t, bytes.Equal(before, genesis["coin"]))
}
