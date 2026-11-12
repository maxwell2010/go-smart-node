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

// ApplyGatewayDELPenalty projects an authenticated penalty on one ordinary
// active DEL position. The caller verifies the Gateway transition, cursor
// bound and real native burn and uses the same outer transaction cache.
// This method changes only active metadata and its indexes.
func (k *StateKeeper) ApplyGatewayDELPenalty(ctx sdk.Context, before GatewayDELStake, afterAmount sdkmath.Int, afterCursor common.Hash) error {
	return k.applyGatewayDELPenalty(ctx, before, afterAmount, afterCursor, false, nil)
}

// ApplyGatewayDELHoldPenalty projects a verified active DEL hold-bucket
// penalty. Only lots with that exact deadline participate in its loss. The
// caller authenticates the unchanged EVM hold identity and actual native burn.
func (k *StateKeeper) ApplyGatewayDELHoldPenalty(ctx sdk.Context, before GatewayDELStake, afterAmount sdkmath.Int, afterCursor common.Hash) error {
	return k.applyGatewayDELPenalty(ctx, before, afterAmount, afterCursor, true, nil)
}

// ApplyGatewayActiveHistoricalDELPenalty projects one exact historical
// penalty on active direct DEL, including a hold bucket. The caller verifies
// the EVM transition and any native burn in the same outer cache.
func (k *StateKeeper) ApplyGatewayActiveHistoricalDELPenalty(ctx sdk.Context, before GatewayDELStake, infractionHeight int64, afterAmount sdkmath.Int, afterCursor common.Hash) error {
	if new(big.Int).Add(before.PenaltyCursor.Big(), big.NewInt(1)).Cmp(afterCursor.Big()) != 0 {
		return errors.New("historical Gateway DEL penalty cursor must advance exactly once")
	}
	return k.applyGatewayDELPenalty(ctx, before, afterAmount, afterCursor, before.HoldDeadline != 0, &infractionHeight)
}

func (k *StateKeeper) applyGatewayDELPenalty(ctx sdk.Context, before GatewayDELStake, afterAmount sdkmath.Int, afterCursor common.Hash, held bool, infractionHeight *int64) error {
	owner, operator, err := before.nativePairIdentity()
	if err != nil {
		return err
	}
	backed := before.nftBackedAmount()
	if held != (before.HoldDeadline != 0) || (before.Amount.IsZero() && backed.IsZero()) ||
		afterAmount.IsNil() || afterAmount.IsNegative() || afterAmount.GT(before.Amount) {
		if held {
			return errors.New("invalid held Gateway DEL penalty amount or position")
		}
		return errors.New("invalid ordinary Gateway DEL penalty amount or position")
	}
	if bytes.Compare(afterCursor[:], before.PenaltyCursor[:]) <= 0 {
		return errors.New("Gateway DEL penalty cursor must strictly advance")
	}

	child, commit := ctx.CacheContext()
	active, previous, err := k.checkGatewayDELPrestate(child, before, before.Amount, owner, operator)
	if err != nil {
		return err
	}
	reserve, err := k.GetActiveDELReserve(child, operator)
	if err != nil {
		return err
	}
	if reserve.LT(before.Amount) {
		return errors.New("active DEL reserve is below the penalized position")
	}
	loss := before.Amount.Sub(afterAmount)
	var historical *DELHistoricalPenaltyPlan
	if infractionHeight != nil {
		exact, priorTranches, err := k.planActiveDELHistoricalPenalty(child, *active, *infractionHeight)
		if err != nil {
			return err
		}
		if !exact.Before.Equal(before.Amount) || !exact.Remaining.Equal(afterAmount) {
			return errors.New("Gateway DEL penalty amount differs from the historical exposure")
		}
		if held {
			if err := matchDELProvenanceHoldLots(priorTranches, delegationHoldLots(previous), before.HoldDeadline); err != nil {
				return err
			}
		}
		historical = &exact
	}
	change := DelegationChange{Expected: previous}
	if held {
		if historical == nil {
			change.Next, err = before.penalizeHold(previous, loss)
			if err != nil {
				return err
			}
		} else if loss.IsPositive() {
			lots, err := projectHistoricalDELHoldLots(previous.Stake.Holds, before.HoldDeadline, historical.Tranches)
			if err != nil {
				return err
			}
			if len(lots) != 0 {
				remaining := *previous
				remaining.Stake = gatewayDELHoldStake(previous.Stake.Stake.Amount.Sub(loss), lots)
				change.Next = &remaining
			}
		}
	} else if afterAmount.IsPositive() {
		remaining := *previous
		remaining.Stake = types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, afterAmount))
		change.Next = &remaining
	}
	var mutation BindingMutation
	if afterAmount.IsZero() && backed.IsZero() {
		mutation.Remove = &BindingReference{Kind: active.Kind, Gateway: active.Gateway, ID: active.StakeID}
	} else {
		next := before
		next.Amount, next.PenaltyCursor = afterAmount, afterCursor
		row, err := next.binding(afterAmount, active.CreatedHeight, ctx.BlockHeight())
		if err != nil {
			return err
		}
		mutation.Stake = &row
	}
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{mutation})
	if err != nil {
		return err
	}
	if historical == nil {
		if err := k.recordActiveDELProvenancePenalty(child, before, change, loss); err != nil {
			return err
		}
	} else if loss.IsPositive() {
		key := delProvenanceKey(activeDELProvenancePrefix, before.Gateway[:], before.StakeID[:])
		if err := k.storeDELProvenance(child, key, historical.Tranches, time.Time{}); err != nil {
			return err
		}
	}
	if loss.IsPositive() {
		if err := k.ApplyDelegationBatch(child, []DelegationChange{change}); err != nil {
			return err
		}
	}
	if err := k.ApplyBindingPlan(child, plan); err != nil {
		return err
	}
	remaining, err := k.GetActiveDELReserve(child, operator)
	if err != nil {
		return err
	}
	if !remaining.Equal(reserve.Sub(loss)) {
		return errors.New("native active DEL reserve did not decrease by the penalty loss")
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_del_penalty_applied",
		sdk.NewAttribute("stake_id", before.StakeID.Hex()),
		sdk.NewAttribute("previous_cursor", before.PenaltyCursor.Hex()),
		sdk.NewAttribute("penalty_cursor", afterCursor.Hex()),
		sdk.NewAttribute("amount", loss.String())))
	commit()
	return nil
}
