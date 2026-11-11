package keeper

import (
	"bytes"
	"errors"
	"math"
	"sort"

	sdkmath "cosmossdk.io/math"
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
	return k.endBlockValidatorUpdates(ctx, effective, eligible, nil)
}

func (k *StateKeeper) endBlockValidatorUpdates(ctx sdk.Context, effective []types.Validator, eligible []string, stakeBaseline []types.Validator) ([]abci.ValidatorUpdate, error) {
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
	selection, err := k.selectNativeValidators(child, eligible, stakeBaseline != nil)
	if err != nil {
		return nil, err
	}
	if stakeBaseline != nil {
		if err := selection.verifyPreviousPowers(stakeBaseline); err != nil {
			return nil, err
		}
	}
	updates, err := k.applyNativeValidatorSelection(child, selection)
	if err != nil {
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
	if selection.params.HistoricalEntries > 0 {
		if err = k.SetHistoricalInfo(child, types.HistoricalInfo{Header: ctx.BlockHeader(), Valset: effective}); err != nil {
			return nil, err
		}
	}
	commit()
	return updates, nil
}

type nativeValidatorSelection struct {
	params          types.Params
	bonded          []types.Validator
	previous        map[string]int64
	byOperator      map[string]types.Validator
	projectedPowers map[string]int64
	candidates      []types.Validator
}

func (k *StateKeeper) selectNativeValidators(ctx sdk.Context, eligible []string, deriveStakePower bool) (*nativeValidatorSelection, error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	bonded, err := k.bondedValidators(ctx)
	if err != nil {
		return nil, err
	}
	previous, err := k.previousPositivePowers(ctx)
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
		v, err := k.GetValidator(ctx, operator)
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
		_, ok := byOperator[operator]
		if !ok {
			v, err := k.GetValidator(ctx, operator)
			if err != nil {
				return nil, errors.New("eligible validator metadata is missing or invalid")
			}
			byOperator[operator] = v
		}
	}
	var projectedPowers map[string]int64
	if deriveStakePower {
		projectedPowers, err = k.stakeDerivedConsensusPowers(ctx, byOperator)
		if err != nil {
			return nil, err
		}
	}
	for _, operator := range eligible {
		v := byOperator[operator]
		if deriveStakePower {
			v.Stake = projectedPowers[operator]
		}
		if !v.Jailed && v.Stake > 0 && (!deriveStakePower || v.Online) {
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
	return &nativeValidatorSelection{
		params: params, bonded: bonded, previous: previous, byOperator: byOperator,
		projectedPowers: projectedPowers, candidates: candidates,
	}, nil
}

func (s *nativeValidatorSelection) verifyPreviousPowers(effectiveNext []types.Validator) error {
	if len(s.previous) != len(effectiveNext) {
		return errors.New("previous native powers differ from the authenticated next ABCI set")
	}
	for _, v := range effectiveNext {
		if s.previous[v.OperatorAddress] != v.Stake {
			return errors.New("previous native powers differ from the authenticated next ABCI set")
		}
	}
	return nil
}

func (k *StateKeeper) applyNativeValidatorSelection(ctx sdk.Context, selection *nativeValidatorSelection) ([]abci.ValidatorUpdate, error) {
	candidates, previous, byOperator := selection.candidates, selection.previous, selection.byOperator
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
	unionOperators := sortedDelegationKeys(byOperator)
	finalValidators := make([]types.Validator, 0, len(unionOperators))
	for _, operator := range unionOperators {
		v := byOperator[operator]
		if power, ok := selection.projectedPowers[operator]; ok {
			v.Stake = power
		}
		if next[v.OperatorAddress] > 0 {
			v.Status = types.BondStatus_Bonded
		} else if v.Status == types.BondStatus_Bonded {
			v.Status = types.BondStatus_Unbonded
		}
		finalValidators = append(finalValidators, v)
	}
	if err := (types.GenesisState{Params: selection.params, Validators: finalValidators}).Validate(); err != nil {
		return nil, err
	}
	for _, v := range finalValidators {
		old := byOperator[v.OperatorAddress]
		if old.Status == v.Status && old.Stake == v.Stake {
			continue
		}
		key, err := operatorKey(validatorPrefix, v.OperatorAddress)
		if err != nil {
			return nil, err
		}
		if old.Stake != v.Stake {
			dynamicKey := bytes.Clone(key)
			dynamicKey[0] = dynamicPrefix
			row := &types.ValidatorRS{Rewards: v.Rewards, TotalRewards: v.TotalRewards, Stake: v.Stake}
			if err := k.put(ctx, dynamicKey, row); err != nil {
				return nil, err
			}
		}
		if old.Status != v.Status {
			v.Rewards, v.TotalRewards, v.Stake = sdkmath.ZeroInt(), sdkmath.ZeroInt(), 0
			if err := k.put(ctx, key, &v); err != nil {
				return nil, err
			}
		}
	}
	if err := k.replaceBondedMembers(ctx, selection.bonded, candidates); err != nil {
		return nil, err
	}
	if err := k.SetLastValidatorPowers(ctx, powers); err != nil {
		return nil, err
	}
	return updates, nil
}
