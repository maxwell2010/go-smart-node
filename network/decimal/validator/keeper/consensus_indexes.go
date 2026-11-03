package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"

	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const (
	bondedIndexPrefix   byte = 0x13
	bondedTotalsKey     byte = 0x07
	positivePowerPrefix byte = 0x32
)

func bondedTotalsData(count uint64, power int64) []byte {
	data := make([]byte, 17)
	data[0] = 1
	binary.BigEndian.PutUint64(data[1:9], count)
	binary.BigEndian.PutUint64(data[9:], uint64(power))
	return data
}

func (k *StateKeeper) bondedTotals(ctx sdk.Context) (uint64, int64, error) {
	data, err := k.service.OpenKVStore(ctx).Get([]byte{bondedTotalsKey})
	if err != nil {
		return 0, 0, err
	}
	if len(data) != 17 || data[0] != 1 {
		return 0, 0, errors.New("native consensus indexes require explicit cold initialization")
	}
	count, total := binary.BigEndian.Uint64(data[1:9]), int64(binary.BigEndian.Uint64(data[9:]))
	if count > math.MaxUint32 || total < 0 || total > cmttypes.MaxTotalVotingPower || count == 0 && total != 0 {
		return 0, 0, errors.New("invalid indexed bonded totals")
	}
	return count, total, nil
}

func (k *StateKeeper) bondedValidators(ctx sdk.Context) ([]types.Validator, error) {
	count, total, err := k.bondedTotals(ctx)
	if err != nil {
		return nil, err
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	if count > uint64(params.MaxValidators) {
		return nil, errors.New("indexed bonded validator limit exceeded")
	}
	var validators []types.Validator
	var actual int64
	err = k.scan(ctx, bondedIndexPrefix, func(key, value []byte) error {
		if uint64(len(validators)) >= count {
			return errors.New("extra indexed bonded validator")
		}
		// Preserve the stored operator text. The SDK also accepts uppercase
		// Bech32 input; reconstructing lowercase from the key would alter it.
		operator := string(value)
		expected, err := operatorKey(bondedIndexPrefix, operator)
		if err != nil || !bytes.Equal(key, expected) {
			return errors.New("malformed bonded membership")
		}
		v, err := k.GetValidator(ctx, operator)
		if err != nil {
			return err
		}
		if v.Status != types.BondStatus_Bonded || v.Stake > cmttypes.MaxTotalVotingPower-actual {
			return errors.New("indexed bonded validator differs from native metadata")
		}
		actual += v.Stake
		validators = append(validators, v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if uint64(len(validators)) != count || actual != total {
		return nil, errors.New("indexed bonded count or power mismatch")
	}
	return validators, nil
}

// Previous powers intentionally do not equal today's dynamic stake or status:
// genuine transaction updates can precede the next delayed consensus transition.
func (k *StateKeeper) previousPositivePowers(ctx sdk.Context) (map[string]int64, error) {
	if _, _, err := k.bondedTotals(ctx); err != nil {
		return nil, err
	}
	powers := map[string]int64{}
	var total int64
	err := k.scan(ctx, positivePowerPrefix, func(key, value []byte) error {
		if len(key) != 21 || key[0] != positivePowerPrefix || len(value) != 8 {
			return errors.New("malformed positive native power index")
		}
		power := int64(binary.BigEndian.Uint64(value))
		if power <= 0 || power > cmttypes.MaxTotalVotingPower-total {
			return errors.New("invalid indexed previous native power")
		}
		rawKey := bytes.Clone(key)
		rawKey[0] = powerPrefix
		var row types.LastValidatorPower
		if err := k.read(ctx, rawKey, &row); err != nil {
			return err
		}
		expected, err := operatorKey(positivePowerPrefix, row.Address)
		if err != nil || !bytes.Equal(key, expected) || row.Power != power {
			return errors.New("previous native power index mismatch")
		}
		total += power
		powers[row.Address] = power
		return nil
	})
	if err != nil {
		return nil, err
	}
	stored, err := k.service.OpenKVStore(ctx).Get([]byte{3})
	if err != nil {
		return nil, err
	}
	if len(stored) != 8 || binary.BigEndian.Uint64(stored) != uint64(total) {
		return nil, errors.New("previous native total power mismatch")
	}
	return powers, nil
}

func (k *StateKeeper) updateBondedMember(ctx sdk.Context, previous *types.Validator, next types.Validator) error {
	count, total, err := k.bondedTotals(ctx)
	if err != nil {
		return err
	}
	key, err := operatorKey(bondedIndexPrefix, next.OperatorAddress)
	if err != nil {
		return err
	}
	state := k.service.OpenKVStore(ctx)
	member, err := state.Get(key)
	if err != nil {
		return err
	}
	wasBonded := previous != nil && previous.Status == types.BondStatus_Bonded
	if wasBonded && !bytes.Equal(member, []byte(previous.OperatorAddress)) || !wasBonded && member != nil {
		return errors.New("native bonded membership mismatch")
	}
	if wasBonded {
		if count == 0 || previous.Stake > total {
			return errors.New("invalid previous bonded totals")
		}
		count--
		total -= previous.Stake
	}
	if next.Status == types.BondStatus_Bonded {
		if next.Stake > cmttypes.MaxTotalVotingPower-total {
			return errors.New("bonded native power exceeds consensus limit")
		}
		count++
		total += next.Stake
		if err := state.Set(key, []byte(next.OperatorAddress)); err != nil {
			return err
		}
	} else if wasBonded {
		if err := state.Delete(key); err != nil {
			return err
		}
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	if count > uint64(params.MaxValidators) {
		return errors.New("bonded validator limit exceeded")
	}
	return state.Set([]byte{bondedTotalsKey}, bondedTotalsData(count, total))
}

func (k *StateKeeper) replaceBondedMembers(ctx sdk.Context, previous, next []types.Validator) error {
	state := k.service.OpenKVStore(ctx)
	for _, v := range previous {
		key, _ := operatorKey(bondedIndexPrefix, v.OperatorAddress)
		if err := state.Delete(key); err != nil {
			return err
		}
	}
	var total int64
	for _, v := range next {
		if v.Status != types.BondStatus_Bonded || v.Stake < 0 || v.Stake > cmttypes.MaxTotalVotingPower-total {
			return errors.New("invalid final bonded membership")
		}
		total += v.Stake
		key, err := operatorKey(bondedIndexPrefix, v.OperatorAddress)
		if err != nil {
			return err
		}
		if err := state.Set(key, []byte(v.OperatorAddress)); err != nil {
			return err
		}
	}
	return state.Set([]byte{bondedTotalsKey}, bondedTotalsData(uint64(len(next)), total))
}
