package keeper

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const maxBindingCaptureBytes = 4096

func bindingFrame(out, value []byte) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(value)))
	out = append(out, n[:]...)
	return append(out, value...)
}
func (k *StateKeeper) bindingCaptureScope(ctx sdk.Context) ([32]byte, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return [32]byte{}, err
	}
	domain, err := proto.Marshal(k.bindingDomain)
	if err != nil {
		return [32]byte{}, err
	}
	header := ctx.BlockHeader()
	encoded, err := proto.Marshal(&header)
	if err != nil {
		return [32]byte{}, err
	}
	if len(encoded) > 8192 {
		return [32]byte{}, errors.New("binding capture header exceeds its bound")
	}
	out := bindingFrame([]byte("native-binding-capture-scope-v1"), domain)
	out = bindingFrame(out, encoded)
	var height [8]byte
	binary.BigEndian.PutUint64(height[:], uint64(ctx.BlockHeight()))
	out = append(out, height[:]...)
	out = bindingFrame(out, []byte(ctx.ChainID()))
	out = bindingFrame(out, []byte(ctx.BlockTime().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")))
	tx := sha256.Sum256(ctx.TxBytes())
	out = append(out, tx[:]...)
	return sha256.Sum256(out), nil
}
func validBindingRawKey(key []byte) bool {
	if len(key) == 54 && key[0] == stakeBindingPrefix {
		_, err := types.EVMStakeBindingKey(types.EVMStakeBindingKind(key[1]), key[2:22], key[22:])
		return err == nil
	}
	if len(key) == 53 && key[0] == frozenBindingPrefix {
		_, err := types.EVMFrozenBindingKey(key[1:21], key[21:])
		return err == nil
	}
	return false
}
func (k *StateKeeper) bindingCaptureDigest(ctx sdk.Context, prefix []byte, keys [][]byte) ([32]byte, error) {
	out := bindingFrame([]byte("native-binding-capture-data-v1"), prefix)
	indexes := map[string][]byte{}
	for _, key := range keys {
		out = bindingFrame(out, key)
		row, err := k.readBindingRow(ctx, key)
		if errors.Is(err, ErrNotFound) {
			out = bindingFrame(out, nil)
			continue
		}
		if err != nil {
			return [32]byte{}, err
		}
		if err = k.checkBindingIndexes(ctx, row); err != nil {
			return [32]byte{}, err
		}
		out = bindingFrame(out, row.data)
		claims, err := k.bindingClaimKeys(ctx, row)
		if err != nil {
			return [32]byte{}, err
		}
		for _, claim := range claims {
			if _, ok := indexes[string(claim)]; !ok {
				value, err := k.service.OpenKVStore(ctx).Get(claim)
				if err != nil {
					return [32]byte{}, err
				}
				indexes[string(claim)] = bytes.Clone(value)
			}
		}
	}
	for _, key := range sortedBindingKeys(indexes) {
		out = bindingFrame(out, []byte(key))
		out = bindingFrame(out, indexes[key])
	}
	return sha256.Sum256(out), nil
}

// CaptureBindingState returns a bounded prestate token for the shared receipt
// planner. It authenticates metadata freshness, never source EVM execution or
// asset ownership; the controller and NFT planner verify those independently.
func (k *StateKeeper) CaptureBindingState(ctx sdk.Context, references []BindingReference) ([]byte, error) {
	if len(references) > types.MaxBindingBatchSize {
		return nil, errors.New("binding capture exceeds64 source rows")
	}
	scope, err := k.bindingCaptureScope(ctx)
	if err != nil {
		return nil, err
	}
	revision, err := k.BindingRevision(ctx)
	if err != nil {
		return nil, err
	}
	var keys [][]byte
	seen := map[string]bool{}
	for _, reference := range references {
		key, err := reference.key()
		if err != nil {
			return nil, err
		}
		if seen[string(key)] {
			return nil, errors.New("duplicate binding capture reference")
		}
		seen[string(key)] = true
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return bytes.Compare(keys[i], keys[j]) < 0 })
	out := []byte{1}
	out = append(out, scope[:]...)
	var rev [8]byte
	binary.BigEndian.PutUint64(rev[:], revision)
	out = append(out, rev[:]...)
	var count [2]byte
	binary.BigEndian.PutUint16(count[:], uint16(len(keys)))
	out = append(out, count[:]...)
	for _, key := range keys {
		out = append(out, byte(len(key)))
		out = append(out, key...)
	}
	sum, err := k.bindingCaptureDigest(ctx, out, keys)
	if err != nil {
		return nil, err
	}
	out = append(out, sum[:]...)
	if len(out) > maxBindingCaptureBytes {
		return nil, errors.New("binding capture exceeds byte bound")
	}
	return out, nil
}
func (k *StateKeeper) ValidateBindingCapture(ctx sdk.Context, capture []byte) error {
	if len(capture) < 75 || len(capture) > maxBindingCaptureBytes || capture[0] != 1 {
		return errors.New("invalid binding capture encoding")
	}
	scope, err := k.bindingCaptureScope(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(capture[1:33], scope[:]) {
		return errors.New("binding capture belongs to another execution context")
	}
	revision, err := k.BindingRevision(ctx)
	if err != nil {
		return err
	}
	if binary.BigEndian.Uint64(capture[33:41]) != revision {
		return ErrStaleBindingPlan
	}
	count := int(binary.BigEndian.Uint16(capture[41:43]))
	if count > types.MaxBindingBatchSize {
		return errors.New("binding capture exceeds reference bound")
	}
	cursor := 43
	var keys [][]byte
	for i := 0; i < count; i++ {
		if cursor >= len(capture)-32 {
			return errors.New("truncated binding capture")
		}
		length := int(capture[cursor])
		cursor++
		if length > len(capture)-32-cursor {
			return errors.New("truncated binding capture reference")
		}
		key := bytes.Clone(capture[cursor : cursor+length])
		cursor += length
		if !validBindingRawKey(key) || (len(keys) > 0 && bytes.Compare(keys[len(keys)-1], key) >= 0) {
			return errors.New("invalid or noncanonical binding capture references")
		}
		keys = append(keys, key)
	}
	if cursor != len(capture)-32 {
		return errors.New("binding capture has trailing data")
	}
	sum, err := k.bindingCaptureDigest(ctx, capture[:cursor], keys)
	if err != nil {
		return err
	}
	if !bytes.Equal(capture[cursor:], sum[:]) {
		return ErrStaleBindingPlan
	}
	return nil
}
