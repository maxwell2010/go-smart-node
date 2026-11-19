package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/big"

	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const (
	feeRewardOwnerPrefix           byte = 0x73
	feeRewardCountPrefix           byte = 0x74
	feeRewardDirtyOwnerPrefix      byte = 0x75
	feeRewardDirtyCommissionPrefix byte = 0x76
	feeRewardIndexMarker           byte = 0x77
)

var ErrNativeFeeRewardIndexUnavailable = errors.New("native fee reward owner index requires initialization or explicit migration")

func (k *StateKeeper) hasNativeFeeRewardIndex(ctx sdk.Context) (bool, error) {
	b, err := k.service.OpenKVStore(ctx).Get([]byte{feeRewardIndexMarker})
	if err != nil || b == nil {
		return false, err
	}
	if !bytes.Equal(b, []byte{1}) {
		return false, errors.New("invalid native fee reward index marker")
	}
	return true, nil
}

func feeRewardOwnerKey(prefix byte, d types.Delegation) ([]byte, error) {
	key, err := operatorKey(prefix, d.Validator)
	if err != nil {
		return nil, err
	}
	owner, err := feeRewardAddress(d.Delegator)
	if err != nil {
		return nil, err
	}
	return append(key, owner...), nil
}

func (k *StateKeeper) feeRewardOwnerCount(ctx sdk.Context, operator string) (uint32, error) {
	key, err := operatorKey(feeRewardCountPrefix, operator)
	if err != nil {
		return 0, err
	}
	b, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil || b == nil {
		return 0, err
	}
	if len(b) != 4 || binary.BigEndian.Uint32(b) == 0 {
		return 0, errors.New("invalid native fee reward owner census")
	}
	return binary.BigEndian.Uint32(b), nil
}

// updateNativeFeeRewardOwners maintains compact ownership weights from the
// same compare-and-swap batch as the native ledger. No delegation scan occurs.
func (k *StateKeeper) updateNativeFeeRewardOwners(ctx sdk.Context, changes []DelegationChange) error {
	indexed, err := k.hasNativeFeeRewardIndex(ctx)
	if err != nil || !indexed {
		return err
	}
	state := k.service.OpenKVStore(ctx)
	type delta struct {
		removed, added big.Int
		operator       string
	}
	deltas := map[string]*delta{}
	for _, change := range changes {
		for i, d := range []*types.Delegation{change.Expected, change.Next} {
			if d == nil || d.Stake.Stake.Denom != identity.BaseDenom || d.Stake.Stake.IsZero() {
				continue
			}
			key, err := feeRewardOwnerKey(feeRewardOwnerPrefix, *d)
			if err != nil {
				return err
			}
			v := deltas[string(key)]
			if v == nil {
				v = &delta{operator: d.Validator}
				deltas[string(key)] = v
			}
			if i == 0 {
				v.removed.Add(&v.removed, d.Stake.Stake.Amount.BigInt())
			} else {
				v.added.Add(&v.added, d.Stake.Stake.Amount.BigInt())
			}
		}
	}
	counts := map[string]uint32{}
	for _, key := range sortedDelegationKeys(deltas) {
		d := deltas[key]
		b, err := state.Get([]byte(key))
		if err != nil {
			return err
		}
		if b != nil && (len(b) != 32 || new(big.Int).SetBytes(b).Sign() == 0) {
			return errors.New("invalid native fee reward owner weight")
		}
		old := new(big.Int).SetBytes(b)
		if old.Cmp(&d.removed) < 0 {
			return errors.New("native fee reward owner index underflow")
		}
		next := new(big.Int).Add(new(big.Int).Sub(old, &d.removed), &d.added)
		if next.BitLen() > 256 {
			return errors.New("native fee reward owner weight exceeds uint256")
		}
		count, read := counts[d.operator]
		if !read {
			count, err = k.feeRewardOwnerCount(ctx, d.operator)
			if err != nil {
				return err
			}
		}
		if old.Sign() == 0 && next.Sign() > 0 {
			if count == ^uint32(0) {
				return errors.New("native fee reward owner census overflow")
			}
			count++
		}
		if old.Sign() > 0 && next.Sign() == 0 {
			if count == 0 {
				return errors.New("native fee reward owner census underflow")
			}
			count--
		}
		counts[d.operator] = count
		if err = k.setNativeFeeRewardTreeWeight(ctx, []byte(key)[1:21], []byte(key)[21:], old, next); err != nil {
			return err
		}
		if next.Sign() == 0 {
			err = state.Delete([]byte(key))
		} else {
			err = state.Set([]byte(key), next.FillBytes(make([]byte, 32)))
		}
		if err != nil {
			return err
		}
		dirty := []byte(key)
		dirty[0] = feeRewardDirtyOwnerPrefix
		if err = state.Set(dirty, []byte{1}); err != nil {
			return err
		}
		commission, err := operatorKey(feeRewardDirtyCommissionPrefix, d.operator)
		if err != nil {
			return err
		}
		if err = state.Set(commission, []byte{1}); err != nil {
			return err
		}
	}
	for _, operator := range sortedDelegationKeys(counts) {
		key, err := operatorKey(feeRewardCountPrefix, operator)
		if err != nil {
			return err
		}
		count := counts[operator]
		if count == 0 {
			err = state.Delete(key)
		} else {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], count)
			err = state.Set(key, b[:])
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (k *StateKeeper) markNativeFeeRewardCommission(ctx sdk.Context, operator string) error {
	indexed, err := k.hasNativeFeeRewardIndex(ctx)
	if err != nil || !indexed {
		return err
	}
	key, err := operatorKey(feeRewardDirtyCommissionPrefix, operator)
	if err != nil {
		return err
	}
	return k.service.OpenKVStore(ctx).Set(key, []byte{1})
}

func (k *StateKeeper) initNativeFeeRewardIndex(ctx sdk.Context, validators []types.Validator, rows []types.Delegation) error {
	state := k.service.OpenKVStore(ctx)
	if err := state.Set([]byte{feeRewardIndexMarker}, []byte{1}); err != nil {
		return err
	}
	// Genesis is already validated. Bound each update by the ordinary batch
	// limit without imposing a global pilot position limit.
	for start := 0; start < len(rows); start += MaxDelegationBatch {
		end := start + MaxDelegationBatch
		if end > len(rows) {
			end = len(rows)
		}
		changes := make([]DelegationChange, end-start)
		for i := start; i < end; i++ {
			changes[i-start].Next = &rows[i]
		}
		if err := k.updateNativeFeeRewardOwners(ctx, changes); err != nil {
			return err
		}
	}
	for _, v := range validators {
		if err := k.markNativeFeeRewardCommission(ctx, v.OperatorAddress); err != nil {
			return err
		}
	}
	return k.initNativeFeeRewardTree(ctx)
}

// BuildNativeFeeRewardIndex is an explicit, all-or-nothing cold migration.
// It never repairs or overwrites an existing index.
func (k *StateKeeper) BuildNativeFeeRewardIndex(ctx sdk.Context) error {
	child, commit := ctx.CacheContext()
	indexed, err := k.hasNativeFeeRewardIndex(child)
	if err != nil {
		return err
	}
	if indexed {
		return errors.New("native fee reward index already initialized")
	}
	g, err := k.ExportGenesis(child)
	if err != nil {
		return err
	}
	if err = k.initNativeFeeRewardIndex(child, g.Validators, g.Delegations); err != nil {
		return err
	}
	commit()
	return nil
}

// auditNativeFeeRewardIndex is intentionally a cold full-state reconciliation.
// Missing historical indexes permit the explicit migration, orphan rows do not.
func (k *StateKeeper) auditNativeFeeRewardIndex(ctx sdk.Context, rows []types.Delegation) error {
	indexed, err := k.hasNativeFeeRewardIndex(ctx)
	if err != nil {
		return err
	}
	if !indexed {
		for _, prefix := range []byte{feeRewardOwnerPrefix, feeRewardCountPrefix, feeRewardDirtyOwnerPrefix, feeRewardDirtyCommissionPrefix} {
			if err = k.scan(ctx, prefix, func(_, _ []byte) error { return errors.New("native fee reward rows exist without index marker") }); err != nil {
				return err
			}
		}
		return nil
	}
	expected := map[string]*big.Int{}
	counts := map[string]uint32{}
	for _, d := range rows {
		if d.Stake.Stake.Denom != identity.BaseDenom || d.Stake.Stake.IsZero() {
			continue
		}
		key, err := feeRewardOwnerKey(feeRewardOwnerPrefix, d)
		if err != nil {
			return err
		}
		if expected[string(key)] == nil {
			expected[string(key)] = new(big.Int)
			counts[d.Validator]++
		}
		expected[string(key)].Add(expected[string(key)], d.Stake.Stake.Amount.BigInt())
	}
	seen := 0
	if err = k.scan(ctx, feeRewardOwnerPrefix, func(key, b []byte) error {
		amount := expected[string(key)]
		if len(key) != 41 || len(b) != 32 || amount == nil || amount.Sign() <= 0 || amount.Cmp(new(big.Int).SetBytes(b)) != 0 {
			return errors.New("native fee reward owner index differs from native delegations")
		}
		seen++
		return nil
	}); err != nil {
		return err
	}
	if seen != len(expected) {
		return errors.New("missing native fee reward owner index row")
	}
	for _, operator := range sortedDelegationKeys(counts) {
		count, err := k.feeRewardOwnerCount(ctx, operator)
		if err != nil {
			return err
		}
		if count != counts[operator] {
			return errors.New("native fee reward owner census mismatch")
		}
	}
	seen = 0
	if err = k.scan(ctx, feeRewardCountPrefix, func(_, _ []byte) error { seen++; return nil }); err != nil {
		return err
	}
	if seen != len(counts) {
		return errors.New("orphan native fee reward owner census")
	}
	return nil
}

func (k *StateKeeper) readNativeFeeRewardOperator(ctx sdk.Context, operator string) (nativeFeeRewardPositions, error) {
	p := nativeFeeRewardPositions{owners: map[string]*big.Int{}, total: new(big.Int)}
	prefix, err := operatorKey(feeRewardOwnerPrefix, operator)
	if err != nil {
		return p, err
	}
	count, err := k.feeRewardOwnerCount(ctx, operator)
	if err != nil {
		return p, err
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return p, err
	}
	if count > params.MaxDelegations {
		return p, errors.New("native fee reward owner census exceeds delegation limit")
	}
	err = k.scanPrefix(ctx, prefix, func(key, b []byte) error {
		if len(key) != 41 || len(b) != 32 || new(big.Int).SetBytes(b).Sign() == 0 {
			return errors.New("invalid native fee reward owner row")
		}
		if len(p.owners) >= int(count) {
			return errors.New("native fee reward owner census mismatch")
		}
		owner := string(key[21:])
		amount := new(big.Int).SetBytes(b)
		p.owners[owner] = amount
		p.total.Add(p.total, amount)
		return nil
	})
	if err == nil && len(p.owners) != int(count) {
		err = errors.New("native fee reward owner census mismatch")
	}
	if err == nil {
		var total *big.Int
		total, err = k.readActiveDELReserve(ctx, operator)
		if err == nil && total.Cmp(p.total) != 0 {
			err = errors.New("native fee reward positions differ from the indexed active DEL reserve")
		}
	}
	return p, err
}

// ValidateNativeFeeRewardChanges checks only recipients touched since the last
// successful admission. A failed policy check commits no acknowledgements.
// The app must retain its full audit at genesis and when changing the policy.
func (k *StateKeeper) ValidateNativeFeeRewardChanges(ctx sdk.Context, allowed func(sdk.AccAddress) bool) error {
	indexed, err := k.hasNativeFeeRewardIndex(ctx)
	if err != nil {
		return err
	}
	if !indexed {
		return ErrNativeFeeRewardIndexUnavailable
	}
	if allowed == nil {
		return errors.New("native fee reward admission requires a recipient policy")
	}
	child, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	var remove [][]byte
	if err = k.scan(child, feeRewardDirtyOwnerPrefix, func(key, b []byte) error {
		if len(key) != 41 || !bytes.Equal(b, []byte{1}) {
			return errors.New("invalid native fee reward dirty owner")
		}
		live := bytes.Clone(key)
		live[0] = feeRewardOwnerPrefix
		value, err := state.Get(live)
		if err != nil {
			return err
		}
		if value != nil && (len(value) != 32 || new(big.Int).SetBytes(value).Sign() == 0) {
			return errors.New("invalid native fee reward owner weight")
		}
		if value != nil && !allowed(sdk.AccAddress(key[21:])) {
			return errors.New("native fee reward owner address is not admitted")
		}
		remove = append(remove, bytes.Clone(key))
		return nil
	}); err != nil {
		return err
	}
	if err = k.scan(child, feeRewardDirtyCommissionPrefix, func(key, b []byte) error {
		if len(key) != 21 || !bytes.Equal(b, []byte{1}) {
			return errors.New("invalid native fee reward dirty commission")
		}
		operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(key[1:])
		if err != nil {
			return err
		}
		v, err := k.GetValidator(child, operator)
		if err != nil {
			return err
		}
		a, err := feeRewardAddress(v.RewardAddress)
		if err != nil {
			return err
		}
		if !allowed(a) {
			return errors.New("native fee reward commission address is not admitted")
		}
		remove = append(remove, bytes.Clone(key))
		return nil
	}); err != nil {
		return err
	}
	for _, key := range remove {
		if err = state.Delete(key); err != nil {
			return err
		}
	}
	commit()
	return nil
}
