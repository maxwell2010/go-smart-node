package keeper

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/core/store"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
)

type ownerCountTestService struct {
	store.KVStoreService
	failWrites, rejectIterations bool
}

func (s ownerCountTestService) OpenKVStore(ctx context.Context) store.KVStore {
	return ownerCountTestStore{s.KVStoreService.OpenKVStore(ctx), s.failWrites, s.rejectIterations}
}

type ownerCountTestStore struct {
	store.KVStore
	failWrites, rejectIterations bool
}

func (s ownerCountTestStore) Set(key, value []byte) error {
	if s.failWrites && len(key) > 0 && key[0] == nftOwnerCountPrefix {
		return errors.New("NFT owner count write failed")
	}
	return s.KVStore.Set(key, value)
}

func (s ownerCountTestStore) Iterator(start, end []byte) (store.Iterator, error) {
	if s.rejectIterations {
		return nil, errors.New("NFT point count attempted iteration")
	}
	return s.KVStore.Iterator(start, end)
}

func requireOwnerCount(t *testing.T, ctx sdk.Context, k *StateKeeper, id, owner string, want uint32) {
	t.Helper()
	actual, err := k.GetActiveSubTokenCount(ctx, id, owner)
	require.NoError(t, err)
	require.Equal(t, want, actual)
}

func TestActiveSubTokenCountUsesPointReadsAboveIsolationLimit(t *testing.T) {
	ctx, service, k, g, owners := nftFixture(t)
	token := g.Collections[0].Tokens[0]
	token.Minted = types.MaxIsolatedOriginUnits + 2
	token.SubTokens = make(types.SubTokens, token.Minted)
	for i := range token.SubTokens {
		token.SubTokens[i] = &types.SubToken{ID: uint32(i) + 1, Owner: owners[0]}
	}
	token.SubTokens[len(token.SubTokens)-1].Owner = owners[1]
	require.NoError(t, k.InitGenesis(ctx, g))
	hot, err := NewStateKeeper(ownerCountTestService{KVStoreService: service, rejectIterations: true})
	require.NoError(t, err)
	requireOwnerCount(t, ctx, hot, token.ID, owners[0], types.MaxIsolatedOriginUnits+1)
	requireOwnerCount(t, ctx, hot, token.ID, owners[1], 1)
	requireOwnerCount(t, ctx, hot, token.ID, owners[2], 0)
	require.NoError(t, hot.TransferSubTokens(ctx, token.ID, []uint32{1}, owners[0], owners[1]))
	requireOwnerCount(t, ctx, hot, token.ID, owners[0], types.MaxIsolatedOriginUnits)
	requireOwnerCount(t, ctx, hot, token.ID, owners[1], 2)
	_, err = hot.GetActiveSubTokenCount(ctx, "missing-token", owners[0])
	require.ErrorIs(t, err, ErrNotFound)
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
}

func TestActiveSubTokenCountFollowsIsolationAndRollsBackLateFailure(t *testing.T) {
	ctx, k, g, proof, owners := gatewayIsolationFixture(t)
	broken, err := NewStateKeeperWithDomain(ownerCountTestService{KVStoreService: k.service, failWrites: true}, bindingChain, 777)
	require.NoError(t, err)
	require.ErrorContains(t, broken.InitGenesis(ctx, g), "NFT owner count write failed")
	require.NoError(t, k.InitGenesis(ctx, g))
	origin := g.ExternalBindings[1].NativeTokenID
	requireOwnerCount(t, ctx, k, origin, owners[0], 3)
	requireOwnerCount(t, ctx, k, origin, owners[1], 1)
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	_, err = broken.AdmitGatewayIsolation(ctx, proof)
	require.ErrorContains(t, err, "NFT owner count write failed")
	unchanged, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, unchanged)
	require.Empty(t, ctx.EventManager().Events())
	lineage, err := k.AdmitGatewayIsolation(ctx, proof)
	require.NoError(t, err)
	requireOwnerCount(t, ctx, k, origin, owners[0], 1)
	requireOwnerCount(t, ctx, k, origin, owners[1], 1)
	receipt := lineage.ReceiptNativeTokenID
	requireOwnerCount(t, ctx, k, receipt, owners[2], 1)
	require.ErrorContains(t, broken.TransferSubTokens(ctx, receipt, []uint32{1}, owners[2], owners[1]), "NFT owner count write failed")
	requireOwnerCount(t, ctx, k, receipt, owners[2], 1)
	requireOwnerCount(t, ctx, k, receipt, owners[1], 0)
	require.NoError(t, k.TransferSubTokens(ctx, receipt, []uint32{1}, owners[2], owners[1]))
	requireOwnerCount(t, ctx, k, receipt, owners[2], 0)
	requireOwnerCount(t, ctx, k, receipt, owners[1], 1)
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
}

func TestActiveSubTokenCountLegacyBuildAndExportAudit(t *testing.T) {
	ctx, service, k, g, owners := nftFixture(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	id := g.Collections[0].Tokens[0].ID
	state := service.OpenKVStore(ctx)
	address0, err := account(owners[0])
	require.NoError(t, err)
	address1, err := account(owners[1])
	require.NoError(t, err)
	key0, key1 := activeSubTokenCountKey(address0, id), activeSubTokenCountKey(address1, id)
	require.NoError(t, state.Delete([]byte{nftOwnerCountMarker}))
	_, err = k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "rows exist without the index marker")
	require.NoError(t, state.Delete(key0))
	require.NoError(t, state.Delete(key1))
	_, err = k.GetActiveSubTokenCount(ctx, id, owners[0])
	require.ErrorIs(t, err, ErrActiveSubTokenCountUnavailable)
	require.NoError(t, k.TransferSubTokens(ctx, id, []uint32{1}, owners[0], owners[1]))
	require.NoError(t, k.BuildActiveSubTokenCountIndex(ctx))
	require.ErrorContains(t, k.BuildActiveSubTokenCountIndex(ctx), "already initialized")
	requireOwnerCount(t, ctx, k, id, owners[0], 0)
	requireOwnerCount(t, ctx, k, id, owners[1], 2)
	require.NoError(t, state.Set(key1, []byte{0, 0, 0, 3}))
	_, err = k.GetActiveSubTokenCount(ctx, id, owners[1])
	require.ErrorContains(t, err, "exceeds the active token supply")
	_, err = k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "differs from active units")
	require.NoError(t, state.Delete(key1))
	_, err = k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "missing NFT owner unit count")
}
