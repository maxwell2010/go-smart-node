package keeper

import (
	"encoding/binary"
	"errors"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// JailNativeDuplicateVotePenalty excludes an exactly authenticated admitted
// liability before any financial realization begins. It does NOT complete it.
// FinalizeNativeDuplicateVotePenalty still owns completion after all pages.
func (k *StateKeeper) JailNativeDuplicateVotePenalty(ctx sdk.Context, job NativeDuplicateVoteJob) error {
	if ctx.ExecMode() != sdk.ExecModeFinalize {
		return errors.New("duplicate-vote jail requires finalized evidence")
	}
	child, write := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	primary, err := nativeDuplicateVoteRecordKey(job.ConsensusAddress, job.InfractionHeight)
	if err != nil {
		return err
	}
	queued, status, err := k.readNativeDuplicateVoteJob(state, primary)
	if err != nil {
		return err
	}
	if queued.AdmittedHeight != job.AdmittedHeight || queued.AdmittedHeight > ctx.BlockHeight() || !sameNativeDuplicateVoteCandidate(queued.NativeDuplicateVoteCandidate, job.NativeDuplicateVoteCandidate) {
		return errors.New("duplicate-vote jail differs from queued liability")
	}
	if status == nativeDuplicateVotePending {
		if err := k.verifyNativeDuplicateVotePendingKeys(state, queued, primary); err != nil {
			return err
		}
	}
	validator, err := k.GetValidatorByConsAddr(child, job.ConsensusAddress)
	if err != nil {
		return err
	}
	if validator.OperatorAddress != job.Operator {
		return errors.New("duplicate-vote jail operator differs")
	}
	_, err = k.GetNativeDuplicateVoteExclusion(child, job.ConsensusAddress)
	if err == nil {
		if !validator.Jailed {
			return errors.New("excluded duplicate key is not jailed")
		}
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	if status != nativeDuplicateVotePending {
		return errors.New("completed duplicate-vote liability has no permanent exclusion")
	}
	validator.Jailed = true
	if err := k.SetValidator(child, validator); err != nil {
		return err
	}
	var data [17]byte
	data[0] = 1
	binary.BigEndian.PutUint64(data[1:9], uint64(job.InfractionHeight))
	binary.BigEndian.PutUint64(data[9:], uint64(child.BlockHeight()))
	if err := state.Set(append([]byte{nativeDuplicateVoteExclusionPrefix}, job.ConsensusAddress...), data[:]); err != nil {
		return err
	}
	retired, err := k.IsNativeValidatorRetired(child, job.Operator)
	if err != nil {
		return err
	}
	if !retired {
		if err := k.updateNativeLiveValidatorRoster(child, job.Operator, false); err != nil {
			return err
		}
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_native_duplicate_vote_excluded",
		sdk.NewAttribute("operator", job.Operator),
		sdk.NewAttribute("infraction_height", strconv.FormatInt(job.InfractionHeight, 10)),
		sdk.NewAttribute("financial_pending", "true")))
	write()
	return nil
}
