package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
)

const MaxIdentityPageSize uint32 = 256

var ErrAmbiguousURI = errors.New("NFT URI refers to more than one token")

func (k *StateKeeper) checkBindingDomain(ctx sdk.Context, binding types.ExternalNFTBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if k.chainID == "" || k.evmChainID == 0 || ctx.ChainID() != k.chainID || binding.ChainID != k.chainID || binding.EVMChainID != k.evmChainID {
		return errors.New("external NFT binding differs from configured network domain")
	}
	return nil
}

// storeNewBinding writes a validated binding and its reverse index for genesis
// or an authenticated admission in a child cache.
func (k *StateKeeper) storeNewBinding(ctx sdk.Context, binding types.ExternalNFTBinding) error {
	if err := k.checkBindingDomain(ctx, binding); err != nil {
		return err
	}
	state := k.service.OpenKVStore(ctx)
	for _, key := range [][]byte{types.GetExternalBindingKey(binding.NativeTokenID), types.GetExternalIdentityKey(binding)} {
		present, err := state.Has(key)
		if err != nil {
			return err
		}
		if present {
			return errors.New("external NFT identity is already assigned")
		}
	}
	if err := k.put(ctx, types.GetExternalBindingKey(binding.NativeTokenID), &binding); err != nil {
		return err
	}
	return state.Set(types.GetExternalIdentityKey(binding), []byte(binding.NativeTokenID))
}

func (k *StateKeeper) GetExternalNFTBinding(ctx sdk.Context, nativeID string) (types.ExternalNFTBinding, error) {
	var binding types.ExternalNFTBinding
	if err := k.read(ctx, types.GetExternalBindingKey(nativeID), &binding); err != nil {
		return binding, err
	}
	if err := k.checkBindingDomain(ctx, binding); err != nil {
		return types.ExternalNFTBinding{}, err
	}
	if binding.NativeTokenID != nativeID {
		return types.ExternalNFTBinding{}, errors.New("external NFT native key mismatch")
	}
	indexed, err := k.service.OpenKVStore(ctx).Get(types.GetExternalIdentityKey(binding))
	if err != nil {
		return types.ExternalNFTBinding{}, err
	}
	if string(indexed) != nativeID {
		return types.ExternalNFTBinding{}, errors.New("external NFT reverse index mismatch")
	}
	token, err := k.GetToken(ctx, nativeID)
	if err != nil {
		return types.ExternalNFTBinding{}, err
	}
	collection, err := k.GetCollection(ctx, token.Creator, token.Denom)
	if err != nil {
		return types.ExternalNFTBinding{}, err
	}
	if err := types.ValidateBindingToken(binding, token, collection); err != nil {
		return types.ExternalNFTBinding{}, err
	}
	return binding, nil
}

func (k *StateKeeper) ResolveExternalNFT(ctx sdk.Context, contract, tokenID []byte) (types.ExternalNFTBinding, error) {
	identity := types.ExternalNFTBinding{ChainID: k.chainID, EVMChainID: k.evmChainID, Contract: contract, TokenID: tokenID, NativeTokenID: "lookup", NFTType: types.NftType_NFT1155, NextSubTokenID: 1}
	if err := k.checkBindingDomain(ctx, identity); err != nil {
		return types.ExternalNFTBinding{}, err
	}
	id, err := k.service.OpenKVStore(ctx).Get(types.GetExternalIdentityKey(identity))
	if err != nil {
		return types.ExternalNFTBinding{}, err
	}
	if id == nil {
		return types.ExternalNFTBinding{}, ErrNotFound
	}
	binding, err := k.GetExternalNFTBinding(ctx, string(id))
	if err != nil {
		return types.ExternalNFTBinding{}, err
	}
	if !bytes.Equal(binding.Contract, contract) || !bytes.Equal(binding.TokenID, tokenID) {
		return types.ExternalNFTBinding{}, errors.New("external NFT identity index mismatch")
	}
	return binding, nil
}

// ListActiveSubTokenIDs lists the authenticated native owner index in ascending
// uint32 order. after is exclusive; an empty page ends iteration. The returned
// cursor is the last ID read, and never a newly allocated or reused ID.
func (k *StateKeeper) ListActiveSubTokenIDs(ctx sdk.Context, nativeID, owner string, after, limit uint32) (ids []uint32, cursor uint32, err error) {
	if limit == 0 || limit > MaxIdentityPageSize {
		return nil, 0, errors.New("invalid NFT identity page limit")
	}
	ownerAddress, err := account(owner)
	if err != nil {
		return nil, 0, err
	}
	if _, err := k.GetToken(ctx, nativeID); err != nil {
		return nil, 0, err
	}
	prefix := types.GetSubTokensByOwnerKey(ownerAddress, nativeID)
	start := types.GetSubTokenByOwnerKey(ownerAddress, nativeID, after)
	iterator, err := k.service.OpenKVStore(ctx).Iterator(start, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return nil, 0, err
	}
	defer func() { err = errors.Join(err, iterator.Close()) }()
	for ; iterator.Valid() && uint32(len(ids)) < limit; iterator.Next() {
		if err := iterator.Error(); err != nil {
			return nil, 0, err
		}
		key := iterator.Key()
		if len(key) != len(prefix)+4 || !bytes.HasPrefix(key, prefix) || string(iterator.Value()) != nativeID {
			return nil, 0, errors.New("invalid NFT owner index record")
		}
		id := binary.BigEndian.Uint32(key[len(prefix):])
		if id == after {
			continue
		}
		sub, err := k.GetSubToken(ctx, nativeID, id)
		if err != nil {
			return nil, 0, err
		}
		actual, err := account(sub.Owner)
		if err != nil || !actual.Equals(ownerAddress) {
			return nil, 0, errors.New("NFT owner index differs from active unit")
		}
		ids, cursor = append(ids, id), id
	}
	return ids, cursor, nil
}

// ListTokenIDsByURI uses the URI multimap for external collections (including
// shared ERC1155 metadata) and legacy tokens alike. Ordering is the stored ID
// hash, not lexicographic text. afterID is the exclusive previous page cursor.
func (k *StateKeeper) ListTokenIDsByURI(ctx sdk.Context, uri, afterID string, limit uint32) (ids []string, cursor string, err error) {
	if uri == "" || len(uri) > MaxRecordBytes || limit == 0 || limit > MaxIdentityPageSize {
		return nil, "", errors.New("invalid NFT URI page request")
	}
	prefix := types.GetTokenURIMembersKey(uri)
	start := prefix
	if afterID != "" {
		start = types.GetTokenURIMemberKey(uri, afterID)
	}
	iterator, err := k.service.OpenKVStore(ctx).Iterator(start, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return nil, "", err
	}
	defer func() { err = errors.Join(err, iterator.Close()) }()
	for ; iterator.Valid() && uint32(len(ids)) < limit; iterator.Next() {
		if err := iterator.Error(); err != nil {
			return nil, "", err
		}
		id := string(iterator.Value())
		if afterID != "" && id == afterID {
			continue
		}
		if !bytes.Equal(iterator.Key(), types.GetTokenURIMemberKey(uri, id)) {
			return nil, "", fmt.Errorf("invalid NFT URI multimap index")
		}
		token, err := k.GetToken(ctx, id)
		if err != nil {
			return nil, "", err
		}
		if token.URI != uri {
			return nil, "", errors.New("NFT URI multimap differs from token")
		}
		ids, cursor = append(ids, id), id
	}
	return ids, cursor, nil
}

func (k *StateKeeper) GetTokenByURI(ctx sdk.Context, uri string) (types.Token, error) {
	ids, _, err := k.ListTokenIDsByURI(ctx, uri, "", 2)
	if err != nil {
		return types.Token{}, err
	}
	if len(ids) == 0 {
		return types.Token{}, ErrNotFound
	}
	if len(ids) > 1 {
		return types.Token{}, ErrAmbiguousURI
	}
	return k.GetToken(ctx, ids[0])
}
