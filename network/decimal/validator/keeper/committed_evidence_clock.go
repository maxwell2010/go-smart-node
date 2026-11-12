package keeper

import (
	"encoding/binary"
	"errors"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	committedEvidenceClockKey  byte = 0x61
	committedEvidenceClockSize      = 1 + 8 + 8 + 4
)

// PreviousNativeEvidenceBlock returns the canonical clock saved by the
// immediately preceding block. It does not depend on validator history pruning
// or on CometBFT's local evidence pool.
func (k *StateKeeper) PreviousNativeEvidenceBlock(ctx sdk.Context) (CommittedEvidenceBlock, error) {
	if ctx.BlockHeight() <= 1 {
		return CommittedEvidenceBlock{}, errors.New("no previous committed native evidence block")
	}
	previous, err := k.readNativeEvidenceClock(ctx)
	if err != nil {
		return CommittedEvidenceBlock{}, err
	}
	if previous.Height != ctx.BlockHeight()-1 || !ctx.BlockTime().After(previous.Time) {
		return CommittedEvidenceBlock{}, errors.New("native evidence clock disagrees with the current block")
	}
	return previous, nil
}

// NativeEvidenceBlockAtSnapshot authenticates the clock at the read-only
// SDK snapshot used by transaction simulation and RPC gas estimation.
func (k *StateKeeper) NativeEvidenceBlockAtSnapshot(ctx sdk.Context) (CommittedEvidenceBlock, error) {
	clock, err := k.readNativeEvidenceClock(ctx)
	if err != nil {
		return CommittedEvidenceBlock{}, err
	}
	if clock.Height != ctx.BlockHeight() || !clock.Time.Equal(ctx.BlockTime()) {
		return CommittedEvidenceBlock{}, errors.New("native evidence clock disagrees with the committed snapshot")
	}
	return clock, nil
}

// RecordNativeEvidenceBlock stages the current canonical height and time. The
// caller invokes it at EndBlock in the same cache as the native transitions.
// A failed or uncommitted block must discard that cache.
func (k *StateKeeper) RecordNativeEvidenceBlock(ctx sdk.Context) error {
	height, timestamp := ctx.BlockHeight(), ctx.BlockTime()
	if height <= 0 || timestamp.Unix() <= 0 {
		return errors.New("invalid native evidence block header")
	}
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return errors.New("native evidence clock requires stake provenance")
	}
	if height == 1 {
		if _, err := k.readNativeEvidenceClock(ctx); !errors.Is(err, ErrNotFound) {
			if err != nil {
				return err
			}
			return errors.New("native evidence clock already initialized")
		}
	} else if _, err := k.PreviousNativeEvidenceBlock(ctx); err != nil {
		return err
	}
	var data [committedEvidenceClockSize]byte
	data[0] = 1
	binary.BigEndian.PutUint64(data[1:9], uint64(height))
	binary.BigEndian.PutUint64(data[9:17], uint64(timestamp.Unix()))
	binary.BigEndian.PutUint32(data[17:], uint32(timestamp.Nanosecond()))
	return k.service.OpenKVStore(ctx).Set([]byte{committedEvidenceClockKey}, data[:])
}

func (k *StateKeeper) readNativeEvidenceClock(ctx sdk.Context) (CommittedEvidenceBlock, error) {
	data, err := k.service.OpenKVStore(ctx).Get([]byte{committedEvidenceClockKey})
	if err != nil {
		return CommittedEvidenceBlock{}, err
	}
	if data == nil {
		return CommittedEvidenceBlock{}, ErrNotFound
	}
	if len(data) != committedEvidenceClockSize || data[0] != 1 {
		return CommittedEvidenceBlock{}, errors.New("invalid native evidence clock encoding")
	}
	height := int64(binary.BigEndian.Uint64(data[1:9]))
	seconds := int64(binary.BigEndian.Uint64(data[9:17]))
	nanos := int64(binary.BigEndian.Uint32(data[17:]))
	if height <= 0 || seconds <= 0 || nanos >= int64(time.Second) {
		return CommittedEvidenceBlock{}, errors.New("invalid native evidence clock fields")
	}
	return CommittedEvidenceBlock{Height: height, Time: time.Unix(seconds, nanos).UTC()}, nil
}
