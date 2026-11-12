package keeper

import (
	"bytes"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// RetireIsolatedDELReceipt projects an authenticated cash redemption of a
// wallet-held receipt. The caller verifies the EVM burn and refund before
// committing this projection together with the EVM and bank state.
func (k *StateKeeper) RetireIsolatedDELReceipt(ctx sdk.Context, gateway, owner common.Address,
	expected types.IsolatedPositionLineage, nextPositionID common.Hash,
) error {
	if gateway == (common.Address{}) || owner == (common.Address{}) || owner == gateway ||
		bytes.Equal(owner[:], expected.Vault) || nextPositionID == (common.Hash{}) || expected.Reserve.Kind != types.IsolatedReserveDEL {
		return errors.New("isolated DEL redemption requires a wallet-held DEL receipt")
	}
	child, commit := ctx.CacheContext()
	lineage, err := k.GetIsolatedLineage(child, expected.Vault, expected.PositionID)
	if err != nil {
		return err
	}
	if !lineage.Equal(&expected) {
		return errors.New("isolated DEL redemption differs from the current native lineage")
	}
	vault, err := k.GetIsolatedVaultState(child, lineage.Vault)
	if err != nil {
		return err
	}
	if !bytes.Equal(vault.NextPositionID, nextPositionID[:]) {
		return errors.New("isolated DEL redemption changed the native vault cursor")
	}
	token, err := k.GetToken(child, lineage.ReceiptNativeTokenID)
	if err != nil {
		return err
	}
	unit, err := k.getIndexedSubToken(child, token, lineage.ReceiptSubTokenID)
	if err != nil {
		return err
	}
	actualOwner, err := account(unit.Owner)
	if err != nil {
		return err
	}
	if !bytes.Equal(actualOwner, owner[:]) {
		return errors.New("isolated DEL redemption differs from the native wallet owner")
	}
	if token.Reserve.Denom != identity.BaseDenom || token.Reserve.Amount.BigInt().Cmp(common.BytesToHash(lineage.Reserve.Amount).Big()) != 0 ||
		(unit.Reserve != nil && !unit.Reserve.IsEqual(token.Reserve)) {
		return errors.New("isolated DEL redemption differs from the current native receipt reserve")
	}
	collection, err := k.GetCollection(child, token.Creator, token.Denom)
	if err != nil {
		return err
	}
	if collection.Supply == 0 {
		return errors.New("isolated DEL redemption would underflow the receipt collection supply")
	}
	creator, err := account(collection.Creator)
	if err != nil {
		return err
	}
	state := k.service.OpenKVStore(child)
	collectionIndex := types.GetTokenByCollectionKey(creator, collection.Denom, token.ID)
	uriIndex := types.GetTokenURIMemberKey(token.URI, token.ID)
	for _, key := range [][]byte{collectionIndex, uriIndex} {
		indexed, err := state.Get(key)
		if err != nil {
			return err
		}
		if string(indexed) != token.ID {
			return errors.New("isolated DEL redemption has an inconsistent receipt index")
		}
	}
	indexed, err := k.hasActiveSubTokenCounts(child)
	if err != nil {
		return err
	}
	if indexed {
		if err := k.adjustActiveSubTokenCount(child, token.ID, actualOwner, -1, 1, 0); err != nil {
			return err
		}
	}
	external := types.ExternalNFTBinding{ChainID: vault.ChainID, EVMChainID: vault.EVMChainID, Contract: lineage.Vault, TokenID: lineage.PositionID}
	for _, key := range [][]byte{
		types.GetTokenKey(token.ID), types.GetSubTokenKey(token.ID, unit.ID), types.GetSubTokenByOwnerKey(actualOwner, token.ID, unit.ID),
		collectionIndex, uriIndex, types.GetExternalBindingKey(token.ID), types.GetExternalIdentityKey(external),
		types.GetIsolatedPositionKey(lineage.Vault, lineage.PositionID), types.GetIsolatedReceiptIndexKey(token.ID),
	} {
		if err := state.Delete(key); err != nil {
			return err
		}
	}
	// The consumed origin IDs remain burned under the origin's lifetime
	// counter. Only their reverse pointers to this live receipt are retired.
	for _, id := range lineage.OriginSubTokenIDs {
		if err := state.Delete(types.GetIsolatedOriginUnitKey(lineage.OriginNativeTokenID, id)); err != nil {
			return err
		}
	}
	collection.Supply--
	if err := k.put(child, types.GetCollectionKey(creator, collection.Denom), &collection); err != nil {
		return err
	}
	commit()
	return nil
}
