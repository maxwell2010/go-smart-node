// Package keeper stages native validator state for the separate network.
// It installs no financial message routes or consensus lifecycle handlers.
package keeper

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	coreaddress "cosmossdk.io/core/address"
	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/compat"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

var ErrNotFound = errors.New("native validator record does not exist")

const MaxRecordBytes = 64 * 1024
const (
	validatorPrefix    byte = 0x10
	dynamicPrefix      byte = 0x11
	consensusPrefix    byte = 0x12
	delegationPrefix   byte = 0x20
	undelegationPrefix byte = 0x21
	redelegationPrefix byte = 0x22
	powerPrefix        byte = 0x30
	offlinePrefix      byte = 0x31
	historyPrefix      byte = 0x40
)

type StateKeeper struct {
	service store.KVStoreService
	params  compat.ParamsStore[*types.Params]
}

func NewStateKeeper(service store.KVStoreService) (*StateKeeper, error) {
	p, e := compat.NewParamsStore(service, []byte{1}, &types.Params{}, func(p *types.Params) error { return p.Validate() })
	if e != nil {
		return nil, e
	}
	return &StateKeeper{service: service, params: p}, nil
}
func (k *StateKeeper) GetParams(ctx sdk.Context) (types.Params, error) {
	p, e := k.params.Get(ctx)
	if e != nil {
		return types.Params{}, e
	}
	return *p, nil
}
func (k *StateKeeper) SetParams(ctx sdk.Context, p types.Params) error {
	if e := p.Validate(); e != nil {
		return e
	}
	old, e := k.params.Get(ctx)
	if e != nil && !errors.Is(e, compat.ErrMissingParams) {
		return e
	}
	if e == nil && old.BaseDenom != p.BaseDenom {
		return errors.New("base denomination is immutable")
	}
	marker, e := k.service.OpenKVStore(ctx).Get([]byte{4})
	if e != nil {
		return e
	}
	if marker != nil {
		g, e := k.ExportGenesis(ctx)
		if e != nil {
			return e
		}
		g.Params = p
		if e = g.Validate(); e != nil {
			return e
		}
	}
	return k.params.Set(ctx, &p)
}
func (k *StateKeeper) ValidatorAddressCodec() coreaddress.Codec {
	return address.NewBech32Codec(identity.Bech32PrefixValAddr)
}
func (k *StateKeeper) BondDenom(ctx context.Context) (string, error) {
	p, e := k.GetParams(sdk.UnwrapSDKContext(ctx))
	return p.BaseDenom, e
}
func (k *StateKeeper) UnbondingTime(ctx context.Context) (time.Duration, error) {
	p, e := k.GetParams(sdk.UnwrapSDKContext(ctx))
	return p.UndelegationTime, e
}

func operatorKey(prefix byte, operator string) ([]byte, error) {
	data, e := address.NewBech32Codec(identity.Bech32PrefixValAddr).StringToBytes(operator)
	if e != nil {
		return nil, e
	}
	if len(data) != 20 {
		return nil, errors.New("operator address must contain 20 bytes")
	}
	return append([]byte{prefix}, data...), nil
}

// Composite keys use explicit lengths, so arbitrary native asset IDs cannot
// collide through separators. This is a new-network layout, not a legacy reader.
func composite(prefix byte, parts ...string) []byte {
	key := []byte{prefix}
	for _, part := range parts {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		key = append(key, size[:]...)
		key = append(key, part...)
	}
	return key
}
func delegationKey(d types.Delegation) []byte {
	return composite(delegationPrefix, d.Delegator, d.Validator, fmt.Sprint(d.Stake.Type), d.Stake.ID)
}
func (k *StateKeeper) read(ctx sdk.Context, key []byte, message proto.Message) error {
	data, e := k.service.OpenKVStore(ctx).Get(key)
	if e != nil {
		return e
	}
	if data == nil {
		return ErrNotFound
	}
	if len(data) > MaxRecordBytes {
		return errors.New("native validator record exceeds size limit")
	}
	return proto.Unmarshal(data, message)
}
func (k *StateKeeper) put(ctx sdk.Context, key []byte, message proto.Message) error {
	data, e := proto.Marshal(message)
	if e != nil {
		return e
	}
	if len(data) > MaxRecordBytes {
		return errors.New("native validator record exceeds size limit")
	}
	return k.service.OpenKVStore(ctx).Set(key, data)
}
func (k *StateKeeper) scan(ctx sdk.Context, prefix byte, visit func([]byte, []byte) error) error {
	it, e := k.service.OpenKVStore(ctx).Iterator([]byte{prefix}, []byte{prefix + 1})
	if e != nil {
		return e
	}
	for ; it.Valid(); it.Next() {
		if e = it.Error(); e != nil {
			_ = it.Close()
			return e
		}
		if e = visit(bytes.Clone(it.Key()), bytes.Clone(it.Value())); e != nil {
			_ = it.Close()
			return e
		}
	}
	return it.Close()
}
func (k *StateKeeper) GetValidator(ctx sdk.Context, operator string) (types.Validator, error) {
	key, e := operatorKey(validatorPrefix, operator)
	if e != nil {
		return types.Validator{}, e
	}
	var v types.Validator
	if e = k.read(ctx, key, &v); e != nil {
		return v, e
	}
	if v.OperatorAddress != operator {
		return types.Validator{}, errors.New("validator key mismatch")
	}
	if v.Rewards.IsNil() || v.TotalRewards.IsNil() || !v.Rewards.IsZero() || !v.TotalRewards.IsZero() || v.Stake != 0 {
		return types.Validator{}, errors.New("validator metadata contains unhydrated dynamic fields")
	}
	dynamicKey := bytes.Clone(key)
	dynamicKey[0] = dynamicPrefix
	var row types.ValidatorRS
	if e = k.read(ctx, dynamicKey, &row); e != nil {
		return types.Validator{}, fmt.Errorf("missing dynamic validator row: %w", e)
	}
	v.Rewards, v.TotalRewards, v.Stake = row.Rewards, row.TotalRewards, row.Stake
	if e = v.Validate(); e != nil {
		return types.Validator{}, e
	}
	consensus, e := v.GetConsAddr()
	if e != nil {
		return types.Validator{}, e
	}
	index, e := k.service.OpenKVStore(ctx).Get(append([]byte{consensusPrefix}, consensus...))
	if e != nil {
		return types.Validator{}, e
	}
	if string(index) != operator {
		return types.Validator{}, errors.New("validator consensus index mismatch")
	}
	return v, nil
}
func (k *StateKeeper) GetValidatorByConsAddr(ctx context.Context, consensus sdk.ConsAddress) (types.Validator, error) {
	if len(consensus) != 20 {
		return types.Validator{}, errors.New("consensus address must contain 20 bytes")
	}
	sdkctx := sdk.UnwrapSDKContext(ctx)
	data, e := k.service.OpenKVStore(sdkctx).Get(append([]byte{consensusPrefix}, consensus...))
	if e != nil {
		return types.Validator{}, e
	}
	if data == nil {
		return types.Validator{}, ErrNotFound
	}
	v, e := k.GetValidator(sdkctx, string(data))
	if e != nil {
		return v, e
	}
	actual, e := v.GetConsAddr()
	if e != nil {
		return types.Validator{}, e
	}
	if !bytes.Equal(actual, consensus) {
		return types.Validator{}, errors.New("consensus index mismatch")
	}
	return v, nil
}
func (k *StateKeeper) GetValidators(ctx sdk.Context) ([]types.Validator, error) {
	var vals []types.Validator
	e := k.scan(ctx, validatorPrefix, func(key, data []byte) error {
		var v types.Validator
		if len(data) > MaxRecordBytes {
			return errors.New("validator record exceeds size limit")
		}
		if e := proto.Unmarshal(data, &v); e != nil {
			return e
		}
		expected, e := operatorKey(validatorPrefix, v.OperatorAddress)
		if e != nil {
			return e
		}
		if !bytes.Equal(expected, key) {
			return errors.New("validator key mismatch")
		}
		v, e = k.GetValidator(ctx, v.OperatorAddress)
		if e != nil {
			return e
		}
		vals = append(vals, v)
		return nil
	})
	return vals, e
}

// SetValidator writes complete native metadata, its separately stored dynamic
// rewards/power, and consensus index in one child cache. No bank assets move.
func (k *StateKeeper) SetValidator(ctx sdk.Context, v types.Validator) error {
	if e := v.Validate(); e != nil {
		return e
	}
	child, commit := ctx.CacheContext()
	p, e := k.GetParams(child)
	if e != nil {
		return e
	}
	vals, e := k.GetValidators(child)
	if e != nil {
		return e
	}
	replaced := false
	for i, old := range vals {
		if old.OperatorAddress == v.OperatorAddress {
			oldKey, _ := old.GetConsAddr()
			newKey, _ := v.GetConsAddr()
			if !bytes.Equal(oldKey, newKey) {
				return errors.New("consensus key is immutable")
			}
			vals[i] = v
			replaced = true
		}
	}
	if !replaced {
		vals = append(vals, v)
	}
	g := types.GenesisState{Params: p, Validators: vals}
	if e = g.Validate(); e != nil {
		return e
	}
	key, e := operatorKey(validatorPrefix, v.OperatorAddress)
	if e != nil {
		return e
	}
	consensus, e := v.GetConsAddr()
	if e != nil {
		return e
	}
	index := append([]byte{consensusPrefix}, consensus...)
	stored, e := k.service.OpenKVStore(child).Get(index)
	if e != nil {
		return e
	}
	if stored != nil && string(stored) != v.OperatorAddress {
		return errors.New("consensus address belongs to another validator")
	}
	row := &types.ValidatorRS{Rewards: v.Rewards, TotalRewards: v.TotalRewards, Stake: v.Stake}
	// Encode the full row first, then clear only serialized metadata fields.
	dynamicKey := bytes.Clone(key)
	dynamicKey[0] = dynamicPrefix
	if e = k.put(child, dynamicKey, row); e != nil {
		return e
	}
	v.Rewards, v.TotalRewards, v.Stake = sdkmath.ZeroInt(), sdkmath.ZeroInt(), 0
	if e = k.put(child, key, &v); e != nil {
		return e
	}
	if e = k.service.OpenKVStore(child).Set(index, []byte(v.OperatorAddress)); e != nil {
		return e
	}
	commit()
	return nil
}
