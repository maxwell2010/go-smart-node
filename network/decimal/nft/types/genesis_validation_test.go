package types

import (
	"bytes"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	coinconfig "github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func materializedNFTGenesis(t *testing.T) *GenesisState {
	t.Helper()
	owner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(bytes.Repeat([]byte{2}, 20))
	require.NoError(t, err)
	token := &Token{Creator: owner, Denom: "collection", ID: "token", URI: "ipfs://metadata", Reserve: sdk.NewCoin(identity.BaseDenom, sdkmath.NewInt(1)), Minted: 1,
		SubTokens: SubTokens{&SubToken{ID: 1, Owner: owner}}}
	return NewGenesisState(DefaultParams(), []Collection{{Creator: owner, Denom: "collection", Supply: 1, Tokens: Tokens{token}}})
}

func TestNFTGenesisRejectsIncompleteStateWithoutPanics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*GenesisState)
	}{
		{"nil token", func(g *GenesisState) { g.Collections[0].Tokens[0] = nil }},
		{"nil sub-token", func(g *GenesisState) { g.Collections[0].Tokens[0].SubTokens[0] = nil }},
		{"nil token reserve", func(g *GenesisState) { g.Collections[0].Tokens[0].Reserve.Amount = sdkmath.Int{} }},
		{"nil sub-token reserve", func(g *GenesisState) {
			g.Collections[0].Tokens[0].SubTokens[0].Reserve = &sdk.Coin{Denom: identity.BaseDenom}
		}},
		{"negative sub-token reserve", func(g *GenesisState) {
			g.Collections[0].Tokens[0].SubTokens[0].Reserve = &sdk.Coin{Denom: identity.BaseDenom, Amount: sdkmath.NewInt(-1)}
		}},
		{"excess token reserve", func(g *GenesisState) { g.Collections[0].Tokens[0].Reserve.Amount = coinconfig.MaxCoinSupply.AddRaw(1) }},
		{"collection creator", func(g *GenesisState) { g.Collections[0].Creator = "invalid" }},
		{"token creator mismatch", func(g *GenesisState) { g.Collections[0].Tokens[0].Creator = "invalid" }},
		{"token denom mismatch", func(g *GenesisState) { g.Collections[0].Tokens[0].Denom = "other" }},
		{"owner", func(g *GenesisState) { g.Collections[0].Tokens[0].SubTokens[0].Owner = "invalid" }},
		{"supply", func(g *GenesisState) { g.Collections[0].Supply++ }},
		{"burn counter", func(g *GenesisState) { g.Collections[0].Tokens[0].Burnt = 2 }},
		{"reserve denomination mismatch", func(g *GenesisState) {
			g.Collections[0].Tokens[0].SubTokens[0].Reserve = &sdk.Coin{Denom: "other", Amount: sdkmath.NewInt(1)}
		}},
		{"unknown NFT type", func(g *GenesisState) { g.Collections[0].TypeNft = NftType(99) }},
		{"untyped contract", func(g *GenesisState) { g.Collections[0].AddressDRC = "0x1111111111111111111111111111111111111111" }},
		{"zero contract", func(g *GenesisState) {
			g.Collections[0].TypeNft = NftType_NFT721
			g.Collections[0].AddressDRC = "0x0000000000000000000000000000000000000000"
		}},
		{"nil params reserve", func(g *GenesisState) { g.Params.MinReserveAmount = sdkmath.Int{} }},
		{"excess params reserve", func(g *GenesisState) { g.Params.MinReserveAmount = coinconfig.MaxCoinSupply.AddRaw(1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			genesis := materializedNFTGenesis(t)
			tc.change(genesis)
			require.NotPanics(t, func() { require.Error(t, genesis.Validate()) })
		})
	}
}

func TestNFTGenesisStoredReserveAndCounterSemantics(t *testing.T) {
	require.NoError(t, DefaultGenesisState().Validate())
	genesis := materializedNFTGenesis(t)
	require.NoError(t, genesis.Validate()) // reserve below mint admission minimum is valid stored state
	zero := sdk.NewCoin(identity.BaseDenom, sdkmath.ZeroInt())
	genesis.Collections[0].Tokens[0].SubTokens[0].Reserve = &zero
	require.NoError(t, genesis.Validate()) // explicit zero after slashing
	token := genesis.Collections[0].Tokens[0]
	token.Minted, token.Burnt, token.SubTokens[0].ID = 3, 2, 3
	genesis.Params.MaxTokenQuantity = 1
	require.NoError(t, genesis.Validate()) // limit is active supply, not historical minted IDs
	token.Burnt = 3
	token.SubTokens = nil
	require.NoError(t, genesis.Validate()) // fully burned record remains exportable
	var missing *GenesisState
	require.Error(t, missing.Validate())
	var params *Params
	require.Error(t, params.Validate())
}

func TestNFTGenesisDuplicatesAndCapacity(t *testing.T) {
	genesis := materializedNFTGenesis(t)
	genesis.Collections = append(genesis.Collections, genesis.Collections[0])
	require.ErrorContains(t, genesis.Validate(), "duplicated NFT collection")
	genesis = materializedNFTGenesis(t)
	token := *genesis.Collections[0].Tokens[0]
	token.ID = "other"
	genesis.Collections[0].Tokens = append(genesis.Collections[0].Tokens, &token)
	genesis.Collections[0].Supply = 2
	genesis.Params.MaxCollectionSize = 1
	require.Error(t, genesis.Validate())
	genesis = materializedNFTGenesis(t)
	genesis.Collections[0].Tokens[0].Minted = 2
	genesis.Collections[0].Tokens[0].SubTokens = append(genesis.Collections[0].Tokens[0].SubTokens, &SubToken{ID: 2, Owner: genesis.Collections[0].Creator})
	genesis.Params.MaxTokenQuantity = 1
	require.Error(t, genesis.Validate())
}
