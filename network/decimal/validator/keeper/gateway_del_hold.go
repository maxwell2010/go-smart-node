package keeper

import (
	"errors"
	"math/big"
	"slices"
	"sort"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func (k *StateKeeper) checkGatewayDELHoldPrestate(ctx sdk.Context, next GatewayDELStake, amount sdkmath.Int, owner, operator string) (*types.Delegation, error) {
	delegation, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	if errors.Is(err, ErrNotFound) && amount.IsZero() {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stake := delegation.Stake
	total, selected := sdkmath.ZeroInt(), sdkmath.ZeroInt()
	for i, lot := range stake.Holds {
		if !lot.Amount.IsPositive() || lot.HoldStartTime <= 0 || (i > 0 && !holdLotBefore(stake.Holds[i-1], lot.HoldEndTime, lot.HoldStartTime)) {
			return nil, errors.New("native DEL hold lots must be positive and ordered uniquely by deadline and start")
		}
		total = total.Add(lot.Amount) // GetDelegation validates the sum is within stake amount.
		if lot.HoldEndTime == next.HoldDeadline {
			if lot.HoldStartTime < next.HoldStartTime {
				return nil, errors.New("native DEL hold lot predates its EVM bucket")
			}
			selected = selected.Add(lot.Amount)
		}
	}
	if total.IsZero() || !total.Equal(stake.Stake.Amount) || !selected.Equal(amount) {
		return nil, errors.New("Gateway DEL hold differs from the native delegation")
	}
	return &delegation, nil
}

func holdLotBefore(lot *types.StakeHold, end, start int64) bool {
	return lot.HoldEndTime < end || (lot.HoldEndTime == end && lot.HoldStartTime < start)
}

func gatewayDELHoldStake(amount sdkmath.Int, lots []*types.StakeHold) types.Stake {
	stake := types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, amount))
	stake.Type = types.StakeType_CoinHold
	stake.Holds = lots
	return stake
}

func (s GatewayDELStake) depositHold(previous *types.Delegation, deposited sdkmath.Int, now int64, newBucket bool) (types.Stake, error) {
	if s.HoldDeadline <= now || s.HoldStartTime > now || (newBucket && s.HoldStartTime != now) {
		return types.Stake{}, errors.New("Gateway DEL hold deposit has invalid start or deadline at this block")
	}
	amount := deposited
	var lots []*types.StakeHold
	if previous != nil {
		total := previous.Stake.Stake.Amount.BigInt()
		total.Add(total, deposited.BigInt())
		if total.BitLen() > 256 {
			return types.Stake{}, errors.New("native DEL hold aggregate exceeds 256 bits")
		}
		amount = sdkmath.NewIntFromBigInt(total)
		lots = slices.Clone(previous.Stake.Holds)
	}
	// The contract stores the first start per deadline. Native lots retain
	// their own deposit time so a new deposit never inherits an older start.
	index := sort.Search(len(lots), func(i int) bool { return !holdLotBefore(lots[i], s.HoldDeadline, now) })
	lot := &types.StakeHold{Amount: deposited, HoldStartTime: now, HoldEndTime: s.HoldDeadline}
	if index < len(lots) && lots[index].HoldEndTime == s.HoldDeadline && lots[index].HoldStartTime == now {
		lot.Amount = lot.Amount.Add(lots[index].Amount)
		lots[index] = lot
	} else {
		lots = append(lots, nil)
		copy(lots[index+1:], lots[index:])
		lots[index] = lot
	}
	return gatewayDELHoldStake(amount, lots), nil
}

func (s GatewayDELStake) takeHold(previous *types.Delegation, withdrawn sdkmath.Int) (types.Stake, *types.Delegation, error) {
	var moved, kept []*types.StakeHold
	remaining := withdrawn
	for _, lot := range previous.Stake.Holds {
		if lot.HoldEndTime != s.HoldDeadline || remaining.IsZero() {
			kept = append(kept, lot)
			continue
		}
		if lot.Amount.LTE(remaining) {
			moved = append(moved, lot)
			remaining = remaining.Sub(lot.Amount)
			continue
		}
		part, rest := *lot, *lot
		part.Amount, rest.Amount = remaining, lot.Amount.Sub(remaining)
		moved, kept = append(moved, &part), append(kept, &rest)
		remaining = sdkmath.ZeroInt()
	}
	if !remaining.IsZero() {
		return types.Stake{}, nil, errors.New("native DEL hold lots do not cover the withdrawal")
	}
	queued := gatewayDELHoldStake(withdrawn, moved)
	if len(kept) == 0 {
		return queued, nil, nil
	}
	next := *previous
	next.Stake = gatewayDELHoldStake(previous.Stake.Stake.Amount.Sub(withdrawn), kept)
	return queued, &next, nil
}

func (s GatewayDELStake) penalizeHold(previous *types.Delegation, loss sdkmath.Int) (*types.Delegation, error) {
	if loss.IsZero() {
		return previous, nil
	}
	kept, err := reduceDELHoldLots(previous.Stake.Holds, s.HoldDeadline, s.Amount, loss)
	if err != nil || len(kept) == 0 {
		return nil, err
	}
	next := *previous
	next.Stake = gatewayDELHoldStake(previous.Stake.Stake.Amount.Sub(loss), kept)
	return &next, nil
}

// reduceDELHoldLots uses the difference of adjacent cumulative floors in
// canonical start-time order. Each selected lot's share is within one atom
// of exact pro rata and the shares add up to the entire bucket loss. The
// widened product supports multiplication of two valid uint256 amounts.
func reduceDELHoldLots(lots []*types.StakeHold, deadline int64, amount, loss sdkmath.Int) ([]*types.StakeHold, error) {
	bucketAmount, lossAmount := amount.BigInt(), loss.BigInt()
	var prefix, charged, cumulativeLoss, lotLoss big.Int
	kept := make([]*types.StakeHold, 0, len(lots))
	for _, lot := range lots {
		if lot.HoldEndTime != deadline {
			kept = append(kept, lot)
			continue
		}
		prefix.Add(&prefix, lot.Amount.BigInt())
		cumulativeLoss.Mul(&prefix, lossAmount).Quo(&cumulativeLoss, bucketAmount)
		lotLoss.Sub(&cumulativeLoss, &charged)
		charged.Set(&cumulativeLoss)
		remaining := lot.Amount.Sub(sdkmath.NewIntFromBigInt(&lotLoss))
		if remaining.IsPositive() {
			updated := *lot
			updated.Amount = remaining
			kept = append(kept, &updated)
		}
	}
	if prefix.Cmp(bucketAmount) != 0 || charged.Cmp(lossAmount) != 0 {
		return nil, errors.New("native DEL hold lots do not cover the penalty")
	}
	return kept, nil
}
