package keeper

import (
	"bytes"
	"errors"
	"math/big"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const frozenDELPenaltyCursorPrefix byte = 0x5b

type frozenPenaltyMode uint8

const (
	frozenPenaltyRegular frozenPenaltyMode = iota
	frozenPenaltyHistorical
	frozenPenaltyCustodiedCheckpoint
)

func checkMatureCustodiedCheckpoint(entry *types.UndelegationEntry, infractionHeight int64, now time.Time) error {
	if entry.CreationHeight <= 0 || entry.CreationHeight >= infractionHeight {
		return errors.New("custodied checkpoint requires a claim created before the infraction")
	}
	if entry.CompletionTime.After(now) {
		return errors.New("custodied checkpoint requires a mature withdrawal claim")
	}
	return nil
}

func frozenDELPenaltyCursorKey(gateway, index []byte) []byte {
	key := append([]byte{frozenDELPenaltyCursorPrefix}, gateway...)
	return append(key, index...)
}

// ApplyGatewayFrozenDELPenalty projects an authenticated ordinary DEL frozen
// claim adjustment. The caller proves the EVM checkpoint and burn in the same
// outer cache. An ineligible claim may advance its cursor without losing DEL.
func (k *StateKeeper) ApplyGatewayFrozenDELPenalty(ctx sdk.Context, before types.EVMFrozenBinding, infractionHeight int64, oldCursor, newCursor common.Hash, remaining sdkmath.Int) error {
	return k.applyGatewayFrozenDELPenalty(ctx, before, nil, infractionHeight, oldCursor, newCursor, remaining, frozenPenaltyRegular)
}

// GatewayFrozenDELHold is the immutable held-claim identity from the verified
// Gateway getter. HoldStartTime is the source bucket's original start, which
// can precede the individual deposit lots selected for this withdrawal.
type GatewayFrozenDELHold struct {
	WrappedDEL                  common.Address
	HoldDeadline, HoldStartTime int64
}

// ApplyGatewayFrozenDELHoldPenalty reconciles one authenticated, unmatured
// held DEL claim and its matched native queue entry in the same outer cache
// as the verified Gateway checkpoint and burn.
func (k *StateKeeper) ApplyGatewayFrozenDELHoldPenalty(ctx sdk.Context, before types.EVMFrozenBinding, hold GatewayFrozenDELHold, infractionHeight int64, oldCursor, newCursor common.Hash, remaining sdkmath.Int) error {
	return k.applyGatewayFrozenDELPenalty(ctx, before, &hold, infractionHeight, oldCursor, newCursor, remaining, frozenPenaltyRegular)
}

// ApplyGatewayFrozenHistoricalDELPenalty projects the exact historical loss
// on a still-custodied claim. The App verifies the accepted evidence and the
// EVM burn in the same cache. The nominal unfreeze time can precede settlement.
func (k *StateKeeper) ApplyGatewayFrozenHistoricalDELPenalty(ctx sdk.Context, before types.EVMFrozenBinding, hold *GatewayFrozenDELHold, infractionHeight int64, oldCursor, newCursor common.Hash, remaining sdkmath.Int) error {
	return k.applyGatewayFrozenDELPenalty(ctx, before, hold, infractionHeight, oldCursor, newCursor, remaining, frozenPenaltyHistorical)
}

// ApplyGatewayCustodiedDELCheckpoint acknowledges one verified ordinary
// penalty on a mature claim that was already frozen before the infraction.
// Its original DEL and hold lots remain available for later withdrawal.
func (k *StateKeeper) ApplyGatewayCustodiedDELCheckpoint(ctx sdk.Context, before types.EVMFrozenBinding, hold *GatewayFrozenDELHold, infractionHeight int64, oldCursor, newCursor common.Hash) error {
	return k.applyGatewayFrozenDELPenalty(ctx, before, hold, infractionHeight, oldCursor, newCursor, before.RawAmount, frozenPenaltyCustodiedCheckpoint)
}

func (k *StateKeeper) applyGatewayFrozenDELPenalty(ctx sdk.Context, before types.EVMFrozenBinding, hold *GatewayFrozenDELHold, infractionHeight int64, oldCursor, newCursor common.Hash, remaining sdkmath.Int, mode frozenPenaltyMode) error {
	if err := before.Validate(); err != nil {
		return err
	}
	target := types.StakeType_Coin
	if hold != nil {
		target = types.StakeType_CoinHold
	}
	if !before.Active || before.FreezeType != 1 || before.SourceKind != gatewayDELCoinBinding ||
		before.NativeTarget.StakeType != target || before.ReserveAsset.ReserveKind != 1 ||
		before.ReserveAsset.NativeDenom != identity.BaseDenom || !before.RawAmount.IsPositive() ||
		remaining.IsNil() || remaining.IsNegative() || remaining.GT(before.RawAmount) ||
		infractionHeight <= 0 || infractionHeight > ctx.BlockHeight() || before.UpdatedHeight > ctx.BlockHeight() {
		if hold != nil {
			return errors.New("invalid held frozen DEL penalty transition")
		}
		return errors.New("invalid ordinary frozen DEL penalty transition")
	}
	if hold != nil {
		source := GatewayDELStake{
			Gateway: common.BytesToAddress(before.Gateway), WrappedDEL: hold.WrappedDEL,
			Beneficiary: common.BytesToAddress(before.Beneficiary), Validator: common.BytesToAddress(before.SourceValidator),
			StakeID: common.BytesToHash(before.SourceStakeID), Amount: before.RawAmount,
			HoldDeadline: hold.HoldDeadline, HoldStartTime: hold.HoldStartTime,
		}
		if _, _, err := source.nativePairIdentity(); err != nil || hold.HoldDeadline <= 0 || hold.HoldDeadline >= ctx.BlockTime().Unix() {
			return errors.New("frozen held DEL proof differs from its source stake identity or expired hold timing")
		}
	}
	if new(big.Int).Add(oldCursor.Big(), big.NewInt(1)).Cmp(newCursor.Big()) != 0 {
		return errors.New("frozen DEL penalty cursor must advance by exactly one")
	}
	child, commit := ctx.CacheContext()
	current, err := recordFromFrozen(&before)
	if err != nil {
		return err
	}
	actual, err := k.readBindingRow(child, current.key)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual.data, current.data) {
		return errors.New("frozen DEL penalty binding prestate changed")
	}
	state := k.service.OpenKVStore(child)
	cursorKey := frozenDELPenaltyCursorKey(before.Gateway, before.FrozenIndex)
	rawCursor, err := state.Get(cursorKey)
	if err != nil {
		return err
	}
	if (rawCursor != nil && len(rawCursor) != common.HashLength) || common.BytesToHash(rawCursor) != oldCursor {
		return errors.New("frozen DEL penalty cursor prestate changed")
	}
	key, pending, i, err := k.matchWithdrawalEntry(child, actual)
	if err != nil {
		return err
	}
	entry := &pending.Entries[i]
	historical := mode != frozenPenaltyRegular
	if entry.CreationHeight != before.CreatedHeight || (!historical && !entry.CompletionTime.After(ctx.BlockTime())) {
		return errors.New("frozen DEL penalty requires a matching creation height and an unmatured claim for regular penalties")
	}
	if mode == frozenPenaltyCustodiedCheckpoint {
		if err := checkMatureCustodiedCheckpoint(entry, infractionHeight, ctx.BlockTime()); err != nil {
			return err
		}
	}
	if hold != nil {
		if err := hold.verifyQueuedStake(entry.Stake); err != nil {
			return err
		}
	}
	loss := before.RawAmount.Sub(remaining)
	if entry.CreationHeight < infractionHeight && !loss.IsZero() {
		return errors.New("frozen DEL claim predates the infraction")
	}
	previousHolds := entry.Stake.Holds
	var nextHolds []*types.StakeHold
	var holdDeadline int64
	if hold != nil {
		holdDeadline = hold.HoldDeadline
	}
	var history DELHistoricalPenaltyPlan
	if historical {
		var tranches []DELProvenanceTranche
		history, tranches, err = k.planFrozenDELHistoricalPenalty(child, before, infractionHeight)
		if err != nil {
			return err
		}
		if !remaining.Equal(history.Remaining) {
			return errors.New("frozen DEL penalty differs from eligible historical principal")
		}
		if hold != nil {
			if err := matchDELProvenanceHoldLots(tranches, previousHolds, holdDeadline); err != nil {
				return err
			}
		}
	}
	after := before
	after.RawAmount, after.UpdatedHeight = remaining, ctx.BlockHeight()
	writeQueue := mode != frozenPenaltyCustodiedCheckpoint
	if remaining.IsZero() {
		after.Active = false
		pending.Entries = append(pending.Entries[:i], pending.Entries[i+1:]...)
	} else if writeQueue && (hold == nil || loss.IsPositive()) {
		if hold == nil {
			entry.Stake = types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, remaining))
		} else {
			if historical {
				entry.Stake.Holds, err = projectHistoricalDELHoldLots(previousHolds, holdDeadline, history.Tranches)
			} else {
				entry.Stake.Holds, err = reduceDELHoldLots(previousHolds, holdDeadline, before.RawAmount, loss)
			}
			if err != nil {
				return err
			}
			entry.Stake.Stake.Amount = remaining
		}
		after.NativeEntryHash, err = types.NativeQueueEntryHash(1, pending.Delegator, pending.Validator, "", entry)
		if err != nil {
			return err
		}
	}
	if remaining.IsPositive() {
		nextHolds = entry.Stake.Holds
	}
	if historical {
		if loss.IsPositive() {
			err = k.storeDELProvenance(child, delProvenanceKey(frozenDELProvenancePrefix, before.Gateway, before.FrozenIndex), history.Tranches, history.FrozenAt)
		}
	} else {
		err = k.recordFrozenDELProvenancePenalty(child, before, loss, holdDeadline, previousHolds, nextHolds)
	}
	if err != nil {
		return err
	}
	var pair []byte
	if writeQueue {
		pair, err = encodeWithdrawalPair(pending)
		if err != nil {
			return err
		}
	}
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{{Frozen: &after}})
	if err != nil {
		return err
	}
	if err := k.ApplyBindingPlan(child, plan); err != nil {
		return err
	}
	if writeQueue {
		if pair == nil {
			err = state.Delete(key)
		} else {
			err = state.Set(key, pair)
		}
		if err != nil {
			return err
		}
	}
	if after.Active {
		err = state.Set(cursorKey, newCursor[:])
	} else {
		err = state.Delete(cursorKey)
	}
	if err != nil {
		return err
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_frozen_del_penalty",
		sdk.NewAttribute("frozen_index", common.BytesToHash(before.FrozenIndex).Hex()),
		sdk.NewAttribute("penalty_cursor", newCursor.Hex()), sdk.NewAttribute("amount", loss.String())))
	commit()
	return nil
}

func (hold GatewayFrozenDELHold) verifyQueuedStake(stake types.Stake) error {
	total := sdkmath.ZeroInt()
	for i, lot := range stake.Holds {
		if lot == nil || lot.Amount.IsNil() || !lot.Amount.IsPositive() ||
			lot.HoldEndTime != hold.HoldDeadline || lot.HoldStartTime < hold.HoldStartTime ||
			(i > 0 && !holdLotBefore(stake.Holds[i-1], lot.HoldEndTime, lot.HoldStartTime)) {
			return errors.New("frozen held DEL lot differs from the source hold timing")
		}
		total = total.Add(lot.Amount)
	}
	if !total.IsPositive() || !total.Equal(stake.Stake.Amount) {
		return errors.New("frozen held DEL lots do not cover the queued amount")
	}
	return nil
}
