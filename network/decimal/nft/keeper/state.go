// Package keeper stages transactional NFT state for the separate network.
package keeper

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"cosmossdk.io/core/store"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/compat"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

var ErrNotFound = errors.New("NFT record does not exist")

// MaxRecordBytes bounds individual metadata records, not whole collections.
// This staging limit is part of the new-network layout and must be included in
// its reviewed genesis policy before application wiring.
const MaxRecordBytes = 64 * 1024

type StateKeeper struct {
	service    store.KVStoreService
	params     compat.ParamsStore[*types.Params]
	emit       func(sdk.Context, proto.Message) error
	chainID    string
	evmChainID uint64
}

// NewStateKeeperWithDomain enables explicit external identity records for one
// network only. It does not authenticate a contract or admit an EVM receipt.
func NewStateKeeperWithDomain(service store.KVStoreService, chainID string, evmChainID uint64) (*StateKeeper, error) {
	if err := types.ValidateExternalDomain(chainID, evmChainID); err != nil {
		return nil, err
	}
	k, err := NewStateKeeper(service)
	if err != nil {
		return nil, err
	}
	k.chainID, k.evmChainID = chainID, evmChainID
	return k, nil
}

func NewStateKeeper(service store.KVStoreService) (*StateKeeper, error) {
	params, err := compat.NewParamsStore(service, []byte{1}, &types.Params{}, func(p *types.Params) error { return p.Validate() })
	if err != nil {
		return nil, err
	}
	return &StateKeeper{service: service, params: params, emit: func(ctx sdk.Context, event proto.Message) error {
		return ctx.EventManager().EmitTypedEvent(event)
	}}, nil
}

func account(value string) (sdk.AccAddress, error) {
	data, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).StringToBytes(value)
	return sdk.AccAddress(data), err
}

func (k *StateKeeper) read(ctx sdk.Context, key []byte, record proto.Message) error {
	data, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return err
	}
	if data == nil {
		return ErrNotFound
	}
	if len(data) > MaxRecordBytes {
		return errors.New("NFT record exceeds size limit")
	}
	if err := proto.Unmarshal(data, record); err != nil {
		return fmt.Errorf("decode NFT record: %w", err)
	}
	return nil
}

func (k *StateKeeper) put(ctx sdk.Context, key []byte, record proto.Message) error {
	data, err := proto.Marshal(record)
	if err != nil {
		return err
	}
	if len(data) > MaxRecordBytes {
		return errors.New("NFT record exceeds size limit")
	}
	return k.service.OpenKVStore(ctx).Set(key, data)
}

// InitGenesis imports complete, validated records in one child transaction.
// It does not move reserve funds. The application must reconcile bank backing
// before accepting this genesis or registering these methods as EVM hooks.
func (k *StateKeeper) InitGenesis(ctx sdk.Context, genesis *types.GenesisState) error {
	if err := genesis.Validate(); err != nil {
		return err
	}
	bound := make(map[string]bool, len(genesis.ExternalBindings))
	for _, binding := range genesis.ExternalBindings {
		if err := k.checkBindingDomain(ctx, binding); err != nil {
			return err
		}
		bound[binding.NativeTokenID] = true
	}
	for _, vault := range genesis.IsolatedVaults {
		if err := k.checkVaultDomain(ctx, vault); err != nil {
			return err
		}
	}
	_, err := k.params.Get(ctx)
	if err == nil {
		return errors.New("NFT genesis already initialized")
	}
	if !errors.Is(err, compat.ErrMissingParams) {
		return err
	}
	child, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	iterator, err := state.Iterator(nil, nil)
	if err != nil {
		return err
	}
	occupied := iterator.Valid()
	if occupied {
		if err := iterator.Error(); err != nil {
			_ = iterator.Close()
			return err
		}
	}
	if err := iterator.Close(); err != nil {
		return err
	}
	if occupied {
		return errors.New("NFT genesis requires an empty store")
	}
	if err := k.params.Set(child, &genesis.Params); err != nil {
		return err
	}
	for _, collection := range genesis.Collections {
		creator, err := account(collection.Creator)
		if err != nil {
			return err
		}
		metadata := collection
		metadata.Tokens = nil
		present, err := state.Has(types.GetCollectionKey(creator, collection.Denom))
		if err != nil {
			return err
		}
		if present {
			return errors.New("NFT collection key is already assigned")
		}
		if err := k.put(child, types.GetCollectionKey(creator, collection.Denom), &metadata); err != nil {
			return err
		}
		if collection.AddressDRC != "" {
			if err := state.Set(types.GetCollectionsByAddressDRCKey(strings.ToLower(collection.AddressDRC)), types.GetCollectionKey(creator, collection.Denom)); err != nil {
				return err
			}
		}
		for _, token := range collection.Tokens {
			metadata := *token
			metadata.SubTokens = nil
			if err := k.put(child, types.GetTokenKey(token.ID), &metadata); err != nil {
				return err
			}
			if !bound[token.ID] {
				uriKey := types.GetTokenURIKey(token.URI)
				used, err := state.Has(uriKey)
				if err != nil {
					return err
				}
				if used {
					return errors.New("NFT metadata URI is already assigned")
				}
				if err := state.Set(uriKey, []byte(token.ID)); err != nil {
					return err
				}
			}
			if err := state.Set(types.GetTokenURIMemberKey(token.URI, token.ID), []byte(token.ID)); err != nil {
				return err
			}
			if err := state.Set(types.GetTokenByCollectionKey(creator, collection.Denom, token.ID), []byte(token.ID)); err != nil {
				return err
			}
			for _, sub := range token.SubTokens {
				if err := k.put(child, types.GetSubTokenKey(token.ID, sub.ID), sub); err != nil {
					return err
				}
				owner, err := account(sub.Owner)
				if err != nil {
					return err
				}
				if err := state.Set(types.GetSubTokenByOwnerKey(owner, token.ID, sub.ID), []byte(token.ID)); err != nil {
					return err
				}
			}
		}
	}
	for _, binding := range genesis.ExternalBindings {
		if err := k.storeNewBinding(child, binding); err != nil {
			return err
		}
	}
	for _, vault := range genesis.IsolatedVaults {
		if err := k.storeIsolatedVault(child, vault); err != nil {
			return err
		}
	}
	for _, position := range genesis.IsolatedPositions {
		if err := k.storeIsolatedLineage(child, position); err != nil {
			return err
		}
	}
	commit()
	return nil
}

// GetToken returns scalar metadata and counters. Use individual sub-token reads
// or the future complete exporter for supply checks; this is a shallow record.
func (k *StateKeeper) GetToken(ctx sdk.Context, id string) (types.Token, error) {
	var token types.Token
	if err := k.read(ctx, types.GetTokenKey(id), &token); err != nil {
		return token, err
	}
	if token.ID != id || len(token.SubTokens) != 0 || token.Burnt > token.Minted {
		return types.Token{}, errors.New("invalid NFT token record")
	}
	if err := token.Validate(); err != nil {
		return types.Token{}, err
	}
	if token.IsolatedReceipt {
		if err := k.validateIsolatedTokenProfile(ctx, token); err != nil {
			if errors.Is(err, ErrNotFound) {
				return types.Token{}, fmt.Errorf("isolated receipt profile is incomplete: %v", err)
			}
			return types.Token{}, err
		}
	}
	return token, nil
}

func (k *StateKeeper) GetSubToken(ctx sdk.Context, id string, index uint32) (types.SubToken, error) {
	token, err := k.GetToken(ctx, id)
	if err != nil {
		return types.SubToken{}, err
	}
	return k.getIndexedSubToken(ctx, token, index)
}

func (k *StateKeeper) getIndexedSubToken(ctx sdk.Context, token types.Token, index uint32) (types.SubToken, error) {
	sub, err := k.readActiveSubToken(ctx, token, index)
	if err != nil {
		return sub, err
	}
	owner, err := account(sub.Owner)
	if err != nil {
		return types.SubToken{}, err
	}
	indexed, err := k.service.OpenKVStore(ctx).Get(types.GetSubTokenByOwnerKey(owner, token.ID, index))
	if err != nil {
		return types.SubToken{}, err
	}
	if string(indexed) != token.ID {
		return types.SubToken{}, errors.New("NFT owner index is missing or inconsistent")
	}
	return sub, nil
}

// readActiveSubToken checks the primary record. Callers must also check the
// owner index, either with a point read or against their current iterator row.
func (k *StateKeeper) readActiveSubToken(ctx sdk.Context, token types.Token, index uint32) (types.SubToken, error) {
	var sub types.SubToken
	if index == 0 || index > token.Minted {
		return sub, ErrNotFound
	}
	if err := k.read(ctx, types.GetSubTokenKey(token.ID, index), &sub); err != nil {
		return sub, err
	}
	if sub.ID != index {
		return types.SubToken{}, errors.New("NFT sub-token key mismatch")
	}
	if err := sub.Validate(); err != nil {
		return types.SubToken{}, err
	}
	if sub.Reserve != nil && sub.Reserve.Denom != token.Reserve.Denom {
		return types.SubToken{}, errors.New("NFT reserve denomination mismatch")
	}
	if token.ReserveLess && sub.Reserve != nil && !sub.Reserve.IsZero() {
		return types.SubToken{}, errors.New("reserveless NFT sub-token has nonzero reserve")
	}
	return sub, nil
}

func (k *StateKeeper) GetCollection(ctx sdk.Context, creator, denom string) (types.Collection, error) {
	var collection types.Collection
	owner, err := account(creator)
	if err != nil {
		return collection, err
	}
	if err := k.read(ctx, types.GetCollectionKey(owner, denom), &collection); err != nil {
		return collection, err
	}
	actual, err := account(collection.Creator)
	if err != nil {
		return types.Collection{}, err
	}
	if !actual.Equals(owner) || collection.Denom != denom || len(collection.Tokens) != 0 {
		return types.Collection{}, errors.New("NFT collection key mismatch")
	}
	if err := collection.Validate(); err != nil {
		return types.Collection{}, err
	}
	return collection, nil
}

// GetCollectionByDRC resolves a native collection identity only. Authentication
// of factory, implementation and immutable proxy runtime belongs to evmview.
func (k *StateKeeper) GetCollectionByDRC(ctx sdk.Context, contract string) (types.Collection, error) {
	if len(contract) != 42 || !strings.HasPrefix(contract, "0x") || !common.IsHexAddress(contract) || common.HexToAddress(contract) == (common.Address{}) {
		return types.Collection{}, errors.New("invalid NFT collection contract")
	}
	key, err := k.service.OpenKVStore(ctx).Get(types.GetCollectionsByAddressDRCKey(strings.ToLower(contract)))
	if err != nil {
		return types.Collection{}, err
	}
	if key == nil {
		return types.Collection{}, ErrNotFound
	}
	if len(key) > 128 || len(key) == 0 || key[0] != types.GetCollectionsKey()[0] {
		return types.Collection{}, errors.New("NFT collection contract index mismatch")
	}
	var record types.Collection
	if err := k.read(ctx, key, &record); err != nil {
		if errors.Is(err, ErrNotFound) {
			return types.Collection{}, errors.New("NFT collection contract index points to missing record")
		}
		return types.Collection{}, err
	}
	collection, err := k.GetCollection(ctx, record.Creator, record.Denom)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return types.Collection{}, errors.New("NFT collection contract index metadata mismatch")
		}
		return types.Collection{}, err
	}
	creator, err := account(collection.Creator)
	if err != nil {
		return types.Collection{}, err
	}
	if !bytes.Equal(key, types.GetCollectionKey(creator, collection.Denom)) || !strings.EqualFold(collection.AddressDRC, contract) {
		return types.Collection{}, errors.New("NFT collection contract index mismatch")
	}
	return collection, nil
}

func (k *StateKeeper) visit(ctx sdk.Context, prefix []byte, action func([]byte, []byte) error) (err error) {
	iterator, err := k.service.OpenKVStore(ctx).Iterator(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, iterator.Close()) }()
	for ; iterator.Valid(); iterator.Next() {
		if err := iterator.Error(); err != nil {
			return err
		}
		if err := action(iterator.Key(), iterator.Value()); err != nil {
			return err
		}
	}
	// SDK cacheMergeIterator.Error reports invalidity on ordinary exhaustion.
	// Check it while valid; use Close for the supported terminal error channel.
	return nil
}

// ExportGenesis hydrates all counters and active sub-tokens. It also checks that
// primary records and secondary indexes describe exactly the same population.
// Bank reserve backing is deliberately a separate application invariant.
func (k *StateKeeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	params, err := k.params.Get(ctx)
	if err != nil {
		return nil, err
	}
	genesis := types.NewGenesisState(*params, nil)
	bound := make(map[string]bool)
	if err := k.visit(ctx, types.GetExternalBindingsKey(), func(key, _ []byte) error {
		var record types.ExternalNFTBinding
		if err := k.read(ctx, key, &record); err != nil {
			return err
		}
		if !bytes.Equal(key, types.GetExternalBindingKey(record.NativeTokenID)) {
			return errors.New("NFT binding key mismatch")
		}
		binding, err := k.GetExternalNFTBinding(ctx, record.NativeTokenID)
		if err != nil {
			return err
		}
		bound[binding.NativeTokenID] = true
		genesis.ExternalBindings = append(genesis.ExternalBindings, binding)
		return nil
	}); err != nil {
		return nil, err
	}

	if err := k.visit(ctx, types.GetIsolatedVaultsKey(), func(key, _ []byte) error {
		if len(key) != 21 {
			return errors.New("invalid isolated vault key width")
		}
		v, err := k.GetIsolatedVaultState(ctx, key[1:])
		if err != nil {
			return err
		}
		genesis.IsolatedVaults = append(genesis.IsolatedVaults, v)
		return nil
	}); err != nil {
		return nil, err
	}
	var originUnits uint64
	if err := k.visit(ctx, types.GetIsolatedPositionsKey(), func(key, _ []byte) error {
		if len(key) != 53 {
			return errors.New("invalid isolated position key width")
		}
		p, err := k.GetIsolatedLineage(ctx, key[1:21], key[21:])
		if err != nil {
			return err
		}
		genesis.IsolatedPositions = append(genesis.IsolatedPositions, p)
		originUnits += uint64(len(p.OriginSubTokenIDs))
		return nil
	}); err != nil {
		return nil, err
	}
	var tokens, subs, contracts uint64
	err = k.visit(ctx, types.GetCollectionsKey(), func(key, _ []byte) error {
		var record types.Collection
		if err := k.read(ctx, key, &record); err != nil {
			return err
		}
		collection, err := k.GetCollection(ctx, record.Creator, record.Denom)
		if err != nil {
			return err
		}
		creator, err := account(collection.Creator)
		if err != nil {
			return err
		}
		if !bytes.Equal(key, types.GetCollectionKey(creator, collection.Denom)) {
			return errors.New("NFT collection key mismatch")
		}
		if collection.AddressDRC != "" {
			indexed, err := k.service.OpenKVStore(ctx).Get(types.GetCollectionsByAddressDRCKey(strings.ToLower(collection.AddressDRC)))
			if err != nil {
				return err
			}
			if !bytes.Equal(indexed, key) {
				return errors.New("NFT contract index mismatch")
			}
			contracts++
		}
		if err := k.visit(ctx, types.GetTokensByCollectionKey(creator, collection.Denom), func(indexKey, id []byte) error {
			token, err := k.GetToken(ctx, string(id))
			if err != nil {
				return err
			}
			if token.Creator != collection.Creator || token.Denom != collection.Denom || !bytes.Equal(indexKey, types.GetTokenByCollectionKey(creator, collection.Denom, token.ID)) {
				return errors.New("NFT collection/token index mismatch")
			}
			indexed, err := k.service.OpenKVStore(ctx).Get(types.GetTokenURIMemberKey(token.URI, token.ID))
			if err != nil {
				return err
			}
			if string(indexed) != token.ID {
				return errors.New("NFT URI index mismatch")
			}
			if !bound[token.ID] {
				indexed, err := k.service.OpenKVStore(ctx).Get(types.GetTokenURIKey(token.URI))
				if err != nil {
					return err
				}
				if string(indexed) != token.ID {
					return errors.New("NFT legacy URI index mismatch")
				}
			}
			if err := k.visit(ctx, types.GetSubTokensKey(token.ID), func(subKey, _ []byte) error {
				var record types.SubToken
				if err := k.read(ctx, subKey, &record); err != nil {
					return err
				}
				if !bytes.Equal(subKey, types.GetSubTokenKey(token.ID, record.ID)) {
					return errors.New("NFT sub-token key mismatch")
				}
				sub, err := k.GetSubToken(ctx, token.ID, record.ID)
				if err != nil {
					return err
				}
				token.SubTokens = append(token.SubTokens, &sub)
				subs++
				return nil
			}); err != nil {
				return err
			}
			collection.Tokens = append(collection.Tokens, &token)
			tokens++
			return nil
		}); err != nil {
			return err
		}
		genesis.Collections = append(genesis.Collections, collection)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, expected := range []struct {
		prefix byte
		count  uint64
	}{{0x13, contracts}, {0x21, tokens}, {0x31, subs}, {0x41, uint64(len(bound))}, {0xA1, tokens - uint64(len(bound))}, {0xA2, tokens}, {0xA3, subs}, {0xA4, uint64(len(bound))}, {0xA5, tokens}, {0x42, uint64(len(genesis.IsolatedVaults))}, {0x43, uint64(len(genesis.IsolatedPositions))}, {0xA6, uint64(len(genesis.IsolatedPositions))}, {0xA7, originUnits}} {
		var count uint64
		if err := k.visit(ctx, []byte{expected.prefix}, func(_, _ []byte) error { count++; return nil }); err != nil {
			return nil, err
		}
		if count != expected.count {
			return nil, fmt.Errorf("NFT orphan records or indexes under prefix %x", expected.prefix)
		}
	}
	if err := genesis.Validate(); err != nil {
		return nil, err
	}
	return genesis, nil
}

// TransferSubTokens is an internal state transition, not signature authority.
// Its future caller must authenticate the actual native/EVM transfer. A poisoned
// later item or event error discards all earlier owner/index writes in the batch.
func (k *StateKeeper) TransferSubTokens(ctx sdk.Context, id string, indexes []uint32, expectedOwner, recipient string) error {
	from, err := account(expectedOwner)
	if err != nil {
		return err
	}
	to, err := account(recipient)
	if err != nil {
		return err
	}
	if len(indexes) == 0 || from.Equals(to) {
		return errors.New("NFT transfer needs sub-token IDs and a different recipient")
	}
	params, err := k.params.Get(ctx)
	if err != nil {
		return err
	}
	if uint64(len(indexes)) > uint64(params.MaxTokenQuantity) {
		return errors.New("NFT batch exceeds token quantity limit")
	}
	child, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	token, err := k.GetToken(child, id)
	if err != nil {
		return err
	}
	seen := make(map[uint32]bool, len(indexes))
	for _, index := range indexes {
		if seen[index] {
			return errors.New("duplicated NFT sub-token ID")
		}
		seen[index] = true
		sub, err := k.getIndexedSubToken(child, token, index)
		if err != nil {
			return err
		}
		owner, err := account(sub.Owner)
		if err != nil {
			return err
		}
		if !owner.Equals(from) {
			return errors.New("NFT owner differs from authenticated transfer")
		}
		sub.Owner = recipient
		if err := k.put(child, types.GetSubTokenKey(id, index), &sub); err != nil {
			return err
		}
		if err := state.Delete(types.GetSubTokenByOwnerKey(from, id, index)); err != nil {
			return err
		}
		if err := state.Set(types.GetSubTokenByOwnerKey(to, id, index), []byte(id)); err != nil {
			return err
		}
	}
	if err := k.emit(child, &types.EventSendToken{Sender: expectedOwner, ID: id, Recipient: recipient, SubTokenIDs: append([]uint32(nil), indexes...)}); err != nil {
		return err
	}
	commit()
	return nil
}
