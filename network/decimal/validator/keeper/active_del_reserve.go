package keeper

import (
	"bytes"
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const (
	activeDELReservePrefix byte = 0x26
	activeDELReserveMarker byte = 0x27
	activeDELReserveBytes       = 32
)

var ErrActiveDELReserveIndexUnavailable = errors.New("active DEL reserve index requires initialization")

func (k *StateKeeper) hasActiveDELReserveIndex(ctx sdk.Context) (bool, error) {
	data, err := k.service.OpenKVStore(ctx).Get([]byte{activeDELReserveMarker})
	if err != nil || data == nil {
		return false, err
	}
	if !bytes.Equal(data, []byte{1}) {
		return false, errors.New("invalid active DEL reserve index marker")
	}
	return true, nil
}

// GetActiveDELReserve returns exact active DEL atoms for one validator. A
// known validator has an explicit row even at zero. The index is independent
// of consensus power, bank balances and other denominations.
func (k *StateKeeper) GetActiveDELReserve(ctx sdk.Context, operator string) (sdkmath.Int, error) {
	indexed, err := k.hasActiveDELReserveIndex(ctx)
	if err != nil {
		return sdkmath.Int{}, err
	}
	if !indexed {
		return sdkmath.Int{}, ErrActiveDELReserveIndexUnavailable
	}
	if _, err := k.GetValidator(ctx, operator); err != nil {
		return sdkmath.Int{}, err
	}
	value, err := k.readActiveDELReserve(ctx, operator)
	if err != nil {
		return sdkmath.Int{}, err
	}
	return sdkmath.NewIntFromBigInt(value), nil
}

func (k *StateKeeper) readActiveDELReserve(ctx sdk.Context, operator string) (*big.Int, error) {
	key, err := operatorKey(activeDELReservePrefix, operator)
	if err != nil {
		return nil, err
	}
	data, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return nil, err
	}
	if len(data) != activeDELReserveBytes {
		return nil, errors.New("missing or invalid active DEL reserve index row")
	}
	return new(big.Int).SetBytes(data), nil
}

func (k *StateKeeper) writeActiveDELReserve(ctx sdk.Context, operator string, value *big.Int) error {
	if value.Sign() < 0 || value.BitLen() > activeDELReserveBytes*8 {
		return errors.New("active DEL reserve is outside uint256 bounds")
	}
	key, err := operatorKey(activeDELReservePrefix, operator)
	if err != nil {
		return err
	}
	var data [activeDELReserveBytes]byte
	return k.service.OpenKVStore(ctx).Set(key, value.FillBytes(data[:]))
}

func (k *StateKeeper) registerActiveDELReserveValidator(ctx sdk.Context, operator string) error {
	indexed, err := k.hasActiveDELReserveIndex(ctx)
	if err != nil || !indexed {
		return err
	}
	key, err := operatorKey(activeDELReservePrefix, operator)
	if err != nil {
		return err
	}
	state := k.service.OpenKVStore(ctx)
	present, err := state.Has(key)
	if err != nil {
		return err
	}
	if present {
		return errors.New("active DEL reserve row already exists for a new validator")
	}
	return state.Set(key, make([]byte, activeDELReserveBytes))
}

func activeDELTotals(validators []types.Validator, delegations []types.Delegation) (map[string]*big.Int, error) {
	totals := make(map[string]*big.Int, len(validators))
	for _, v := range validators {
		totals[v.OperatorAddress] = new(big.Int)
	}
	for _, d := range delegations {
		stake := d.Stake.Stake
		if stake.Denom != identity.BaseDenom {
			continue
		}
		total := totals[d.Validator]
		if total == nil {
			return nil, errors.New("active DEL delegation references an unknown validator")
		}
		total.Add(total, stake.Amount.BigInt())
		if total.BitLen() > activeDELReserveBytes*8 {
			return nil, errors.New("active DEL reserve exceeds uint256")
		}
	}
	return totals, nil
}

func (k *StateKeeper) initActiveDELReserveIndex(ctx sdk.Context, validators []types.Validator, delegations []types.Delegation) error {
	totals, err := activeDELTotals(validators, delegations)
	if err != nil {
		return err
	}
	for _, operator := range sortedDelegationKeys(totals) {
		if err := k.writeActiveDELReserve(ctx, operator, totals[operator]); err != nil {
			return err
		}
	}
	return k.service.OpenKVStore(ctx).Set([]byte{activeDELReserveMarker}, []byte{1})
}

// BuildActiveDELReserveIndex is a one-time cold migration for older initialized
// stores. It audits the native ledger before deriving new rows and does not
// replace or repair an already initialized index.
func (k *StateKeeper) BuildActiveDELReserveIndex(ctx sdk.Context) error {
	child, commit := ctx.CacheContext()
	indexed, err := k.hasActiveDELReserveIndex(child)
	if err != nil {
		return err
	}
	if indexed {
		return errors.New("active DEL reserve index already initialized")
	}
	g, err := k.ExportGenesis(child)
	if err != nil {
		return err
	}
	if err := k.initActiveDELReserveIndex(child, g.Validators, g.Delegations); err != nil {
		return err
	}
	commit()
	return nil
}

func (k *StateKeeper) validateActiveDELReserveIndex(ctx sdk.Context, validators []types.Validator, delegations []types.Delegation) error {
	indexed, err := k.hasActiveDELReserveIndex(ctx)
	if err != nil {
		return err
	}
	if !indexed {
		return k.scan(ctx, activeDELReservePrefix, func(_, _ []byte) error {
			return errors.New("active DEL reserve rows exist without the index marker")
		})
	}
	totals, err := activeDELTotals(validators, delegations)
	if err != nil {
		return err
	}
	for _, operator := range sortedDelegationKeys(totals) {
		got, err := k.readActiveDELReserve(ctx, operator)
		if err != nil {
			return err
		}
		if got.Cmp(totals[operator]) != 0 {
			return errors.New("active DEL reserve index differs from native delegations")
		}
	}
	var rows int
	if err := k.scan(ctx, activeDELReservePrefix, func(_, _ []byte) error { rows++; return nil }); err != nil {
		return err
	}
	if rows != len(totals) {
		return errors.New("orphan active DEL reserve index row")
	}
	return nil
}

type activeDELReserveDelta struct {
	removed, added big.Int
}

func recordActiveDELReserveDelta(deltas map[string]*activeDELReserveDelta, d types.Delegation, replacing bool) {
	stake := d.Stake.Stake
	if deltas == nil || stake.Denom != identity.BaseDenom || stake.IsZero() {
		return
	}
	delta := deltas[d.Validator]
	if delta == nil {
		delta = new(activeDELReserveDelta)
		deltas[d.Validator] = delta
	}
	if replacing {
		delta.removed.Add(&delta.removed, stake.Amount.BigInt())
	} else {
		delta.added.Add(&delta.added, stake.Amount.BigInt())
	}
}

func (k *StateKeeper) applyActiveDELReserveDeltas(ctx sdk.Context, deltas map[string]*activeDELReserveDelta) error {
	for _, operator := range sortedDelegationKeys(deltas) {
		delta := deltas[operator]
		total, err := k.readActiveDELReserve(ctx, operator)
		if err != nil {
			return err
		}
		if total.Cmp(&delta.removed) < 0 {
			return errors.New("active DEL reserve index underflows the native prestate")
		}
		total.Sub(total, &delta.removed)
		total.Add(total, &delta.added)
		if err := k.writeActiveDELReserve(ctx, operator, total); err != nil {
			return err
		}
	}
	return nil
}
