package keeper

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
)

func TestRetireIsolatedDELReceiptPreservesOtherPositionsAndOriginHistory(t *testing.T) {
	ctx, keeper, genesis, firstProof, owners := gatewayIsolationFixture(t)
	require.NoError(t, keeper.InitGenesis(ctx, genesis))
	first, err := keeper.AdmitGatewayIsolation(ctx, firstProof)
	require.NoError(t, err)
	owner, err := account(owners[0])
	require.NoError(t, err)
	otherOwner, err := account(owners[1])
	require.NoError(t, err)
	secondProof := firstProof
	secondProof.Owner = common.BytesToAddress(otherOwner)
	secondProof.PositionID, secondProof.NextPositionID = firstProof.NextPositionID, stateIsolationID(3)
	secondProof.Quantity, secondProof.OriginSupplyAfter = 1, 1
	secondProof.Reserve = genesis.Collections[0].Tokens[1].Reserve.Amount
	secondProof.OriginReserveAfter = secondProof.Reserve
	second, err := keeper.AdmitGatewayIsolation(ctx, secondProof)
	require.NoError(t, err)
	require.ErrorContains(t, keeper.RetireIsolatedDELReceipt(ctx, firstProof.Gateway, firstProof.Gateway, first, secondProof.NextPositionID), "wallet-held")
	require.NoError(t, keeper.TransferSubTokens(ctx, first.ReceiptNativeTokenID, []uint32{1}, owners[2], owners[0]))
	before, err := keeper.ExportGenesis(ctx)
	require.NoError(t, err)
	ownerAddress := common.BytesToAddress(owner)
	require.ErrorContains(t, keeper.RetireIsolatedDELReceipt(ctx, firstProof.Gateway, common.BytesToAddress(otherOwner), first, secondProof.NextPositionID), "native wallet owner")
	require.ErrorContains(t, keeper.RetireIsolatedDELReceipt(ctx, firstProof.Gateway, ownerAddress, first, firstProof.NextPositionID), "vault cursor")
	stale := first
	stale.Reserve.Amount = stateIsolationID(1).Bytes()
	require.ErrorContains(t, keeper.RetireIsolatedDELReceipt(ctx, firstProof.Gateway, ownerAddress, stale, secondProof.NextPositionID), "current native lineage")
	unchanged, err := keeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, unchanged)

	origin, err := keeper.GetToken(ctx, first.OriginNativeTokenID)
	require.NoError(t, err)
	originBinding, err := keeper.GetExternalNFTBinding(ctx, origin.ID)
	require.NoError(t, err)
	firstToken, err := keeper.GetToken(ctx, first.ReceiptNativeTokenID)
	require.NoError(t, err)
	require.NoError(t, keeper.RetireIsolatedDELReceipt(ctx, firstProof.Gateway, ownerAddress, first, secondProof.NextPositionID))
	require.ErrorIs(t, keeper.RetireIsolatedDELReceipt(ctx, firstProof.Gateway, ownerAddress, first, secondProof.NextPositionID), ErrNotFound)
	_, err = keeper.GetToken(ctx, first.ReceiptNativeTokenID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = keeper.GetSubToken(ctx, first.ReceiptNativeTokenID, 1)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = keeper.GetExternalNFTBinding(ctx, first.ReceiptNativeTokenID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = keeper.ResolveExternalNFT(ctx, first.Vault, first.PositionID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = keeper.GetIsolatedLineage(ctx, first.Vault, first.PositionID)
	require.ErrorIs(t, err, ErrNotFound)
	uriIDs, _, err := keeper.ListTokenIDsByURI(ctx, firstToken.URI, "", 8)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{genesis.Collections[0].Tokens[0].ID, origin.ID, second.ReceiptNativeTokenID}, uriIDs)
	receiptCollection, err := keeper.GetCollectionByDRC(ctx, firstProof.Vault.Hex())
	require.NoError(t, err)
	require.EqualValues(t, 1, receiptCollection.Supply)
	stillOrigin, err := keeper.GetToken(ctx, origin.ID)
	require.NoError(t, err)
	require.Equal(t, origin, stillOrigin)
	stillBinding, err := keeper.GetExternalNFTBinding(ctx, origin.ID)
	require.NoError(t, err)
	require.Equal(t, originBinding, stillBinding)
	for _, id := range first.OriginSubTokenIDs {
		_, err = keeper.GetSubToken(ctx, origin.ID, id)
		require.ErrorIs(t, err, ErrNotFound)
	}
	other, err := keeper.GetIsolatedLineage(ctx, second.Vault, second.PositionID)
	require.NoError(t, err)
	require.Equal(t, second, other)

	exported, err := keeper.ExportGenesis(ctx)
	require.NoError(t, err, "all retired primary and secondary records must disappear together")
	require.Equal(t, []types.IsolatedPositionLineage{second}, exported.IsolatedPositions)
	require.Equal(t, secondProof.NextPositionID.Bytes(), exported.IsolatedVaults[0].NextPositionID)
	restoredCtx, restored, _, _, _ := gatewayIsolationFixture(t)
	require.NoError(t, restored.InitGenesis(restoredCtx, exported))
	_, err = restored.ResolveExternalNFT(restoredCtx, first.Vault, first.PositionID)
	require.ErrorIs(t, err, ErrNotFound)
	restoredOrigin, err := restored.GetExternalNFTBinding(restoredCtx, origin.ID)
	require.NoError(t, err)
	require.Equal(t, originBinding, restoredOrigin)
}

func TestRetireIsolatedDELReceiptRollsBackLateFailureAtZeroReserve(t *testing.T) {
	ctx, keeper, genesis, proof, owners := gatewayIsolationFixture(t)
	require.NoError(t, keeper.InitGenesis(ctx, genesis))
	lineage, err := keeper.AdmitGatewayIsolation(ctx, proof)
	require.NoError(t, err)
	require.NoError(t, keeper.ApplyGatewayIsolatedDELPenalty(ctx, proof.Gateway, lineage, sdkmath.ZeroInt()))
	require.NoError(t, keeper.TransferSubTokens(ctx, lineage.ReceiptNativeTokenID, []uint32{1}, owners[2], owners[0]))
	lineage, err = keeper.GetIsolatedLineage(ctx, lineage.Vault, lineage.PositionID)
	require.NoError(t, err)
	owner, err := account(owners[0])
	require.NoError(t, err)
	collection, err := keeper.GetCollectionByDRC(ctx, proof.Vault.Hex())
	require.NoError(t, err)
	creator, err := account(collection.Creator)
	require.NoError(t, err)
	broken, err := NewStateKeeperWithDomain(failReceiptRetirementService{
		KVStoreService: keeper.service, collectionKey: types.GetCollectionKey(creator, collection.Denom),
	}, bindingChain, 777)
	require.NoError(t, err)
	before, err := keeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.ErrorContains(t, broken.RetireIsolatedDELReceipt(ctx, proof.Gateway, common.BytesToAddress(owner), lineage, proof.NextPositionID), "receipt collection update failed")
	after, err := keeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	requireOwnerCount(t, ctx, keeper, lineage.ReceiptNativeTokenID, owners[0], 1)
	require.NoError(t, keeper.RetireIsolatedDELReceipt(ctx, proof.Gateway, common.BytesToAddress(owner), lineage, proof.NextPositionID))
	_, err = keeper.ExportGenesis(ctx)
	require.NoError(t, err)
}

type failReceiptRetirementService struct {
	store.KVStoreService
	collectionKey []byte
}

func (s failReceiptRetirementService) OpenKVStore(ctx context.Context) store.KVStore {
	return failReceiptRetirementStore{KVStore: s.KVStoreService.OpenKVStore(ctx), collectionKey: s.collectionKey}
}

type failReceiptRetirementStore struct {
	store.KVStore
	collectionKey []byte
}

func (s failReceiptRetirementStore) Set(key, value []byte) error {
	if bytes.Equal(key, s.collectionKey) {
		return errors.New("receipt collection update failed")
	}
	return s.KVStore.Set(key, value)
}
