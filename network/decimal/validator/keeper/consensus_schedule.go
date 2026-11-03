package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"sort"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

func scheduledKey(height int64) []byte {
	key := make([]byte, 9)
	key[0] = schedulePrefix
	binary.BigEndian.PutUint64(key[1:], uint64(height))
	return key
}

func decodeScheduledSet(key, data []byte) (types.ScheduledValidatorSet, error) {
	var row types.ScheduledValidatorSet
	if len(data) > MaxHistoricalBytes {
		return row, errors.New("scheduled consensus set exceeds size limit")
	}
	if err := proto.Unmarshal(data, &row); err != nil {
		return row, err
	}
	if row.EffectiveHeight <= 0 || !bytes.Equal(key, scheduledKey(row.EffectiveHeight)) {
		return row, errors.New("scheduled consensus key mismatch")
	}
	if _, err := ConsensusSet(row.Validators); err != nil {
		return row, err
	}
	return row, nil
}

func (k *StateKeeper) writeScheduledSet(ctx sdk.Context, row types.ScheduledValidatorSet) error {
	if row.EffectiveHeight <= 0 {
		return errors.New("invalid scheduled consensus height")
	}
	if _, err := ConsensusSet(row.Validators); err != nil {
		return err
	}
	data, err := proto.Marshal(&row)
	if err != nil {
		return err
	}
	if len(data) > MaxHistoricalBytes {
		return errors.New("scheduled consensus set exceeds size limit")
	}
	state := k.service.OpenKVStore(ctx)
	key := scheduledKey(row.EffectiveHeight)
	existing, err := state.Get(key)
	if err != nil {
		return err
	}
	if existing != nil && !bytes.Equal(existing, data) {
		return errors.New("scheduled consensus set is immutable")
	}
	return state.Set(key, data)
}

func canonicalScheduledSet(height int64, vals []types.Validator) (types.ScheduledValidatorSet, error) {
	data, err := proto.Marshal(&types.ScheduledValidatorSet{EffectiveHeight: height, Validators: vals})
	if err != nil {
		return types.ScheduledValidatorSet{}, err
	}
	var row types.ScheduledValidatorSet
	if err = proto.Unmarshal(data, &row); err != nil {
		return row, err
	}
	if _, err = ConsensusSet(row.Validators); err != nil {
		return row, err
	}
	sort.Slice(row.Validators, func(i, j int) bool {
		a, b := row.Validators[i], row.Validators[j]
		if a.Stake != b.Stake {
			return a.Stake > b.Stake
		}
		ka, _ := a.GetConsAddr()
		kb, _ := b.GetConsAddr()
		return bytes.Compare(ka, kb) < 0
	})
	return row, nil
}

// BootstrapConsensusSchedule is an explicit InitChain step. It seeds the first
// two effective sets, never infers a genesis set from a later mutable power row.
func (k *StateKeeper) BootstrapConsensusSchedule(ctx sdk.Context, initialHeight int64, initial []types.Validator) error {
	if initialHeight <= 0 || initialHeight > math.MaxInt64-2 || (ctx.BlockHeight() != 0 && ctx.BlockHeight() != initialHeight-1) {
		return errors.New("invalid initial consensus schedule context")
	}
	g, err := k.ExportGenesis(ctx)
	if err != nil {
		return err
	}
	if len(g.ScheduledValidatorSets) != 0 {
		return errors.New("consensus schedule already initialized")
	}
	first, err := canonicalScheduledSet(initialHeight, initial)
	if err != nil {
		return err
	}
	second, err := canonicalScheduledSet(initialHeight+1, initial)
	if err != nil {
		return err
	}
	g.ScheduledValidatorSets = []types.ScheduledValidatorSet{first, second}
	if err = g.Validate(); err != nil {
		return err
	}
	child, commit := ctx.CacheContext()
	for _, row := range g.ScheduledValidatorSets {
		if err = k.writeScheduledSet(child, row); err != nil {
			return err
		}
	}
	commit()
	return nil
}

// ScheduledEndBlock atomically consumes the exact effective H set, preserves
// H+1, schedules the newly selected set at H+2 and records real H history.
func (k *StateKeeper) ScheduledEndBlock(ctx sdk.Context, eligible []string) ([]abci.ValidatorUpdate, error) {
	if ctx.BlockHeight() <= 0 || ctx.BlockHeight() > math.MaxInt64-2 {
		return nil, errors.New("invalid scheduled end block height")
	}
	g, err := k.ExportGenesis(ctx)
	if err != nil {
		return nil, err
	}
	if len(g.ScheduledValidatorSets) != 2 || g.ScheduledValidatorSets[0].EffectiveHeight != ctx.BlockHeight() {
		return nil, errors.New("effective consensus schedule does not match current height")
	}
	child, commit := ctx.CacheContext()
	updates, err := k.EndBlockValidatorUpdates(child, g.ScheduledValidatorSets[0].Validators, eligible)
	if err != nil {
		return nil, err
	}
	// Power rows were committed only into child by the transition above.
	after, err := k.ExportGenesis(child)
	if err != nil {
		return nil, err
	}
	selected := map[string]int64{}
	for _, power := range after.LastValidatorPowers {
		selected[power.Address] = power.Power
	}
	var vals []types.Validator
	for _, v := range after.Validators {
		if selected[v.OperatorAddress] > 0 {
			if v.Stake != selected[v.OperatorAddress] || v.Status != types.BondStatus_Bonded {
				return nil, errors.New("selected native power differs from dynamic state")
			}
			vals = append(vals, v)
		}
	}
	future, err := canonicalScheduledSet(ctx.BlockHeight()+2, vals)
	if err != nil {
		return nil, err
	}
	if err = k.writeScheduledSet(child, future); err != nil {
		return nil, err
	}
	if err = k.service.OpenKVStore(child).Delete(scheduledKey(ctx.BlockHeight())); err != nil {
		return nil, err
	}
	if _, err = k.ExportGenesis(child); err != nil {
		return nil, err
	}
	commit()
	return updates, nil
}
