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

// ApplyGatewayDELDelegation creates one new DEL position in the native ledger.
// It never creates bank balances or modifies SDK staking power.
func (k *StateKeeper) ApplyGatewayDELDelegation(ctx sdk.Context, next GatewayDELStake, deposited sdkmath.Int) error {
	owner, operator, err := next.nativePair()
	if err != nil {
		return err
	}
	if deposited.IsNil() || !deposited.IsPositive() || !deposited.Equal(next.Amount) {
		return errors.New("new Gateway DEL position differs from the deposit")
	}
	child, commit := ctx.CacheContext()
	if _, err = k.GetEVMStakeBinding(child, gatewayDELCoinBinding, next.Gateway[:], next.StakeID[:]); err == nil {
		return errors.New("Gateway DEL position already has a native binding")
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	row := next.binding(deposited, ctx.BlockHeight(), ctx.BlockHeight())
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{{Stake: &row}})
	if err != nil {
		return err
	}
	delegation := types.Delegation{Delegator: owner, Validator: operator, Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, deposited))}
	if err := k.ApplyDelegationBatch(child, []DelegationChange{{Next: &delegation}}); err != nil {
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

// ApplyGatewayDELWithdrawal moves the full direct DEL position into the
// native queue and creates the matching frozen binding and maturity index.
func (k *StateKeeper) ApplyGatewayDELWithdrawal(ctx sdk.Context, next GatewayDELStake, index common.Hash, amount sdkmath.Int, deadline time.Time) error {
	owner, operator, err := next.nativePair()
	if err != nil {
		return err
	}
	if amount.IsNil() || !amount.IsPositive() || !next.Amount.IsZero() || index == common.MaxHash ||
		deadline.Unix() <= 0 || deadline.Nanosecond() != 0 || deadline.Unix() < ctx.BlockTime().Unix() {
		return errors.New("invalid full Gateway DEL withdrawal or deadline")
	}
	child, commit := ctx.CacheContext()
	active, err := k.GetEVMStakeBinding(child, gatewayDELCoinBinding, next.Gateway[:], next.StakeID[:])
	if err != nil {
		return err
	}
	expected := next.binding(amount, active.CreatedHeight, active.UpdatedHeight)
	actualBytes, err := types.CanonicalBindingBytes(&active)
	if err != nil {
		return err
	}
	expectedBytes, err := types.CanonicalBindingBytes(&expected)
	if err != nil {
		return err
	}
	if active.UpdatedHeight > ctx.BlockHeight() || !bytes.Equal(actualBytes, expectedBytes) {
		return errors.New("withdrawn DEL differs from the active native binding")
	}
	delegation, err := k.GetDelegation(child, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	if err != nil {
		return err
	}
	stake := types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, amount))
	if !delegation.Stake.Equal(stake) {
		return errors.New("withdrawn DEL differs from the native delegation")
	}
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
	remove := BindingReference{Kind: active.Kind, Gateway: active.Gateway, ID: active.StakeID}
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{{Remove: &remove}, {Frozen: &frozen}})
	if err != nil {
		return err
	}
	if err := k.ApplyDelegationBatch(child, []DelegationChange{{Expected: &delegation}}); err != nil {
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
