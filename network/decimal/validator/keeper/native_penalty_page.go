package keeper

import (
	"bytes"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

// GetPendingActivePenaltyKindPage starts at the first still-unrealized source
// of one kind under an authenticated old cursor. Each successful mutation
// removes its row from this interval. It never reuses a snapshot continuation
// after the cursor-index keys move. The coordinator must fence custody changes.
func (k *StateKeeper) GetPendingActivePenaltyKindPage(ctx sdk.Context, validator, cursor []byte, kind types.EVMStakeBindingKind, limit uint32) ([]types.EVMStakeBinding, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return nil, err
	}
	if !validBindingValidator(validator) || len(cursor) != 32 || (kind != types.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN && kind != types.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT) {
		return nil, errors.New("invalid pending penalty page identity")
	}
	prefix := append([]byte{activePenaltyBindingPrefix}, validator...)
	prefix = append(prefix, cursor...)
	prefix = append(prefix, stakeBindingPrefix, byte(kind))
	rows, _, err := k.validatorBindingPage(ctx, prefix, nil, activePenaltySourceOffset, 54, limit, func(key, source []byte) (*bindingRecord, error) {
		return k.loadActivePenaltyRow(ctx, validator, key, source)
	})
	if err != nil {
		return nil, err
	}
	out := make([]types.EVMStakeBinding, 0, len(rows))
	for _, row := range rows {
		if row.stake.Kind != kind || !bytes.Equal(row.stake.PenaltyCursor, cursor) {
			return nil, errors.New("pending penalty page differs from exact cursor and kind")
		}
		out = append(out, *row.stake)
	}
	return out, nil
}
