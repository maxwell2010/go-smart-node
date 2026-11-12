package keeper

import (
	"bytes"
	"errors"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

var ErrPendingStakePenalties = errors.New("active native stake bindings still require penalty catchup")
var ErrSourcePenaltyCursor = errors.New("active source penalty cursor exceeds authenticated append count")

func activePenaltyBindingKey(validator, cursor, sourceKey []byte) []byte {
	key := append([]byte{activePenaltyBindingPrefix}, validator...)
	key = append(key, cursor...)
	return append(key, sourceKey...)
}

// extremeActivePenaltyCursor performs one ordered prefix seek and one source
// row read. It never walks unrelated stakes or invokes ExportGenesis. The source
// Genesis/export establish complete index coverage; this hot query is not a
// full-store corruption audit. The row is structurally validated; contract
// authenticity and NFT backing remain
// separate mandatory controller attestations.
func (k *StateKeeper) extremeActivePenaltyCursor(ctx sdk.Context, validator []byte, reverse bool) (cursor []byte, found bool, err error) {
	if err = k.checkBindingDomain(ctx); err != nil {
		return nil, false, err
	}
	if !validBindingValidator(validator) {
		return nil, false, errors.New("invalid native source validator address")
	}
	prefix := append([]byte{activePenaltyBindingPrefix}, validator...)
	state := k.service.OpenKVStore(ctx)
	open := state.Iterator
	if reverse {
		open = state.ReverseIterator
	}
	it, err := open(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if closeErr := it.Close(); err == nil && closeErr != nil {
			cursor = nil
			found = false
			err = closeErr
		}
	}()
	if !it.Valid() {
		return nil, false, nil
	}
	key, sourceKey := bytes.Clone(it.Key()), bytes.Clone(it.Value())
	if err = it.Error(); err != nil {
		return nil, false, err
	}
	row, err := k.loadActivePenaltyRow(ctx, validator, key, sourceKey)
	if err != nil {
		return nil, false, err
	}
	return bytes.Clone(row.stake.PenaltyCursor), true, nil
}

const activePenaltySourceOffset = 1 + 20 + 32

func (k *StateKeeper) loadActivePenaltyRow(ctx sdk.Context, validator, key, sourceKey []byte) (*bindingRecord, error) {
	if len(key) != activePenaltySourceOffset+54 || len(sourceKey) != 54 || !bytes.Equal(key[activePenaltySourceOffset:], sourceKey) {
		return nil, errors.New("corrupt active penalty cursor index")
	}
	row, err := k.readBindingRow(ctx, sourceKey)
	if err != nil {
		return nil, err
	}
	if row.stake == nil || !row.stake.Active || !bytes.Equal(row.stake.Validator, validator) || !bytes.Equal(activePenaltyBindingKey(row.stake.Validator, row.stake.PenaltyCursor, row.key), key) {
		return nil, errors.New("active penalty cursor index/source row mismatch")
	}
	return row, nil
}

// GetActiveStakePenaltyPage returns active coin and NFT sources for one
// validator, in full-word cursor and source-key order. Use the continuation
// only in the same state snapshot. A nil continuation exhausts the prefix.
func (k *StateKeeper) GetActiveStakePenaltyPage(ctx sdk.Context, validator, after []byte, limit uint32) ([]types.EVMStakeBinding, []byte, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return nil, nil, err
	}
	if !validBindingValidator(validator) {
		return nil, nil, errors.New("invalid native source validator address")
	}
	prefix := append([]byte{activePenaltyBindingPrefix}, validator...)
	rows, cursor, err := k.validatorBindingPage(ctx, prefix, after, activePenaltySourceOffset, 54, limit,
		func(key, source []byte) (*bindingRecord, error) {
			return k.loadActivePenaltyRow(ctx, validator, key, source)
		})
	if err != nil {
		return nil, nil, err
	}
	var out []types.EVMStakeBinding
	for _, row := range rows {
		out = append(out, *row.stake)
	}
	return out, cursor, nil
}

// GetMinActiveStakePenaltyCursor returns the lowest full uint256 cursor among
// active coin and NFT bindings. Frozen/closed positions do not own this index.
// An empty result does not establish positive power or validator eligibility.
func (k *StateKeeper) GetMinActiveStakePenaltyCursor(ctx sdk.Context, validator []byte) ([]byte, bool, error) {
	return k.extremeActivePenaltyCursor(ctx, validator, false)
}

// RequireActiveStakePenaltyCatchup checks both ordered endpoints in constant
// seeks. The maximum guard rejects a future/corrupt cursor even when another
// current cursor is the minimum. The target must come from the authenticated
// same-context MasterValidator append count, never a caller-selected value.
// Call after planned writes in the coordinator's child cache, before committing
// eligibility/power. Empty bindings still require the separate backing/power gate.
func (k *StateKeeper) RequireActiveStakePenaltyCatchup(ctx sdk.Context, validator, currentCount []byte) error {
	if len(currentCount) != 32 {
		return errors.New("authenticated penalty append count must retain 32 bytes")
	}
	minimum, found, err := k.extremeActivePenaltyCursor(ctx, validator, false)
	if err != nil || !found {
		return err
	}
	if bytes.Compare(minimum, currentCount) < 0 {
		return ErrPendingStakePenalties
	}
	if bytes.Compare(minimum, currentCount) > 0 {
		return ErrSourcePenaltyCursor
	}
	maximum, found, err := k.extremeActivePenaltyCursor(ctx, validator, true)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("active penalty cursor index changed during same-context check")
	}
	if bytes.Compare(maximum, currentCount) > 0 {
		return ErrSourcePenaltyCursor
	}
	if bytes.Compare(maximum, currentCount) < 0 {
		return ErrPendingStakePenalties
	}
	return nil
}
