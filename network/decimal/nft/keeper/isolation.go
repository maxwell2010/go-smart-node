package keeper

import (
	"bytes"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
)

func (k *StateKeeper) checkVaultDomain(ctx sdk.Context, v types.IsolatedVaultState) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if k.chainID == "" || ctx.ChainID() != k.chainID || v.ChainID != k.chainID || v.EVMChainID != k.evmChainID {
		return errors.New("isolated vault differs from configured network domain")
	}
	return nil
}

// GetIsolatedVaultState returns detached current-state metadata. Presence is not
// runtime authentication; only a reviewed bootstrap/admission builder may create it.
func (k *StateKeeper) GetIsolatedVaultState(ctx sdk.Context, vault []byte) (types.IsolatedVaultState, error) {
	var result types.IsolatedVaultState
	if len(vault) != common.AddressLength {
		return result, errors.New("invalid isolated vault lookup")
	}
	if err := k.read(ctx, types.GetIsolatedVaultKey(vault), &result); err != nil {
		return result, err
	}
	if !bytes.Equal(result.Vault, vault) {
		return types.IsolatedVaultState{}, errors.New("isolated vault key mismatch")
	}
	if err := k.checkVaultDomain(ctx, result); err != nil {
		return types.IsolatedVaultState{}, err
	}
	c, err := k.GetCollection(ctx, result.ReceiptCreator, result.ReceiptDenom)
	if err != nil {
		return types.IsolatedVaultState{}, err
	}
	if c.TypeNft != types.NftType_NFT721 || c.ReserveLess || !bytes.Equal(common.HexToAddress(c.AddressDRC).Bytes(), vault) {
		return types.IsolatedVaultState{}, errors.New("isolated vault receipt collection mismatch")
	}
	return result, nil
}

// GetIsolatedLineage resolves exact archived origin units independently of stake
// bindings, so transfers to a wallet and deletion of a closed parent do not lose
// the surviving receipt's origin. Work is bounded by MaxIsolatedOriginUnits.
func (k *StateKeeper) GetIsolatedLineage(ctx sdk.Context, vault, id []byte) (types.IsolatedPositionLineage, error) {
	var p types.IsolatedPositionLineage
	if len(vault) != common.AddressLength || len(id) != 32 {
		return p, errors.New("invalid isolated position lookup")
	}
	key := types.GetIsolatedPositionKey(vault, id)
	if err := k.read(ctx, key, &p); err != nil {
		return p, err
	}
	if err := p.Validate(); err != nil {
		return types.IsolatedPositionLineage{}, err
	}
	if !bytes.Equal(p.Vault, vault) || !bytes.Equal(p.PositionID, id) {
		return types.IsolatedPositionLineage{}, errors.New("isolated lineage key mismatch")
	}
	v, err := k.GetIsolatedVaultState(ctx, vault)
	if err != nil {
		return types.IsolatedPositionLineage{}, err
	}
	if bytes.Compare(id, v.NextPositionID) >= 0 {
		return types.IsolatedPositionLineage{}, errors.New("isolated position exceeds vault high-water mark")
	}
	receipt, err := k.GetExternalNFTBinding(ctx, p.ReceiptNativeTokenID)
	if err != nil {
		return types.IsolatedPositionLineage{}, err
	}
	if receipt.NFTType != types.NftType_NFT721 || !bytes.Equal(receipt.Contract, vault) || !bytes.Equal(receipt.TokenID, id) {
		return types.IsolatedPositionLineage{}, errors.New("isolated receipt binding mismatch")
	}
	receiptToken, err := k.GetToken(ctx, p.ReceiptNativeTokenID)
	if err != nil {
		return types.IsolatedPositionLineage{}, err
	}
	if receiptToken.Creator != v.ReceiptCreator || receiptToken.Denom != v.ReceiptDenom || receiptToken.ReserveLess || !receiptToken.IsolatedReceipt || receiptToken.Minted-receiptToken.Burnt != 1 {
		return types.IsolatedPositionLineage{}, errors.New("isolated receipt is not one active backed unit")
	}
	if _, err := k.GetSubToken(ctx, p.ReceiptNativeTokenID, p.ReceiptSubTokenID); err != nil {
		return types.IsolatedPositionLineage{}, err
	}
	origin, err := k.GetExternalNFTBinding(ctx, p.OriginNativeTokenID)
	if err != nil {
		return types.IsolatedPositionLineage{}, err
	}
	if origin.NFTType != types.NftType_NFT1155 || !bytes.Equal(origin.Contract, p.OriginContract) || !bytes.Equal(origin.TokenID, p.OriginTokenID) {
		return types.IsolatedPositionLineage{}, errors.New("isolated origin binding mismatch")
	}
	originToken, err := k.GetToken(ctx, p.OriginNativeTokenID)
	if err != nil {
		return types.IsolatedPositionLineage{}, err
	}
	if originToken.ReserveLess {
		return types.IsolatedPositionLineage{}, errors.New("isolated origin cannot be reserve-less")
	}
	state := k.service.OpenKVStore(ctx)
	indexed, err := state.Get(types.GetIsolatedReceiptIndexKey(p.ReceiptNativeTokenID))
	if err != nil {
		return types.IsolatedPositionLineage{}, err
	}
	if !bytes.Equal(indexed, key) {
		return types.IsolatedPositionLineage{}, errors.New("isolated receipt reverse index mismatch")
	}
	for _, unit := range p.OriginSubTokenIDs {
		if unit > originToken.Minted {
			return types.IsolatedPositionLineage{}, errors.New("isolated origin unit exceeds lifetime cursor")
		}
		// The origin token was validated above. Archived units must have no
		// primary record, so a lookup needs neither metadata nor decoding.
		active, err := state.Has(types.GetSubTokenKey(p.OriginNativeTokenID, unit))
		if err != nil {
			return types.IsolatedPositionLineage{}, err
		}
		if active {
			return types.IsolatedPositionLineage{}, errors.New("isolated origin unit remains active")
		}
		indexed, err = state.Get(types.GetIsolatedOriginUnitKey(p.OriginNativeTokenID, unit))
		if err != nil {
			return types.IsolatedPositionLineage{}, err
		}
		if !bytes.Equal(indexed, key) {
			return types.IsolatedPositionLineage{}, errors.New("isolated origin reverse index mismatch")
		}
	}
	return p, nil
}

// These writers are internal to validated genesis and authenticated admission.
// A production event alone is never permission to admit a new receipt.
func (k *StateKeeper) storeIsolatedVault(ctx sdk.Context, v types.IsolatedVaultState) error {
	if err := k.checkVaultDomain(ctx, v); err != nil {
		return err
	}
	return k.put(ctx, types.GetIsolatedVaultKey(v.Vault), &v)
}
func (k *StateKeeper) storeIsolatedLineage(ctx sdk.Context, p types.IsolatedPositionLineage) error {
	if err := p.Validate(); err != nil {
		return err
	}
	key := types.GetIsolatedPositionKey(p.Vault, p.PositionID)
	if err := k.put(ctx, key, &p); err != nil {
		return err
	}
	state := k.service.OpenKVStore(ctx)
	if err := state.Set(types.GetIsolatedReceiptIndexKey(p.ReceiptNativeTokenID), key); err != nil {
		return err
	}
	for _, unit := range p.OriginSubTokenIDs {
		if err := state.Set(types.GetIsolatedOriginUnitKey(p.OriginNativeTokenID, unit), key); err != nil {
			return err
		}
	}
	return nil
}

// validateIsolatedTokenProfile avoids GetToken -> GetIsolatedLineage -> GetToken
// recursion. It checks bounded raw identity/profile records; the full lineage
// reader separately audits every archived origin-unit index.
func (k *StateKeeper) validateIsolatedTokenProfile(ctx sdk.Context, token types.Token) error {
	collection, err := k.GetCollection(ctx, token.Creator, token.Denom)
	if err != nil {
		return err
	}
	if collection.TypeNft != types.NftType_NFT721 || collection.ReserveLess {
		return errors.New("isolated receipt token requires a backed ERC721 collection")
	}
	vault := common.HexToAddress(collection.AddressDRC).Bytes()
	state, err := k.GetIsolatedVaultState(ctx, vault)
	if err != nil {
		return err
	}
	if state.ReceiptCreator != token.Creator || state.ReceiptDenom != token.Denom {
		return errors.New("isolated receipt metadata differs from known vault")
	}
	indexed, err := k.service.OpenKVStore(ctx).Get(types.GetIsolatedReceiptIndexKey(token.ID))
	if err != nil {
		return err
	}
	if len(indexed) != 53 || indexed[0] != 0x43 || !bytes.Equal(indexed[1:21], vault) {
		return errors.New("isolated receipt flag has no valid lineage index")
	}
	var p types.IsolatedPositionLineage
	if err := k.read(ctx, indexed, &p); err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if p.ReceiptNativeTokenID != token.ID || !bytes.Equal(p.Vault, vault) || !bytes.Equal(p.PositionID, indexed[21:]) || bytes.Compare(p.PositionID, state.NextPositionID) >= 0 || token.Minted-token.Burnt != 1 {
		return errors.New("isolated receipt flag differs from active lineage")
	}
	var binding types.ExternalNFTBinding
	if err := k.read(ctx, types.GetExternalBindingKey(token.ID), &binding); err != nil {
		return err
	}
	if err := k.checkBindingDomain(ctx, binding); err != nil {
		return err
	}
	if err := types.ValidateBindingToken(binding, token, collection); err != nil {
		return err
	}
	if !bytes.Equal(binding.Contract, vault) || !bytes.Equal(binding.TokenID, p.PositionID) {
		return errors.New("isolated receipt external identity differs from lineage")
	}
	return nil
}
