package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"sort"
)

// InitGenesis is all-or-nothing in the SDK cache. Full cross-module reserve and
// ownership reconciliation is an application prerequisite, not a fabricated mint.
func (k *StateKeeper) InitGenesis(ctx sdk.Context, g *types.GenesisState) error {
	if g == nil {
		return errors.New("validator genesis is missing")
	}
	prepared, err := k.prepareEVMGenesis(ctx, g)
	if err != nil {
		return err
	}
	g = prepared
	child, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	it, e := state.Iterator(nil, nil)
	if e != nil {
		return e
	}
	occupied := it.Valid()
	if occupied {
		e = it.Error()
	}
	closeErr := it.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if occupied {
		return errors.New("validator store is not empty")
	}
	if e = k.SetParams(child, g.Params); e != nil {
		return e
	}
	if e = k.initConsensusIndexes(child); e != nil {
		return e
	}
	for _, v := range g.Validators {
		if e = k.SetValidator(child, v); e != nil {
			return e
		}
	}
	for _, d := range g.Delegations {
		if e = k.put(child, delegationKey(d), &d); e != nil {
			return e
		}
	}
	if e = k.initDelegationIndexes(child, g.Delegations); e != nil {
		return e
	}
	for _, u := range g.Undelegations {
		if e = k.put(child, composite(undelegationPrefix, u.Delegator, u.Validator), &u); e != nil {
			return e
		}
	}
	for _, r := range g.Redelegations {
		if e = k.put(child, composite(redelegationPrefix, r.Delegator, r.ValidatorSrc, r.ValidatorDst), &r); e != nil {
			return e
		}
	}
	if e = k.SetLastValidatorPowers(child, g.LastValidatorPowers); e != nil {
		return e
	}
	for _, o := range g.ValidatorOfflineSince {
		key, e := operatorKey(offlinePrefix, o.ValidatorAddress)
		if e != nil {
			return e
		}
		if e = k.put(child, key, &o); e != nil {
			return e
		}
	}
	for _, row := range g.ScheduledValidatorSets {
		if e = k.writeScheduledSet(child, row); e != nil {
			return e
		}
	}
	if e = k.initEVMGenesis(child, g); e != nil {
		return e
	}
	flag := byte(0)
	if g.Exported {
		flag = 1
	}
	if e = state.Set([]byte{2}, []byte{flag}); e != nil {
		return e
	}
	if e = state.Set([]byte{4}, []byte{1}); e != nil {
		return e
	}
	commit()
	return nil
}

func (k *StateKeeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	return k.exportGenesis(ctx, true)
}

func (k *StateKeeper) exportGenesis(ctx sdk.Context, auditIndexes bool) (*types.GenesisState, error) {
	marker, e := k.service.OpenKVStore(ctx).Get([]byte{4})
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(marker, []byte{1}) {
		return nil, errors.New("validator genesis is not initialized")
	}
	if e = k.checkEVMExportDomain(ctx); e != nil {
		return nil, e
	}
	p, e := k.GetParams(ctx)
	if e != nil {
		return nil, e
	}
	vals, e := k.GetValidators(ctx)
	if e != nil {
		return nil, e
	}
	g := &types.GenesisState{Params: p, Validators: vals}
	decode := func(key, data []byte, m proto.Message, expected func() ([]byte, error)) error {
		if len(data) > MaxRecordBytes {
			return errors.New("native validator record exceeds size limit")
		}
		if e := proto.Unmarshal(data, m); e != nil {
			return e
		}
		want, e := expected()
		if e != nil {
			return e
		}
		if !bytes.Equal(key, want) {
			return errors.New("native record key mismatch")
		}
		return nil
	}
	if e = k.scan(ctx, delegationPrefix, func(key, data []byte) error {
		var d types.Delegation
		if e := decode(key, data, &d, func() ([]byte, error) { return delegationKey(d), nil }); e != nil {
			return e
		}
		g.Delegations = append(g.Delegations, d)
		return nil
	}); e != nil {
		return nil, e
	}
	if e = k.scan(ctx, undelegationPrefix, func(key, data []byte) error {
		var u types.Undelegation
		if e := decode(key, data, &u, func() ([]byte, error) { return composite(undelegationPrefix, u.Delegator, u.Validator), nil }); e != nil {
			return e
		}
		g.Undelegations = append(g.Undelegations, u)
		return nil
	}); e != nil {
		return nil, e
	}
	if e = k.scan(ctx, redelegationPrefix, func(key, data []byte) error {
		var r types.Redelegation
		if e := decode(key, data, &r, func() ([]byte, error) {
			return composite(redelegationPrefix, r.Delegator, r.ValidatorSrc, r.ValidatorDst), nil
		}); e != nil {
			return e
		}
		g.Redelegations = append(g.Redelegations, r)
		return nil
	}); e != nil {
		return nil, e
	}
	if e = k.scan(ctx, powerPrefix, func(key, data []byte) error {
		var p types.LastValidatorPower
		if e := decode(key, data, &p, func() ([]byte, error) { return operatorKey(powerPrefix, p.Address) }); e != nil {
			return e
		}
		g.LastValidatorPowers = append(g.LastValidatorPowers, p)
		return nil
	}); e != nil {
		return nil, e
	}
	if e = k.scan(ctx, offlinePrefix, func(key, data []byte) error {
		var o types.ValidatorOfflineSince
		if e := decode(key, data, &o, func() ([]byte, error) { return operatorKey(offlinePrefix, o.ValidatorAddress) }); e != nil {
			return e
		}
		g.ValidatorOfflineSince = append(g.ValidatorOfflineSince, o)
		return nil
	}); e != nil {
		return nil, e
	}
	if e = k.scan(ctx, schedulePrefix, func(key, data []byte) error {
		row, err := decodeScheduledSet(key, data)
		if err != nil {
			return err
		}
		g.ScheduledValidatorSets = append(g.ScheduledValidatorSets, row)
		return nil
	}); e != nil {
		return nil, e
	}
	data, e := k.service.OpenKVStore(ctx).Get([]byte{3})
	if e != nil {
		return nil, e
	}
	if len(data) != 8 {
		return nil, errors.New("native total power is missing or malformed")
	}
	g.LastTotalPower = int64(binary.BigEndian.Uint64(data))
	flag, e := k.service.OpenKVStore(ctx).Get([]byte{2})
	if e != nil {
		return nil, e
	}
	if len(flag) != 1 || flag[0] > 1 {
		return nil, errors.New("invalid genesis export flag")
	}
	g.Exported = flag[0] == 1
	if e = k.exportEVMGenesis(ctx, g); e != nil {
		return nil, e
	}
	if e = g.Validate(); e != nil {
		return nil, fmt.Errorf("invalid native export: %w", e)
	}
	if e = k.validateDelegationIndexes(ctx, g.Delegations); e != nil {
		return nil, e
	}
	if auditIndexes {
		if e = k.auditConsensusIndexes(ctx, g); e != nil {
			return nil, e
		}
	}
	return g, nil
}

// SetDelegation persists native totals/holds only. It never transfers reserves
// or recomputes CRR/NFT voting power; those require native financial execution.
func (k *StateKeeper) SetDelegation(ctx sdk.Context, d types.Delegation) error {
	var previous types.Delegation
	var expected *types.Delegation
	err := k.read(ctx, delegationKey(d), &previous)
	if err == nil {
		expected = &previous
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return k.ApplyDelegationBatch(ctx, []DelegationChange{{Expected: expected, Next: &d}})
}

func (k *StateKeeper) SetLastValidatorPowers(ctx sdk.Context, powers []types.LastValidatorPower) error {
	child, commit := ctx.CacheContext()
	previous, e := k.previousPositivePowers(child)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	var positive []types.LastValidatorPower
	var total int64
	for _, v := range powers {
		if seen[v.Address] {
			return errors.New("duplicate previous native validator power")
		}
		seen[v.Address] = true
		if _, e := k.GetValidator(child, v.Address); e != nil {
			return e
		}
		if v.Power < 0 || v.Power > cmttypes.MaxTotalVotingPower-total {
			return errors.New("invalid native total power")
		}
		total += v.Power
		if v.Power > 0 {
			positive = append(positive, v)
		}
	}
	state := k.service.OpenKVStore(child)
	operators := make([]string, 0, len(previous))
	for operator := range previous {
		operators = append(operators, operator)
	}
	sort.Strings(operators)
	for _, operator := range operators {
		key, _ := operatorKey(powerPrefix, operator)
		if e = state.Delete(key); e != nil {
			return e
		}
		key, _ = operatorKey(positivePowerPrefix, operator)
		if e = state.Delete(key); e != nil {
			return e
		}
	}
	for _, v := range positive {
		key, e := operatorKey(powerPrefix, v.Address)
		if e != nil {
			return e
		}
		if e = k.put(child, key, &v); e != nil {
			return e
		}
		key, _ = operatorKey(positivePowerPrefix, v.Address)
		var value [8]byte
		binary.BigEndian.PutUint64(value[:], uint64(v.Power))
		if e = state.Set(key, value[:]); e != nil {
			return e
		}
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(total))
	if e = state.Set([]byte{3}, encoded[:]); e != nil {
		return e
	}
	commit()
	return nil
}
