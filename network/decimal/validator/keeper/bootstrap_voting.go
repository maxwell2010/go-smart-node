package keeper

import (
	"errors"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

// BootstrapVotingSnapshotFits checks the same active prefixes used by
// ReadActiveDELVotingState before an optional early tally. Beyond this
// small bootstrap size the regular voting deadline remains in effect.
func (k *StateKeeper) BootstrapVotingSnapshotFits(ctx sdk.Context, expected [4]string) (bool, error) {
	const maxRowsPerPrefix = int(types.MaxBindingPageSize)
	roster, err := k.NativeLiveValidatorOperators(ctx)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil || len(roster) != len(expected) {
		return false, err
	}
	wanted := make(map[string]bool, len(expected))
	for _, operator := range expected {
		wanted[operator] = true
	}
	for _, operator := range roster {
		if !wanted[operator] {
			return false, nil
		}
		delete(wanted, operator)
		raw, err := k.ValidatorAddressCodec().StringToBytes(operator)
		if err != nil {
			return false, err
		}
		for _, prefix := range [][]byte{composite(validatorDelegationPrefix, operator), append([]byte{activePenaltyBindingPrefix}, raw...)} {
			it, err := k.service.OpenKVStore(ctx).Iterator(prefix, storetypes.PrefixEndBytes(prefix))
			if err != nil {
				return false, err
			}
			n := 0
			for ; it.Valid() && n <= maxRowsPerPrefix; it.Next() {
				if err := it.Error(); err != nil {
					return false, errors.Join(err, it.Close())
				}
				n++
			}
			if err := it.Close(); err != nil {
				return false, err
			}
			if n > maxRowsPerPrefix {
				return false, nil
			}
		}
	}
	return len(wanted) == 0, nil
}
