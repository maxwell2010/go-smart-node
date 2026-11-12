package keeper

import (
	"bytes"
	"errors"

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
	return k.applyGatewayDELPenalty(ctx, before, afterAmount, afterCursor, false)
}

// ApplyGatewayDELHoldPenalty projects a verified active DEL hold-bucket
// penalty. Only lots with that exact deadline participate in its loss. The
// caller authenticates the unchanged EVM hold identity and actual native burn.
func (k *StateKeeper) ApplyGatewayDELHoldPenalty(ctx sdk.Context, before GatewayDELStake, afterAmount sdkmath.Int, afterCursor common.Hash) error {
	return k.applyGatewayDELPenalty(ctx, before, afterAmount, afterCursor, true)
}

func (k *StateKeeper) applyGatewayDELPenalty(ctx sdk.Context, before GatewayDELStake, afterAmount sdkmath.Int, afterCursor common.Hash, held bool) error {
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
	change := DelegationChange{Expected: previous}
	if held {
		change.Next, err = before.penalizeHold(previous, loss)
		if err != nil {
			return err
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
