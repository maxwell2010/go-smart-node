package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"

	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

func (k *StateKeeper) initConsensusIndexes(ctx sdk.Context) error {
	state := k.service.OpenKVStore(ctx)
	if err := state.Set([]byte{bondedTotalsKey}, bondedTotalsData(0, 0)); err != nil {
		return err
	}
	return state.Set([]byte{3}, make([]byte, 8))
}

func (k *StateKeeper) auditConsensusIndexes(ctx sdk.Context, g *types.GenesisState) error {
	expected := map[byte]map[string][]byte{bondedIndexPrefix: {}, positivePowerPrefix: {}}
	var bondedCount uint64
	var bondedPower int64
	for _, v := range g.Validators {
		if v.Status == types.BondStatus_Bonded {
			key, _ := operatorKey(bondedIndexPrefix, v.OperatorAddress)
			expected[bondedIndexPrefix][string(key)] = []byte(v.OperatorAddress)
			bondedCount++
			bondedPower += v.Stake
		}
	}
	for _, row := range g.LastValidatorPowers {
		if row.Power == 0 {
			return errors.New("zero previous powers require explicit cold normalization")
		}
		key, _ := operatorKey(positivePowerPrefix, row.Address)
		data := make([]byte, 8)
		binary.BigEndian.PutUint64(data, uint64(row.Power))
		expected[positivePowerPrefix][string(key)] = data
	}
	data, err := k.service.OpenKVStore(ctx).Get([]byte{bondedTotalsKey})
	if err != nil {
		return err
	}
	if !bytes.Equal(data, bondedTotalsData(bondedCount, bondedPower)) {
		return errors.New("native bonded totals index mismatch")
	}
	for _, prefix := range []byte{bondedIndexPrefix, positivePowerPrefix} {
		count := 0
		if err := k.scan(ctx, prefix, func(key, value []byte) error {
			want, ok := expected[prefix][string(key)]
			if !ok || !bytes.Equal(value, want) {
				return errors.New("native consensus membership index mismatch")
			}
			count++
			return nil
		}); err != nil {
			return err
		}
		if count != len(expected[prefix]) {
			return errors.New("native consensus membership index is incomplete")
		}
	}
	return nil
}

// RebuildConsensusIndexes is an explicit cold operation. It validates the whole
// source ledger first, canonicalizes economically empty previous-power rows and
// atomically rebuilds derived indexes. Hot block execution never invokes it.
func (k *StateKeeper) RebuildConsensusIndexes(ctx sdk.Context) error {
	g, err := k.exportGenesis(ctx, false)
	if err != nil {
		return err
	}
	child, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	for _, prefix := range []byte{bondedIndexPrefix, positivePowerPrefix, powerPrefix} {
		var keys [][]byte
		if err := k.scan(child, prefix, func(key, _ []byte) error { keys = append(keys, key); return nil }); err != nil {
			return err
		}
		for _, key := range keys {
			if err := state.Delete(key); err != nil {
				return err
			}
		}
	}
	var bonded []types.Validator
	for _, v := range g.Validators {
		if v.Status == types.BondStatus_Bonded {
			bonded = append(bonded, v)
		}
	}
	if err := k.replaceBondedMembers(child, nil, bonded); err != nil {
		return err
	}
	var total int64
	for _, p := range g.LastValidatorPowers {
		if p.Power == 0 {
			continue
		}
		if p.Power < 0 || p.Power > cmttypes.MaxTotalVotingPower-total {
			return errors.New("invalid native total power")
		}
		total += p.Power
		key, _ := operatorKey(powerPrefix, p.Address)
		if err := k.put(child, key, &p); err != nil {
			return err
		}
		key, _ = operatorKey(positivePowerPrefix, p.Address)
		value := make([]byte, 8)
		binary.BigEndian.PutUint64(value, uint64(p.Power))
		if err := state.Set(key, value); err != nil {
			return err
		}
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(total))
	if err := state.Set([]byte{3}, encoded[:]); err != nil {
		return err
	}
	if _, err := k.ExportGenesis(child); err != nil {
		return err
	}
	commit()
	return nil
}
