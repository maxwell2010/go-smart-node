package keeper

import (
	"encoding/binary"
	"errors"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	nativeEvidenceLimitsKey     byte = 0x65
	nativeEvidenceLimitsVersion byte = 1
	nativeEvidenceLimitsSize         = 1 + 8 + 8
)

// NativeEvidenceAgeLimits are the immutable age limits shared by historical
// penalty admission and withdrawal finality on a fresh native network.
type NativeEvidenceAgeLimits struct {
	MaxAgeNumBlocks int64
	MaxAgeDuration  time.Duration
}

// InitializeNativeEvidenceLimits pins the age limits in the original InitChain
// cache. The caller commits them with the authenticated genesis profile.
func (k *StateKeeper) InitializeNativeEvidenceLimits(ctx sdk.Context, evidence *cmtproto.EvidenceParams) error {
	if ctx.BlockHeight() != 0 || ctx.BlockTime().Unix() <= 0 {
		return errors.New("native evidence limits require fresh height-one genesis")
	}
	limits, err := nativeEvidenceAgeFromParams(evidence)
	if err != nil {
		return err
	}
	state := k.service.OpenKVStore(ctx)
	key := []byte{nativeEvidenceLimitsKey}
	exists, err := state.Has(key)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("native evidence limits are already initialized")
	}
	var data [nativeEvidenceLimitsSize]byte
	data[0] = nativeEvidenceLimitsVersion
	binary.BigEndian.PutUint64(data[1:9], uint64(limits.MaxAgeNumBlocks))
	binary.BigEndian.PutUint64(data[9:], uint64(limits.MaxAgeDuration))
	return state.Set(key, data[:])
}

// NativeEvidenceLimits returns the genesis age limits from the requested
// consensus-state snapshot.
func (k *StateKeeper) NativeEvidenceLimits(ctx sdk.Context) (NativeEvidenceAgeLimits, error) {
	data, err := k.service.OpenKVStore(ctx).Get([]byte{nativeEvidenceLimitsKey})
	if err != nil {
		return NativeEvidenceAgeLimits{}, err
	}
	if data == nil {
		return NativeEvidenceAgeLimits{}, ErrNotFound
	}
	if len(data) != nativeEvidenceLimitsSize || data[0] != nativeEvidenceLimitsVersion {
		return NativeEvidenceAgeLimits{}, errors.New("invalid native evidence limits encoding")
	}
	limits := NativeEvidenceAgeLimits{
		MaxAgeNumBlocks: int64(binary.BigEndian.Uint64(data[1:9])),
		MaxAgeDuration:  time.Duration(binary.BigEndian.Uint64(data[9:])),
	}
	if limits.MaxAgeNumBlocks <= 0 || limits.MaxAgeDuration <= 0 {
		return NativeEvidenceAgeLimits{}, errors.New("invalid native evidence age limits")
	}
	return limits, nil
}

// VerifyNativeEvidenceLimits requires the current Comet evidence age limits to
// match genesis and the current evidence byte budget to remain positive.
func (k *StateKeeper) VerifyNativeEvidenceLimits(ctx sdk.Context, evidence *cmtproto.EvidenceParams) error {
	current, err := nativeEvidenceAgeFromParams(evidence)
	if err != nil {
		return err
	}
	pinned, err := k.NativeEvidenceLimits(ctx)
	if err != nil {
		return err
	}
	if current != pinned {
		return errors.New("native evidence age limits differ from genesis")
	}
	return nil
}

func nativeEvidenceAgeFromParams(evidence *cmtproto.EvidenceParams) (NativeEvidenceAgeLimits, error) {
	if evidence == nil || evidence.MaxAgeNumBlocks <= 0 || evidence.MaxAgeDuration <= 0 || evidence.MaxBytes <= 0 {
		return NativeEvidenceAgeLimits{}, errors.New("native evidence requires positive age limits and evidence bytes")
	}
	return NativeEvidenceAgeLimits{MaxAgeNumBlocks: evidence.MaxAgeNumBlocks, MaxAgeDuration: evidence.MaxAgeDuration}, nil
}
