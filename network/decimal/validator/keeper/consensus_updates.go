package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	cmttypes "github.com/cometbft/cometbft/types"
	"math"
	"sort"

	abci "github.com/cometbft/cometbft/abci/types"
	cryptoencoding "github.com/cometbft/cometbft/crypto/encoding"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

// EndBlockValidatorUpdates selects and commits the native next validator powers.
// Comet applies returned updates at H+2. effective must be the actual set active
// for H, supplied by the application's persisted delayed-set lifecycle; this
// method verifies it against H's header rather than inventing it from new state.
// eligible is the complete authenticated MasterValidator approved/unpaused set,
// supplied by the application live-contract reader, not guessed from BondStatus.
// No bank tokens or SDK staking shares are created by this transition.
func (k *StateKeeper) EndBlockValidatorUpdates(ctx sdk.Context, effective []types.Validator, eligible []string) ([]abci.ValidatorUpdate, error) {
	if ctx.BlockHeight() <= 0 || ctx.BlockHeight() > math.MaxInt64-2 {
		return nil, errors.New("invalid validator transition height")
	}
	actual, err := ConsensusSet(effective)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(actual.Hash(), ctx.BlockHeader().ValidatorsHash) {
		return nil, errors.New("effective native validator set does not match block header")
	}
	child, commit := ctx.CacheContext()
	params, err := k.GetParams(child)
	if err != nil {
		return nil, err
	}
	all, err := k.GetValidators(child)
	if err != nil {
		return nil, err
	}
	byOperator := map[string]types.Validator{}
	var candidates []types.Validator
	for _, v := range all {
		byOperator[v.OperatorAddress] = v
	}
	seenEligible := map[string]bool{}
	for _, operator := range eligible {
		if seenEligible[operator] {
			return nil, errors.New("duplicate eligible validator")
		}
		seenEligible[operator] = true
		v, ok := byOperator[operator]
		if !ok {
			return nil, errors.New("eligible validator metadata is missing")
		}
		if !v.Jailed && v.Stake > 0 {
			candidates = append(candidates, v)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Stake != candidates[j].Stake {
			return candidates[i].Stake > candidates[j].Stake
		}
		a, _ := candidates[i].GetConsAddr()
		b, _ := candidates[j].GetConsAddr()
		return bytes.Compare(a, b) < 0
	})
	if uint64(len(candidates)) > uint64(params.MaxValidators) {
		candidates = candidates[:params.MaxValidators]
	}
	for i := range candidates {
		candidates[i].Status = types.BondStatus_Bonded
	}
	if _, err = ConsensusSet(candidates); err != nil {
		return nil, err
	}
	previous := map[string]int64{}
	var previousTotal int64
	err = k.scan(child, powerPrefix, func(key, data []byte) error {
		var p types.LastValidatorPower
		if len(data) > MaxRecordBytes {
			return errors.New("previous validator power record exceeds size limit")
		}
		if err := proto.Unmarshal(data, &p); err != nil {
			return err
		}
		expected, err := operatorKey(powerPrefix, p.Address)
		if err != nil {
			return err
		}
		if !bytes.Equal(key, expected) || p.Power < 0 {
			return errors.New("invalid previous validator power record")
		}
		if _, ok := byOperator[p.Address]; !ok {
			return errors.New("previous validator metadata is missing")
		}
		if p.Power > cmttypes.MaxTotalVotingPower-previousTotal {
			return errors.New("invalid previous total power")
		}
		previousTotal += p.Power
		previous[p.Address] = p.Power
		return nil
	})
	if err != nil {
		return nil, err
	}
	storedTotal, err := k.service.OpenKVStore(child).Get([]byte{3})
	if err != nil {
		return nil, err
	}
	if len(storedTotal) != 8 || binary.BigEndian.Uint64(storedTotal) != uint64(previousTotal) {
		return nil, errors.New("previous native total power mismatch")
	}
	next := map[string]int64{}
	powers := make([]types.LastValidatorPower, 0, len(candidates))
	for _, v := range candidates {
		next[v.OperatorAddress] = v.Stake
		powers = append(powers, types.LastValidatorPower{Address: v.OperatorAddress, Power: v.Stake})
	}
	changed := map[string]int64{}
	for operator, power := range next {
		if previous[operator] != power {
			changed[operator] = power
		}
	}
	for operator, power := range previous {
		if power > 0 && next[operator] == 0 {
			changed[operator] = 0
		}
	}
	operators := make([]string, 0, len(changed))
	for operator := range changed {
		operators = append(operators, operator)
	}
	sort.Slice(operators, func(i, j int) bool {
		a, _ := byOperator[operators[i]].GetConsAddr()
		b, _ := byOperator[operators[j]].GetConsAddr()
		return bytes.Compare(a, b) < 0
	})
	updates := make([]abci.ValidatorUpdate, 0, len(operators))
	for _, operator := range operators {
		pk, err := byOperator[operator].ConsensusPubKey()
		if err != nil {
			return nil, err
		}
		key, err := cryptocodec.ToCmtPubKeyInterface(pk)
		if err != nil {
			return nil, err
		}
		wire, err := cryptoencoding.PubKeyToProto(key)
		if err != nil {
			return nil, err
		}
		updates = append(updates, abci.ValidatorUpdate{PubKey: wire, Power: changed[operator]})
	}
	// Demote before promoting to preserve MaxValidators at every validated write.
	for _, v := range all {
		if next[v.OperatorAddress] == 0 && v.Status == types.BondStatus_Bonded {
			v.Status = types.BondStatus_Unbonded
			if err = k.SetValidator(child, v); err != nil {
				return nil, err
			}
		}
	}
	for _, v := range all {
		if next[v.OperatorAddress] > 0 && v.Status != types.BondStatus_Bonded {
			v.Status = types.BondStatus_Bonded
			if err = k.SetValidator(child, v); err != nil {
				return nil, err
			}
		}
	}
	if err = k.SetLastValidatorPowers(child, powers); err != nil {
		return nil, err
	}
	effective = append([]types.Validator(nil), effective...)
	sort.Slice(effective, func(i, j int) bool {
		if effective[i].Stake != effective[j].Stake {
			return effective[i].Stake > effective[j].Stake
		}
		a, _ := effective[i].GetConsAddr()
		b, _ := effective[j].GetConsAddr()
		return bytes.Compare(a, b) < 0
	})
	if params.HistoricalEntries > 0 {
		if err = k.SetHistoricalInfo(child, types.HistoricalInfo{Header: ctx.BlockHeader(), Valset: effective}); err != nil {
			return nil, err
		}
	}
	commit()
	return updates, nil
}
