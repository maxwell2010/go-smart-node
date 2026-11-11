package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/big"
	"strconv"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// GatewayIsolation is the authenticated result of one signed DEL-backed
// ERC1155 isolation into Gateway custody.
type GatewayIsolation struct {
	Vault, Origin, Owner, Gateway             common.Address
	PositionID, OriginTokenID, NextPositionID common.Hash
	Quantity, OriginSupplyAfter               uint32
	Reserve, OriginReserveAfter               sdkmath.Int
}

// AdmitGatewayIsolation replaces the selected native origin units with one
// vault receipt. The caller authenticates the receipt and actual EVM backing
// and commits this projection with the validator changes in the same cache.
func (k *StateKeeper) AdmitGatewayIsolation(ctx sdk.Context, proof GatewayIsolation) (types.IsolatedPositionLineage, error) {
	var empty types.IsolatedPositionLineage
	if proof.Vault == (common.Address{}) || proof.Origin == (common.Address{}) || proof.Vault == proof.Origin ||
		proof.Gateway == (common.Address{}) || proof.Owner == (common.Address{}) || proof.Owner == proof.Gateway ||
		proof.Gateway == proof.Vault || proof.Gateway == proof.Origin ||
		proof.PositionID == (common.Hash{}) || proof.Quantity == 0 || proof.Quantity > types.MaxIsolatedOriginUnits ||
		proof.Reserve.IsNil() || !proof.Reserve.IsPositive() || proof.OriginReserveAfter.IsNil() || proof.OriginReserveAfter.IsNegative() {
		return empty, errors.New("invalid Gateway isolation identity, quantity or DEL reserve")
	}
	nextID := proof.PositionID.Big()
	if nextID.Add(nextID, big.NewInt(1)).Cmp(proof.NextPositionID.Big()) != 0 {
		return empty, errors.New("Gateway isolation does not advance the vault cursor by one")
	}
	child, commit := ctx.CacheContext()
	indexed, err := k.hasActiveSubTokenCounts(child)
	if err != nil {
		return empty, err
	}
	vault, err := k.GetIsolatedVaultState(child, proof.Vault[:])
	if err != nil {
		return empty, err
	}
	if !bytes.Equal(vault.NextPositionID, proof.PositionID[:]) {
		return empty, errors.New("Gateway isolation does not match the native vault cursor")
	}
	collection, err := k.GetCollectionByDRC(child, proof.Vault.Hex())
	if err != nil {
		return empty, err
	}
	if collection.Creator != vault.ReceiptCreator || collection.Denom != vault.ReceiptDenom {
		return empty, errors.New("Gateway isolation vault collection index differs")
	}
	params, err := k.params.Get(child)
	if err != nil {
		return empty, err
	}
	if collection.Supply >= params.MaxCollectionSize || proof.Quantity > params.MaxTokenQuantity {
		return empty, errors.New("Gateway isolation exceeds native NFT limits")
	}

	originBinding, err := k.ResolveExternalNFT(child, proof.Origin[:], proof.OriginTokenID[:])
	if err != nil {
		return empty, err
	}
	origin, err := k.GetToken(child, originBinding.NativeTokenID)
	if err != nil {
		return empty, err
	}
	supply := origin.Minted - origin.Burnt
	if originBinding.NFTType != types.NftType_NFT1155 || origin.IsolatedReceipt || origin.ReserveLess ||
		origin.Reserve.Denom != identity.BaseDenom || proof.Quantity > supply ||
		proof.OriginSupplyAfter != supply-proof.Quantity ||
		!matchesIsolatedDELReserve(origin.Reserve.Amount, proof.Quantity, proof.Reserve) ||
		!matchesIsolatedDELReserve(origin.Reserve.Amount, proof.OriginSupplyAfter, proof.OriginReserveAfter) {
		return empty, errors.New("Gateway isolation differs from the native uniform DEL reserve or supply")
	}
	owner := sdk.AccAddress(proof.Owner[:])
	units, err := k.selectIsolationOriginUnits(child, origin, owner, proof.Quantity)
	if err != nil {
		return empty, err
	}
	receiptID, err := types.FreshExternalTokenID(vault.ChainID, vault.EVMChainID, vault.Vault, proof.PositionID[:])
	if err != nil {
		return empty, err
	}
	gateway := sdk.AccAddress(proof.Gateway[:])
	gatewayOwner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(gateway)
	if err != nil {
		return empty, err
	}
	receipt := types.Token{
		ID: receiptID, Creator: collection.Creator, Denom: collection.Denom, URI: origin.URI,
		Reserve: sdk.NewCoin(identity.BaseDenom, proof.Reserve), Minted: 1, IsolatedReceipt: true,
	}
	if err := receipt.Validate(); err != nil {
		return empty, err
	}
	receiptUnit := types.SubToken{ID: 1, Owner: gatewayOwner}
	receiptBinding := types.ExternalNFTBinding{
		ChainID: vault.ChainID, EVMChainID: vault.EVMChainID, Contract: vault.Vault, TokenID: proof.PositionID.Bytes(),
		NativeTokenID: receiptID, NFTType: types.NftType_NFT721, NextSubTokenID: 2,
	}
	if err := types.ValidateBindingToken(receiptBinding, receipt, collection); err != nil {
		return empty, err
	}
	lineage := types.IsolatedPositionLineage{
		Vault: vault.Vault, PositionID: proof.PositionID.Bytes(), ParentPositionID: make([]byte, common.HashLength),
		OriginContract: proof.Origin.Bytes(), OriginTokenID: proof.OriginTokenID.Bytes(),
		OriginNativeTokenID: origin.ID, OriginSubTokenIDs: units,
		ReceiptNativeTokenID: receiptID, ReceiptSubTokenID: receiptUnit.ID,
		Reserve: types.IsolatedReserveSnapshot{
			Kind: types.IsolatedReserveDEL, Token: make([]byte, common.AddressLength),
			Amount: common.BigToHash(proof.Reserve.BigInt()).Bytes(),
		},
	}

	creator, err := account(collection.Creator)
	if err != nil {
		return empty, err
	}
	tokenKey := types.GetTokenKey(receiptID)
	unitKey := types.GetSubTokenKey(receiptID, receiptUnit.ID)
	ownerKey := types.GetSubTokenByOwnerKey(gateway, receiptID, receiptUnit.ID)
	collectionIndex := types.GetTokenByCollectionKey(creator, collection.Denom, receiptID)
	uriIndex := types.GetTokenURIMemberKey(receipt.URI, receiptID)
	state := k.service.OpenKVStore(child)
	for _, key := range [][]byte{tokenKey, unitKey, ownerKey, collectionIndex, uriIndex,
		types.GetIsolatedPositionKey(vault.Vault, lineage.PositionID), types.GetIsolatedReceiptIndexKey(receiptID)} {
		present, err := state.Has(key)
		if err != nil {
			return empty, err
		}
		if present {
			return empty, errors.New("Gateway isolation receipt identity or index already exists")
		}
	}
	if err := k.storeNewBinding(child, receiptBinding); err != nil {
		return empty, err
	}
	for _, id := range units {
		if err := state.Delete(types.GetSubTokenKey(origin.ID, id)); err != nil {
			return empty, err
		}
		if err := state.Delete(types.GetSubTokenByOwnerKey(owner, origin.ID, id)); err != nil {
			return empty, err
		}
	}
	origin.Burnt += proof.Quantity
	if err := k.put(child, types.GetTokenKey(origin.ID), &origin); err != nil {
		return empty, err
	}
	if err := k.put(child, tokenKey, &receipt); err != nil {
		return empty, err
	}
	if err := k.put(child, unitKey, &receiptUnit); err != nil {
		return empty, err
	}
	for _, key := range [][]byte{ownerKey, collectionIndex, uriIndex} {
		if err := state.Set(key, []byte(receiptID)); err != nil {
			return empty, err
		}
	}
	collection.Supply++
	if err := k.put(child, types.GetCollectionKey(creator, collection.Denom), &collection); err != nil {
		return empty, err
	}
	if err := k.storeIsolatedLineage(child, lineage); err != nil {
		return empty, err
	}
	vault.NextPositionID = proof.NextPositionID.Bytes()
	if err := k.storeIsolatedVault(child, vault); err != nil {
		return empty, err
	}
	if indexed {
		if err := k.adjustActiveSubTokenCount(child, origin.ID, owner, -int64(proof.Quantity), supply, proof.OriginSupplyAfter); err != nil {
			return empty, err
		}
		if err := k.adjustActiveSubTokenCount(child, receiptID, gateway, 1, 0, 1); err != nil {
			return empty, err
		}
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_nft_isolated",
		sdk.NewAttribute("position_id", proof.PositionID.Hex()),
		sdk.NewAttribute("origin_token_id", origin.ID), sdk.NewAttribute("receipt_token_id", receiptID),
		sdk.NewAttribute("quantity", strconv.FormatUint(uint64(proof.Quantity), 10)),
		sdk.NewAttribute("reserve", proof.Reserve.String())))
	commit()
	return lineage, nil
}

func matchesIsolatedDELReserve(perUnit sdkmath.Int, quantity uint32, amount sdkmath.Int) bool {
	expected := new(big.Int).Mul(perUnit.BigInt(), new(big.Int).SetUint64(uint64(quantity)))
	return expected.Cmp(amount.BigInt()) == 0
}

// The owner index orders IDs numerically. Read it once and validate each
// selected primary and archive key without decoding token metadata per unit.
func (k *StateKeeper) selectIsolationOriginUnits(ctx sdk.Context, token types.Token, owner sdk.AccAddress, quantity uint32) (units []uint32, err error) {
	state := k.service.OpenKVStore(ctx)
	prefix := types.GetSubTokensByOwnerKey(owner, token.ID)
	iterator, err := state.Iterator(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, iterator.Close()) }()
	units = make([]uint32, 0, quantity)
	for ; iterator.Valid(); iterator.Next() {
		if err := iterator.Error(); err != nil {
			return nil, err
		}
		key := iterator.Key()
		if len(key) != len(prefix)+4 || !bytes.HasPrefix(key, prefix) || string(iterator.Value()) != token.ID {
			return nil, errors.New("invalid Gateway isolation origin owner index")
		}
		id := binary.BigEndian.Uint32(key[len(prefix):])
		if id == 0 || id > token.Minted {
			return nil, errors.New("Gateway isolation origin unit exceeds its lifetime cursor")
		}
		var unit types.SubToken
		if err := k.read(ctx, types.GetSubTokenKey(token.ID, id), &unit); err != nil {
			return nil, err
		}
		actualOwner, err := account(unit.Owner)
		if err != nil {
			return nil, err
		}
		if unit.ID != id || !actualOwner.Equals(owner) {
			return nil, errors.New("Gateway isolation origin unit differs from the owner index")
		}
		if unit.Reserve != nil && (unit.Reserve.Denom != token.Reserve.Denom || unit.Reserve.Amount.IsNil() ||
			!unit.Reserve.Amount.Equal(token.Reserve.Amount)) {
			return nil, errors.New("Gateway isolation requires a uniform native DEL reserve on each origin unit")
		}
		archived, err := state.Has(types.GetIsolatedOriginUnitKey(token.ID, id))
		if err != nil {
			return nil, err
		}
		if archived {
			return nil, errors.New("Gateway isolation origin unit is already archived")
		}
		units = append(units, id)
		if uint32(len(units)) == quantity {
			return units, nil
		}
	}
	return nil, errors.New("Gateway isolation owner has insufficient native origin units")
}
