package keeper

import (
	"bytes"
	"errors"
	"fmt"

	"cosmossdk.io/core/store"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
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
// cache. It must execute the Gateway and apply its native receipt projection
// in that cache, returning an error on a revert or incomplete projection. The
// projection retires the binding, and the keeper removes the matching native
// undelegation entry before committing any writes.
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
	var result WithdrawalSettlementResult
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
	keys := make([][]byte, 0, maxWithdrawalAttempts)
	if cursor != nil && bytes.Compare(cursor, end) < 0 {
		after := append(bytes.Clone(cursor), 0)
		if err = appendWithdrawalKeys(state, after, end, &keys); err != nil {
			return result, err
		}
		end = after
	}
	if err = appendWithdrawalKeys(state, start, end, &keys); err != nil {
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

func appendWithdrawalKeys(state store.KVStore, start, end []byte, keys *[][]byte) error {
	if len(*keys) == maxWithdrawalAttempts || bytes.Compare(start, end) >= 0 {
		return nil
	}
	it, err := state.Iterator(start, end)
	if err != nil {
		return err
	}
	for ; it.Valid() && len(*keys) < maxWithdrawalAttempts; it.Next() {
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
	// Give the executor a separate copy so it cannot alter the expected row.
	input, err := decodeBindingRecord(row.key, row.data)
	if err != nil {
		return err
	}
	if err = k.checkBindingIndexes(child, input); err != nil {
		return err
	}
	if err = execute(child, *input.frozen); err != nil {
		return err
	}
	after, err := k.GetEVMFrozenBinding(child, row.frozen.Gateway, row.frozen.FrozenIndex)
	if err != nil {
		return err
	}
	expected := *row.frozen
	expected.Active = false
	expected.UpdatedHeight = child.BlockHeight()
	want, err := types.CanonicalBindingBytes(&expected)
	if err != nil {
		return err
	}
	got, err := types.CanonicalBindingBytes(&after)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return errors.New("withdrawal execution did not settle the matching native binding")
	}
	if err = k.removeSettledUndelegation(child, row.frozen); err != nil {
		return err
	}
	commit()
	return nil
}

func (k *StateKeeper) removeSettledUndelegation(ctx sdk.Context, binding *types.EVMFrozenBinding) error {
	owner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(binding.Beneficiary)
	if err != nil {
		return err
	}
	operator, err := k.ValidatorAddressCodec().BytesToString(binding.SourceValidator)
	if err != nil {
		return err
	}
	key := composite(undelegationPrefix, owner, operator)
	var pending types.Undelegation
	if err = k.read(ctx, key, &pending); err != nil {
		return err
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	if pending.Delegator != owner || pending.Validator != operator || uint64(len(pending.Entries)) > uint64(params.MaxEntries) {
		return errors.New("invalid native undelegation pair")
	}
	for i := range pending.Entries {
		entry := &pending.Entries[i]
		hash, err := types.NativeQueueEntryHash(1, owner, operator, "", entry)
		if err != nil {
			return err
		}
		if !bytes.Equal(hash, binding.NativeEntryHash) {
			continue
		}
		deadline := entry.CompletionTime.Unix()
		if deadline <= 0 || !bytes.Equal(binding.CompletionTimestamp[24:], sdk.Uint64ToBigEndian(uint64(deadline))) {
			return errors.New("native undelegation deadline differs from settled binding")
		}
		// Two requests may have byte-identical entries. Remove one copy.
		pending.Entries = append(pending.Entries[:i], pending.Entries[i+1:]...)
		if len(pending.Entries) == 0 {
			return k.service.OpenKVStore(ctx).Delete(key)
		}
		return k.put(ctx, key, &pending)
	}
	return errors.New("settled binding has no matching native undelegation")
}
