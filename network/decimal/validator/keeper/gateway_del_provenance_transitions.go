package keeper

import (
	"errors"
	"math/big"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

func (k *StateKeeper) activeDELProvenance(ctx sdk.Context, position GatewayDELStake, amount sdkmath.Int, delegation *types.Delegation) ([]byte, []DELProvenanceTranche, error) {
	key := delProvenanceKey(activeDELProvenancePrefix, position.Gateway[:], position.StakeID[:])
	lots, _, err := k.readDELProvenance(ctx, key, amount)
	if err != nil {
		return nil, nil, err
	}
	if position.HoldDeadline != 0 {
		if err := matchDELProvenanceHoldLots(lots, delegationHoldLots(delegation), position.HoldDeadline); err != nil {
			return nil, nil, err
		}
	}
	return key, lots, nil
}

func delegationHoldLots(delegation *types.Delegation) []*types.StakeHold {
	if delegation == nil {
		return nil
	}
	return delegation.Stake.Holds
}

// matchDELProvenanceHoldLots requires every selected native hold lot to equal
// its provenance parts. Multiple consensus blocks can share a Unix second.
func matchDELProvenanceHoldLots(lots []DELProvenanceTranche, holds []*types.StakeHold, deadline int64) error {
	for _, hold := range holds {
		if hold.HoldEndTime != deadline {
			continue
		}
		var sum big.Int
		for len(lots) != 0 && lots[0].FundedAt.Unix() == hold.HoldStartTime {
			sum.Add(&sum, lots[0].Amount.BigInt())
			lots = lots[1:]
		}
		if sum.Cmp(hold.Amount.BigInt()) != 0 {
			return errors.New("DEL provenance differs from a native hold lot")
		}
	}
	if len(lots) != 0 {
		return errors.New("DEL provenance has no matching native hold lot")
	}
	return nil
}

func (k *StateKeeper) recordDELProvenanceDeposit(ctx sdk.Context, position GatewayDELStake, previous, next *types.Delegation, deposited sdkmath.Int) error {
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	key, lots, err := k.activeDELProvenance(ctx, position, position.Amount.Sub(deposited), previous)
	if err != nil {
		return err
	}
	if ctx.BlockHeight() <= 0 {
		return errors.New("DEL deposit provenance requires a positive block height")
	}
	updated, err := combineDELProvenance(lots, []DELProvenanceTranche{{FundedHeight: ctx.BlockHeight(), FundedAt: ctx.BlockTime(), Amount: deposited}})
	if err != nil {
		return err
	}
	if len(updated) > len(lots) {
		count, err := k.countDELProvenanceTranches(ctx, position.Validator)
		if err != nil {
			return err
		}
		// Leave one slot for a partial withdrawal to split one funding tranche.
		if count >= maxDELProvenanceTranches-1 {
			return errors.New("DEL deposit exceeds the validator provenance tranche limit")
		}
	}
	if position.HoldDeadline != 0 {
		if err := matchDELProvenanceHoldLots(updated, delegationHoldLots(next), position.HoldDeadline); err != nil {
			return err
		}
	}
	return k.storeDELProvenance(ctx, key, updated, time.Time{})
}

func (k *StateKeeper) countDELProvenanceTranches(ctx sdk.Context, validator common.Address) (int, error) {
	active, activeNext, err := k.GetActiveStakePenaltyPage(ctx, validator[:], nil, maxDELProvenancePositions)
	if err != nil {
		return 0, err
	}
	frozen, frozenNext, err := k.GetValidatorFrozenBindingsPage(ctx, validator[:], nil, maxDELProvenancePositions)
	if err != nil {
		return 0, err
	}
	if activeNext != nil || frozenNext != nil || len(active)+len(frozen) > maxDELProvenancePositions {
		return 0, errors.New("DEL provenance exceeds the validator position limit")
	}
	count := 0
	for _, row := range active {
		if row.Kind == gatewayDELCoinBinding {
			lots, err := k.GetActiveDELProvenance(ctx, row)
			if err != nil {
				return 0, err
			}
			count += len(lots)
		}
	}
	for _, row := range frozen {
		if row.SourceKind == gatewayDELCoinBinding {
			lots, _, err := k.GetFrozenDELProvenance(ctx, row)
			if err != nil {
				return 0, err
			}
			count += len(lots)
		}
	}
	return count, nil
}

func (k *StateKeeper) recordDELProvenanceWithdrawal(ctx sdk.Context, position GatewayDELStake, frozen types.EVMFrozenBinding, entry types.Stake, change DelegationChange) error {
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	key, lots, err := k.activeDELProvenance(ctx, position, position.Amount.Add(frozen.RawAmount), change.Expected)
	if err != nil {
		return err
	}
	remaining, moved, err := splitDELProvenance(lots, frozen.RawAmount)
	if err != nil {
		return err
	}
	if position.HoldDeadline != 0 {
		if err := matchDELProvenanceHoldLots(moved, entry.Holds, position.HoldDeadline); err != nil {
			return err
		}
		if err := matchDELProvenanceHoldLots(remaining, delegationHoldLots(change.Next), position.HoldDeadline); err != nil {
			return err
		}
	}
	frozenKey := delProvenanceKey(frozenDELProvenancePrefix, frozen.Gateway, frozen.FrozenIndex)
	if _, _, err := k.readDELProvenance(ctx, frozenKey, sdkmath.ZeroInt()); err != nil {
		return err
	}
	if err := k.storeDELProvenance(ctx, key, remaining, time.Time{}); err != nil {
		return err
	}
	return k.storeDELProvenance(ctx, frozenKey, moved, ctx.BlockTime())
}

func splitDELProvenance(lots []DELProvenanceTranche, amount sdkmath.Int) (remaining, moved []DELProvenanceTranche, err error) {
	for i, lot := range lots {
		if amount.IsZero() {
			return lots[i:], moved, nil
		}
		if amount.LT(lot.Amount) {
			part, rest := lot, lot
			part.Amount, rest.Amount = amount, lot.Amount.Sub(amount)
			return append([]DELProvenanceTranche{rest}, lots[i+1:]...), append(moved, part), nil
		}
		moved = append(moved, lot)
		amount = amount.Sub(lot.Amount)
	}
	if !amount.IsZero() {
		return nil, nil, errors.New("DEL provenance does not cover the withdrawal")
	}
	return nil, moved, nil
}

func (k *StateKeeper) recordDELProvenanceHoldReset(ctx sdk.Context, held, ordinary GatewayDELStake, moved sdkmath.Int, previousHeld, previousPlain *types.Delegation) error {
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	heldKey, heldLots, err := k.activeDELProvenance(ctx, held, moved, previousHeld)
	if err != nil {
		return err
	}
	plainKey, plainLots, err := k.activeDELProvenance(ctx, ordinary, ordinary.Amount.Sub(moved), previousPlain)
	if err != nil {
		return err
	}
	updated, err := combineDELProvenance(plainLots, heldLots)
	if err != nil {
		return err
	}
	if err := k.storeDELProvenance(ctx, heldKey, nil, time.Time{}); err != nil {
		return err
	}
	return k.storeDELProvenance(ctx, plainKey, updated, time.Time{})
}

func (k *StateKeeper) recordActiveDELProvenancePenalty(ctx sdk.Context, position GatewayDELStake, change DelegationChange, loss sdkmath.Int) error {
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	key, lots, err := k.activeDELProvenance(ctx, position, position.Amount, change.Expected)
	if err != nil || loss.IsZero() {
		return err
	}
	updated, err := reduceDELProvenance(lots, position.Amount, loss, position.HoldDeadline, delegationHoldLots(change.Next))
	if err != nil {
		return err
	}
	return k.storeDELProvenance(ctx, key, updated, time.Time{})
}

func (k *StateKeeper) recordFrozenDELProvenancePenalty(ctx sdk.Context, row types.EVMFrozenBinding, loss sdkmath.Int, deadline int64, before, after []*types.StakeHold) error {
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	lots, frozenAt, err := k.GetFrozenDELProvenance(ctx, row)
	if err != nil {
		return err
	}
	if deadline != 0 {
		if err := matchDELProvenanceHoldLots(lots, before, deadline); err != nil {
			return err
		}
	}
	if loss.IsZero() {
		return nil
	}
	updated, err := reduceDELProvenance(lots, row.RawAmount, loss, deadline, after)
	if err != nil {
		return err
	}
	key := delProvenanceKey(frozenDELProvenancePrefix, row.Gateway, row.FrozenIndex)
	return k.storeDELProvenance(ctx, key, updated, frozenAt)
}

func (k *StateKeeper) removeFrozenDELProvenance(ctx sdk.Context, row types.EVMFrozenBinding) error {
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	if _, _, err := k.GetFrozenDELProvenance(ctx, row); err != nil {
		return err
	}
	key := delProvenanceKey(frozenDELProvenancePrefix, row.Gateway, row.FrozenIndex)
	return k.service.OpenKVStore(ctx).Delete(key)
}

// reduceDELProvenance mirrors the existing keeper loss per native hold lot.
// It allocates that loss among the exact funding-height parts of the lot.
func reduceDELProvenance(lots []DELProvenanceTranche, amount, loss sdkmath.Int, deadline int64, after []*types.StakeHold) ([]DELProvenanceTranche, error) {
	if deadline == 0 {
		return reduceDELProvenanceGroup(nil, lots, amount, loss), nil
	}
	remainingByStart := make(map[int64]sdkmath.Int, len(after))
	for _, hold := range after {
		if hold.HoldEndTime == deadline {
			remainingByStart[hold.HoldStartTime] = hold.Amount
		}
	}
	updated := make([]DELProvenanceTranche, 0, len(lots))
	remainingTotal := sdkmath.ZeroInt()
	for len(lots) != 0 {
		start := lots[0].FundedAt.Unix()
		lotTotal := sdkmath.ZeroInt()
		end := 0
		for end < len(lots) && lots[end].FundedAt.Unix() == start {
			lotTotal = lotTotal.Add(lots[end].Amount)
			end++
		}
		remaining, found := remainingByStart[start]
		if !found {
			remaining = sdkmath.ZeroInt()
		}
		if remaining.GT(lotTotal) {
			return nil, errors.New("native hold penalty increased DEL provenance")
		}
		remainingTotal = remainingTotal.Add(remaining)
		updated = reduceDELProvenanceGroup(updated, lots[:end], lotTotal, lotTotal.Sub(remaining))
		lots = lots[end:]
	}
	if !remainingTotal.Equal(amount.Sub(loss)) {
		return nil, errors.New("native hold penalty differs from the DEL provenance loss")
	}
	if err := matchDELProvenanceHoldLots(updated, after, deadline); err != nil {
		return nil, err
	}
	return updated, nil
}

// The difference between successive cumulative floors preserves every atom
// of the verified loss while leaving each part's pro rata error below one.
func reduceDELProvenanceGroup(updated, lots []DELProvenanceTranche, amount, loss sdkmath.Int) []DELProvenanceTranche {
	if loss.IsZero() {
		return append(updated, lots...)
	}
	denominator, lossAmount := amount.BigInt(), loss.BigInt()
	var prefix, charged, cumulative, partLoss big.Int
	for _, lot := range lots {
		prefix.Add(&prefix, lot.Amount.BigInt())
		cumulative.Mul(&prefix, lossAmount).Quo(&cumulative, denominator)
		partLoss.Sub(&cumulative, &charged)
		charged.Set(&cumulative)
		lot.Amount = lot.Amount.Sub(sdkmath.NewIntFromBigInt(&partLoss))
		if lot.Amount.IsPositive() {
			updated = append(updated, lot)
		}
	}
	return updated
}
