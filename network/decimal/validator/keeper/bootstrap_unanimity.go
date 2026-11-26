package keeper

import (
	"encoding/binary"
	"errors"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// An absent key has never been activated. Zero marks a disabled, consumed plan.
const bootstrapUnanimityKey byte = 0x85

func (k *StateKeeper) BootstrapUnanimityMinimumID(ctx sdk.Context) (uint64, error) {
	// Historical transactions consumed no gas for this marker. Use a separate
	// meter for this one fixed-size read so replay charges the original gas.
	read := ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	value, err := k.service.OpenKVStore(read).Get([]byte{bootstrapUnanimityKey})
	if err != nil || value == nil {
		return 0, err
	}
	if len(value) != 8 {
		return 0, errors.New("invalid bootstrap unanimity marker")
	}
	return binary.BigEndian.Uint64(value), nil
}

func (k *StateKeeper) InitializeBootstrapUnanimity(ctx sdk.Context, minimum uint64) error {
	store := k.service.OpenKVStore(ctx)
	key := []byte{bootstrapUnanimityKey}
	exists, err := store.Has(key)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("bootstrap unanimity plan already consumed")
	}
	return store.Set(key, binary.BigEndian.AppendUint64(nil, minimum))
}
