package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"

	"cosmossdk.io/core/store"
	storetypes "cosmossdk.io/store/types"
	cmtcrypto "github.com/cometbft/cometbft/crypto"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	nativeDuplicateVoteRecordPrefix       byte = 0x62
	nativeDuplicateVotePendingPrefix      byte = 0x63
	nativeDuplicateVoteOperatorPrefix     byte = 0x64
	nativeDuplicateVoteRecordVersion      byte = 1
	nativeDuplicateVotePending            byte = 0
	nativeDuplicateVoteCompleted          byte = 1
	nativeDuplicateVoteRecordKeySize           = 1 + 8 + cmtcrypto.AddressSize
	nativeDuplicateVoteOperatorPrefixSize      = 1 + 20
	nativeDuplicateVoteRecordSize              = 1 + 1 + 8 + 20 + 8 + 4 + 8 + 8 + 2

	// MaxNativeDuplicateVoteQueuePage bounds the read-only pending list.
	MaxNativeDuplicateVoteQueuePage = 8
)

var ErrNativeDuplicateVoteAlreadyCompleted = errors.New("native duplicate-vote penalty already completed")

// NativeDuplicateVoteJob is an immutable liability admitted from a finalized
// block's Comet evidence. The queue retains the first admission height.
type NativeDuplicateVoteJob struct {
	NativeDuplicateVoteCandidate
	AdmittedHeight int64
}

// EnqueueNativeDuplicateVotePenalties admits the verified candidates in the
// current FinalizeBlock context. Run it before any consensus parameter update
// in that block. Reobserving an identical liability is idempotent.
func (k *StateKeeper) EnqueueNativeDuplicateVotePenalties(ctx sdk.Context) (int, error) {
	candidates, err := k.PlanNativeDuplicateVotePenalties(ctx)
	if err != nil {
		return 0, err
	}
	return k.enqueueNativeDuplicateVoteCandidates(ctx, candidates)
}

func (k *StateKeeper) enqueueNativeDuplicateVoteCandidates(ctx sdk.Context, candidates []NativeDuplicateVoteCandidate) (int, error) {
	if len(candidates) == 0 {
		return 0, nil
	}
	child, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	added := 0
	for _, candidate := range candidates {
		job := NativeDuplicateVoteJob{NativeDuplicateVoteCandidate: candidate, AdmittedHeight: ctx.BlockHeight()}
		primary, err := nativeDuplicateVoteRecordKey(candidate.ConsensusAddress, candidate.InfractionHeight)
		if err != nil {
			return 0, err
		}
		if !validNativeDuplicateVoteJob(job) || !ctx.BlockTime().After(candidate.InfractionTime) {
			return 0, errors.New("invalid native duplicate-vote admission")
		}
		fifo, operator, err := k.nativeDuplicateVotePendingKeys(job, primary)
		if err != nil {
			return 0, err
		}
		current, status, err := k.readNativeDuplicateVoteJob(state, primary)
		if err == nil {
			if current.AdmittedHeight > ctx.BlockHeight() ||
				!sameNativeDuplicateVoteCandidate(current.NativeDuplicateVoteCandidate, candidate) {
				return 0, errors.New("conflicting native duplicate-vote liability for the same validator and block")
			}
			if status == nativeDuplicateVotePending {
				if err := k.verifyNativeDuplicateVotePendingKeys(state, current, primary); err != nil {
					return 0, err
				}
			}
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			return 0, err
		}
		if err := state.Set(primary, encodeNativeDuplicateVoteJob(job, operator[1:nativeDuplicateVoteOperatorPrefixSize], nativeDuplicateVotePending)); err != nil {
			return 0, err
		}
		if err := state.Set(fifo, primary); err != nil {
			return 0, err
		}
		if err := state.Set(operator, primary); err != nil {
			return 0, err
		}
		added++
	}
	commit()
	return added, nil
}

// PendingNativeDuplicateVotePenalties reads a bounded first page ordered by
// admission height, infraction height, then consensus address.
func (k *StateKeeper) PendingNativeDuplicateVotePenalties(ctx sdk.Context, limit int) ([]NativeDuplicateVoteJob, error) {
	if limit < 1 || limit > MaxNativeDuplicateVoteQueuePage {
		return nil, errors.New("invalid native duplicate-vote queue page size")
	}
	state := k.service.OpenKVStore(ctx)
	start := []byte{nativeDuplicateVotePendingPrefix}
	it, err := state.Iterator(start, storetypes.PrefixEndBytes(start))
	if err != nil {
		return nil, err
	}
	jobs := make([]NativeDuplicateVoteJob, 0, limit)
	for ; it.Valid(); it.Next() {
		if err = it.Error(); err != nil {
			_ = it.Close()
			return nil, err
		}
		primary := it.Value()
		job, err := k.readIndexedNativeDuplicateVoteJob(state, primary)
		if err != nil {
			_ = it.Close()
			return nil, err
		}
		if !bytes.Equal(it.Key(), nativeDuplicateVotePendingKey(job.AdmittedHeight, primary)) {
			_ = it.Close()
			return nil, errors.New("corrupt native duplicate-vote FIFO index")
		}
		jobs = append(jobs, job)
		if len(jobs) == limit {
			break
		}
	}
	return jobs, it.Close()
}

// HasPendingNativeDuplicateVotePenalty checks the first unresolved liability
// for the operator address stored at admission.
func (k *StateKeeper) HasPendingNativeDuplicateVotePenalty(ctx sdk.Context, operator string) (bool, error) {
	_, _, err := k.nativeDuplicateVoteOperatorHead(k.service.OpenKVStore(ctx), operator)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// CompleteNativeDuplicateVotePenalty retires the operator's first exact
// pending job and keeps its identity for replay protection. The caller stages
// the penalty and this completion in the same outer cache.
func (k *StateKeeper) CompleteNativeDuplicateVotePenalty(ctx sdk.Context, expected NativeDuplicateVoteJob) error {
	primary, err := nativeDuplicateVoteRecordKey(expected.ConsensusAddress, expected.InfractionHeight)
	if err != nil {
		return err
	}
	child, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	current, status, err := k.readNativeDuplicateVoteJob(state, primary)
	if err != nil {
		return err
	}
	if current.AdmittedHeight != expected.AdmittedHeight || current.AdmittedHeight > ctx.BlockHeight() ||
		!sameNativeDuplicateVoteCandidate(current.NativeDuplicateVoteCandidate, expected.NativeDuplicateVoteCandidate) {
		return errors.New("native duplicate-vote completion differs from the queued liability")
	}
	if status == nativeDuplicateVoteCompleted {
		return ErrNativeDuplicateVoteAlreadyCompleted
	}
	fifo, operator, err := k.nativeDuplicateVotePendingKeys(current, primary)
	if err != nil {
		return err
	}
	if err := verifyNativeDuplicateVotePendingKeys(state, primary, fifo, operator); err != nil {
		return err
	}
	_, head, err := k.nativeDuplicateVoteOperatorHead(state, current.Operator)
	if err != nil {
		return err
	}
	if !bytes.Equal(head, primary) {
		return errors.New("native duplicate-vote completion must settle the operator's first pending liability")
	}
	record := encodeNativeDuplicateVoteJob(current, operator[1:nativeDuplicateVoteOperatorPrefixSize], nativeDuplicateVoteCompleted)
	if err := state.Set(primary, record); err != nil {
		return err
	}
	if err := state.Delete(fifo); err != nil {
		return err
	}
	if err := state.Delete(operator); err != nil {
		return err
	}
	commit()
	return nil
}

func nativeDuplicateVoteRecordKey(consensus sdk.ConsAddress, height int64) ([]byte, error) {
	if height <= 0 || len(consensus) != cmtcrypto.AddressSize {
		return nil, errors.New("invalid native duplicate-vote identity")
	}
	key := binary.BigEndian.AppendUint64([]byte{nativeDuplicateVoteRecordPrefix}, uint64(height))
	return append(key, consensus...), nil
}

func nativeDuplicateVotePendingKey(admittedHeight int64, primary []byte) []byte {
	return nativeDuplicateVoteOrderKey([]byte{nativeDuplicateVotePendingPrefix}, admittedHeight, primary)
}

func nativeDuplicateVoteOrderKey(prefix []byte, admittedHeight int64, primary []byte) []byte {
	key := binary.BigEndian.AppendUint64(prefix, uint64(admittedHeight))
	return append(key, primary[1:]...)
}

func (k *StateKeeper) nativeDuplicateVotePendingKeys(job NativeDuplicateVoteJob, primary []byte) (fifo, operator []byte, err error) {
	prefix, err := operatorKey(nativeDuplicateVoteOperatorPrefix, job.Operator)
	if err != nil {
		return nil, nil, err
	}
	canonical, err := k.ValidatorAddressCodec().BytesToString(prefix[1:])
	if err != nil || canonical != job.Operator {
		return nil, nil, errors.New("invalid native duplicate-vote operator encoding")
	}
	return nativeDuplicateVotePendingKey(job.AdmittedHeight, primary), nativeDuplicateVoteOrderKey(prefix, job.AdmittedHeight, primary), nil
}

func (k *StateKeeper) verifyNativeDuplicateVotePendingKeys(state store.KVStore, job NativeDuplicateVoteJob, primary []byte) error {
	fifo, operator, err := k.nativeDuplicateVotePendingKeys(job, primary)
	if err != nil {
		return err
	}
	return verifyNativeDuplicateVotePendingKeys(state, primary, fifo, operator)
}

func verifyNativeDuplicateVotePendingKeys(state store.KVStore, primary, fifo, operator []byte) error {
	for _, key := range [][]byte{fifo, operator} {
		value, err := state.Get(key)
		if err != nil {
			return err
		}
		if !bytes.Equal(value, primary) {
			return errors.New("corrupt native duplicate-vote pending indexes")
		}
	}
	return nil
}

func validNativeDuplicateVoteJob(job NativeDuplicateVoteJob) bool {
	return job.InfractionHeight > 0 && job.AdmittedHeight > job.InfractionHeight && job.InfractionTime.Unix() > 0 &&
		job.ObservedPower > 0 && job.TotalVotingPower >= job.ObservedPower && job.PenaltyPercent > 0 && job.PenaltyPercent <= 100
}

func sameNativeDuplicateVoteCandidate(a, b NativeDuplicateVoteCandidate) bool {
	return a.Operator == b.Operator && bytes.Equal(a.ConsensusAddress, b.ConsensusAddress) &&
		a.InfractionHeight == b.InfractionHeight && a.InfractionTime.Equal(b.InfractionTime) &&
		a.ObservedPower == b.ObservedPower && a.TotalVotingPower == b.TotalVotingPower && a.PenaltyPercent == b.PenaltyPercent
}

func encodeNativeDuplicateVoteJob(job NativeDuplicateVoteJob, operator []byte, status byte) []byte {
	var data [nativeDuplicateVoteRecordSize]byte
	data[0], data[1] = nativeDuplicateVoteRecordVersion, status
	binary.BigEndian.PutUint64(data[2:10], uint64(job.AdmittedHeight))
	copy(data[10:30], operator)
	binary.BigEndian.PutUint64(data[30:38], uint64(job.InfractionTime.Unix()))
	binary.BigEndian.PutUint32(data[38:42], uint32(job.InfractionTime.Nanosecond()))
	binary.BigEndian.PutUint64(data[42:50], uint64(job.ObservedPower))
	binary.BigEndian.PutUint64(data[50:58], uint64(job.TotalVotingPower))
	binary.BigEndian.PutUint16(data[58:], job.PenaltyPercent)
	return data[:]
}

func (k *StateKeeper) readNativeDuplicateVoteJob(state store.KVStore, key []byte) (NativeDuplicateVoteJob, byte, error) {
	var job NativeDuplicateVoteJob
	if len(key) != nativeDuplicateVoteRecordKeySize || key[0] != nativeDuplicateVoteRecordPrefix {
		return job, 0, errors.New("invalid native duplicate-vote record key")
	}
	data, err := state.Get(key)
	if err != nil {
		return job, 0, err
	}
	if data == nil {
		return job, 0, ErrNotFound
	}
	if len(data) != nativeDuplicateVoteRecordSize || data[0] != nativeDuplicateVoteRecordVersion || data[1] > nativeDuplicateVoteCompleted {
		return job, 0, errors.New("invalid native duplicate-vote record encoding")
	}
	operator, err := k.ValidatorAddressCodec().BytesToString(data[10:30])
	if err != nil {
		return job, 0, err
	}
	seconds, nanos := int64(binary.BigEndian.Uint64(data[30:38])), int64(binary.BigEndian.Uint32(data[38:42]))
	if seconds <= 0 || nanos >= int64(time.Second) {
		return job, 0, errors.New("invalid native duplicate-vote infraction time")
	}
	job.AdmittedHeight = int64(binary.BigEndian.Uint64(data[2:10]))
	job.NativeDuplicateVoteCandidate = NativeDuplicateVoteCandidate{
		Operator: operator, ConsensusAddress: sdk.ConsAddress(bytes.Clone(key[9:])),
		InfractionHeight: int64(binary.BigEndian.Uint64(key[1:9])), InfractionTime: time.Unix(seconds, nanos).UTC(),
		ObservedPower: int64(binary.BigEndian.Uint64(data[42:50])), TotalVotingPower: int64(binary.BigEndian.Uint64(data[50:58])),
		PenaltyPercent: binary.BigEndian.Uint16(data[58:]),
	}
	if !validNativeDuplicateVoteJob(job) {
		return NativeDuplicateVoteJob{}, 0, errors.New("invalid native duplicate-vote record fields")
	}
	return job, data[1], nil
}
