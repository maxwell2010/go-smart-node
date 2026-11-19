package keeper

import (
	"errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const nativeValidatorAvatarPrefix byte = 0x82

func (k *StateKeeper) GetNativeValidatorAvatar(ctx sdk.Context, operator string) (string, error) {
	raw, err := k.ValidatorAddressCodec().StringToBytes(operator)
	if err != nil {
		return "", err
	}
	value, err := k.service.OpenKVStore(ctx).Get(append([]byte{nativeValidatorAvatarPrefix}, raw...))
	if err != nil {
		return "", err
	}
	if len(value) > 512 {
		return "", errors.New("stored validator avatar exceeds bound")
	}
	return string(value), nil
}

// SetNativeValidatorAvatar is called only by the authenticated control projector.
func (k *StateKeeper) SetNativeValidatorAvatar(ctx sdk.Context, operator, value string) error {
	if len(value) > 512 {
		return errors.New("validator avatar exceeds bound")
	}
	if _, err := k.GetValidator(ctx, operator); err != nil {
		return err
	}
	raw, err := k.ValidatorAddressCodec().StringToBytes(operator)
	if err != nil {
		return err
	}
	key := append([]byte{nativeValidatorAvatarPrefix}, raw...)
	if value == "" {
		return k.service.OpenKVStore(ctx).Delete(key)
	}
	return k.service.OpenKVStore(ctx).Set(key, []byte(value))
}
