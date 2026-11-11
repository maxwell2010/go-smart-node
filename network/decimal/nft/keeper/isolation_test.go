package keeper

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
)

func isolatedLineageFixture(t *testing.T) (sdk.Context, *StateKeeper, *types.GenesisState, []string) {
	t.Helper()
	ctx, k, g, owners := externalFixture(t)
	vault := common.HexToAddress("0x7777777777777777777777777777777777777777")
	id := common.BigToHash(big.NewInt(1)).Bytes()
	origin := g.ExternalBindings[1]
	receipt := &types.Token{Creator: owners[0], Denom: "positions", ID: "isolated_receipt", IsolatedReceipt: true, URI: "ipfs://isolated-receipt", Reserve: sdk.NewCoin("del", sdkmath.NewIntWithDecimal(1, 18)), Minted: 1, SubTokens: types.SubTokens{{ID: 1, Owner: owners[1]}}}
	g.Collections = append(g.Collections, types.Collection{Creator: owners[0], Denom: "positions", Supply: 1, Tokens: types.Tokens{receipt}, TypeNft: types.NftType_NFT721, AddressDRC: vault.Hex()})
	g.ExternalBindings = append(g.ExternalBindings, types.ExternalNFTBinding{ChainID: bindingChain, EVMChainID: 777, Contract: vault.Bytes(), TokenID: id, NativeTokenID: receipt.ID, NFTType: types.NftType_NFT721, NextSubTokenID: 2})
	g.IsolatedVaults = []types.IsolatedVaultState{{ChainID: bindingChain, EVMChainID: 777, Vault: vault.Bytes(), NextPositionID: common.BigToHash(big.NewInt(2)).Bytes(), ReceiptCreator: owners[0], ReceiptDenom: "positions"}}
	g.IsolatedPositions = []types.IsolatedPositionLineage{{Vault: vault.Bytes(), PositionID: id, ReceiptNativeTokenID: receipt.ID, ReceiptSubTokenID: 1, OriginContract: origin.Contract, OriginTokenID: origin.TokenID, OriginNativeTokenID: origin.NativeTokenID, OriginSubTokenIDs: []uint32{1}, ParentPositionID: make([]byte, 32), Reserve: types.IsolatedReserveSnapshot{Kind: types.IsolatedReserveDEL, Token: make([]byte, 20), Amount: common.BigToHash(big.NewInt(1)).Bytes()}}}
	require.NoError(t, g.Validate())
	return ctx, k, g, owners
}

func TestIsolatedLineagePreservesWalletOwnershipAndRoundTrip(t *testing.T) {
	ctx, k, g, owners := isolatedLineageFixture(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	p := g.IsolatedPositions[0]
	read, err := k.GetIsolatedLineage(ctx, p.Vault, p.PositionID)
	require.NoError(t, err)
	require.Equal(t, p, read)
	read.OriginSubTokenIDs[0] = 99
	read.Reserve.Amount[0] = 99
	again, err := k.GetIsolatedLineage(ctx, p.Vault, p.PositionID)
	require.NoError(t, err)
	require.Equal(t, p, again, "callers cannot mutate persisted lineage")
	require.NoError(t, k.TransferSubTokens(ctx, p.ReceiptNativeTokenID, []uint32{1}, owners[1], owners[2]))
	again, err = k.GetIsolatedLineage(ctx, p.Vault, p.PositionID)
	require.NoError(t, err)
	require.Equal(t, p, again, "wallet transfer must not discard origin IDs")
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, g.IsolatedPositions, exported.IsolatedPositions)
	otherCtx, other, _, _ := isolatedLineageFixture(t)
	require.NoError(t, other.InitGenesis(otherCtx, exported))
	restored, err := other.GetIsolatedLineage(otherCtx, p.Vault, p.PositionID)
	require.NoError(t, err)
	require.Equal(t, p, restored)
	sub, err := other.GetSubToken(otherCtx, p.ReceiptNativeTokenID, 1)
	require.NoError(t, err)
	require.Equal(t, owners[2], sub.Owner)
}

func TestIsolatedLineageRejectsInvalidIdentityAndActiveOrigins(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*types.GenesisState)
	}{
		{"active origin", func(g *types.GenesisState) { g.IsolatedPositions[0].OriginSubTokenIDs = []uint32{2} }},
		{"duplicate unit", func(g *types.GenesisState) { g.IsolatedPositions[0].OriginSubTokenIDs = []uint32{1, 1} }},
		{"parent after child", func(g *types.GenesisState) {
			g.IsolatedPositions[0].ParentPositionID = common.BigToHash(big.NewInt(2)).Bytes()
		}},
		{"position at HWM", func(g *types.GenesisState) {
			g.IsolatedVaults[0].NextPositionID = common.BigToHash(big.NewInt(1)).Bytes()
		}},
		{"missing lineage", func(g *types.GenesisState) { g.IsolatedPositions = nil }},
		{"duplicate receipt", func(g *types.GenesisState) { g.IsolatedPositions = append(g.IsolatedPositions, g.IsolatedPositions[0]) }},
		{"reserveless kind", func(g *types.GenesisState) { g.IsolatedPositions[0].Reserve.Kind = 0 }},
		{"truncated token", func(g *types.GenesisState) { g.IsolatedPositions[0].OriginTokenID = []byte{1} }},
		{"domain", func(g *types.GenesisState) { g.IsolatedVaults[0].ChainID = "other-network" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, k, g, _ := isolatedLineageFixture(t)
			test.change(g)
			require.Error(t, k.InitGenesis(ctx, g))
			_, err := k.GetToken(ctx, "token")
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestIsolatedLineageZeroBackedKindAndIndependentParent(t *testing.T) {
	ctx, k, g, _ := isolatedLineageFixture(t)
	p := &g.IsolatedPositions[0]
	p.PositionID = common.BigToHash(big.NewInt(9)).Bytes()
	p.ParentPositionID = common.BigToHash(big.NewInt(8)).Bytes()
	p.Reserve.Amount = make([]byte, 32)
	g.ExternalBindings[2].TokenID = append([]byte(nil), p.PositionID...)
	g.IsolatedVaults[0].NextPositionID = common.BigToHash(big.NewInt(10)).Bytes()
	require.NoError(t, k.InitGenesis(ctx, g))
	read, err := k.GetIsolatedLineage(ctx, p.Vault, p.PositionID)
	require.NoError(t, err)
	require.Equal(t, types.IsolatedReserveDEL, read.Reserve.Kind)
	require.Equal(t, p.ParentPositionID, read.ParentPositionID, "closed informational parent needs no record lookup")
	require.True(t, bytes.Equal(make([]byte, 32), read.Reserve.Amount))
}

func TestIsolatedLineageResourceBoundary(t *testing.T) {
	for _, quantity := range []uint32{1024, 1025} {
		t.Run(big.NewInt(int64(quantity)).String(), func(t *testing.T) {
			ctx, k, g, _ := isolatedLineageFixture(t)
			origin := g.Collections[0].Tokens[1]
			origin.Minted, origin.Burnt, origin.SubTokens = quantity, quantity, nil
			g.ExternalBindings[1].NextSubTokenID = uint64(quantity) + 1
			ids := make([]uint32, quantity)
			for i := range ids {
				ids[i] = uint32(i + 1)
			}
			g.IsolatedPositions[0].OriginSubTokenIDs = ids
			if quantity > types.MaxIsolatedOriginUnits {
				require.Error(t, k.InitGenesis(ctx, g))
				_, err := k.GetToken(ctx, "token")
				require.ErrorIs(t, err, ErrNotFound)
				return
			}
			require.NoError(t, k.InitGenesis(ctx, g))
			ctx = ctx.WithGasMeter(storetypes.NewGasMeter(30_000_000))
			p := g.IsolatedPositions[0]
			got, err := k.GetIsolatedLineage(ctx, p.Vault, p.PositionID)
			require.NoError(t, err)
			require.Len(t, got.OriginSubTokenIDs, int(quantity))
			gas := ctx.GasMeter().GasConsumed()
			require.Less(t, gas, uint64(2_800_000), "archive validation must not reread token metadata for every unit")
			t.Logf("%d-unit native lineage: protobuf=%d bytes, authenticated index read SDK gas=%d", quantity, proto.Size(&p), gas)
		})
	}
}

func TestIsolatedLineageChecksArchivedUnitRecords(t *testing.T) {
	ctx, k, g, _ := isolatedLineageFixture(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	position := g.IsolatedPositions[0]
	state := k.service.OpenKVStore(ctx)
	unit := position.OriginSubTokenIDs[0]
	unitKey := types.GetSubTokenKey(position.OriginNativeTokenID, unit)
	require.NoError(t, state.Set(unitKey, []byte{0xff}))
	_, err := k.GetIsolatedLineage(ctx, position.Vault, position.PositionID)
	require.ErrorContains(t, err, "isolated origin unit remains active")
	require.NoError(t, state.Delete(unitKey))
	require.NoError(t, state.Delete(types.GetIsolatedOriginUnitKey(position.OriginNativeTokenID, unit)))
	_, err = k.GetIsolatedLineage(ctx, position.Vault, position.PositionID)
	require.ErrorContains(t, err, "isolated origin reverse index mismatch")
}

type failIsolatedIndexService struct{ store.KVStoreService }

func (s failIsolatedIndexService) OpenKVStore(ctx context.Context) store.KVStore {
	return failIsolatedIndexStore{s.KVStoreService.OpenKVStore(ctx)}
}

type failIsolatedIndexStore struct{ store.KVStore }

func (s failIsolatedIndexStore) Set(key, value []byte) error {
	if len(key) > 0 && key[0] == 0xA7 {
		return errors.New("isolated origin index write failed")
	}
	return s.KVStore.Set(key, value)
}
func TestIsolatedLineageLateIndexFailureRollsBackGenesis(t *testing.T) {
	ctx, k, g, _ := isolatedLineageFixture(t)
	failing, err := NewStateKeeperWithDomain(failIsolatedIndexService{k.service}, bindingChain, 777)
	require.NoError(t, err)
	require.ErrorContains(t, failing.InitGenesis(ctx, g), "isolated origin index write failed")
	_, err = k.GetToken(ctx, "token")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = k.GetIsolatedVaultState(ctx, g.IsolatedVaults[0].Vault)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, k.InitGenesis(ctx, g), "failed import leaves the complete store empty")
}

func TestIsolatedLineageExportRejectsCorruptIndexes(t *testing.T) {
	for _, prefix := range []byte{0xA6, 0xA7} {
		t.Run(big.NewInt(int64(prefix)).String(), func(t *testing.T) {
			ctx, k, g, _ := isolatedLineageFixture(t)
			require.NoError(t, k.InitGenesis(ctx, g))
			p := g.IsolatedPositions[0]
			key := types.GetIsolatedReceiptIndexKey(p.ReceiptNativeTokenID)
			if prefix == 0xA7 {
				key = types.GetIsolatedOriginUnitKey(p.OriginNativeTokenID, 1)
			}
			require.NoError(t, k.service.OpenKVStore(ctx).Set(key, []byte("wrong position")))
			_, err := k.GetIsolatedLineage(ctx, p.Vault, p.PositionID)
			require.Error(t, err)
			_, err = k.ExportGenesis(ctx)
			require.Error(t, err)
		})
	}
}

func TestIsolatedLineageSplitSubsetsCannotOverlap(t *testing.T) {
	for _, overlap := range []bool{false, true} {
		t.Run(map[bool]string{false: "independent", true: "overlap"}[overlap], func(t *testing.T) {
			ctx, k, g, _ := isolatedLineageFixture(t)
			origin := g.Collections[0].Tokens[1]
			origin.Burnt, origin.SubTokens = origin.Minted, nil
			encoded, err := proto.Marshal(g.Collections[1].Tokens[0])
			require.NoError(t, err)
			childToken := new(types.Token)
			require.NoError(t, proto.Unmarshal(encoded, childToken))
			childToken.ID = "isolated_child"
			g.Collections[1].Tokens = append(g.Collections[1].Tokens, childToken)
			g.Collections[1].Supply = 2
			childBinding := g.ExternalBindings[2]
			childBinding.NativeTokenID = childToken.ID
			childBinding.TokenID = common.BigToHash(big.NewInt(2)).Bytes()
			g.ExternalBindings = append(g.ExternalBindings, childBinding)
			child := g.IsolatedPositions[0]
			child.PositionID = childBinding.TokenID
			child.ReceiptNativeTokenID = childToken.ID
			child.ParentPositionID = common.BigToHash(big.NewInt(1)).Bytes()
			child.OriginSubTokenIDs = []uint32{2}
			if overlap {
				child.OriginSubTokenIDs = []uint32{1}
			}
			g.IsolatedPositions = append(g.IsolatedPositions, child)
			g.IsolatedVaults[0].NextPositionID = common.BigToHash(big.NewInt(3)).Bytes()
			err = k.InitGenesis(ctx, g)
			if overlap {
				require.ErrorContains(t, err, "multiple live receipts")
				return
			}
			require.NoError(t, err)
			first, err := k.GetIsolatedLineage(ctx, g.IsolatedPositions[0].Vault, g.IsolatedPositions[0].PositionID)
			require.NoError(t, err)
			second, err := k.GetIsolatedLineage(ctx, child.Vault, child.PositionID)
			require.NoError(t, err)
			require.Equal(t, []uint32{1}, first.OriginSubTokenIDs)
			require.Equal(t, []uint32{2}, second.OriginSubTokenIDs)
			exported, err := k.ExportGenesis(ctx)
			require.NoError(t, err)
			require.Len(t, exported.IsolatedPositions, 2)
		})
	}
}

func TestIsolatedLineageExportRejectsOrphanConsumedIndex(t *testing.T) {
	ctx, k, g, _ := isolatedLineageFixture(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.service.OpenKVStore(ctx).Set(types.GetIsolatedOriginUnitKey("orphan", 9), []byte("unowned")))
	_, err := k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "orphan records or indexes")
}

func TestFreshZeroBackedChildNeedsExplicitVaultProfile(t *testing.T) {
	for _, profile := range []bool{false, true} {
		t.Run(map[bool]string{false: "generic rejected", true: "isolated accepted"}[profile], func(t *testing.T) {
			ctx, k, g, _ := isolatedLineageFixture(t)
			token := g.Collections[1].Tokens[0]
			token.Reserve = sdk.NewCoin("del", sdkmath.ZeroInt())
			token.IsolatedReceipt = profile
			g.IsolatedPositions[0].Reserve.Amount = make([]byte, 32)
			err := k.InitGenesis(ctx, g)
			if !profile {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			loaded, err := k.GetToken(ctx, token.ID)
			require.NoError(t, err)
			require.True(t, loaded.IsolatedReceipt)
			require.False(t, loaded.ReserveLess)
			require.True(t, loaded.Reserve.IsZero())
			exported, err := k.ExportGenesis(ctx)
			require.NoError(t, err)
			found := false
			for _, collection := range exported.Collections {
				for _, item := range collection.Tokens {
					if item.ID == token.ID {
						found = true
						require.True(t, item.IsolatedReceipt)
					}
				}
			}
			require.True(t, found)
		})
	}
	ctx, k, g, _ := isolatedLineageFixture(t)
	g.Collections[0].Tokens[0].IsolatedReceipt = true
	require.Error(t, k.InitGenesis(ctx, g), "generic origin cannot activate the receipt profile")
}

func TestIsolatedReceiptFlagCannotOutliveKnownLineage(t *testing.T) {
	for _, missing := range []string{"receipt index", "lineage", "vault", "binding"} {
		t.Run(missing, func(t *testing.T) {
			ctx, k, g, _ := isolatedLineageFixture(t)
			require.NoError(t, k.InitGenesis(ctx, g))
			p := g.IsolatedPositions[0]
			key := types.GetIsolatedReceiptIndexKey(p.ReceiptNativeTokenID)
			switch missing {
			case "lineage":
				key = types.GetIsolatedPositionKey(p.Vault, p.PositionID)
			case "vault":
				key = types.GetIsolatedVaultKey(p.Vault)
			case "binding":
				key = types.GetExternalBindingKey(p.ReceiptNativeTokenID)
			}
			require.NoError(t, k.service.OpenKVStore(ctx).Delete(key))
			_, err := k.GetToken(ctx, p.ReceiptNativeTokenID)
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrNotFound, "a broken known receipt is corruption, not an unknown asset")
			_, err = k.ExportGenesis(ctx)
			require.Error(t, err)
		})
	}
}

func TestClosedVaultTokenCannotDropProfileToKeepGenericTombstone(t *testing.T) {
	ctx, k, g, _ := isolatedLineageFixture(t)
	receipt := g.Collections[1].Tokens[0]
	receipt.IsolatedReceipt = false
	receipt.Burnt = 1
	receipt.SubTokens = nil
	g.IsolatedPositions = nil
	require.ErrorContains(t, k.InitGenesis(ctx, g), "active profiled receipt lineage")
}

func TestIsolatedReceiptCannotEnableGenericRemint(t *testing.T) {
	ctx, k, g, _ := isolatedLineageFixture(t)
	g.Collections[1].Tokens[0].AllowMint = true
	require.ErrorContains(t, k.InitGenesis(ctx, g), "cannot be reminted")
}
