package keeper

import (
	"bytes"
	"errors"
	"slices"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	nativeLiveValidatorRosterKey     byte = 0x68
	nativeLiveValidatorRosterVersion byte = 1
	nativeLiveValidatorAddressSize        = 20
	MaxNativeLiveValidators               = 16
)

var ErrNativeLiveValidatorRosterFull = errors.New("native live validator roster is full")

type nativeLiveOperatorID [nativeLiveValidatorAddressSize]byte

// InitializeNativeLiveValidatorRoster opts a fresh genesis into bounded
// operator selection while preserving metadata for later evidence lookups.
func (k *StateKeeper) InitializeNativeLiveValidatorRoster(ctx sdk.Context) error {
	if ctx.BlockHeight() != 0 {
		return errors.New("native live validator roster requires fresh genesis")
	}
	child, write := ctx.CacheContext()
	if _, err := k.readNativeLiveValidatorRoster(child); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return err
		}
		return errors.New("native live validator roster is already initialized")
	}
	enabled, err := k.NativeStakeProvenanceEnabled(child)
	if err != nil {
		return err
	}
	if !enabled {
		return errors.New("native live validator roster requires stake provenance")
	}
	if _, err := k.readNativeEvidenceClock(child); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return err
		}
		return errors.New("native live validator roster must initialize before the first block")
	}
	validators, err := k.GetValidators(child)
	if err != nil {
		return err
	}
	if len(validators) == 0 || len(validators) > MaxNativeLiveValidators {
		return errors.New("native genesis validator roster must contain between one and sixteen operators")
	}
	operators := make([]nativeLiveOperatorID, 0, len(validators))
	for _, validator := range validators {
		id, err := k.nativeLiveOperatorID(validator.OperatorAddress)
		if err != nil {
			return err
		}
		consensus, err := validator.GetConsAddr()
		if err != nil {
			return err
		}
		if _, err := k.GetNativeDuplicateVoteExclusion(child, consensus); !errors.Is(err, ErrNotFound) {
			if err != nil {
				return err
			}
			return ErrNativeConsensusKeyPermanentlyExcluded
		}
		operators = append(operators, id)
	}
	slices.SortFunc(operators, compareNativeLiveOperators)
	if err := k.writeNativeLiveValidatorRoster(child, operators); err != nil {
		return err
	}
	write()
	return nil
}

// NativeLiveValidatorOperators returns the bounded set of registered
// operators whose keys have not been permanently excluded. The order is the
// raw operator address order. ErrNotFound identifies an uninitialized profile.
func (k *StateKeeper) NativeLiveValidatorOperators(ctx sdk.Context) ([]string, error) {
	ids, err := k.readNativeLiveValidatorRoster(ctx)
	if err != nil {
		return nil, err
	}
	codec := k.ValidatorAddressCodec()
	operators := make([]string, 0, len(ids))
	for _, id := range ids {
		operator, err := codec.BytesToString(id[:])
		if err != nil {
			return nil, err
		}
		operators = append(operators, operator)
	}
	return operators, nil
}

func compareNativeLiveOperators(a, b nativeLiveOperatorID) int {
	return bytes.Compare(a[:], b[:])
}

func (k *StateKeeper) nativeLiveOperatorID(operator string) (nativeLiveOperatorID, error) {
	key, err := operatorKey(nativeLiveValidatorRosterKey, operator)
	if err != nil {
		return nativeLiveOperatorID{}, err
	}
	raw := key[1:]
	canonical, err := k.ValidatorAddressCodec().BytesToString(raw)
	if err != nil {
		return nativeLiveOperatorID{}, err
	}
	if operator != canonical {
		return nativeLiveOperatorID{}, errors.New("native live validator operator address must use canonical Bech32")
	}
	return nativeLiveOperatorID(raw), nil
}

func (k *StateKeeper) readNativeLiveValidatorRoster(ctx sdk.Context) ([]nativeLiveOperatorID, error) {
	data, err := k.service.OpenKVStore(ctx).Get([]byte{nativeLiveValidatorRosterKey})
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, ErrNotFound
	}
	if len(data) == 0 || data[0] != nativeLiveValidatorRosterVersion || (len(data)-1)%nativeLiveValidatorAddressSize != 0 ||
		len(data) > 1+nativeLiveValidatorAddressSize*MaxNativeLiveValidators {
		return nil, errors.New("invalid native live validator roster encoding")
	}
	ids := make([]nativeLiveOperatorID, (len(data)-1)/nativeLiveValidatorAddressSize)
	for i := range ids {
		start := 1 + i*nativeLiveValidatorAddressSize
		copy(ids[i][:], data[start:start+nativeLiveValidatorAddressSize])
		if i > 0 && compareNativeLiveOperators(ids[i-1], ids[i]) >= 0 {
			return nil, errors.New("native live validator roster is not sorted and unique")
		}
	}
	return ids, nil
}

func (k *StateKeeper) writeNativeLiveValidatorRoster(ctx sdk.Context, ids []nativeLiveOperatorID) error {
	data := make([]byte, 1, 1+nativeLiveValidatorAddressSize*len(ids))
	data[0] = nativeLiveValidatorRosterVersion
	for _, id := range ids {
		data = append(data, id[:]...)
	}
	return k.service.OpenKVStore(ctx).Set([]byte{nativeLiveValidatorRosterKey}, data)
}

func (k *StateKeeper) updateNativeLiveValidatorRoster(ctx sdk.Context, operator string, register bool) error {
	ids, err := k.readNativeLiveValidatorRoster(ctx)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	id, err := k.nativeLiveOperatorID(operator)
	if err != nil {
		return err
	}
	i, found := slices.BinarySearchFunc(ids, id, compareNativeLiveOperators)
	if register {
		if found {
			return errors.New("native live validator roster contains a new operator before registration")
		}
		if len(ids) == MaxNativeLiveValidators {
			return ErrNativeLiveValidatorRosterFull
		}
		ids = slices.Insert(ids, i, id)
	} else {
		if !found {
			return errors.New("native live validator roster is missing the operator being excluded")
		}
		ids = slices.Delete(ids, i, i+1)
	}
	return k.writeNativeLiveValidatorRoster(ctx, ids)
}
