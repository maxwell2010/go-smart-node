package keeper

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/compat"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
)

func nftFixture(t *testing.T) (sdk.Context, store.KVStoreService, *StateKeeper, *types.GenesisState, []string) {
	t.Helper()
	key := storetypes.NewKVStoreKey("nft-state")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient"))
	service := runtime.NewKVStoreService(key)
	keeper, err := NewStateKeeper(service)
	require.NoError(t, err)
	owners := make([]string, 3)
	for i := range owners {
		owners[i], err = address.NewBech32Codec("d0").BytesToString(bytes.Repeat([]byte{byte(i + 1)}, 20))
		require.NoError(t, err)
	}
	reserve := sdk.NewCoin("del", sdkmath.NewIntWithDecimal(1, 18))
	zero := sdk.NewCoin("del", sdkmath.ZeroInt())
	genesis := types.NewGenesisState(types.DefaultParams(), []types.Collection{{Creator: owners[0], Denom: "collection", Supply: 1, Tokens: types.Tokens{
		&types.Token{Creator: owners[0], Denom: "collection", ID: "token", URI: "ipfs://metadata", Reserve: reserve, Minted: 2, SubTokens: types.SubTokens{
			&types.SubToken{ID: 1, Owner: owners[0]}, &types.SubToken{ID: 2, Owner: owners[1], Reserve: &zero},
		}},
	}}})
	return ctx, service, keeper, genesis, owners
}

func TestNFTPartialBatchAndEventsRollback(t *testing.T) {
	ctx, _, keeper, genesis, owners := nftFixture(t)
	require.NoError(t, keeper.InitGenesis(ctx, genesis))
	require.ErrorContains(t, keeper.TransferSubTokens(ctx, "token", []uint32{1, 2}, owners[0], owners[2]), "owner differs")
	first, err := keeper.GetSubToken(ctx, "token", 1)
	require.NoError(t, err)
	require.Equal(t, owners[0], first.Owner, "a later invalid item must undo the first transfer")
	require.Nil(t, first.Reserve, "inherited reserve must retain its wire representation")
	second, err := keeper.GetSubToken(ctx, "token", 2)
	require.NoError(t, err)
	require.Equal(t, owners[1], second.Owner)
	require.True(t, second.Reserve.IsZero(), "explicit zero slashed reserve cannot become inherited reserve")
	require.Empty(t, ctx.EventManager().Events())
	require.ErrorContains(t, keeper.TransferSubTokens(ctx, "token", []uint32{1, 1}, owners[0], owners[2]), "duplicated")
	keeper.emit = func(child sdk.Context, event proto.Message) error {
		require.NoError(t, child.EventManager().EmitTypedEvent(event))
		return errors.New("event conversion failed")
	}
	require.ErrorContains(t, keeper.TransferSubTokens(ctx, "token", []uint32{1}, owners[0], owners[2]), "event conversion failed")
	first, err = keeper.GetSubToken(ctx, "token", 1)
	require.NoError(t, err)
	require.Equal(t, owners[0], first.Owner)
	require.Empty(t, ctx.EventManager().Events())
}

func TestNFTOwnerIndexesFollowOuterCommit(t *testing.T) {
	ctx, service, keeper, genesis, owners := nftFixture(t)
	require.NoError(t, keeper.InitGenesis(ctx, genesis))
	child, commit := ctx.CacheContext()
	require.NoError(t, keeper.TransferSubTokens(child, "token", []uint32{1}, owners[0], owners[2]))
	parent, err := keeper.GetSubToken(ctx, "token", 1)
	require.NoError(t, err)
	require.Equal(t, owners[0], parent.Owner)
	require.Empty(t, ctx.EventManager().Events())
	pending, err := keeper.GetSubToken(child, "token", 1)
	require.NoError(t, err)
	require.Equal(t, owners[2], pending.Owner)
	commit()
	parent, err = keeper.GetSubToken(ctx, "token", 1)
	require.NoError(t, err)
	require.Equal(t, owners[2], parent.Owner)
	require.Len(t, ctx.EventManager().Events(), 1)
	old, err := account(owners[0])
	require.NoError(t, err)
	present, err := service.OpenKVStore(ctx).Has(types.GetSubTokenByOwnerKey(old, "token", 1))
	require.NoError(t, err)
	require.False(t, present)
	metadata, err := keeper.GetToken(ctx, "token")
	require.NoError(t, err)
	require.Equal(t, uint32(2), metadata.Minted)
	require.Zero(t, metadata.Burnt)
	require.Equal(t, genesis.Collections[0].Tokens[0].Reserve, metadata.Reserve)
}

func TestNFTIndexWriteFailureRestoresOldOwner(t *testing.T) {
	ctx, service, keeper, genesis, owners := nftFixture(t)
	require.NoError(t, keeper.InitGenesis(ctx, genesis))
	failing, err := NewStateKeeper(failOwnerIndexService{service})
	require.NoError(t, err)
	require.ErrorContains(t, failing.TransferSubTokens(ctx, "token", []uint32{1}, owners[0], owners[2]), "index write failed")
	parent, err := keeper.GetSubToken(ctx, "token", 1)
	require.NoError(t, err)
	require.Equal(t, owners[0], parent.Owner, "original record and owner index must both survive")
	require.Empty(t, ctx.EventManager().Events())
}

func TestNFTGenesisFailureIsAtomic(t *testing.T) {
	for _, duplicateURI := range []bool{false, true} {
		ctx, _, keeper, genesis, _ := nftFixture(t)
		if duplicateURI {
			other := *genesis.Collections[0].Tokens[0]
			other.ID = "secondtoken"
			genesis.Collections[0].Tokens = append(genesis.Collections[0].Tokens, &other)
			genesis.Collections[0].Supply++
		} else {
			genesis.Collections[0].Tokens[0].URI = strings.Repeat("x", MaxRecordBytes)
		}
		require.Error(t, keeper.InitGenesis(ctx, genesis))
		_, err := keeper.params.Get(ctx)
		require.ErrorIs(t, err, compat.ErrMissingParams)
		_, err = keeper.GetToken(ctx, "token")
		require.ErrorIs(t, err, ErrNotFound)
	}
}

func TestNFTCompleteExportRoundTripAndOrphanDetection(t *testing.T) {
	ctx, service, keeper, genesis, owners := nftFixture(t)
	require.NoError(t, keeper.InitGenesis(ctx, genesis))
	require.NoError(t, keeper.TransferSubTokens(ctx, "token", []uint32{1}, owners[0], owners[2]))
	exported, err := keeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.Collections[0].Tokens[0].SubTokens, 2)
	require.Equal(t, owners[2], exported.Collections[0].Tokens[0].SubTokens[0].Owner)
	require.Nil(t, exported.Collections[0].Tokens[0].SubTokens[0].Reserve)
	require.True(t, exported.Collections[0].Tokens[0].SubTokens[1].Reserve.IsZero())
	otherCtx, _, otherKeeper, _, _ := nftFixture(t)
	require.NoError(t, otherKeeper.InitGenesis(otherCtx, exported))
	roundTrip, err := otherKeeper.ExportGenesis(otherCtx)
	require.NoError(t, err)
	first, err := proto.Marshal(exported)
	require.NoError(t, err)
	second, err := proto.Marshal(roundTrip)
	require.NoError(t, err)
	require.Equal(t, first, second, "complete exported state must reimport without losing counters, owners or reserve representation")
	staleOwner, err := account(owners[0])
	require.NoError(t, err)
	require.NoError(t, service.OpenKVStore(ctx).Set(types.GetSubTokenByOwnerKey(staleOwner, "token", 1), []byte("token")))
	_, err = keeper.ExportGenesis(ctx)
	require.ErrorContains(t, err, "orphan")
}

type failOwnerIndexService struct{ store.KVStoreService }

func (s failOwnerIndexService) OpenKVStore(ctx context.Context) store.KVStore {
	return failOwnerIndexStore{s.KVStoreService.OpenKVStore(ctx)}
}

type failOwnerIndexStore struct{ store.KVStore }

func (s failOwnerIndexStore) Set(key, value []byte) error {
	if len(key) > 0 && key[0] == 0xA3 {
		return errors.New("index write failed")
	}
	return s.KVStore.Set(key, value)
}
