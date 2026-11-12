package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"

	"cosmossdk.io/core/store"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	nativeDuplicateVoteCursorKey      byte = 0x67
	nativeDuplicateVotePendingKeySize      = nativeDuplicateVoteRecordKeySize + 8
)

// NextNativeDuplicateVotePenalty rotates through the pending FIFO index and
// selects the oldest admitted job for the encountered operator. It returns
// ErrNotFound when the queue is empty. Call in the surrounding block context
// before branching the financial attempt so a failure still moves the cursor.
func (k *StateKeeper) NextNativeDuplicateVotePenalty(ctx sdk.Context) (NativeDuplicateVoteJob, error) {
	var empty NativeDuplicateVoteJob
	state := k.service.OpenKVStore(ctx)
	cursorKey := []byte{nativeDuplicateVoteCursorKey}
	cursor, err := state.Get(cursorKey)
	if err != nil {
		return empty, err
	}
	fifo, primary, err := nextNativeDuplicateVoteIndexEntry(state, cursor)
	if err != nil {
		return empty, err
	}
	job, err := k.readIndexedNativeDuplicateVoteJob(state, primary)
	if err != nil {
		return empty, err
	}
	if !bytes.Equal(fifo, nativeDuplicateVotePendingKey(job.AdmittedHeight, primary)) {
		return empty, errors.New("corrupt native duplicate-vote FIFO index")
	}

	head, headPrimary, err := k.nativeDuplicateVoteOperatorHead(state, job.Operator)
	if errors.Is(err, ErrNotFound) {
		return empty, errors.New("native duplicate-vote FIFO entry has no operator index")
	}
	if err != nil {
		return empty, err
	}
	headFIFO := nativeDuplicateVotePendingKey(head.AdmittedHeight, headPrimary)
	if order := bytes.Compare(headFIFO, fifo); order > 0 {
		return empty, errors.New("native duplicate-vote operator index skipped an older liability")
	} else if order < 0 {
		value, err := state.Get(headFIFO)
		if err != nil {
			return empty, err
		}
		if !bytes.Equal(value, headPrimary) {
			return empty, errors.New("native duplicate-vote operator head has no matching FIFO entry")
		}
	}
	// Advance past the examined FIFO row even when the chosen operator head
	// has an earlier key. This keeps subsequent operators reachable on retry.
	if err := state.Set(cursorKey, fifo); err != nil {
		return empty, err
	}
	return head, nil
}

func nextNativeDuplicateVoteIndexEntry(state store.KVStore, cursor []byte) ([]byte, []byte, error) {
	start := []byte{nativeDuplicateVotePendingPrefix}
	end := storetypes.PrefixEndBytes(start)
	if cursor == nil {
		return firstNativeDuplicateVoteIndexEntry(state, start, end)
	}
	if len(cursor) != nativeDuplicateVotePendingKeySize || cursor[0] != nativeDuplicateVotePendingPrefix {
		return nil, nil, errors.New("invalid native duplicate-vote queue cursor")
	}
	admitted := int64(binary.BigEndian.Uint64(cursor[1:9]))
	infraction := int64(binary.BigEndian.Uint64(cursor[9:17]))
	if infraction <= 0 || admitted <= infraction {
		return nil, nil, errors.New("invalid native duplicate-vote queue cursor")
	}
	after := append(bytes.Clone(cursor), 0)
	key, primary, err := firstNativeDuplicateVoteIndexEntry(state, after, end)
	if errors.Is(err, ErrNotFound) {
		return firstNativeDuplicateVoteIndexEntry(state, start, after)
	}
	return key, primary, err
}

func firstNativeDuplicateVoteIndexEntry(state store.KVStore, start, end []byte) ([]byte, []byte, error) {
	it, err := state.Iterator(start, end)
	if err != nil {
		return nil, nil, err
	}
	if !it.Valid() {
		if err := it.Close(); err != nil {
			return nil, nil, err
		}
		// SDK cache iterators report an error after normal exhaustion.
		return nil, nil, ErrNotFound
	}
	if err := it.Error(); err != nil {
		_ = it.Close()
		return nil, nil, err
	}
	key, primary := bytes.Clone(it.Key()), bytes.Clone(it.Value())
	if err := it.Close(); err != nil {
		return nil, nil, err
	}
	return key, primary, nil
}

func (k *StateKeeper) nativeDuplicateVoteOperatorHead(state store.KVStore, operator string) (NativeDuplicateVoteJob, []byte, error) {
	var empty NativeDuplicateVoteJob
	prefix, err := operatorKey(nativeDuplicateVoteOperatorPrefix, operator)
	if err != nil {
		return empty, nil, err
	}
	key, primary, err := firstNativeDuplicateVoteIndexEntry(state, prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return empty, nil, err
	}
	head, err := k.readIndexedNativeDuplicateVoteJob(state, primary)
	if err != nil {
		return empty, nil, err
	}
	if head.Operator != operator || !bytes.Equal(key, nativeDuplicateVoteOrderKey(prefix, head.AdmittedHeight, primary)) {
		return empty, nil, errors.New("corrupt native duplicate-vote operator index")
	}
	return head, primary, nil
}

func (k *StateKeeper) readIndexedNativeDuplicateVoteJob(state store.KVStore, primary []byte) (NativeDuplicateVoteJob, error) {
	job, status, err := k.readNativeDuplicateVoteJob(state, primary)
	if errors.Is(err, ErrNotFound) {
		return NativeDuplicateVoteJob{}, errors.New("native duplicate-vote index points to a missing liability")
	}
	if err != nil {
		return NativeDuplicateVoteJob{}, err
	}
	if status != nativeDuplicateVotePending {
		return NativeDuplicateVoteJob{}, errors.New("native duplicate-vote index points to a completed liability")
	}
	return job, nil
}
