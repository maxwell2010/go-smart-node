package keeper

import (
	"bytes"
	sdkmath "cosmossdk.io/math"
	"errors"
	"math"
	"sort"

	abci "github.com/cometbft/cometbft/abci/types"
	cryptoencoding "github.com/cometbft/cometbft/crypto/encoding"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
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
	bonded, err := k.bondedValidators(child)
	if err != nil {
		return nil, err
	}
	previous, err := k.previousPositivePowers(child)
	if err != nil {
		return nil, err
	}
	byOperator := map[string]types.Validator{}
	var candidates []types.Validator
	for _, v := range bonded {
		byOperator[v.OperatorAddress] = v
	}
	previousOperators := make([]string, 0, len(previous))
	for operator := range previous {
		previousOperators = append(previousOperators, operator)
	}
	sort.Strings(previousOperators)
	for _, operator := range previousOperators {
		if _, exists := byOperator[operator]; exists {
			continue
		}
		v, err := k.GetValidator(child, operator)
		if err != nil {
			return nil, errors.New("previous validator metadata is missing or invalid")
		}
		byOperator[operator] = v
	}
	seenEligible := map[string]bool{}
	for _, operator := range eligible {
		if seenEligible[operator] {
			return nil, errors.New("duplicate eligible validator")
		}
		seenEligible[operator] = true
		v, ok := byOperator[operator]
		if !ok {
			v, err = k.GetValidator(child, operator)
			if err != nil {
				return nil, errors.New("eligible validator metadata is missing or invalid")
			}
			byOperator[operator] = v
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
	// The union covers every old bonded row plus every previous/eligible row.
	// Other unbonded candidates cannot change the final bonded constraint.
	changedStatuses := make([]types.Validator, 0)
	unionOperators := make([]string, 0, len(byOperator))
	for operator := range byOperator {
		unionOperators = append(unionOperators, operator)
	}
	sort.Strings(unionOperators)
	finalValidators := make([]types.Validator, 0, len(unionOperators))
	for _, operator := range unionOperators {
		finalValidators = append(finalValidators, byOperator[operator])
	}
	for i, v := range finalValidators {
		status := v.Status
		if next[v.OperatorAddress] > 0 {
			status = types.BondStatus_Bonded
		} else if status == types.BondStatus_Bonded {
			status = types.BondStatus_Unbonded
		}
		if status != v.Status {
			v.Status = status
			finalValidators[i] = v
			changedStatuses = append(changedStatuses, v)
		}
	}
	if err = (types.GenesisState{Params: params, Validators: finalValidators}).Validate(); err != nil {
		return nil, err
	}
	for _, v := range changedStatuses {
		key, err := operatorKey(validatorPrefix, v.OperatorAddress)
		if err != nil {
			return nil, err
		}
		// Status is metadata only. The independently hydrated RS row and consensus
		// index remain unchanged, including earned rewards and aggregate power.
		v.Rewards, v.TotalRewards, v.Stake = sdkmath.ZeroInt(), sdkmath.ZeroInt(), 0
		if err = k.put(child, key, &v); err != nil {
			return nil, err
		}
	}
	if err = k.replaceBondedMembers(child, bonded, candidates); err != nil {
		return nil, err
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
