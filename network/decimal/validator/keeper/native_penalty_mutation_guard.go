package keeper

import (
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var ErrNativePenaltyPending = errors.New("validator financial mutations await pending native penalty")

// RequireNoPendingNativePenalty fences user custody mutations, including jobs
// which are admitted but have not yet announced their Master cursor. Private
// penalty realization must not call this user-entrypoint guard.
func (k *StateKeeper) RequireNoPendingNativePenalty(ctx sdk.Context, validators ...[]byte) error {
	if k == nil || nilInterface(k.service) {
		return errors.New("native penalty mutation guard requires a keeper")
	}
	for _, validator := range validators {
		if !validBindingValidator(validator) {
			return errors.New("native penalty mutation guard requires a validator identity")
		}
		if k.bindingDomain != nil {
			pending, err := k.PendingNativePenalty(ctx, validator)
			if err != nil {
				return err
			}
			if pending {
				return ErrNativePenaltyPending
			}
		} else {
			// Legacy in-memory keeper users have no EVM source domain, but must
			// still fail closed if this shared store contains any progress row.
			for _, reason := range []byte{NativePenaltyDowntime, NativePenaltyDuplicateVote} {
				key, err := nativePenaltyProgressKey(reason, validator)
				if err != nil {
					return err
				}
				present, err := k.service.OpenKVStore(ctx).Has(key)
				if err != nil {
					return err
				}
				if present {
					return ErrNativePenaltyPending
				}
			}
		}
		operator, err := k.ValidatorAddressCodec().BytesToString(validator)
		if err != nil {
			return err
		}
		pending, err := k.HasPendingNativeDuplicateVotePenalty(ctx, operator)
		if err != nil {
			return err
		}
		if pending {
			return ErrNativePenaltyPending
		}
	}
	return nil
}
