package keeper

import (
	"bytes"
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const frozenDELPenaltyCursorPrefix byte = 0x5b

func frozenDELPenaltyCursorKey(gateway, index []byte) []byte {
	key := append([]byte{frozenDELPenaltyCursorPrefix}, gateway...)
	return append(key, index...)
}

// ApplyGatewayFrozenDELPenalty projects an authenticated ordinary DEL frozen
// claim adjustment. The caller proves the EVM checkpoint and burn in the same
// outer cache. An ineligible claim may advance its cursor without losing DEL.
func (k *StateKeeper) ApplyGatewayFrozenDELPenalty(ctx sdk.Context, before types.EVMFrozenBinding, infractionHeight int64, oldCursor, newCursor common.Hash, remaining sdkmath.Int) error {
	if err := before.Validate(); err != nil {
		return err
	}
	if !before.Active || before.FreezeType != 1 || before.SourceKind != gatewayDELCoinBinding ||
		before.NativeTarget.StakeType != types.StakeType_Coin || before.ReserveAsset.ReserveKind != 1 ||
		before.ReserveAsset.NativeDenom != identity.BaseDenom || !before.RawAmount.IsPositive() ||
		remaining.IsNil() || remaining.IsNegative() || remaining.GT(before.RawAmount) ||
		infractionHeight <= 0 || infractionHeight > ctx.BlockHeight() || before.UpdatedHeight > ctx.BlockHeight() {
		return errors.New("invalid ordinary frozen DEL penalty transition")
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
	if entry.CreationHeight != before.CreatedHeight || !entry.CompletionTime.After(ctx.BlockTime()) {
		return errors.New("frozen DEL penalty requires an unmatured claim with matching creation height")
	}
	loss := before.RawAmount.Sub(remaining)
	if entry.CreationHeight < infractionHeight && !loss.IsZero() {
		return errors.New("frozen DEL claim predates the infraction")
	}
	after := before
	after.RawAmount, after.UpdatedHeight = remaining, ctx.BlockHeight()
	if remaining.IsZero() {
		after.Active = false
		pending.Entries = append(pending.Entries[:i], pending.Entries[i+1:]...)
	} else {
		entry.Stake = types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, remaining))
		after.NativeEntryHash, err = types.NativeQueueEntryHash(1, pending.Delegator, pending.Validator, "", entry)
		if err != nil {
			return err
		}
	}
	pair, err := encodeWithdrawalPair(pending)
	if err != nil {
		return err
	}
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{{Frozen: &after}})
	if err != nil {
		return err
	}
	if err := k.ApplyBindingPlan(child, plan); err != nil {
		return err
	}
	if pair == nil {
		err = state.Delete(key)
	} else {
		err = state.Set(key, pair)
	}
	if err != nil {
		return err
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
