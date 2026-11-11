package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"sort"

	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
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

// readScheduledPair visits at most three positions, rejecting the third without
// decoding it. Full financial/genesis validation belongs to cold state audit.
func (k *StateKeeper) readScheduledPair(ctx sdk.Context) ([]types.ScheduledValidatorSet, error) {
	state := k.service.OpenKVStore(ctx)
	marker, err := state.Get([]byte{4})
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(marker, []byte{1}) {
		return nil, errors.New("validator genesis is not initialized")
	}
	if k.bindingDomain != nil {
		if err := k.checkBindingDomain(ctx); err != nil {
			return nil, err
		}
	} else {
		domain, err := state.Get([]byte{bindingDomainMarker})
		if err != nil {
			return nil, err
		}
		if domain != nil {
			return nil, ErrBindingDomain
		}
	}
	it, err := state.Iterator([]byte{schedulePrefix}, []byte{schedulePrefix + 1})
	if err != nil {
		return nil, err
	}
	var rows []types.ScheduledValidatorSet
	for ; it.Valid(); it.Next() {
		if err = it.Error(); err != nil {
			break
		}
		if len(rows) == 2 {
			err = errors.New("effective consensus schedule must contain exactly two rows")
			break
		}
		row, e := decodeScheduledSet(it.Key(), it.Value())
		if e != nil {
			err = e
			break
		}
		rows = append(rows, row)
	}
	closeErr := it.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(rows) != 2 || rows[0].EffectiveHeight != ctx.BlockHeight() || rows[1].EffectiveHeight != ctx.BlockHeight()+1 {
		return nil, errors.New("effective consensus schedule does not match current height")
	}
	known := map[string]bool{}
	var identities []types.Validator
	for _, row := range rows {
		for _, scheduled := range row.Validators {
			if known[scheduled.OperatorAddress] {
				continue
			}
			v, err := k.GetValidator(ctx, scheduled.OperatorAddress)
			if err != nil {
				return nil, err
			}
			known[v.OperatorAddress] = true
			identities = append(identities, v)
		}
	}
	if err := types.ValidateScheduledSets(rows, identities); err != nil {
		return nil, err
	}
	return rows, nil
}

// selectedScheduledSet point-reads the complete authenticated eligible set after
// the transition. EndBlock selection only writes positive powers for its selected
// subset; it already validated/replaced the previous power map atomically.
func (k *StateKeeper) selectedScheduledSet(ctx sdk.Context, eligible []string) (types.ScheduledValidatorSet, error) {
	var validators []types.Validator
	var total int64
	for _, operator := range eligible {
		key, err := operatorKey(powerPrefix, operator)
		if err != nil {
			return types.ScheduledValidatorSet{}, err
		}
		var power types.LastValidatorPower
		if err = k.read(ctx, key, &power); errors.Is(err, ErrNotFound) {
			continue
		} else if err != nil {
			return types.ScheduledValidatorSet{}, err
		}
		if power.Address != operator || power.Power <= 0 || power.Power > cmttypes.MaxTotalVotingPower-total {
			return types.ScheduledValidatorSet{}, errors.New("invalid selected native validator power")
		}
		v, err := k.GetValidator(ctx, operator)
		if err != nil {
			return types.ScheduledValidatorSet{}, err
		}
		if v.Stake != power.Power || v.Status != types.BondStatus_Bonded || v.Jailed {
			return types.ScheduledValidatorSet{}, errors.New("selected native power differs from dynamic state")
		}
		total += power.Power
		validators = append(validators, v)
	}
	storedTotal, err := k.service.OpenKVStore(ctx).Get([]byte{3})
	if err != nil {
		return types.ScheduledValidatorSet{}, err
	}
	if len(storedTotal) != 8 || binary.BigEndian.Uint64(storedTotal) != uint64(total) {
		return types.ScheduledValidatorSet{}, errors.New("selected native total power mismatch")
	}
	return canonicalScheduledSet(ctx.BlockHeight()+2, validators)
}

// BootstrapConsensusSchedule is an explicit InitChain step. It seeds the first
// two effective sets, never infers a genesis set from a later mutable power row.
func (k *StateKeeper) BootstrapConsensusSchedule(ctx sdk.Context, initialHeight int64, initial []types.Validator) error {
	if initialHeight <= 0 || initialHeight > math.MaxInt64-2 || (ctx.BlockHeight() != 0 && ctx.BlockHeight() != initialHeight-1) {
		return errors.New("invalid initial consensus schedule context")
	}
	child, commit := ctx.CacheContext()
	marker, err := k.service.OpenKVStore(child).Get([]byte{bondedTotalsKey})
	if err != nil {
		return err
	}
	if marker == nil {
		if err := k.RebuildConsensusIndexes(child); err != nil {
			return err
		}
	}
	g, err := k.ExportGenesis(child)
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
	anchored, err := k.service.OpenKVStore(ctx).Has([]byte{abciConsensusAnchorKey})
	if err != nil {
		return nil, err
	}
	if anchored {
		return nil, errors.New("anchored native consensus requires the ABCI scheduled end block")
	}
	rows, err := k.readScheduledPair(ctx)
	if err != nil {
		return nil, err
	}
	return k.advanceScheduledConsensus(ctx, rows, eligible, false)
}

func (k *StateKeeper) advanceScheduledConsensus(ctx sdk.Context, rows []types.ScheduledValidatorSet, eligible []string, deriveStakePower bool) ([]abci.ValidatorUpdate, error) {
	child, commit := ctx.CacheContext()
	var stakeBaseline []types.Validator
	if deriveStakePower {
		stakeBaseline = rows[1].Validators
	}
	updates, err := k.endBlockValidatorUpdates(child, rows[0].Validators, eligible, stakeBaseline)
	if err != nil {
		return nil, err
	}
	future, err := k.selectedScheduledSet(child, eligible)
	if err != nil {
		return nil, err
	}
	if err = k.writeScheduledSet(child, future); err != nil {
		return nil, err
	}
	if err = k.service.OpenKVStore(child).Delete(scheduledKey(ctx.BlockHeight())); err != nil {
		return nil, err
	}
	commit()
	return updates, nil
}
