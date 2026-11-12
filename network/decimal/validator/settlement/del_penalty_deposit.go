package settlement

import (
	"bytes"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

type downtimeDepositReceipt struct {
	cursor, reserveCursor  common.Hash
	emptyStakeAcknowledged bool
	logs                   []*ethtypes.Log
}

func (e *Executor) readUnpausedDepositCursor(ctx sdk.Context, validator common.Address) (common.Hash, error) {
	state, err := e.read(ctx, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, validator)
	if err != nil {
		return common.Hash{}, err
	}
	status := common.BytesToHash(state[:common.HashLength])
	if (status != smallWord(1) && status != smallWord(2)) || common.BytesToHash(state[common.HashLength:2*common.HashLength]) != (common.Hash{}) {
		return common.Hash{}, errors.New("deposit requires an active unpaused Master validator")
	}
	return common.BytesToHash(state[2*common.HashLength:]), nil
}

func (e *Executor) verifyDowntimeDELDepositReceipt(ctx sdk.Context, receipt *ethtypes.Receipt, deposit DELTransition) (downtimeDepositReceipt, error) {
	if err := e.verifyDELReceiptEnvelope(ctx, receipt, deposit.Beneficiary, 3, 5); err != nil {
		return downtimeDepositReceipt{}, err
	}
	cursor, err := e.readUnpausedDepositCursor(ctx, deposit.Validator)
	if err != nil {
		return downtimeDepositReceipt{}, err
	}
	position, err := e.read(ctx, e.roles.Gateway, e.gateway, "getStakePenaltyIndex", common.HashLength, deposit.StakeID)
	if err != nil {
		return downtimeDepositReceipt{}, err
	}
	if common.BytesToHash(position) != cursor {
		return downtimeDepositReceipt{}, errors.New("DEL deposit position cursor differs from the exact Master count")
	}
	return e.verifyDepositPenaltyAcknowledgements(ctx, receipt.Logs, deposit.Validator, deposit.StakeID, cursor)
}

// The DEL and isolated NFT routes both add to the same ordinary DEL
// companion. Only the pinned Gateway's exact zero-loss acknowledgements may
// precede that addition, after the custody events for an NFT isolation.
func (e *Executor) verifyDepositPenaltyAcknowledgements(ctx sdk.Context, logs []*ethtypes.Log, validator common.Address,
	stakeID, cursor common.Hash,
) (downtimeDepositReceipt, error) {
	const expectedLogs = 3
	out := downtimeDepositReceipt{cursor: cursor, logs: logs}
	if cursor == (common.Hash{}) {
		if len(logs) != expectedLogs {
			return out, errors.New("unpenalized deposit has unexpected logs")
		}
		return out, nil
	}
	reserve, err := e.read(ctx, e.roles.Gateway, e.gateway, "getValidatorReserve", 2*common.HashLength, validator, e.roles.WrappedDEL)
	if err != nil {
		return out, err
	}
	out.reserveCursor = common.BytesToHash(reserve[:common.HashLength])
	if bytes.Compare(out.reserveCursor[:], out.cursor[:]) > 0 {
		return out, errors.New("DEL deposit reserve cursor exceeds the Master count")
	}
	if len(out.logs) > expectedLogs {
		log := out.logs[0]
		// The compiled Gateway only emits this stand-alone position event for
		// an empty slot. The caller verifies new funding and native absence.
		if log.Address == e.roles.Gateway && len(log.Topics) == 2 &&
			log.Topics[0] == e.gateway.Events["PenaltyAppliedToStake"].ID && log.Topics[1] == stakeID && bytes.Equal(log.Data, out.cursor[:]) {
			out.emptyStakeAcknowledged = true
			out.logs = out.logs[1:]
		}
	}
	if len(out.logs) == expectedLogs+1 && out.reserveCursor == out.cursor {
		if err := e.verifyDELReserveAcknowledgement(out.logs[0], validator, out.cursor); err != nil {
			return out, err
		}
		out.logs = out.logs[1:]
	}
	if len(out.logs) != expectedLogs {
		return out, errors.New("DEL deposit cannot acknowledge an unproved penalty or burn")
	}
	return out, nil
}

func (r downtimeDepositReceipt) validatesPosition(isNew bool) bool {
	if isNew {
		return true
	}
	return !r.emptyStakeAcknowledged && r.reserveCursor == r.cursor
}
