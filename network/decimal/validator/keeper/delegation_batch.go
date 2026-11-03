package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const (
	delegatedNFTPrefix        byte = 0x23
	validatorDelegationPrefix byte = 0x24
	delegationCountPrefix     byte = 0x25
	MaxDelegationBatch             = 256
	maxDelegationBatchBytes        = 8 << 20
)

var delegationIndexMarker = []byte{6}

func sortedDelegationKeys[V any](rows map[string]V) []string {
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// DelegationChange describes a compare-and-swap of structural native records.
// It performs no bank transfer, price calculation or signature authorization.
// A nil Expected inserts, a nil Next removes; a reassignment may change the key.
type DelegationChange struct {
	Expected *types.Delegation
	Next     *types.Delegation
}

// GetDelegation reads one native record, without scanning its owner's or
// validator's other positions. It returns no independently priced bank claim.
func (k *StateKeeper) GetDelegation(ctx sdk.Context, delegator, validator string, stakeType types.StakeType, assetID string) (types.Delegation, error) {
	identity := types.Delegation{Delegator: delegator, Validator: validator, Stake: types.Stake{Type: stakeType, ID: assetID}}
	key := delegationKey(identity)
	var d types.Delegation
	if err := k.read(ctx, key, &d); err != nil {
		return d, err
	}
	if !bytes.Equal(key, delegationKey(d)) {
		return types.Delegation{}, errors.New("native delegation key mismatch")
	}
	if err := d.Stake.Validate(); err != nil {
		return types.Delegation{}, err
	}
	return d, nil
}

func nftDelegationKeys(d types.Delegation) [][]byte {
	var keys [][]byte
	for _, id := range d.Stake.SubTokenIDs {
		keys = append(keys, composite(delegatedNFTPrefix, d.Stake.ID, fmt.Sprint(id)))
	}
	return keys
}

func validatorDelegationKey(d types.Delegation) []byte {
	return append(composite(validatorDelegationPrefix, d.Validator), delegationKey(d)...)
}

func delegationCountKey(operator string) []byte {
	return composite(delegationCountPrefix, operator)
}

func (k *StateKeeper) checkDelegationIndex(ctx sdk.Context) error {
	marker, err := k.service.OpenKVStore(ctx).Get(delegationIndexMarker)
	if err != nil {
		return err
	}
	if !bytes.Equal(marker, []byte{1}) {
		return errors.New("native delegation index requires initialization or explicit migration")
	}
	return nil
}

func (k *StateKeeper) delegationCount(ctx sdk.Context, operator string) (uint32, error) {
	data, err := k.service.OpenKVStore(ctx).Get(delegationCountKey(operator))
	if err != nil {
		return 0, err
	}
	if data == nil {
		return 0, nil
	}
	if len(data) != 4 || binary.BigEndian.Uint32(data) == 0 {
		return 0, errors.New("native delegation counter is malformed")
	}
	return binary.BigEndian.Uint32(data), nil
}

func (k *StateKeeper) writeDelegationCount(ctx sdk.Context, operator string, count uint32) error {
	state := k.service.OpenKVStore(ctx)
	if count == 0 {
		return state.Delete(delegationCountKey(operator))
	}
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], count)
	return state.Set(delegationCountKey(operator), encoded[:])
}

func (k *StateKeeper) addDelegationIndexes(ctx sdk.Context, d types.Delegation) error {
	state := k.service.OpenKVStore(ctx)
	key := delegationKey(d)
	for _, index := range append(nftDelegationKeys(d), validatorDelegationKey(d)) {
		present, err := state.Has(index)
		if err != nil {
			return err
		}
		if present {
			return errors.New("native delegation unit or reverse index is occupied")
		}
		if err := state.Set(index, key); err != nil {
			return err
		}
	}
	return nil
}

func (k *StateKeeper) initDelegationIndexes(ctx sdk.Context, delegations []types.Delegation) error {
	counts := map[string]uint32{}
	for _, d := range delegations {
		if err := k.addDelegationIndexes(ctx, d); err != nil {
			return err
		}
		counts[d.Validator]++ // Complete genesis validation bounds this count.
	}
	for _, operator := range sortedDelegationKeys(counts) {
		if err := k.writeDelegationCount(ctx, operator, counts[operator]); err != nil {
			return err
		}
	}
	return k.service.OpenKVStore(ctx).Set(delegationIndexMarker, []byte{1})
}

func (k *StateKeeper) validateDelegationIndexes(ctx sdk.Context, delegations []types.Delegation) error {
	if err := k.checkDelegationIndex(ctx); err != nil {
		return err
	}
	state := k.service.OpenKVStore(ctx)
	counts := map[string]uint32{}
	var units uint64
	for _, d := range delegations {
		key := delegationKey(d)
		for _, index := range append(nftDelegationKeys(d), validatorDelegationKey(d)) {
			actual, err := state.Get(index)
			if err != nil {
				return err
			}
			if !bytes.Equal(actual, key) {
				return errors.New("native delegation index mismatch")
			}
		}
		counts[d.Validator]++
		units += uint64(len(d.Stake.SubTokenIDs))
	}
	for _, operator := range sortedDelegationKeys(counts) {
		actual, err := k.delegationCount(ctx, operator)
		if err != nil {
			return err
		}
		if actual != counts[operator] {
			return errors.New("native delegation counter mismatch")
		}
	}
	for _, expected := range []struct {
		prefix byte
		count  uint64
	}{{delegatedNFTPrefix, units}, {validatorDelegationPrefix, uint64(len(delegations))}, {delegationCountPrefix, uint64(len(counts))}} {
		var count uint64
		if err := k.scan(ctx, expected.prefix, func(_, _ []byte) error { count++; return nil }); err != nil {
			return err
		}
		if count != expected.count {
			return errors.New("orphan native delegation index")
		}
	}
	return nil
}

// ApplyDelegationBatch validates only affected records and indexes. Its work is
// bounded by the supplied batch, not every unrelated delegation in the store.
// Final-state checks run after removing old indexes, allowing atomic swaps at a
// full delegation limit without transient duplicate NFT ownership.
func (k *StateKeeper) ApplyDelegationBatch(ctx sdk.Context, changes []DelegationChange) error {
	if len(changes) == 0 || len(changes) > MaxDelegationBatch {
		return errors.New("native delegation batch size outside limits")
	}
	if err := k.checkDelegationIndex(ctx); err != nil {
		return err
	}
	p, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	child, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	type record struct {
		key, data []byte
		value     types.Delegation
	}
	oldRows, newRows := map[string]record{}, map[string]record{}
	counts, removals, additions := map[string]uint64{}, map[string]uint64{}, map[string]uint64{}
	totalBytes := 0
	for _, change := range changes {
		if change.Expected == nil && change.Next == nil {
			return errors.New("empty native delegation change")
		}
		for index, input := range []*types.Delegation{change.Expected, change.Next} {
			if input == nil {
				continue
			}
			if err := input.Stake.Validate(); err != nil {
				return err
			}
			size := proto.Size(input)
			totalBytes += size
			if size > MaxRecordBytes || totalBytes > maxDelegationBatchBytes {
				return errors.New("native delegation batch byte limit exceeded")
			}
			data, err := proto.Marshal(input)
			if err != nil {
				return err
			}
			var detached types.Delegation
			if err := proto.Unmarshal(data, &detached); err != nil {
				return err
			}
			validator, err := k.GetValidator(child, detached.Validator)
			if err != nil {
				return err
			}
			check := types.GenesisState{Params: p, Validators: []types.Validator{validator}, Delegations: []types.Delegation{detached}}
			if err := check.Validate(); err != nil {
				return err
			}
			key := delegationKey(detached)
			rows := oldRows
			if index == 1 {
				rows = newRows
			}
			if _, duplicate := rows[string(key)]; duplicate {
				return errors.New("duplicate native delegation batch key")
			}
			rows[string(key)] = record{key: key, data: data, value: detached}
			if _, read := counts[detached.Validator]; !read {
				count, err := k.delegationCount(child, detached.Validator)
				if err != nil {
					return err
				}
				counts[detached.Validator] = uint64(count)
			}
			if index == 0 {
				removals[detached.Validator]++
			} else {
				additions[detached.Validator]++
			}
		}
	}
	for operator, count := range counts {
		if count < removals[operator] {
			return errors.New("native delegation counter underflow")
		}
		count = count - removals[operator] + additions[operator]
		if count > uint64(p.MaxDelegations) {
			return errors.New("validator delegation limit exceeded")
		}
		counts[operator] = count
	}
	for _, key := range sortedDelegationKeys(oldRows) {
		row := oldRows[key]
		actual, err := state.Get(row.key)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, row.data) {
			return errors.New("native delegation prestate changed")
		}
		for _, index := range append(nftDelegationKeys(row.value), validatorDelegationKey(row.value)) {
			actual, err := state.Get(index)
			if err != nil {
				return err
			}
			if !bytes.Equal(actual, row.key) {
				return errors.New("native delegation prestate index mismatch")
			}
		}
	}
	for _, key := range sortedDelegationKeys(newRows) {
		row := newRows[key]
		if _, replacing := oldRows[key]; replacing {
			continue
		}
		present, err := state.Has(row.key)
		if err != nil {
			return err
		}
		if present {
			return errors.New("native delegation target already exists")
		}
	}
	// Canonical key ordering keeps store I/O, gas and failure behavior stable.
	for _, key := range sortedDelegationKeys(oldRows) {
		row := oldRows[key]
		for _, index := range append(nftDelegationKeys(row.value), validatorDelegationKey(row.value)) {
			if err := state.Delete(index); err != nil {
				return err
			}
		}
		if err := state.Delete(row.key); err != nil {
			return err
		}
	}
	for _, key := range sortedDelegationKeys(newRows) {
		row := newRows[key]
		if err := k.addDelegationIndexes(child, row.value); err != nil {
			return err
		}
		if err := state.Set(row.key, row.data); err != nil {
			return err
		}
	}
	for _, operator := range sortedDelegationKeys(counts) {
		if err := k.writeDelegationCount(child, operator, uint32(counts[operator])); err != nil {
			return err
		}
	}
	commit()
	return nil
}
