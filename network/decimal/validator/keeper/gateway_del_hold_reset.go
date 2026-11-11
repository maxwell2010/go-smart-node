package keeper

import (
	"errors"
	"strconv"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// ApplyGatewayDELHoldReset moves one entire expired bucket to ordinary DEL.
// The caller authenticates the signed Gateway receipt and both poststates.
// This projection keeps the pending queue and escrow unchanged.
func (k *StateKeeper) ApplyGatewayDELHoldReset(ctx sdk.Context, held, ordinary GatewayDELStake, moved sdkmath.Int) error {
	owner, operator, err := held.nativePair()
	if err != nil {
		return err
	}
	if _, _, err := ordinary.nativePair(); err != nil {
		return err
	}
	if held.Gateway != ordinary.Gateway || held.WrappedDEL != ordinary.WrappedDEL ||
		held.Beneficiary != ordinary.Beneficiary || held.Validator != ordinary.Validator ||
		held.HoldDeadline == 0 || ordinary.HoldDeadline != 0 || !held.Amount.IsZero() ||
		moved.IsNil() || !moved.IsPositive() || ordinary.Amount.LT(moved) {
		return errors.New("invalid Gateway DEL hold reset positions or amount")
	}
	if held.HoldDeadline > ctx.BlockTime().Unix() {
		return errors.New("Gateway DEL hold has not expired")
	}
	child, commit := ctx.CacheContext()
	activeHeld, previousHeld, err := k.checkGatewayDELPrestate(child, held, moved, owner, operator)
	if err != nil {
		return err
	}
	activePlain, previousPlain, err := k.checkGatewayDELPrestate(child, ordinary, ordinary.Amount.Sub(moved), owner, operator)
	if err != nil {
		return err
	}
	_, nextHeld, err := held.takeHold(previousHeld, moved)
	if err != nil {
		return err
	}
	created := ctx.BlockHeight()
	if activePlain != nil {
		created = activePlain.CreatedHeight
	}
	nextPlain := types.Delegation{Delegator: owner, Validator: operator, Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, ordinary.Amount))}
	remove := BindingReference{Kind: activeHeld.Kind, Gateway: activeHeld.Gateway, ID: activeHeld.StakeID}
	row, err := ordinary.binding(ordinary.Amount, created, ctx.BlockHeight())
	if err != nil {
		return err
	}
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{{Remove: &remove}, {Stake: &row}})
	if err != nil {
		return err
	}
	if err := k.ApplyDelegationBatch(child, []DelegationChange{{Expected: previousHeld, Next: nextHeld}, {Expected: previousPlain, Next: &nextPlain}}); err != nil {
		return err
	}
	if err := k.ApplyBindingPlan(child, plan); err != nil {
		return err
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_del_hold_reset",
		sdk.NewAttribute("stake_id", held.StakeID.Hex()), sdk.NewAttribute("unheld_stake_id", ordinary.StakeID.Hex()),
		sdk.NewAttribute("hold_deadline", strconv.FormatInt(held.HoldDeadline, 10)), sdk.NewAttribute("amount", moved.String())))
	commit()
	return nil
}
