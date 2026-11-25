package keeper

import (
	"errors"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// AdmitUserCollection is called only after application verification of the
// signed pinned-factory deployment. It grants no stake or reserve authority.
func (k *StateKeeper) AdmitUserCollection(ctx sdk.Context, collection types.Collection) error {
	if err := collection.Validate(); err != nil {
		return err
	}
	if collection.Supply != 0 || len(collection.Tokens) != 0 || collection.AddressDRC == "" {
		return errors.New("new user collection must be empty and assigned")
	}
	creator, err := account(collection.Creator)
	if err != nil {
		return err
	}
	child, write := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	for _, key := range [][]byte{types.GetCollectionKey(creator, collection.Denom), types.GetCollectionsByAddressDRCKey(strings.ToLower(collection.AddressDRC))} {
		exists, err := state.Has(key)
		if err != nil {
			return err
		}
		if exists {
			return errors.New("user collection already exists")
		}
	}
	if err = k.put(child, types.GetCollectionKey(creator, collection.Denom), &collection); err != nil {
		return err
	}
	if err = state.Set(types.GetCollectionsByAddressDRCKey(strings.ToLower(collection.AddressDRC)), types.GetCollectionKey(creator, collection.Denom)); err != nil {
		return err
	}
	write()
	return nil
}

// AdmitUserMint materializes bounded units only after EVM code, receipt,
// ownership and native backing have been authenticated by the application.
func (k *StateKeeper) AdmitUserMint(ctx sdk.Context, contract common.Address, tokenID common.Hash, uri, owner string, quantity uint32, reserve sdk.Coin) error {
	if quantity == 0 || quantity > 128 || len(uri) > 2048 {
		return errors.New("user mint exceeds bounded metadata or units")
	}
	collection, err := k.GetCollectionByDRC(ctx, contract.Hex())
	if err != nil {
		return err
	}
	addr, err := account(owner)
	if err != nil {
		return err
	}
	nativeID, err := types.FreshExternalTokenID(k.chainID, k.evmChainID, contract[:], tokenID[:])
	if err != nil {
		return err
	}
	token, err := k.GetToken(ctx, nativeID)
	fresh := errors.Is(err, ErrNotFound)
	if err != nil && !fresh {
		return err
	}
	if fresh {
		token = types.Token{Creator: collection.Creator, Denom: collection.Denom, ID: nativeID, URI: uri, Reserve: reserve, ReserveLess: collection.ReserveLess, AllowMint: true}
	}
	if token.URI != uri || !token.Reserve.IsEqual(reserve) || token.IsolatedReceipt || token.Minted > 1024-quantity {
		return errors.New("user mint conflicts with immutable metadata or bounded lifetime supply")
	}
	if collection.TypeNft == types.NftType_NFT721 && (quantity != 1 || !fresh) {
		return errors.New("ERC721 cannot remint an indexed token")
	}
	previous := token.Minted - token.Burnt
	first := token.Minted + 1
	token.Minted += quantity
	if err = token.Validate(); err != nil {
		return err
	}
	child, write := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	if err = k.put(child, types.GetTokenKey(nativeID), &token); err != nil {
		return err
	}
	if fresh {
		binding := types.ExternalNFTBinding{ChainID: k.chainID, EVMChainID: k.evmChainID, Contract: contract[:], TokenID: tokenID[:], NativeTokenID: nativeID, NFTType: collection.TypeNft, NextSubTokenID: uint64(token.Minted) + 1}
		if err = k.storeNewBinding(child, binding); err != nil {
			return err
		}
		creator, err := account(collection.Creator)
		if err != nil {
			return err
		}
		if err = state.Set(types.GetTokenByCollectionKey(creator, collection.Denom, nativeID), []byte(nativeID)); err != nil {
			return err
		}
		if err = state.Set(types.GetTokenURIMemberKey(uri, nativeID), []byte(nativeID)); err != nil {
			return err
		}
		collection.Supply++
		if err = k.put(child, types.GetCollectionKey(creator, collection.Denom), &collection); err != nil {
			return err
		}
	} else {
		binding, err := k.GetExternalNFTBinding(ctx, nativeID)
		if err != nil {
			return err
		}
		binding.NextSubTokenID = uint64(token.Minted) + 1
		if err = k.put(child, types.GetExternalBindingKey(nativeID), &binding); err != nil {
			return err
		}
	}
	for i := uint32(0); i < quantity; i++ {
		unit := types.SubToken{ID: first + i, Owner: owner}
		if err = k.put(child, types.GetSubTokenKey(nativeID, unit.ID), &unit); err != nil {
			return err
		}
		if err = state.Set(types.GetSubTokenByOwnerKey(addr, nativeID, unit.ID), []byte(nativeID)); err != nil {
			return err
		}
	}
	if err = k.adjustActiveSubTokenCount(child, nativeID, addr, int64(quantity), previous, previous+quantity); err != nil {
		return err
	}
	write()
	return nil
}

// RetireUserNFTUnits projects a proved wallet refund. The lifetime counters,
// URI and binding remain so retired serial numbers cannot reappear.
func (k *StateKeeper) RetireUserNFTUnits(ctx sdk.Context, nativeID string, owner sdk.AccAddress, quantity uint32) ([]uint32, error) {
	token, err := k.GetToken(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	if token.ReserveLess || token.Reserve.Denom != identity.BaseDenom {
		return nil, errors.New("invalid refundable native NFT units")
	}
	return k.retireUserNFTUnits(ctx, token, owner, quantity)
}

func (k *StateKeeper) retireUserNFTUnits(ctx sdk.Context, token types.Token, owner sdk.AccAddress, quantity uint32) ([]uint32, error) {
	before := token.Minted - token.Burnt
	if quantity == 0 || quantity > 128 || quantity > before || token.IsolatedReceipt {
		return nil, errors.New("invalid native NFT retirement")
	}
	nativeID := token.ID
	child, commit := ctx.CacheContext()
	indexed, err := k.hasActiveSubTokenCounts(child)
	if err != nil {
		return nil, err
	}
	if !indexed {
		return nil, ErrActiveSubTokenCountUnavailable
	}
	ids, err := k.selectUniformOriginUnits(child, token, owner, quantity)
	if err != nil {
		return nil, err
	}
	state := k.service.OpenKVStore(child)
	for _, id := range ids {
		if err := state.Delete(types.GetSubTokenKey(nativeID, id)); err != nil {
			return nil, err
		}
		if err := state.Delete(types.GetSubTokenByOwnerKey(owner, nativeID, id)); err != nil {
			return nil, err
		}
	}
	token.Burnt += quantity
	if err := k.put(child, types.GetTokenKey(nativeID), &token); err != nil {
		return nil, err
	}
	if err := k.adjustActiveSubTokenCount(child, nativeID, owner, -int64(quantity), before, before-quantity); err != nil {
		return nil, err
	}
	commit()
	return ids, nil
}
