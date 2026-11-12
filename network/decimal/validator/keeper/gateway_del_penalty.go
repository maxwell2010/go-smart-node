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
	owner, operator, err := before.nativePairIdentity()
	if err != nil {
		return err
	}
	if before.HoldDeadline != 0 || !before.nftBackedAmount().IsZero() || !before.Amount.IsPositive() ||
		afterAmount.IsNil() || afterAmount.IsNegative() || afterAmount.GT(before.Amount) {
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
	var mutation BindingMutation
	if afterAmount.IsZero() {
		mutation.Remove = &BindingReference{Kind: active.Kind, Gateway: active.Gateway, ID: active.StakeID}
	} else {
		next := before
		next.Amount, next.PenaltyCursor = afterAmount, afterCursor
		row, err := next.binding(afterAmount, active.CreatedHeight, ctx.BlockHeight())
		if err != nil {
			return err
		}
		mutation.Stake = &row
		remaining := *previous
		remaining.Stake = types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, afterAmount))
		change.Next = &remaining
	}
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{mutation})
	if err != nil {
		return err
	}
	if err := k.ApplyDelegationBatch(child, []DelegationChange{change}); err != nil {
		return err
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
