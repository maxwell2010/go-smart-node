package keeper

import (
	"bytes"
	"errors"
	"slices"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const nativeValidatorRetirementPrefix byte = 0x69

var ErrNativeValidatorRetired = errors.New("native validator has retired permanently")

func (k *StateKeeper) IsNativeValidatorRetired(ctx sdk.Context, operator string) (bool, error) {
	key, err := operatorKey(nativeValidatorRetirementPrefix, operator)
	if err != nil {
		return false, err
	}
	marker, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil || marker == nil {
		return false, err
	}
	if !bytes.Equal(marker, []byte{1}) {
		return false, errors.New("invalid native validator retirement marker")
	}
	return true, nil
}

func (k *StateKeeper) requireNativeValidatorNotRetired(ctx sdk.Context, operator string) error {
	retired, err := k.IsNativeValidatorRetired(ctx, operator)
	if err != nil {
		return err
	}
	if retired {
		return ErrNativeValidatorRetired
	}
	return nil
}

// RetireNativeValidator removes an empty operator from the live roster during
// governance EndBlock, after the H+1/H+2 schedule is recorded. The caller
// proves the Master pause and checks the future set in the same outer cache.
func (k *StateKeeper) RetireNativeValidator(ctx sdk.Context, operator string) error {
	if ctx.ExecMode() != sdk.ExecModeFinalize {
		return errors.New("native validator retirement requires a finalized block")
	}
	child, write := ctx.CacheContext()
	operators, err := k.NativeLiveValidatorOperators(child)
	if err != nil {
		return err
	}
	if !slices.Contains(operators, operator) {
		return errors.New("native validator retirement requires a live operator")
	}
	validator, err := k.GetValidator(child, operator)
	if err != nil {
		return err
	}
	if validator.Jailed || validator.Status != types.BondStatus_Unbonded || validator.Stake != 0 {
		return errors.New("native validator retirement requires an unjailed operator with no consensus power")
	}
	pending, err := k.HasPendingNativeDuplicateVotePenalty(child, operator)
	if err != nil {
		return err
	}
	if pending {
		return errors.New("native validator retirement requires no unresolved historical penalty")
	}
	count, err := k.delegationCount(child, operator)
	if err != nil {
		return err
	}
	reserve, err := k.GetActiveDELReserve(child, operator)
	if err != nil {
		return err
	}
	raw, err := k.ValidatorAddressCodec().StringToBytes(operator)
	if err != nil {
		return err
	}
	active, _, err := k.GetActiveStakePenaltyPage(child, raw, nil, 1)
	if err != nil {
		return err
	}
	frozen, _, err := k.GetValidatorFrozenBindingsPage(child, raw, nil, 1)
	if err != nil {
		return err
	}
	if count != 0 || !reserve.IsZero() || len(active) != 0 || len(frozen) != 0 {
		return errors.New("native validator retirement requires no active stake or frozen custody")
	}
	schedule, err := k.readScheduledPair(child.WithBlockHeight(ctx.BlockHeight() + 1))
	if err != nil {
		return err
	}
	for _, set := range schedule {
		for _, scheduled := range set.Validators {
			if scheduled.OperatorAddress == operator {
				return errors.New("native validator retirement requires absence from the committed future consensus sets")
			}
		}
	}
	validator.Online = false
	if err := k.SetValidator(child, validator); err != nil {
		return err
	}
	if err := k.updateNativeLiveValidatorRoster(child, operator, false); err != nil {
		return err
	}
	key, err := operatorKey(nativeValidatorRetirementPrefix, operator)
	if err != nil {
		return err
	}
	if err := k.service.OpenKVStore(child).Set(key, []byte{1}); err != nil {
		return err
	}
	write()
	return nil
}
