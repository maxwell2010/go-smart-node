package keeper

import (
	"bytes"
	"errors"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const (
	validatorFrozenBindingPrefix byte = 0x59
	validatorFrozenIndexMarker   byte = 0x5a
	validatorFrozenSourceOffset       = 1 + 20
)

// ErrValidatorFrozenIndexUnavailable means this store predates the complete
// validator-frozen index. An empty census cannot be proven on that store.
var ErrValidatorFrozenIndexUnavailable = errors.New("native validator frozen index is unavailable")

func validBindingValidator(validator []byte) bool {
	return len(validator) == 20 && !bytes.Equal(validator, make([]byte, 20))
}

func validatorFrozenBindingKey(validator, sourceKey []byte) []byte {
	key := append([]byte{validatorFrozenBindingPrefix}, validator...)
	return append(key, sourceKey...)
}

func (k *StateKeeper) hasValidatorFrozenIndex(ctx sdk.Context) (bool, error) {
	marker, err := k.service.OpenKVStore(ctx).Get([]byte{validatorFrozenIndexMarker})
	if err != nil || marker == nil {
		return false, err
	}
	if !bytes.Equal(marker, []byte{1}) {
		return false, errors.New("invalid native validator frozen index marker")
	}
	return true, nil
}

// GetValidatorFrozenBindingsPage lists pending EVM-bound withdrawals from and
// redelegations involving one validator. It includes both endpoints of a
// redelegation and excludes completed claims. A nil continuation exhausts
// the prefix at the queried snapshot. Completeness for native-only queue rows
// requires the bound profile's exact EVM/native queue coverage invariant.
func (k *StateKeeper) GetValidatorFrozenBindingsPage(ctx sdk.Context, validator, after []byte, limit uint32) ([]types.EVMFrozenBinding, []byte, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return nil, nil, err
	}
	if !validBindingValidator(validator) {
		return nil, nil, errors.New("invalid native frozen validator address")
	}
	present, err := k.hasValidatorFrozenIndex(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !present {
		return nil, nil, ErrValidatorFrozenIndexUnavailable
	}
	prefix := append([]byte{validatorFrozenBindingPrefix}, validator...)
	rows, cursor, err := k.validatorBindingPage(ctx, prefix, after, validatorFrozenSourceOffset, 53, limit,
		func(key, source []byte) (*bindingRecord, error) {
			row, err := k.readBindingRow(ctx, source)
			if err != nil {
				return nil, err
			}
			if row.frozen == nil || !row.frozen.Active ||
				(!bytes.Equal(validator, row.frozen.SourceValidator) && !bytes.Equal(validator, row.frozen.DestinationValidator)) ||
				!bytes.Equal(key, validatorFrozenBindingKey(validator, row.key)) {
				return nil, errors.New("native validator frozen index/source row mismatch")
			}
			return row, nil
		})
	if err != nil {
		return nil, nil, err
	}
	var out []types.EVMFrozenBinding
	for _, row := range rows {
		out = append(out, *row.frozen)
	}
	return out, cursor, nil
}

func (k *StateKeeper) validatorBindingPage(ctx sdk.Context, prefix, after []byte, sourceOffset, sourceSize int, limit uint32,
	load func(key, source []byte) (*bindingRecord, error),
) (rows []*bindingRecord, cursor []byte, err error) {
	if limit == 0 || limit > types.MaxBindingPageSize {
		return nil, nil, errors.New("invalid native validator binding page limit")
	}
	start := prefix
	if len(after) != 0 {
		if len(after) != sourceOffset+sourceSize || !bytes.HasPrefix(after, prefix) || !validBindingRawKey(after[sourceOffset:]) {
			return nil, nil, errors.New("invalid native validator binding continuation")
		}
		start = append(bytes.Clone(after), 0)
	}
	it, err := k.service.OpenKVStore(ctx).Iterator(start, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if closeErr := it.Close(); err == nil && closeErr != nil {
			rows, cursor, err = nil, nil, closeErr
		}
	}()
	for ; it.Valid() && uint32(len(rows)) < limit; it.Next() {
		if err := it.Error(); err != nil {
			return nil, nil, err
		}
		key, source := bytes.Clone(it.Key()), bytes.Clone(it.Value())
		if len(key) != sourceOffset+sourceSize || !bytes.HasPrefix(key, prefix) || !bytes.Equal(key[sourceOffset:], source) {
			return nil, nil, errors.New("invalid native validator binding index")
		}
		row, err := load(key, source)
		if err != nil {
			return nil, nil, err
		}
		if err := k.checkBindingIndexes(ctx, row); err != nil {
			return nil, nil, err
		}
		rows = append(rows, row)
		cursor = key
	}
	if !it.Valid() {
		cursor = nil
	}
	return rows, cursor, nil
}
