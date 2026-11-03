package app

import (
	"bytes"
	"encoding/json"
	"testing"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/gogoproto/proto"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
)

func assertMountedNativeAssetsBeforeInit(t *testing.T, a *App) {
	t.Helper()
	parent := a.NewUncachedContext(false, cmtproto.Header{ChainID: a.chainID})
	child, _ := parent.CacheContext()
	first, err := address.NewBech32Codec("d0").BytesToString(bytes.Repeat([]byte{1}, 20))
	require.NoError(t, err)
	second, err := address.NewBech32Codec("d0").BytesToString(bytes.Repeat([]byte{2}, 20))
	require.NoError(t, err)
	unit := sdkmath.NewIntWithDecimal(1, 18)
	coin := cointypes.Coin{Denom: "fixture1", Title: "Native metadata fixture", Creator: first, CRR: 50, Volume: unit.MulRaw(10), Reserve: unit.MulRaw(5), MinVolume: sdkmath.ZeroInt(), LimitVolume: unit.MulRaw(100)}
	coin2 := coin
	coin2.Denom = "fixture2"
	coinGenesis := cointypes.NewGenesisState(cointypes.NewParams("del", "Declared fixture allocation", unit.MulRaw(100)), []cointypes.Coin{coin, coin2}, nil)
	zero := sdk.NewCoin("del", sdkmath.ZeroInt())
	nftGenesis := nfttypes.NewGenesisState(nfttypes.DefaultParams(), []nfttypes.Collection{{Creator: first, Denom: "collection", Supply: 1, Tokens: nfttypes.Tokens{&nfttypes.Token{ID: "fixturetoken", Creator: first, Denom: "collection", URI: "ipfs://fixturetoken", Minted: 2, Reserve: sdk.NewCoin("del", unit), SubTokens: nfttypes.SubTokens{&nfttypes.SubToken{ID: 1, Owner: first}, &nfttypes.SubToken{ID: 2, Owner: second, Reserve: &zero}}}}}})
	// These isolated cache records test real mounted keepers and query routing.
	// They are not an accepted contract-backed application genesis.
	require.NoError(t, a.NativeCoinKeeper.InitGenesis(child, coinGenesis))
	require.NoError(t, a.NativeNFTKeeper.InitGenesis(child, nftGenesis))
	invoke := func(ctx sdk.Context, path string, request, response proto.Message) {
		t.Helper()
		handler := a.GRPCQueryRouter().Route(path)
		require.NotNil(t, handler)
		result, err := handler(ctx, &abci.RequestQuery{Path: path, Data: a.appCodec.MustMarshal(request)})
		require.NoError(t, err)
		response.Reset()
		require.NoError(t, a.appCodec.Unmarshal(result.Value, response))
	}
	var coinResponse cointypes.QueryCoinResponse
	invoke(child, "/decimal.coin.v1.Query/Coin", &cointypes.QueryCoinRequest{Denom: coin.Denom}, &coinResponse)
	require.Equal(t, coin.Volume, coinResponse.Coin.Volume)
	var coinsResponse cointypes.QueryCoinsResponse
	invoke(child, "/decimal.coin.v1.Query/Coins", &cointypes.QueryCoinsRequest{Pagination: &query.PageRequest{Limit: 1}}, &coinsResponse)
	require.Len(t, coinsResponse.Coins, 1)
	require.Equal(t, "fixture1", coinsResponse.Coins[0].Denom)
	require.Equal(t, []byte("fixture2"), coinsResponse.Pagination.NextKey)
	invoke(child, "/decimal.coin.v1.Query/Coins", &cointypes.QueryCoinsRequest{Pagination: &query.PageRequest{Limit: 1, Key: coinsResponse.Pagination.NextKey}}, &coinsResponse)
	require.Equal(t, "fixture2", coinsResponse.Coins[0].Denom)
	invoke(child, "/decimal.coin.v1.Query/Coins", &cointypes.QueryCoinsRequest{Pagination: &query.PageRequest{Limit: 1, Reverse: true}}, &coinsResponse)
	require.Equal(t, "fixture2", coinsResponse.Coins[0].Denom)
	invoke(child, "/decimal.coin.v1.Query/Coins", &cointypes.QueryCoinsRequest{Pagination: &query.PageRequest{Limit: 1, Reverse: true, Key: coinsResponse.Pagination.NextKey}}, &coinsResponse)
	require.Equal(t, "fixture1", coinsResponse.Coins[0].Denom)
	var tokenResponse nfttypes.QueryTokenResponse
	invoke(child, "/decimal.nft.v1.Query/Token", &nfttypes.QueryTokenRequest{TokenId: "fixturetoken"}, &tokenResponse)
	require.Len(t, tokenResponse.Token.SubTokens, 2)
	require.Equal(t, unit, tokenResponse.Token.SubTokens[0].Reserve.Amount)
	require.True(t, tokenResponse.Token.SubTokens[1].Reserve.IsZero())
	var collectionResponse nfttypes.QueryCollectionResponse
	invoke(child, "/decimal.nft.v1.Query/Collection", &nfttypes.QueryCollectionRequest{Creator: first, Denom: "collection"}, &collectionResponse)
	require.Len(t, collectionResponse.Collection.Tokens, 1)
	require.Empty(t, collectionResponse.Collection.Tokens[0].SubTokens)
	var byCreator nfttypes.QueryCollectionsByCreatorResponse
	invoke(child, "/decimal.nft.v1.Query/CollectionsByCreator", &nfttypes.QueryCollectionsByCreatorRequest{Creator: first}, &byCreator)
	require.Len(t, byCreator.Collections, 1)
	require.Len(t, byCreator.Collections[0].Tokens, 1)
	require.Error(t, a.NativeNFTKeeper.TransferSubTokens(child, "fixturetoken", []uint32{1, 2}, first, second))
	unchanged, err := a.NativeNFTKeeper.GetSubToken(child, "fixturetoken", 1)
	require.NoError(t, err)
	require.Equal(t, first, unchanged.Owner)
	exported, err := a.ModuleManager.ExportGenesisForModules(child, a.appCodec, []string{"coin", "nft"})
	require.NoError(t, err)
	var coinExport cointypes.GenesisState
	var nftExport nfttypes.GenesisState
	a.appCodec.MustUnmarshalJSON(exported["coin"], &coinExport)
	a.appCodec.MustUnmarshalJSON(exported["nft"], &nftExport)
	require.Equal(t, a.appCodec.MustMarshalJSON(coinGenesis), a.appCodec.MustMarshalJSON(&coinExport))
	require.Equal(t, a.appCodec.MustMarshalJSON(nftGenesis), a.appCodec.MustMarshalJSON(&nftExport))
	other, _ := parent.CacheContext()
	require.NoError(t, a.NativeCoinKeeper.InitGenesis(other, &coinExport))
	require.NoError(t, a.NativeNFTKeeper.InitGenesis(other, &nftExport))
	reimported, err := a.NativeNFTKeeper.ExportGenesis(other)
	require.NoError(t, err)
	require.Equal(t, a.appCodec.MustMarshalJSON(reimported), a.appCodec.MustMarshalJSON(&nftExport))
	_, err = a.NativeCoinKeeper.GetCoin(parent, coin.Denom)
	require.Error(t, err)
	_, err = a.NativeNFTKeeper.GetToken(parent, "fixturetoken")
	require.Error(t, err)
	// Full application admission fails closed before any module writes.
	genesis := a.DefaultGenesis()
	genesis["nft"] = a.appCodec.MustMarshalJSON(nftGenesis)
	data, err := json.Marshal(genesis)
	require.NoError(t, err)
	_, err = a.InitChainer(parent, &abci.RequestInitChain{ChainId: a.chainID, AppStateBytes: data})
	require.ErrorContains(t, err, "authenticated contract bootstrap")
	_, err = a.NativeCoinKeeper.GetParams(parent)
	require.Error(t, err)
	for _, message := range []sdk.Msg{&cointypes.MsgCreateCoin{}, &nfttypes.MsgMintToken{}} {
		require.Nil(t, a.MsgServiceRouter().Handler(message), "native financial message routes must remain disabled")
	}
}

func assertNativeAllocationExport(t *testing.T, a *App) {
	t.Helper()
	ctx := a.NewUncachedContext(false, cmtproto.Header{ChainID: a.chainID, Height: a.LastBlockHeight()})
	before, err := a.NativeCoinKeeper.GetParams(ctx)
	require.NoError(t, err)
	exported, err := a.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(exported.AppState, &state))
	require.NoError(t, ValidateNativeGenesisAllocation(a.appCodec, state))
	after, err := a.NativeCoinKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.True(t, before.BaseVolume.Equal(after.BaseVolume), "export allocation reconciliation must not mutate stored parameters")
	_, err = a.ExportAppStateAndValidators(false, nil, []string{"bank"})
	require.Error(t, err)
	_, err = a.ExportAppStateAndValidators(false, nil, []string{"coin"})
	require.Error(t, err)
}
