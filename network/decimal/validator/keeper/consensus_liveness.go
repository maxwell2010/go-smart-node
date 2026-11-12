package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	signingProgressKey  byte = 0x44
	signingWindowPrefix byte = 0x45
	signingWindowHeader      = 17
	maxSigningWindow         = 4096
)

// VerifiedABCIVote is one observation from the prior effective Comet set.
// Power comes from the actual prior commit metadata, independently of the
// validator's current status or newly scheduled power.
type VerifiedABCIVote struct {
	Operator string
	Power    int64
	Flag     cmtproto.BlockIDFlag
}

// VerifiedABCICommit contains the previous block's vote observations. The
// first block has no previous commit and returns height zero and no votes.
type VerifiedABCICommit struct {
	Height int64
	Votes  []VerifiedABCIVote
}

// VerifyABCICommit verifies the real FinalizeBlock input against the native
// schedule and resolves prior consensus identities through the immutable index.
// Comet supplies the vote flags after validating the underlying commit.
func (k *StateKeeper) VerifyABCICommit(ctx sdk.Context) (VerifiedABCICommit, error) {
	if ctx.ExecMode() != sdk.ExecModeFinalize {
		return VerifiedABCICommit{}, errors.New("native commit observation requires FinalizeBlock")
	}
	_, _, anchor, err := k.verifyABCIBlock(ctx)
	if err != nil || anchor.previousCount == 0 {
		return VerifiedABCICommit{}, err
	}
	result := VerifiedABCICommit{Height: ctx.BlockHeight() - 1}
	for _, vote := range ctx.VoteInfos() {
		validator, err := k.GetValidatorByConsAddr(ctx, sdk.ConsAddress(vote.Validator.Address))
		if err != nil {
			return VerifiedABCICommit{}, fmt.Errorf("resolve prior native consensus identity: %w", err)
		}
		result.Votes = append(result.Votes, VerifiedABCIVote{
			Operator: validator.OperatorAddress,
			Power:    vote.Validator.Power,
			Flag:     vote.BlockIdFlag,
		})
	}
	return result, nil
}

// ValidateSigningWindowSize checks the bounded native observation profile.
func ValidateSigningWindowSize(size int64) error {
	if size <= 0 || size > maxSigningWindow {
		return fmt.Errorf("native signing window size must be between 1 and %d", maxSigningWindow)
	}
	return nil
}

// SigningWindow summarizes a validator's most recent effective-set
// opportunities. Heights where it is outside the set do not add samples.
type SigningWindow struct {
	LastHeight int64
	Size       uint16
	Samples    uint16
	Missed     uint16
}

type signingWindow struct {
	SigningWindow
	next   uint16
	misses []byte
}

func (k *StateKeeper) readSigningWindow(ctx sdk.Context, key []byte) (signingWindow, error) {
	data, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return signingWindow{}, err
	}
	if data == nil {
		return signingWindow{}, ErrNotFound
	}
	if len(data) < signingWindowHeader || data[0] != 1 {
		return signingWindow{}, errors.New("invalid native signing window encoding")
	}
	row := signingWindow{
		SigningWindow: SigningWindow{
			LastHeight: int64(binary.BigEndian.Uint64(data[1:9])),
			Size:       binary.BigEndian.Uint16(data[9:11]),
			Samples:    binary.BigEndian.Uint16(data[11:13]),
			Missed:     binary.BigEndian.Uint16(data[13:15]),
		},
		next:   binary.BigEndian.Uint16(data[15:17]),
		misses: data[signingWindowHeader:],
	}
	if ValidateSigningWindowSize(int64(row.Size)) != nil || len(row.misses) != (int(row.Size)+7)/8 ||
		row.LastHeight <= 0 || row.Samples == 0 || row.Samples > row.Size || row.Missed > row.Samples ||
		row.next >= row.Size || (row.Samples < row.Size && row.next != row.Samples) {
		return signingWindow{}, errors.New("invalid native signing window counters")
	}
	var missed int
	for i, slots := range row.misses {
		remaining := int(row.Samples) - i*8
		if (remaining <= 0 && slots != 0) || (remaining > 0 && remaining < 8 && slots>>remaining != 0) {
			return signingWindow{}, errors.New("native signing window contains unused observations")
		}
		missed += bits.OnesCount8(slots)
	}
	if missed != int(row.Missed) {
		return signingWindow{}, errors.New("native signing window missed count differs from observations")
	}
	return row, nil
}

// GetSigningWindow returns ErrNotFound before a validator's first observation.
func (k *StateKeeper) GetSigningWindow(ctx sdk.Context, operator string) (SigningWindow, error) {
	key, err := operatorKey(signingWindowPrefix, operator)
	if err != nil {
		return SigningWindow{}, err
	}
	row, err := k.readSigningWindow(ctx, key)
	return row.SigningWindow, err
}

func (k *StateKeeper) addSigningSample(ctx sdk.Context, size uint16, height int64, vote VerifiedABCIVote) error {
	key, err := operatorKey(signingWindowPrefix, vote.Operator)
	if err != nil {
		return err
	}
	row, err := k.readSigningWindow(ctx, key)
	if errors.Is(err, ErrNotFound) {
		row.Size = size
		row.misses = make([]byte, (int(size)+7)/8)
	} else if err != nil {
		return err
	} else {
		row.misses = bytes.Clone(row.misses)
	}
	if row.Size != size || row.LastHeight >= height {
		return errors.New("native signing window size or height differs from the observation")
	}
	slot := row.next / 8
	mask := byte(1 << (row.next % 8))
	if row.misses[slot]&mask != 0 {
		row.Missed--
	}
	if vote.Flag == cmtproto.BlockIDFlagCommit {
		row.misses[slot] &^= mask
	} else {
		row.misses[slot] |= mask
		row.Missed++
	}
	if row.Samples < size {
		row.Samples++
	}
	row.LastHeight = height
	row.next = (row.next + 1) % size
	data := make([]byte, signingWindowHeader+len(row.misses))
	data[0] = 1
	binary.BigEndian.PutUint64(data[1:9], uint64(row.LastHeight))
	binary.BigEndian.PutUint16(data[9:11], row.Size)
	binary.BigEndian.PutUint16(data[11:13], row.Samples)
	binary.BigEndian.PutUint16(data[13:15], row.Missed)
	binary.BigEndian.PutUint16(data[15:17], row.next)
	copy(data[signingWindowHeader:], row.misses)
	return k.service.OpenKVStore(ctx).Set(key, data)
}

func (k *StateKeeper) signingProgress(ctx sdk.Context) (int64, error) {
	data, err := k.service.OpenKVStore(ctx).Get([]byte{signingProgressKey})
	if err != nil || data == nil {
		return 0, err
	}
	if len(data) != 9 || data[0] != 1 {
		return 0, errors.New("invalid native commit observation checkpoint")
	}
	height := int64(binary.BigEndian.Uint64(data[1:]))
	if height <= 0 {
		return 0, errors.New("invalid native commit observation height")
	}
	return height, nil
}

// RecordABCICommit authenticates and records the previous commit in BeginBlock.
// It advances a single sequential checkpoint, including on the first block,
// and updates all participating windows atomically. Existing stores begin
// observation at the first authenticated block after this feature is enabled.
func (k *StateKeeper) RecordABCICommit(ctx sdk.Context) (VerifiedABCICommit, error) {
	observed, err := k.VerifyABCICommit(ctx)
	if err != nil {
		return VerifiedABCICommit{}, err
	}
	child, write := ctx.CacheContext()
	progress, err := k.signingProgress(child)
	if err != nil {
		return VerifiedABCICommit{}, err
	}
	if progress != 0 && progress != observed.Height {
		return VerifiedABCICommit{}, errors.New("native commit observation is replayed or skipped")
	}
	params, err := k.GetParams(child)
	if err != nil {
		return VerifiedABCICommit{}, err
	}
	if err := ValidateSigningWindowSize(params.SignedBlocksWindow); err != nil {
		return VerifiedABCICommit{}, err
	}
	for _, vote := range observed.Votes {
		if err := k.addSigningSample(child, uint16(params.SignedBlocksWindow), observed.Height, vote); err != nil {
			return VerifiedABCICommit{}, err
		}
	}
	var next [9]byte
	next[0] = 1
	binary.BigEndian.PutUint64(next[1:], uint64(ctx.BlockHeight()))
	if err := k.service.OpenKVStore(child).Set([]byte{signingProgressKey}, next[:]); err != nil {
		return VerifiedABCICommit{}, err
	}
	write()
	return observed, nil
}
