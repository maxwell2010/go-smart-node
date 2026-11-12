package settlement

import (
	"bytes"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

type downtimeDELDepositReceipt struct {
	cursor, reserveCursor  common.Hash
	emptyStakeAcknowledged bool
	logs                   []*ethtypes.Log
}

func (e *Executor) verifyDowntimeDELDepositReceipt(ctx sdk.Context, receipt *ethtypes.Receipt, deposit DELTransition) (downtimeDELDepositReceipt, error) {
	var out downtimeDELDepositReceipt
	if err := e.verifyDELReceiptEnvelope(ctx, receipt, deposit.Beneficiary, 3, 5); err != nil {
		return out, err
	}
	state, err := e.read(ctx, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, deposit.Validator)
	if err != nil {
		return out, err
	}
	status := common.BytesToHash(state[:common.HashLength])
	if (status != smallWord(1) && status != smallWord(2)) || common.BytesToHash(state[common.HashLength:2*common.HashLength]) != (common.Hash{}) {
		return out, errors.New("DEL deposit requires an active unpaused Master validator")
	}
	out.cursor = common.BytesToHash(state[2*common.HashLength:])
	position, err := e.read(ctx, e.roles.Gateway, e.gateway, "getStakePenaltyIndex", common.HashLength, deposit.StakeID)
	if err != nil {
		return out, err
	}
	if common.BytesToHash(position) != out.cursor {
		return out, errors.New("DEL deposit position cursor differs from the exact Master count")
	}
	out.logs = receipt.Logs
	if out.cursor == (common.Hash{}) {
		if len(out.logs) != 3 {
			return out, errors.New("unpenalized DEL deposit has unexpected logs")
		}
		return out, nil
	}
	reserve, err := e.read(ctx, e.roles.Gateway, e.gateway, "getValidatorReserve", 2*common.HashLength, deposit.Validator, e.roles.WrappedDEL)
	if err != nil {
		return out, err
	}
	out.reserveCursor = common.BytesToHash(reserve[:common.HashLength])
	if bytes.Compare(out.reserveCursor[:], out.cursor[:]) > 0 {
		return out, errors.New("DEL deposit reserve cursor exceeds the Master count")
	}
	if len(out.logs) > 3 {
		log := out.logs[0]
		// The compiled Gateway only emits this stand-alone position event for
		// an empty slot. The caller verifies new funding and native absence.
		if log.Address == e.roles.Gateway && len(log.Topics) == 2 &&
			log.Topics[0] == e.gateway.Events["PenaltyAppliedToStake"].ID && log.Topics[1] == deposit.StakeID && bytes.Equal(log.Data, out.cursor[:]) {
			out.emptyStakeAcknowledged = true
			out.logs = out.logs[1:]
		}
	}
	if len(out.logs) == 4 && out.reserveCursor == out.cursor {
		if err := e.verifyDELReserveAcknowledgement(out.logs[0], deposit.Validator, out.cursor); err != nil {
			return out, err
		}
		out.logs = out.logs[1:]
	}
	if len(out.logs) != 3 {
		return out, errors.New("DEL deposit cannot acknowledge an unproved penalty or burn")
	}
	return out, nil
}
