package keeper

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
)

const bindingChain = "mintcandy-bindings-1"

func externalFixture(t *testing.T) (sdk.Context, *StateKeeper, *types.GenesisState, []string) {
	t.Helper()
	ctx, service, _, genesis, owners := nftFixture(t)
	ctx = ctx.WithChainID(bindingChain)
	k, err := NewStateKeeperWithDomain(service, bindingChain, 777)
	require.NoError(t, err)
	collection := &genesis.Collections[0]
	collection.TypeNft = types.NftType_NFT1155
	contract := common.HexToAddress("0x1234567890123456789012345678901234567890")
	collection.AddressDRC = contract.Hex()
	collection.ExternalSymbol, collection.ExternalName = "ART", "External collection"
	largeID := make([]byte, 32)
	largeID[6] = 1 // 2^200, intentionally outside uint64.
	freshID, err := types.FreshExternalTokenID(bindingChain, 777, contract.Bytes(), largeID)
	require.NoError(t, err)
	first := collection.Tokens[0]
	second := new(types.Token)
	encoded, err := proto.Marshal(first)
	require.NoError(t, err)
	require.NoError(t, proto.Unmarshal(encoded, second))
	second.ID, second.Minted, second.Burnt = freshID, 3, 1
	second.SubTokens = types.SubTokens{{ID: 2, Owner: owners[0]}, {ID: 3, Owner: owners[1]}}
	collection.Tokens = append(collection.Tokens, second)
	collection.Supply = 2
	genesis.ExternalBindings = []types.ExternalNFTBinding{
		{ChainID: bindingChain, EVMChainID: 777, Contract: contract.Bytes(), TokenID: make([]byte, 32), NativeTokenID: first.ID, NFTType: collection.TypeNft, NextSubTokenID: 3},
		{ChainID: bindingChain, EVMChainID: 777, Contract: contract.Bytes(), TokenID: largeID, NativeTokenID: freshID, NFTType: collection.TypeNft, NextSubTokenID: 4},
	}
	return ctx, k, genesis, owners
}

func TestExternalNFTBindingWriteFailureIsAtomic(t *testing.T) {
	ctx, k, g, _ := externalFixture(t)
	failing, err := NewStateKeeperWithDomain(failBindingIndexService{k.service}, bindingChain, 777)
	require.NoError(t, err)
	require.ErrorContains(t, failing.InitGenesis(ctx, g), "binding index write failed")
	_, err = k.GetToken(ctx, "token")
	require.ErrorIs(t, err, ErrNotFound, "metadata and owner writes before the failed reverse index must roll back")
	_, err = k.GetExternalNFTBinding(ctx, "token")
	require.ErrorIs(t, err, ErrNotFound)
	require.Empty(t, ctx.EventManager().Events())
	require.NoError(t, k.InitGenesis(ctx, g), "the failed import must leave params and every index empty")
}

func TestExternalNFTCollectionContractLookupChecksNativeIndex(t *testing.T) {
	ctx, k, g, _ := externalFixture(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	contract := g.Collections[0].AddressDRC
	collection, err := k.GetCollectionByDRC(ctx, contract)
	require.NoError(t, err)
	require.Equal(t, g.Collections[0].Denom, collection.Denom)
	require.Empty(t, collection.Tokens, "contract identity lookup must stay shallow")
	_, err = k.GetCollectionByDRC(ctx, "0x0000000000000000000000000000000000000000")
	require.Error(t, err)
	_, err = k.GetCollectionByDRC(ctx, "0x9999999999999999999999999999999999999999")
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, k.service.OpenKVStore(ctx).Set(types.GetCollectionsByAddressDRCKey(strings.ToLower(contract)), []byte{0x21, 1}))
	_, err = k.GetCollectionByDRC(ctx, contract)
	require.ErrorContains(t, err, "index mismatch")
}

type failBindingIndexService struct{ store.KVStoreService }

func (s failBindingIndexService) OpenKVStore(ctx context.Context) store.KVStore {
	return failBindingIndexStore{s.KVStoreService.OpenKVStore(ctx)}
}

type failBindingIndexStore struct{ store.KVStore }

func (s failBindingIndexStore) Set(key, value []byte) error {
	if len(key) > 0 && key[0] == 0xA4 {
		return errors.New("binding index write failed")
	}
	return s.KVStore.Set(key, value)
}

func TestExternalNFTIdentityRoundTripAndOwnershipPages(t *testing.T) {
	ctx, k, genesis, owners := externalFixture(t)
	require.NoError(t, k.InitGenesis(ctx, genesis))
	for _, expected := range genesis.ExternalBindings {
		binding, err := k.ResolveExternalNFT(ctx, expected.Contract, expected.TokenID)
		require.NoError(t, err)
		require.True(t, expected.Equal(&binding))
		byNative, err := k.GetExternalNFTBinding(ctx, expected.NativeTokenID)
		require.NoError(t, err)
		require.True(t, binding.Equal(&byNative))
	}
	_, err := k.ResolveExternalNFT(ctx, genesis.ExternalBindings[1].Contract, []byte{1})
	require.Error(t, err, "uint256 identities must never be accepted as lossy slices")
	_, err = k.GetExternalNFTBinding(ctx.WithChainID("another-chain"), "token")
	require.ErrorContains(t, err, "network domain")
	_, err = k.GetTokenByURI(ctx, genesis.Collections[0].Tokens[0].URI)
	require.ErrorIs(t, err, ErrAmbiguousURI)
	ids, cursor, err := k.ListTokenIDsByURI(ctx, genesis.Collections[0].Tokens[0].URI, "", 1)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	secondIDs, _, err := k.ListTokenIDsByURI(ctx, genesis.Collections[0].Tokens[0].URI, cursor, 1)
	require.NoError(t, err)
	require.Len(t, secondIDs, 1)
	require.ElementsMatch(t, []string{"token", genesis.ExternalBindings[1].NativeTokenID}, append(ids, secondIDs...))
	ids, _, err = k.ListTokenIDsByURI(ctx, genesis.Collections[0].Tokens[0].URI, secondIDs[0], 1)
	require.NoError(t, err)
	require.Empty(t, ids)

	nativeID := genesis.ExternalBindings[1].NativeTokenID
	units, after, err := k.ListActiveSubTokenIDs(ctx, nativeID, owners[0], 0, 1)
	require.NoError(t, err)
	require.Equal(t, []uint32{2}, units)
	require.Equal(t, uint32(2), after)
	require.NoError(t, k.TransferSubTokens(ctx, nativeID, units, owners[0], owners[1]))
	units, after, err = k.ListActiveSubTokenIDs(ctx, nativeID, owners[1], 0, 1)
	require.NoError(t, err)
	require.Equal(t, []uint32{2}, units)
	units, _, err = k.ListActiveSubTokenIDs(ctx, nativeID, owners[1], after, 1)
	require.NoError(t, err)
	require.Equal(t, []uint32{3}, units)
	_, _, err = k.ListActiveSubTokenIDs(ctx, nativeID, owners[1], 0, MaxIdentityPageSize+1)
	require.Error(t, err)
	_, _, err = k.ListTokenIDsByURI(ctx, "uri", "", 0)
	require.Error(t, err)

	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, exported.ExternalBindings, 2)
	require.Equal(t, "token", genesis.ExternalBindings[0].NativeTokenID, "explicit legacy native IDs remain unchanged")
	newCtx, service, _, _, _ := nftFixture(t)
	newCtx = newCtx.WithChainID(bindingChain)
	reimported, err := NewStateKeeperWithDomain(service, bindingChain, 777)
	require.NoError(t, err)
	require.NoError(t, reimported.InitGenesis(newCtx, exported))
	secondExport, err := reimported.ExportGenesis(newCtx)
	require.NoError(t, err)
	require.True(t, exported.Equal(secondExport), "all identity, cursor, ownership, metadata and index populations survive export/reimport")
}

func TestExternalNFTDomainAndCursorValidation(t *testing.T) {
	for _, item := range []struct {
		name   string
		change func(*types.GenesisState)
	}{
		{"short address", func(g *types.GenesisState) { g.ExternalBindings[0].Contract = []byte{1} }},
		{"short uint256", func(g *types.GenesisState) { g.ExternalBindings[0].TokenID = []byte{1} }},
		{"duplicate native", func(g *types.GenesisState) { g.ExternalBindings[1].NativeTokenID = g.ExternalBindings[0].NativeTokenID }},
		{"duplicate external", func(g *types.GenesisState) {
			g.ExternalBindings[1].TokenID = bytes.Clone(g.ExternalBindings[0].TokenID)
		}},
		{"missing token", func(g *types.GenesisState) { g.ExternalBindings[0].NativeTokenID = "missing" }},
		{"wrong collection", func(g *types.GenesisState) { g.ExternalBindings[0].Contract[0] ^= 1 }},
		{"wrong standard", func(g *types.GenesisState) { g.ExternalBindings[0].NFTType = types.NftType_NFT721 }},
		{"reused cursor", func(g *types.GenesisState) { g.ExternalBindings[0].NextSubTokenID = 2 }},
		{"zero cursor", func(g *types.GenesisState) { g.ExternalBindings[0].NextSubTokenID = 0 }},
		{"oversized cursor", func(g *types.GenesisState) { g.ExternalBindings[0].NextSubTokenID = types.MaxNextSubTokenID + 1 }},
		{"mixed domain", func(g *types.GenesisState) { g.ExternalBindings[1].EVMChainID++ }},
	} {
		t.Run(item.name, func(t *testing.T) {
			ctx, k, g, _ := externalFixture(t)
			item.change(g)
			require.Error(t, k.InitGenesis(ctx, g))
			require.Empty(t, ctx.EventManager().Events())
			_, err := k.ExportGenesis(ctx)
			require.Error(t, err)
		})
	}
	ctx, k, g, _ := externalFixture(t)
	unbound, err := NewStateKeeper(k.service)
	require.NoError(t, err)
	require.ErrorContains(t, unbound.InitGenesis(ctx, g), "network domain")
	g.ExternalBindings[0].ChainID, g.ExternalBindings[1].ChainID = "another-chain", "another-chain"
	require.ErrorContains(t, k.InitGenesis(ctx, g), "network domain")
}

func TestExternalNFTTerminalCursorAndReserveLessPolicy(t *testing.T) {
	ctx, k, g, owners := externalFixture(t)
	collection := &g.Collections[0]
	collection.Tokens = collection.Tokens[:1]
	collection.Supply = 1
	g.ExternalBindings = g.ExternalBindings[:1]
	token := collection.Tokens[0]
	token.Minted, token.Burnt = math.MaxUint32, math.MaxUint32-1
	token.SubTokens = types.SubTokens{{ID: math.MaxUint32, Owner: owners[0]}}
	g.ExternalBindings[0].NextSubTokenID = types.MaxNextSubTokenID
	require.NoError(t, k.InitGenesis(ctx, g))
	binding, err := k.GetExternalNFTBinding(ctx, "token")
	require.NoError(t, err)
	require.Equal(t, types.MaxNextSubTokenID, binding.NextSubTokenID)
	units, _, err := k.ListActiveSubTokenIDs(ctx, "token", owners[0], math.MaxUint32-1, 1)
	require.NoError(t, err)
	require.Equal(t, []uint32{math.MaxUint32}, units)

	ctx, k, g, _ = externalFixture(t)
	g.Collections[0].ReserveLess = true
	for _, token := range g.Collections[0].Tokens {
		token.ReserveLess = true
		token.Reserve = sdk.NewCoin("del", sdkmath.ZeroInt())
		for _, sub := range token.SubTokens {
			sub.Reserve = nil
		}
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
	noBinding := new(types.GenesisState)
	encoded, err := proto.Marshal(g)
	require.NoError(t, err)
	require.NoError(t, proto.Unmarshal(encoded, noBinding))
	noBinding.ExternalBindings = nil
	require.ErrorContains(t, noBinding.Validate(), "explicit external identity")
	g.Collections[0].Tokens[0].Reserve = sdk.NewCoin("del", sdkmath.OneInt())
	require.Error(t, g.Validate(), "reserve-less metadata cannot excuse positive financial backing")
}

func TestExternalNFTIndexConservation(t *testing.T) {
	for _, item := range []struct {
		name    string
		corrupt func(*StateKeeper, sdk.Context, *types.GenesisState) error
	}{
		{"missing reverse", func(k *StateKeeper, ctx sdk.Context, g *types.GenesisState) error {
			return k.service.OpenKVStore(ctx).Delete(types.GetExternalIdentityKey(g.ExternalBindings[0]))
		}},
		{"missing URI", func(k *StateKeeper, ctx sdk.Context, g *types.GenesisState) error {
			return k.service.OpenKVStore(ctx).Delete(types.GetTokenURIMemberKey(g.Collections[0].Tokens[0].URI, "token"))
		}},
		{"orphan reverse", func(k *StateKeeper, ctx sdk.Context, g *types.GenesisState) error {
			return k.service.OpenKVStore(ctx).Set(append([]byte{0xA4}, bytes.Repeat([]byte{9}, 32)...), []byte("token"))
		}},
		{"orphan URI", func(k *StateKeeper, ctx sdk.Context, g *types.GenesisState) error {
			return k.service.OpenKVStore(ctx).Set(types.GetTokenURIMemberKey("orphan", "token"), []byte("token"))
		}},
	} {
		t.Run(item.name, func(t *testing.T) {
			ctx, k, g, _ := externalFixture(t)
			require.NoError(t, k.InitGenesis(ctx, g))
			require.NoError(t, item.corrupt(k, ctx, g))
			_, err := k.ExportGenesis(ctx)
			require.Error(t, err)
		})
	}
}
