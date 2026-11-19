package keeper

import (
	"errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const nativeValidatorAvatarPrefix byte = 0x82

func (k *StateKeeper) GetNativeValidatorAvatar(ctx sdk.Context, operator string) (string, error) {
	key, err := operatorKey(nativeValidatorAvatarPrefix, operator)
	if err != nil {
		return "", err
	}
	value, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return "", err
	}
	if len(value) > types.MaxValidatorAvatarURIBytes {
		return "", errors.New("stored validator avatar exceeds bound")
	}
	if value != nil && len(value) == 0 {
		return "", errors.New("stored validator avatar is empty instead of absent")
	}
	if err := types.ValidateValidatorAvatarURI(string(value)); err != nil {
		return "", err
	}
	return string(value), nil
}

// SetNativeValidatorAvatar is internal state plumbing for the authenticated
// self-control projector and validated genesis import, not an authorization API.
func (k *StateKeeper) SetNativeValidatorAvatar(ctx sdk.Context, operator, value string) error {
	if err := types.ValidateValidatorAvatarURI(value); err != nil {
		return err
	}
	if _, err := k.GetValidator(ctx, operator); err != nil {
		return err
	}
	key, err := operatorKey(nativeValidatorAvatarPrefix, operator)
	if err != nil {
		return err
	}
	if value == "" {
		return k.service.OpenKVStore(ctx).Delete(key)
	}
	return k.service.OpenKVStore(ctx).Set(key, []byte(value))
}

// Avatar entries are exported in the KV iterator's ascending raw operator-byte
// order. Association is derived from validated validators, never URI contents.
func (k *StateKeeper) exportNativeValidatorAvatars(ctx sdk.Context, g *types.GenesisState) error {
	owners := make(map[string]string, len(g.Validators))
	for _, validator := range g.Validators {
		key, err := operatorKey(nativeValidatorAvatarPrefix, validator.OperatorAddress)
		if err != nil {
			return err
		}
		if _, duplicate := owners[string(key)]; duplicate {
			return errors.New("duplicate validator avatar key owner")
		}
		owners[string(key)] = validator.OperatorAddress
	}
	return k.scan(ctx, nativeValidatorAvatarPrefix, func(key, value []byte) error {
		if len(key) != 21 || key[0] != nativeValidatorAvatarPrefix {
			return errors.New("malformed validator avatar key")
		}
		operator, exists := owners[string(key)]
		if !exists {
			return errors.New("validator avatar key references unknown operator")
		}
		if len(value) == 0 || len(value) > types.MaxValidatorAvatarURIBytes {
			return errors.New("stored validator avatar reference has invalid size")
		}
		if err := types.ValidateValidatorAvatarURI(string(value)); err != nil {
			return err
		}
		g.ValidatorAvatars = append(g.ValidatorAvatars, types.ValidatorAvatar{OperatorAddress: operator, AvatarURI: string(value)})
		return nil
	})
}
