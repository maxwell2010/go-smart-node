package keeper

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"cosmossdk.io/core/store"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const (
	withdrawalDuePrefix   = byte(0x57)
	withdrawalCursorKey   = byte(0x58)
	withdrawalDueKeyLen   = 1 + 32 + 1 + 20 + 32
	maxWithdrawalAttempts = 8
)

func withdrawalDueKey(r *bindingRecord) []byte {
	key := make([]byte, 0, withdrawalDueKeyLen)
	key = append(key, withdrawalDuePrefix)
	key = append(key, r.frozen.CompletionTimestamp...)
	return append(key, r.key...)
}

// WithdrawalExecutor settles one authenticated frozen binding in the supplied
// cache. Execute the Gateway and project its receipt with
// ApplyWithdrawalCompletion in that cache. A revert or projection failure must
// return an error. The scheduler verifies the exact native result before commit.
type WithdrawalExecutor func(sdk.Context, types.EVMFrozenBinding) error

type WithdrawalSettlementResult struct {
	Settled int
	Retried int
}

// SettleMatureWithdrawals attempts at most eight due withdrawals in index
// order. The cursor wraps so a repeatedly failing withdrawal cannot block
// later jobs. Failed attempts keep their index entry and discard all writes.
// The caller supplies a deterministic gas cap per attempt and runs this once
// in the block lifecycle. This method does not install an App handler.
func (k *StateKeeper) SettleMatureWithdrawals(ctx sdk.Context, attemptGas uint64, execute WithdrawalExecutor) (WithdrawalSettlementResult, error) {
	return k.SettleMatureWithdrawalsLimit(ctx, maxWithdrawalAttempts, attemptGas, execute)
}

// SettleMatureWithdrawalsLimit uses the same persistent cursor with a lower
// per-block attempt limit. It accepts at most the default eight attempts.
func (k *StateKeeper) SettleMatureWithdrawalsLimit(ctx sdk.Context, maxAttempts int, attemptGas uint64, execute WithdrawalExecutor) (WithdrawalSettlementResult, error) {
	var result WithdrawalSettlementResult
	if maxAttempts < 1 || maxAttempts > maxWithdrawalAttempts {
		return result, errors.New("withdrawal attempt limit must be between one and eight")
	}
	if execute == nil || attemptGas == 0 {
		return result, errors.New("withdrawal execution and gas limit are required")
	}
	if err := k.checkBindingDomain(ctx); err != nil {
		return result, err
	}
	now := ctx.BlockTime().Unix()
	if now <= 0 {
		return result, errors.New("invalid withdrawal settlement block time")
	}
	batch, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(batch)
	cursor, err := state.Get([]byte{withdrawalCursorKey})
	if err != nil {
		return result, err
	}
	if cursor != nil && !validWithdrawalDueKey(cursor) {
		return result, errors.New("invalid withdrawal settlement cursor")
	}
	start := []byte{withdrawalDuePrefix}
	// A 0xff byte after the timestamp includes all keys at exactly now,
	// since the next byte of each valid key is frozenBindingPrefix.
	end := append(sdk.Uint64ToBigEndian(uint64(now)), 0xff)
	end = append(append(bytes.Clone(start), make([]byte, 24)...), end...)
	keys := make([][]byte, 0, maxAttempts)
	if cursor != nil && bytes.Compare(cursor, end) < 0 {
		after := append(bytes.Clone(cursor), 0)
		if err = appendWithdrawalKeys(state, after, end, maxAttempts, &keys); err != nil {
			return result, err
		}
		end = after
	}
	if err = appendWithdrawalKeys(state, start, end, maxAttempts, &keys); err != nil {
		return result, err
	}
	for _, key := range keys {
		row, err := k.readBindingRow(batch, key[33:])
		if err != nil {
			return WithdrawalSettlementResult{}, err
		}
		if row.frozen == nil || !row.active() || row.frozen.FreezeType != 1 || !bytes.Equal(key, withdrawalDueKey(row)) {
			return WithdrawalSettlementResult{}, errors.New("invalid due withdrawal index entry")
		}
		if err = k.attemptWithdrawal(batch, attemptGas, row, execute); err != nil {
			result.Retried++
			ctx.Logger().Error("withdrawal settlement deferred", "gateway", fmt.Sprintf("%x", row.frozen.Gateway), "id", fmt.Sprintf("%x", row.frozen.FrozenIndex), "error", err)
		} else {
			result.Settled++
		}
	}
	if len(keys) != 0 {
		if err = state.Set([]byte{withdrawalCursorKey}, keys[len(keys)-1]); err != nil {
			return WithdrawalSettlementResult{}, err
		}
		commit()
	}
	return result, nil
}

func validWithdrawalDueKey(key []byte) bool {
	return len(key) == withdrawalDueKeyLen && key[0] == withdrawalDuePrefix && key[33] == frozenBindingPrefix
}

func appendWithdrawalKeys(state store.KVStore, start, end []byte, maxAttempts int, keys *[][]byte) error {
	if len(*keys) == maxAttempts || bytes.Compare(start, end) >= 0 {
		return nil
	}
	it, err := state.Iterator(start, end)
	if err != nil {
		return err
	}
	for ; it.Valid() && len(*keys) < maxAttempts; it.Next() {
		if err = it.Error(); err != nil {
			_ = it.Close()
			return err
		}
		key := it.Key()
		if !validWithdrawalDueKey(key) || !bytes.Equal(it.Value(), key[33:]) {
			_ = it.Close()
			return errors.New("corrupt due withdrawal index")
		}
		*keys = append(*keys, bytes.Clone(key))
	}
	// The SDK iterator reports invalid on normal exhaustion. Follow the
	// existing keeper iteration convention and propagate Close errors.
	return it.Close()
}

func (k *StateKeeper) attemptWithdrawal(ctx sdk.Context, gas uint64, row *bindingRecord, execute WithdrawalExecutor) (err error) {
	child, commit := ctx.CacheContext()
	meter := storetypes.NewGasMeter(gas)
	child = child.WithGasMeter(meter)
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(storetypes.ErrorOutOfGas); !ok {
				panic(recovered)
			}
			err = errors.New("withdrawal settlement ran out of gas")
		}
		ctx.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), "withdrawal settlement")
	}()
	plan, err := k.planWithdrawalCompletion(child, row)
	if err != nil {
		return err
	}
	// Give the executor a separate copy so it cannot alter the expected row.
	input, err := decodeBindingRecord(row.key, row.data)
	if err != nil {
		return err
	}
	if err = execute(child, *input.frozen); err != nil {
		return err
	}
	if err = k.verifyWithdrawalCompletion(child, plan); err != nil {
		return err
	}
	commit()
	return nil
}

// ErrWithdrawalAlreadyCompleted identifies a replay of the same completed
// frozen identity. It carries no payment or authorization semantics.
var ErrWithdrawalAlreadyCompleted = errors.New("withdrawal binding already completed")

type withdrawalCompletion struct {
	closed            *bindingRecord
	pairKey, nextPair []byte
}

func closedWithdrawalBinding(row *bindingRecord, height int64) (*bindingRecord, error) {
	if row == nil || row.frozen == nil || !row.active() || row.frozen.FreezeType != 1 || height < row.frozen.UpdatedHeight {
		return nil, errors.New("expected an active withdrawal binding at the current height")
	}
	closed := *row.frozen
	closed.Active = false
	closed.UpdatedHeight = height
	return recordFromFrozen(&closed)
}

// ApplyWithdrawalCompletion retires an exact, mature frozen binding and one
// matching native undelegation entry atomically. The caller authenticates the
// Gateway transition and supplies the pre-execution binding in the same outer
// cache as the EVM and NFT receipt changes. This method only projects state.
func (k *StateKeeper) ApplyWithdrawalCompletion(ctx sdk.Context, expected types.EVMFrozenBinding) error {
	if err := k.checkBindingDomain(ctx); err != nil {
		return err
	}
	preimage, err := recordFromFrozen(&expected)
	if err != nil {
		return err
	}
	if !expected.Active || expected.FreezeType != 1 || ctx.BlockHeight() < expected.UpdatedHeight {
		return errors.New("expected an active withdrawal binding at the current height")
	}
	child, commit := ctx.CacheContext()
	current, err := k.readBindingRow(child, preimage.key)
	if err != nil {
		return err
	}
	if current.frozen.UpdatedHeight > ctx.BlockHeight() {
		return errors.New("withdrawal binding is newer than the execution context")
	}
	if !current.active() {
		closed, err := closedWithdrawalBinding(preimage, current.frozen.UpdatedHeight)
		if err != nil || !bytes.Equal(current.data, closed.data) {
			return errors.New("completed withdrawal binding differs from the expected identity")
		}
		if err = k.checkBindingIndexes(child, current); err != nil {
			return err
		}
		return ErrWithdrawalAlreadyCompleted
	}
	if !bytes.Equal(current.data, preimage.data) {
		return errors.New("withdrawal binding differs from the expected preimage")
	}
	completion, err := k.planWithdrawalCompletion(child, current)
	if err != nil {
		return err
	}
	bindingPlan, err := k.PrepareBindingPlan(child, []BindingMutation{{Frozen: completion.closed.frozen}})
	if err != nil {
		return err
	}
	if err = k.ApplyBindingPlan(child, bindingPlan); err != nil {
		return err
	}
	state := k.service.OpenKVStore(child)
	if completion.nextPair == nil {
		err = state.Delete(completion.pairKey)
	} else {
		err = state.Set(completion.pairKey, completion.nextPair)
	}
	if err != nil {
		return err
	}
	if expected.SourceKind == gatewayDELCoinBinding {
		cursorKey := frozenDELPenaltyCursorKey(expected.Gateway, expected.FrozenIndex)
		hasCursor, err := state.Has(cursorKey)
		if err != nil {
			return err
		}
		if hasCursor {
			if err := state.Delete(cursorKey); err != nil {
				return err
			}
		}
	}
	child.EventManager().EmitEvent(sdk.NewEvent(
		"decimal_withdrawal_completed",
		sdk.NewAttribute("gateway", fmt.Sprintf("0x%x", completion.closed.frozen.Gateway)),
		sdk.NewAttribute("frozen_index", fmt.Sprintf("0x%x", completion.closed.frozen.FrozenIndex)),
	))
	commit()
	return nil
}

func (k *StateKeeper) verifyWithdrawalCompletion(ctx sdk.Context, plan *withdrawalCompletion) error {
	actual, err := k.readBindingRow(ctx, plan.closed.key)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual.data, plan.closed.data) {
		return errors.New("withdrawal execution did not settle the matching native binding")
	}
	if err = k.checkBindingIndexes(ctx, actual); err != nil {
		return err
	}
	pair, err := k.service.OpenKVStore(ctx).Get(plan.pairKey)
	if err != nil {
		return err
	}
	if (pair == nil) != (plan.nextPair == nil) || !bytes.Equal(pair, plan.nextPair) {
		return errors.New("withdrawal execution did not settle exactly one native undelegation entry")
	}
	return nil
}

// Read at most one native pair record, bounded by MaxRecordBytes and the
// existing MaxEntries parameter. The content hash identifies one multiset entry.
func (k *StateKeeper) planWithdrawalCompletion(ctx sdk.Context, row *bindingRecord) (*withdrawalCompletion, error) {
	closed, err := closedWithdrawalBinding(row, ctx.BlockHeight())
	if err != nil {
		return nil, err
	}
	key, pending, i, err := k.matchWithdrawalEntry(ctx, row)
	if err != nil {
		return nil, err
	}
	if pending.Entries[i].CompletionTime.After(ctx.BlockTime()) {
		return nil, errors.New("native undelegation has not matured")
	}
	// Two requests may have byte-identical entries. Remove one copy.
	pending.Entries = append(pending.Entries[:i], pending.Entries[i+1:]...)
	next, err := encodeWithdrawalPair(pending)
	if err != nil {
		return nil, err
	}
	return &withdrawalCompletion{closed: closed, pairKey: key, nextPair: next}, nil
}

func encodeWithdrawalPair(pending types.Undelegation) ([]byte, error) {
	if len(pending.Entries) == 0 {
		return nil, nil
	}
	data, err := proto.Marshal(&pending)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxRecordBytes {
		return nil, errors.New("native undelegation pair exceeds size limit")
	}
	return data, nil
}

func (k *StateKeeper) matchWithdrawalEntry(ctx sdk.Context, row *bindingRecord) ([]byte, types.Undelegation, int, error) {
	var pending types.Undelegation
	if err := k.checkBindingIndexes(ctx, row); err != nil {
		return nil, pending, 0, err
	}
	binding := row.frozen
	owner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(binding.Beneficiary)
	if err != nil {
		return nil, pending, 0, err
	}
	operator, err := k.ValidatorAddressCodec().BytesToString(binding.SourceValidator)
	if err != nil {
		return nil, pending, 0, err
	}
	key := composite(undelegationPrefix, owner, operator)
	if err = k.read(ctx, key, &pending); err != nil {
		return nil, pending, 0, err
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, pending, 0, err
	}
	if pending.Delegator != owner || pending.Validator != operator || uint64(len(pending.Entries)) > uint64(params.MaxEntries) {
		return nil, pending, 0, errors.New("invalid native undelegation pair")
	}
	for i := range pending.Entries {
		entry := &pending.Entries[i]
		hash, err := types.NativeQueueEntryHash(1, owner, operator, "", entry)
		if err != nil {
			return nil, pending, 0, err
		}
		if !bytes.Equal(hash, binding.NativeEntryHash) {
			continue
		}
		if err = entry.Stake.Validate(); err != nil {
			return nil, pending, 0, err
		}
		target := binding.NativeTarget
		if entry.Stake.Type != target.StakeType || entry.Stake.ID != target.ID || entry.Stake.Stake.Denom != binding.ReserveAsset.NativeDenom || !slices.Equal(entry.Stake.SubTokenIDs, target.SubTokenIDs) {
			return nil, pending, 0, errors.New("native undelegation stake differs from the frozen target")
		}
		if binding.SourceKind == types.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN && !binding.RawAmount.Equal(entry.Stake.Stake.Amount) {
			return nil, pending, 0, errors.New("native undelegation amount differs from the frozen coin amount")
		}
		deadline := entry.CompletionTime.Unix()
		var expectedTimestamp [32]byte
		copy(expectedTimestamp[24:], sdk.Uint64ToBigEndian(uint64(deadline)))
		if deadline <= 0 || !bytes.Equal(binding.CompletionTimestamp, expectedTimestamp[:]) {
			return nil, pending, 0, errors.New("native undelegation deadline differs from the frozen binding")
		}
		return key, pending, i, nil
	}
	return nil, pending, 0, errors.New("frozen binding has no matching native undelegation")
}
