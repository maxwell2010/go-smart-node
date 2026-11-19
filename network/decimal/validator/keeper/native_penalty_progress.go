package keeper

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	nativePenaltyProgressPrefix byte = 0x72
	NativePenaltyDowntime       byte = 1
	NativePenaltyDuplicateVote  byte = 2
	NativePenaltyActiveNFT      byte = 1
	NativePenaltyActiveCoin     byte = 2
	NativePenaltyFrozen         byte = 3
	NativePenaltyFinished       byte = 4
)

// NativePenaltyProgress pins one Master announcement while financial work is
// realized across bounded blocks. It is not a user message or a trust source.
// An authenticated coordinator must atomically announce, jail and create it.
type NativePenaltyProgress struct {
	Version                          uint8
	Reason                           byte
	Operator                         string
	ConsensusAddress                 []byte
	InfractionHeight, AdmittedHeight int64
	Percentage                       uint16
	BeforeCursor, AfterCursor        []byte
	Phase                            byte
	FrozenAfter                      []byte
	Burned                           sdkmath.Int
}

func nativePenaltyProgressKey(reason byte, validator []byte) ([]byte, error) {
	if (reason != NativePenaltyDowntime && reason != NativePenaltyDuplicateVote) || !validBindingValidator(validator) {
		return nil, errors.New("invalid penalty progress identity")
	}
	return append([]byte{nativePenaltyProgressPrefix, reason}, validator...), nil
}

func (k *StateKeeper) penaltyProgressValidator(p NativePenaltyProgress) ([]byte, error) {
	raw, err := k.ValidatorAddressCodec().StringToBytes(p.Operator)
	if err != nil || !validBindingValidator(raw) {
		return nil, errors.New("invalid penalty progress operator")
	}
	if p.Version != 1 || !validBindingValidator(p.ConsensusAddress) || p.InfractionHeight <= 0 || p.AdmittedHeight <= p.InfractionHeight || p.Percentage == 0 || p.Percentage > 100 ||
		len(p.BeforeCursor) != 32 || len(p.AfterCursor) != 32 || p.Phase < NativePenaltyActiveNFT || p.Phase > NativePenaltyFinished || len(p.FrozenAfter) > 74 || p.Burned.IsNil() || p.Burned.IsNegative() {
		return nil, errors.New("invalid penalty progress record")
	}
	if len(p.FrozenAfter) != 0 {
		prefix := append([]byte{validatorFrozenBindingPrefix}, raw...)
		if p.Phase != NativePenaltyFrozen || len(p.FrozenAfter) != 74 || !bytes.HasPrefix(p.FrozenAfter, prefix) || !validBindingRawKey(p.FrozenAfter[validatorFrozenSourceOffset:]) {
			return nil, errors.New("invalid frozen penalty continuation")
		}
	}
	if new(big.Int).Add(new(big.Int).SetBytes(p.BeforeCursor), big.NewInt(1)).BitLen() > 256 || new(big.Int).Add(new(big.Int).SetBytes(p.BeforeCursor), big.NewInt(1)).Cmp(new(big.Int).SetBytes(p.AfterCursor)) != 0 {
		return nil, errors.New("penalty progress cursor is not one exact announcement")
	}
	if _, err := nativePenaltyProgressKey(p.Reason, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// PendingNativePenalty fences custody in constant reads, even if its durable
// job is malformed. Corrupt metadata must not turn a payout barrier off.
func (k *StateKeeper) PendingNativePenalty(ctx sdk.Context, validator []byte) (bool, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return false, err
	}
	for _, reason := range []byte{NativePenaltyDowntime, NativePenaltyDuplicateVote} {
		key, err := nativePenaltyProgressKey(reason, validator)
		if err != nil {
			return false, err
		}
		present, err := k.service.OpenKVStore(ctx).Has(key)
		if err != nil || present {
			return present, err
		}
	}
	return false, nil
}

func (k *StateKeeper) GetNativePenaltyProgress(ctx sdk.Context, validator []byte) (NativePenaltyProgress, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return NativePenaltyProgress{}, err
	}
	var found *NativePenaltyProgress
	for _, reason := range []byte{NativePenaltyDowntime, NativePenaltyDuplicateVote} {
		key, err := nativePenaltyProgressKey(reason, validator)
		if err != nil {
			return NativePenaltyProgress{}, err
		}
		raw, err := k.service.OpenKVStore(ctx).Get(key)
		if err != nil {
			return NativePenaltyProgress{}, err
		}
		if raw == nil {
			continue
		}
		if len(raw) == 0 || len(raw) > 4096 {
			return NativePenaltyProgress{}, errors.New("invalid penalty progress size")
		}
		var p NativePenaltyProgress
		if err := json.Unmarshal(raw, &p); err != nil {
			return NativePenaltyProgress{}, err
		}
		canonical, err := json.Marshal(p)
		if err != nil || !bytes.Equal(raw, canonical) {
			return NativePenaltyProgress{}, errors.New("noncanonical penalty progress")
		}
		owner, err := k.penaltyProgressValidator(p)
		if err != nil || p.Reason != reason || !bytes.Equal(owner, validator) || p.AdmittedHeight > ctx.BlockHeight() {
			return NativePenaltyProgress{}, errors.New("penalty progress differs from validator or block")
		}
		if found != nil {
			return NativePenaltyProgress{}, errors.New("overlapping validator penalty progress")
		}
		found = &p
	}
	if found == nil {
		return NativePenaltyProgress{}, ErrNotFound
	}
	return *found, nil
}

func (k *StateKeeper) StartNativePenaltyProgress(ctx sdk.Context, p NativePenaltyProgress) error {
	if ctx.ExecMode() != sdk.ExecModeFinalize {
		return errors.New("penalty progress requires finalized context")
	}
	if err := k.checkBindingDomain(ctx); err != nil {
		return err
	}
	validator, err := k.penaltyProgressValidator(p)
	if err != nil {
		return err
	}
	if p.AdmittedHeight != ctx.BlockHeight() || p.Phase != NativePenaltyActiveNFT || len(p.FrozenAfter) != 0 || !p.Burned.IsZero() {
		return errors.New("penalty progress must start at its admission block")
	}
	pending, err := k.PendingNativePenalty(ctx, validator)
	if err != nil {
		return err
	}
	if pending {
		return errors.New("validator already has pending penalty progress")
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	key, _ := nativePenaltyProgressKey(p.Reason, validator)
	return k.service.OpenKVStore(ctx).Set(key, data)
}

func (k *StateKeeper) AdvanceNativePenaltyProgress(ctx sdk.Context, before, after NativePenaltyProgress) error {
	validator, err := k.penaltyProgressValidator(before)
	if err != nil {
		return err
	}
	if _, err := k.penaltyProgressValidator(after); err != nil {
		return err
	}
	current, err := k.GetNativePenaltyProgress(ctx, validator)
	if err != nil {
		return err
	}
	expected, _ := json.Marshal(before)
	actual, _ := json.Marshal(current)
	if !bytes.Equal(expected, actual) {
		return errors.New("penalty progress prestate changed")
	}
	immutable := after
	immutable.Phase = before.Phase
	immutable.FrozenAfter = before.FrozenAfter
	immutable.Burned = before.Burned
	i, _ := json.Marshal(immutable)
	if ctx.ExecMode() != sdk.ExecModeFinalize || !bytes.Equal(i, expected) || after.Phase < before.Phase || after.Phase > before.Phase+1 || after.Burned.LT(before.Burned) ||
		(after.Phase == before.Phase && bytes.Compare(after.FrozenAfter, before.FrozenAfter) < 0) {
		return errors.New("invalid penalty progress transition")
	}
	data, _ := json.Marshal(after)
	key, _ := nativePenaltyProgressKey(after.Reason, validator)
	return k.service.OpenKVStore(ctx).Set(key, data)
}

func (k *StateKeeper) CompleteNativePenaltyProgress(ctx sdk.Context, expected NativePenaltyProgress) error {
	if expected.Phase != NativePenaltyFinished || ctx.ExecMode() != sdk.ExecModeFinalize {
		return errors.New("penalty progress is not fully reconciled")
	}
	validator, err := k.penaltyProgressValidator(expected)
	if err != nil {
		return err
	}
	current, err := k.GetNativePenaltyProgress(ctx, validator)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(expected)
	b, _ := json.Marshal(current)
	if !bytes.Equal(a, b) {
		return errors.New("penalty completion prestate differs")
	}
	key, _ := nativePenaltyProgressKey(expected.Reason, validator)
	return k.service.OpenKVStore(ctx).Delete(key)
}

// NextNativePenaltyProgress rotates before branching the financial attempt, so
// a poisoned page cannot starve other validators' jobs.
func (k *StateKeeper) NextNativePenaltyProgress(ctx sdk.Context, reason byte) (NativePenaltyProgress, error) {
	if ctx.ExecMode() != sdk.ExecModeFinalize {
		return NativePenaltyProgress{}, errors.New("penalty scheduling requires finalized context")
	}
	if err := k.checkBindingDomain(ctx); err != nil {
		return NativePenaltyProgress{}, err
	}
	if reason != NativePenaltyDowntime && reason != NativePenaltyDuplicateVote {
		return NativePenaltyProgress{}, errors.New("invalid progress reason")
	}
	state := k.service.OpenKVStore(ctx)
	prefix := []byte{nativePenaltyProgressPrefix, reason}
	cursorKey := []byte{nativePenaltyProgressPrefix, 0, reason}
	previous, err := state.Get(cursorKey)
	if err != nil {
		return NativePenaltyProgress{}, err
	}
	if previous != nil && (len(previous) != 22 || !bytes.HasPrefix(previous, prefix)) {
		return NativePenaltyProgress{}, errors.New("invalid progress rotation cursor")
	}
	seek := func(start, end []byte) (key []byte, err error) {
		it, err := state.Iterator(start, end)
		if err != nil {
			return nil, err
		}
		defer func() {
			if closeErr := it.Close(); err == nil && closeErr != nil {
				key, err = nil, closeErr
			}
		}()
		if err := it.Error(); err != nil {
			return nil, err
		}
		if !it.Valid() {
			return nil, ErrNotFound
		}
		key = bytes.Clone(it.Key())
		if len(key) != 22 || !bytes.HasPrefix(key, prefix) {
			return nil, errors.New("invalid penalty job key")
		}
		return key, nil
	}
	start := prefix
	if previous != nil {
		start = append(bytes.Clone(previous), 0)
	}
	key, err := seek(start, storetypes.PrefixEndBytes(prefix))
	if errors.Is(err, ErrNotFound) && previous != nil {
		key, err = seek(prefix, storetypes.PrefixEndBytes(prefix))
	}
	if err != nil {
		return NativePenaltyProgress{}, err
	}
	p, err := k.GetNativePenaltyProgress(ctx, key[2:])
	if err != nil {
		return NativePenaltyProgress{}, err
	}
	if err := state.Set(cursorKey, key); err != nil {
		return NativePenaltyProgress{}, err
	}
	return p, nil
}
