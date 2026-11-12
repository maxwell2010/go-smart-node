package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"

	cmtcrypto "github.com/cometbft/cometbft/crypto"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	nativeInfractionPrefix byte = 0x46
	nativeActiveJailPrefix byte = 0x47
	nativeDowntimeReason   byte = 1
)

var ErrNativeDowntimeAlreadyApplied = errors.New("native downtime infraction already applied")

// NativeDowntimeOutcome is the durable marker for one completed downtime
// infraction. JailUntil sets the earliest possible future release time.
type NativeDowntimeOutcome struct {
	Reason           string
	ConsensusAddress sdk.ConsAddress
	InfractionHeight int64
	JailUntil        time.Time
}

func nativeDowntimeOutcomeKey(consensus sdk.ConsAddress, infractionHeight int64) ([]byte, error) {
	if len(consensus) != cmtcrypto.AddressSize || infractionHeight <= 0 {
		return nil, errors.New("invalid native downtime infraction identity")
	}
	key := append([]byte{nativeInfractionPrefix, nativeDowntimeReason}, consensus...)
	return binary.BigEndian.AppendUint64(key, uint64(infractionHeight)), nil
}

// GetNativeDowntimeOutcome point-reads the exact consensus key and infraction
// height. It returns ErrNotFound before that infraction has been applied.
func (k *StateKeeper) GetNativeDowntimeOutcome(ctx sdk.Context, consensus sdk.ConsAddress, infractionHeight int64) (NativeDowntimeOutcome, error) {
	key, err := nativeDowntimeOutcomeKey(consensus, infractionHeight)
	if err != nil {
		return NativeDowntimeOutcome{}, err
	}
	data, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return NativeDowntimeOutcome{}, err
	}
	if data == nil {
		return NativeDowntimeOutcome{}, ErrNotFound
	}
	if len(data) != 13 || data[0] != 1 {
		return NativeDowntimeOutcome{}, errors.New("invalid native downtime outcome encoding")
	}
	seconds := int64(binary.BigEndian.Uint64(data[1:9]))
	nanos := binary.BigEndian.Uint32(data[9:])
	if seconds <= 0 || nanos >= uint32(time.Second) {
		return NativeDowntimeOutcome{}, errors.New("invalid native downtime outcome jail time")
	}
	return NativeDowntimeOutcome{
		Reason: "downtime", ConsensusAddress: bytes.Clone(consensus), InfractionHeight: infractionHeight,
		JailUntil: time.Unix(seconds, int64(nanos)).UTC(),
	}, nil
}

// ApplyNativeDowntimeOutcome records one exactly revalidated infraction and
// jails the operator. The caller commits the verified financial changes and
// checks future set eligibility in the same outer cache. EndBlock still owns
// bond status and power selection, including the existing H+2 schedule.
func (k *StateKeeper) ApplyNativeDowntimeOutcome(ctx sdk.Context, candidate NativeDowntimeCandidate) error {
	key, err := nativeDowntimeOutcomeKey(candidate.ConsensusAddress, candidate.InfractionHeight)
	if err != nil {
		return err
	}
	child, commit := ctx.CacheContext()
	if _, err := k.GetNativeDowntimeOutcome(child, candidate.ConsensusAddress, candidate.InfractionHeight); err == nil {
		return ErrNativeDowntimeAlreadyApplied
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	planned, err := k.PlanNativeDowntimePenalties(child)
	if err != nil {
		return err
	}
	for _, want := range planned {
		if want.Operator != candidate.Operator || !bytes.Equal(want.ConsensusAddress, candidate.ConsensusAddress) ||
			want.InfractionHeight != candidate.InfractionHeight || want.ObservedPower != candidate.ObservedPower ||
			want.Window != candidate.Window || candidate.SlashFraction.IsNil() || !want.SlashFraction.Equal(candidate.SlashFraction) ||
			!want.JailUntil.Equal(candidate.JailUntil) {
			continue
		}
		if want.JailUntil.Unix() <= 0 {
			return errors.New("invalid native downtime jail time")
		}
		validator, err := k.GetValidator(child, want.Operator)
		if err != nil {
			return err
		}
		validator.Jailed = true
		if err := k.SetValidator(child, validator); err != nil {
			return err
		}
		var data [13]byte
		data[0] = 1
		binary.BigEndian.PutUint64(data[1:9], uint64(want.JailUntil.Unix()))
		binary.BigEndian.PutUint32(data[9:], uint32(want.JailUntil.Nanosecond()))
		store := k.service.OpenKVStore(child)
		if err := store.Set(key, data[:]); err != nil {
			return err
		}
		active := append([]byte{nativeActiveJailPrefix}, want.ConsensusAddress...)
		if err := store.Set(active, binary.BigEndian.AppendUint64(nil, uint64(want.InfractionHeight))); err != nil {
			return err
		}
		commit()
		return nil
	}
	return errors.New("native downtime outcome differs from the authenticated current candidate")
}

// ReleaseNativeDowntimeJail clears a matured native jail after the caller
// verifies the operator's exact signed MasterValidator unpause and that its
// positions have caught up. The historical infraction marker is retained.
func (k *StateKeeper) ReleaseNativeDowntimeJail(ctx sdk.Context, operator string) error {
	child, write := ctx.CacheContext()
	validator, err := k.GetValidator(child, operator)
	if err != nil {
		return err
	}
	if !validator.Jailed {
		return errors.New("operator has no active native jail")
	}
	consensus, err := validator.GetConsAddr()
	if err != nil {
		return err
	}
	store := k.service.OpenKVStore(child)
	active := append([]byte{nativeActiveJailPrefix}, consensus...)
	value, err := store.Get(active)
	if err != nil {
		return err
	}
	if len(value) != 8 {
		return errors.New("active native jail has no downtime outcome reference")
	}
	outcome, err := k.GetNativeDowntimeOutcome(child, consensus, int64(binary.BigEndian.Uint64(value)))
	if err != nil {
		return err
	}
	if ctx.BlockTime().Before(outcome.JailUntil) {
		return errors.New("native downtime jail period has not elapsed")
	}
	validator.Jailed = false
	if err := k.SetValidator(child, validator); err != nil {
		return err
	}
	window, err := operatorKey(signingWindowPrefix, operator)
	if err != nil {
		return err
	}
	if err := store.Delete(window); err != nil {
		return err
	}
	if err := store.Delete(active); err != nil {
		return err
	}
	write()
	return nil
}
