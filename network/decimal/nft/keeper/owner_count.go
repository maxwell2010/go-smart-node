package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"slices"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
)

const (
	nftOwnerCountPrefix byte = 0xA8
	nftOwnerCountMarker byte = 0x44
)

// ErrActiveSubTokenCountUnavailable identifies an older initialized store
// without the derived count index. Its unit ownership records remain usable.
var ErrActiveSubTokenCountUnavailable = errors.New("NFT owner unit count index requires initialization")

func (k *StateKeeper) hasActiveSubTokenCounts(ctx sdk.Context) (bool, error) {
	data, err := k.service.OpenKVStore(ctx).Get([]byte{nftOwnerCountMarker})
	if err != nil || data == nil {
		return false, err
	}
	if !bytes.Equal(data, []byte{1}) {
		return false, errors.New("invalid NFT owner unit count index marker")
	}
	return true, nil
}

func activeSubTokenCountKey(owner sdk.AccAddress, nativeID string) []byte {
	key := types.GetSubTokensByOwnerKey(owner, nativeID)
	key[0] = nftOwnerCountPrefix
	return key
}

func (k *StateKeeper) readActiveSubTokenCount(ctx sdk.Context, key []byte) (uint32, error) {
	data, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil || data == nil {
		return 0, err
	}
	if len(data) != 4 || binary.BigEndian.Uint32(data) == 0 {
		return 0, errors.New("invalid NFT owner unit count")
	}
	return binary.BigEndian.Uint32(data), nil
}

// GetActiveSubTokenCount returns the current native owner count using bounded
// point reads. Zero means the existing token has no units for that owner.
func (k *StateKeeper) GetActiveSubTokenCount(ctx sdk.Context, nativeID, owner string) (uint32, error) {
	indexed, err := k.hasActiveSubTokenCounts(ctx)
	if err != nil {
		return 0, err
	}
	if !indexed {
		return 0, ErrActiveSubTokenCountUnavailable
	}
	addr, err := account(owner)
	if err != nil {
		return 0, err
	}
	token, err := k.GetToken(ctx, nativeID)
	if err != nil {
		return 0, err
	}
	count, err := k.readActiveSubTokenCount(ctx, activeSubTokenCountKey(addr, nativeID))
	if err != nil {
		return 0, err
	}
	if count > token.Minted-token.Burnt {
		return 0, errors.New("NFT owner unit count exceeds the active token supply")
	}
	return count, nil
}

func (k *StateKeeper) adjustActiveSubTokenCount(ctx sdk.Context, nativeID string, owner sdk.AccAddress, delta int64, supplyBefore, supplyAfter uint32) error {
	key := activeSubTokenCountKey(owner, nativeID)
	previous, err := k.readActiveSubTokenCount(ctx, key)
	if err != nil {
		return err
	}
	next := int64(previous) + delta
	if previous > supplyBefore || next < 0 || next > int64(supplyAfter) {
		return errors.New("NFT owner unit count change differs from active token supply")
	}
	state := k.service.OpenKVStore(ctx)
	if next == 0 {
		return state.Delete(key)
	}
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], uint32(next))
	return state.Set(key, encoded[:])
}

func collectActiveSubTokenCounts(collections []types.Collection) (map[string]uint32, error) {
	counts := make(map[string]uint32)
	for _, collection := range collections {
		for _, token := range collection.Tokens {
			for _, sub := range token.SubTokens {
				owner, err := account(sub.Owner)
				if err != nil {
					return nil, err
				}
				key := activeSubTokenCountKey(owner, token.ID)
				counts[string(key)]++ // Validated genesis bounds all units by uint32 supply.
			}
		}
	}
	return counts, nil
}

func (k *StateKeeper) initActiveSubTokenCounts(ctx sdk.Context, collections []types.Collection) error {
	counts, err := collectActiveSubTokenCounts(collections)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	state := k.service.OpenKVStore(ctx)
	for _, key := range keys {
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], counts[key])
		if err := state.Set([]byte(key), encoded[:]); err != nil {
			return err
		}
	}
	return state.Set([]byte{nftOwnerCountMarker}, []byte{1})
}

// BuildActiveSubTokenCountIndex explicitly initializes the count index for an
// older store after auditing its existing primary ownership and indexes.
func (k *StateKeeper) BuildActiveSubTokenCountIndex(ctx sdk.Context) error {
	child, commit := ctx.CacheContext()
	indexed, err := k.hasActiveSubTokenCounts(child)
	if err != nil {
		return err
	}
	if indexed {
		return errors.New("NFT owner unit count index already initialized")
	}
	genesis, err := k.ExportGenesis(child)
	if err != nil {
		return err
	}
	if err := k.initActiveSubTokenCounts(child, genesis.Collections); err != nil {
		return err
	}
	commit()
	return nil
}

func (k *StateKeeper) validateActiveSubTokenCounts(ctx sdk.Context, collections []types.Collection) error {
	indexed, err := k.hasActiveSubTokenCounts(ctx)
	if err != nil {
		return err
	}
	prefix := []byte{nftOwnerCountPrefix}
	if !indexed {
		return k.visit(ctx, prefix, func(_, _ []byte) error {
			return errors.New("NFT owner unit count rows exist without the index marker")
		})
	}
	expected, err := collectActiveSubTokenCounts(collections)
	if err != nil {
		return err
	}
	if err := k.visit(ctx, prefix, func(key, data []byte) error {
		count, ok := expected[string(key)]
		if !ok || len(data) != 4 || binary.BigEndian.Uint32(data) != count {
			return errors.New("NFT owner unit count differs from active units")
		}
		delete(expected, string(key))
		return nil
	}); err != nil {
		return err
	}
	if len(expected) != 0 {
		return errors.New("missing NFT owner unit count for active units")
	}
	return nil
}
