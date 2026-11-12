package keeper

import (
	"encoding/binary"
	"errors"

	cmtcrypto "github.com/cometbft/cometbft/crypto"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const nativeDuplicateVoteExclusionPrefix byte = 0x66

var ErrNativeConsensusKeyPermanentlyExcluded = errors.New("native consensus key permanently excluded for duplicate voting")

// NativeDuplicateVoteExclusion records the first settled infraction for one
// permanently excluded consensus key. It survives later financial outcomes.
type NativeDuplicateVoteExclusion struct {
	InfractionHeight int64
	SettledHeight    int64
}

func (k *StateKeeper) GetNativeDuplicateVoteExclusion(ctx sdk.Context, consensus sdk.ConsAddress) (NativeDuplicateVoteExclusion, error) {
	if len(consensus) != cmtcrypto.AddressSize {
		return NativeDuplicateVoteExclusion{}, errors.New("invalid native consensus key exclusion identity")
	}
	key := append([]byte{nativeDuplicateVoteExclusionPrefix}, consensus...)
	data, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return NativeDuplicateVoteExclusion{}, err
	}
	if data == nil {
		return NativeDuplicateVoteExclusion{}, ErrNotFound
	}
	if len(data) != 17 || data[0] != 1 {
		return NativeDuplicateVoteExclusion{}, errors.New("invalid native consensus key exclusion encoding")
	}
	out := NativeDuplicateVoteExclusion{InfractionHeight: int64(binary.BigEndian.Uint64(data[1:9])), SettledHeight: int64(binary.BigEndian.Uint64(data[9:]))}
	if out.InfractionHeight <= 0 || out.SettledHeight <= out.InfractionHeight {
		return NativeDuplicateVoteExclusion{}, errors.New("invalid native consensus key exclusion heights")
	}
	return out, nil
}

// FinalizeNativeDuplicateVotePenalty atomically completes an authenticated
// liability and jails its key permanently. The caller reconciles the exact
// custody and checks the future validator set in the same outer cache.
func (k *StateKeeper) FinalizeNativeDuplicateVotePenalty(ctx sdk.Context, job NativeDuplicateVoteJob) error {
	if ctx.ExecMode() != sdk.ExecModeFinalize {
		return errors.New("native duplicate-vote exclusion requires a finalized block")
	}
	child, commit := ctx.CacheContext()
	validator, err := k.GetValidatorByConsAddr(child, job.ConsensusAddress)
	if err != nil {
		return err
	}
	if validator.OperatorAddress != job.Operator {
		return errors.New("native duplicate-vote key belongs to a different operator")
	}
	_, err = k.GetNativeDuplicateVoteExclusion(child, job.ConsensusAddress)
	first := errors.Is(err, ErrNotFound)
	if err != nil && !first {
		return err
	}
	if !first && !validator.Jailed {
		return errors.New("permanently excluded native validator is no longer jailed")
	}
	if err := k.CompleteNativeDuplicateVotePenalty(child, job); err != nil {
		return err
	}
	if first {
		validator.Jailed = true
		if err := k.SetValidator(child, validator); err != nil {
			return err
		}
		var data [17]byte
		data[0] = 1
		binary.BigEndian.PutUint64(data[1:9], uint64(job.InfractionHeight))
		binary.BigEndian.PutUint64(data[9:], uint64(child.BlockHeight()))
		key := append([]byte{nativeDuplicateVoteExclusionPrefix}, job.ConsensusAddress...)
		if err := k.service.OpenKVStore(child).Set(key, data[:]); err != nil {
			return err
		}
	}
	commit()
	return nil
}
