package keeper

import (
	"bytes"
	"errors"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// GatewayDELStake is the authenticated post-EVM state of one unheld, direct
// DEL position. The caller must authenticate the Gateway, its escrow and the
// receipt before projecting in the same transaction cache.
type GatewayDELStake struct {
	Gateway, WrappedDEL, Beneficiary, Validator common.Address
	StakeID, PenaltyCursor                      common.Hash
	Amount                                      sdkmath.Int
}

const gatewayDELCoinBinding = types.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN

func (s GatewayDELStake) nativePair() (string, string, error) {
	var zeroWords [64]byte // token ID and hold timestamp in Gateway getStakeId.
	if s.Gateway == (common.Address{}) || s.WrappedDEL == (common.Address{}) ||
		s.Beneficiary == (common.Address{}) || s.Validator == (common.Address{}) ||
		s.PenaltyCursor != (common.Hash{}) || s.Amount.IsNil() || s.Amount.IsNegative() ||
		s.StakeID != crypto.Keccak256Hash(s.Validator[:], s.Beneficiary[:], s.WrappedDEL[:], zeroWords[:]) {
		return "", "", errors.New("invalid unheld Gateway DEL position or unsupported penalty cursor")
	}
	owner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(s.Beneficiary[:])
	if err != nil {
		return "", "", err
	}
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(s.Validator[:])
	return owner, operator, err
}

func (s GatewayDELStake) binding(amount sdkmath.Int, created, updated int64) types.EVMStakeBinding {
	return types.EVMStakeBinding{
		Gateway: s.Gateway.Bytes(), Kind: gatewayDELCoinBinding, StakeID: s.StakeID.Bytes(),
		Validator: s.Validator.Bytes(), Beneficiary: s.Beneficiary.Bytes(), TokenContract: s.WrappedDEL.Bytes(),
		TokenID: make([]byte, 32), HoldTimestamp: make([]byte, 32), RawHoldStartTime: make([]byte, 32),
		PenaltyCursor: s.PenaltyCursor.Bytes(), RawAmount: amount, RawNftBackedAmount: sdkmath.ZeroInt(),
		ReportedReserveAmount: sdkmath.ZeroInt(), Active: true, CreatedHeight: created, UpdatedHeight: updated,
		ReserveAsset: &types.EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom},
		NativeTarget: &types.NativeStakeTarget{StakeType: types.StakeType_Coin, ID: identity.BaseDenom},
	}
}

func (k *StateKeeper) checkGatewayDELPrestate(ctx sdk.Context, next GatewayDELStake, amount sdkmath.Int, owner, operator string) (*types.EVMStakeBinding, *types.Delegation, error) {
	active, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, next.Gateway[:], next.StakeID[:])
	if amount.IsZero() {
		if errors.Is(err, ErrNotFound) {
			return nil, nil, nil
		}
		if err == nil {
			err = errors.New("Gateway DEL position already has a native binding")
		}
		return nil, nil, err
	}
	if err != nil {
		return nil, nil, err
	}
	expected := next.binding(amount, active.CreatedHeight, active.UpdatedHeight)
	actualBytes, err := types.CanonicalBindingBytes(&active)
	if err != nil {
		return nil, nil, err
	}
	expectedBytes, err := types.CanonicalBindingBytes(&expected)
	if err != nil {
		return nil, nil, err
	}
	if active.UpdatedHeight > ctx.BlockHeight() || !bytes.Equal(actualBytes, expectedBytes) {
		return nil, nil, errors.New("Gateway DEL transition differs from the active native binding")
	}
	delegation, err := k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	if err != nil {
		return nil, nil, err
	}
	if !delegation.Stake.Equal(types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, amount))) {
		return nil, nil, errors.New("Gateway DEL transition differs from the native delegation")
	}
	return &active, &delegation, nil
}

// ApplyGatewayDELDelegation creates or increases one DEL position in the native ledger.
// It never creates bank balances or modifies SDK staking power.
func (k *StateKeeper) ApplyGatewayDELDelegation(ctx sdk.Context, next GatewayDELStake, deposited sdkmath.Int) error {
	owner, operator, err := next.nativePair()
	if err != nil {
		return err
	}
	if deposited.IsNil() || !deposited.IsPositive() || next.Amount.LT(deposited) {
		return errors.New("Gateway DEL position cannot account for the deposit")
	}
	child, commit := ctx.CacheContext()
	active, previous, err := k.checkGatewayDELPrestate(child, next, next.Amount.Sub(deposited), owner, operator)
	if err != nil {
		return err
	}
	created := ctx.BlockHeight()
	if active != nil {
		created = active.CreatedHeight
	}
	row := next.binding(next.Amount, created, ctx.BlockHeight())
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{{Stake: &row}})
	if err != nil {
		return err
	}
	delegation := types.Delegation{Delegator: owner, Validator: operator, Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, next.Amount))}
	if err := k.ApplyDelegationBatch(child, []DelegationChange{{Expected: previous, Next: &delegation}}); err != nil {
		return err
	}
	if err := k.ApplyBindingPlan(child, plan); err != nil {
		return err
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_del_delegated",
		sdk.NewAttribute("stake_id", next.StakeID.Hex()), sdk.NewAttribute("amount", deposited.String())))
	commit()
	return nil
}

// ApplyGatewayDELWithdrawal moves the withdrawn amount into the native queue
// and keeps any remaining direct DEL stake active under the same binding.
func (k *StateKeeper) ApplyGatewayDELWithdrawal(ctx sdk.Context, next GatewayDELStake, index common.Hash, amount sdkmath.Int, deadline time.Time) error {
	owner, operator, err := next.nativePair()
	if err != nil {
		return err
	}
	if amount.IsNil() || !amount.IsPositive() || index == common.MaxHash ||
		deadline.Unix() <= 0 || deadline.Nanosecond() != 0 || deadline.Unix() < ctx.BlockTime().Unix() {
		return errors.New("invalid Gateway DEL withdrawal or deadline")
	}
	before := next.Amount.BigInt()
	before.Add(before, amount.BigInt())
	if before.BitLen() > 256 {
		return errors.New("Gateway DEL withdrawal prestate exceeds 256 bits")
	}
	child, commit := ctx.CacheContext()
	active, delegation, err := k.checkGatewayDELPrestate(child, next, sdkmath.NewIntFromBigInt(before), owner, operator)
	if err != nil {
		return err
	}
	stake := types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, amount))
	if _, err := k.GetEVMFrozenBinding(child, next.Gateway[:], index[:]); err == nil {
		return errors.New("Gateway frozen ID already has a native binding")
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	entry := types.UndelegationEntry{CreationHeight: ctx.BlockHeight(), CompletionTime: deadline.UTC(), Stake: stake}
	entryHash, err := types.NativeQueueEntryHash(1, owner, operator, "", &entry)
	if err != nil {
		return err
	}
	frozen := types.EVMFrozenBinding{
		Gateway: active.Gateway, FrozenIndex: index.Bytes(), SourceKind: active.Kind, SourceStakeID: active.StakeID,
		SourceValidator: active.Validator, Beneficiary: active.Beneficiary, FreezeType: 1,
		CompletionTimestamp: common.BytesToHash(sdk.Uint64ToBigEndian(uint64(deadline.Unix()))).Bytes(), RawAmount: amount,
		ReportedReserveAmount: active.ReportedReserveAmount, ReserveAsset: active.ReserveAsset, NativeTarget: active.NativeTarget,
		NativeEntryHash: entryHash, Active: true, CreatedHeight: ctx.BlockHeight(), UpdatedHeight: ctx.BlockHeight(),
	}
	change := DelegationChange{Expected: delegation}
	var mutation BindingMutation
	if next.Amount.IsZero() {
		mutation.Remove = &BindingReference{Kind: active.Kind, Gateway: active.Gateway, ID: active.StakeID}
	} else {
		row := next.binding(next.Amount, active.CreatedHeight, ctx.BlockHeight())
		mutation.Stake = &row
		remaining := *delegation
		remaining.Stake = types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, next.Amount))
		change.Next = &remaining
	}
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{mutation, {Frozen: &frozen}})
	if err != nil {
		return err
	}
	if err := k.ApplyDelegationBatch(child, []DelegationChange{change}); err != nil {
		return err
	}
	if err := k.ApplyBindingPlan(child, plan); err != nil {
		return err
	}
	if err := k.appendGatewayDELUndelegation(child, owner, operator, entry); err != nil {
		return err
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_del_withdrawal_requested",
		sdk.NewAttribute("stake_id", next.StakeID.Hex()), sdk.NewAttribute("frozen_index", index.Hex()), sdk.NewAttribute("amount", amount.String())))
	commit()
	return nil
}

func (k *StateKeeper) appendGatewayDELUndelegation(ctx sdk.Context, owner, operator string, entry types.UndelegationEntry) error {
	key := composite(undelegationPrefix, owner, operator)
	pending := types.Undelegation{Delegator: owner, Validator: operator}
	if err := k.read(ctx, key, &pending); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	if pending.Delegator != owner || pending.Validator != operator || uint64(len(pending.Entries)) >= uint64(params.MaxEntries) {
		return errors.New("invalid or full native undelegation pair")
	}
	pending.Entries = append(pending.Entries, entry)
	return k.put(ctx, key, &pending)
}
