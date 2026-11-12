package settlement

import (
	"bytes"
	"errors"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

func (e *Executor) verifyPenalizedDELWithdrawalReceipt(ctx sdk.Context, receipt *ethtypes.Receipt, withdrawal DELTransition) ([]*ethtypes.Log, error) {
	if err := e.verifyDELReceiptEnvelope(ctx, receipt, withdrawal.Beneficiary, 3, 4); err != nil {
		return nil, err
	}
	master, err := e.read(ctx, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, withdrawal.Validator)
	if err != nil {
		return nil, err
	}
	status := common.BytesToHash(master[:common.HashLength])
	if (status != smallWord(1) && status != smallWord(2)) || common.BytesToHash(master[2*common.HashLength:]) != withdrawal.PenaltyCursor {
		return nil, errors.New("penalized DEL withdrawal requires a live validator with the exact Master penalty count")
	}
	positionCursor, err := e.read(ctx, e.roles.Gateway, e.gateway, "getStakePenaltyIndex", common.HashLength, withdrawal.StakeID)
	if err != nil {
		return nil, err
	}
	reserve, err := e.read(ctx, e.roles.Gateway, e.gateway, "getValidatorReserve", 2*common.HashLength, withdrawal.Validator, e.roles.WrappedDEL)
	if err != nil {
		return nil, err
	}
	if common.BytesToHash(positionCursor) != withdrawal.PenaltyCursor || common.BytesToHash(reserve[:common.HashLength]) != withdrawal.PenaltyCursor {
		return nil, errors.New("penalized DEL withdrawal requires both position and reserve at the exact Master penalty count")
	}
	// The pinned Gateway can acknowledge an aggregate cursor before the
	// withdrawal. It must carry zero loss. Exact subsequent logs leave no
	// room for implicit per-position penalty or burn events.
	logs := receipt.Logs
	if len(logs) == 4 {
		if err := e.verifyDELReserveAcknowledgement(logs[0], withdrawal.Validator, withdrawal.PenaltyCursor); err != nil {
			return nil, err
		}
		logs = logs[1:]
	}
	return logs, nil
}

func (e *Executor) verifyDELReserveAcknowledgement(log *ethtypes.Log, validator common.Address, cursor common.Hash) error {
	ack := e.gateway.Events["PenaltyAppliedToValidator"]
	index := new(big.Int).Sub(cursor.Big(), big.NewInt(1))
	data, err := ack.Inputs.NonIndexed().Pack(e.roles.WrappedDEL, uint8(tokenDEL), new(big.Int), index, new(big.Int))
	if err != nil {
		return err
	}
	if log.Address != e.roles.Gateway || len(log.Topics) != 2 || log.Topics[0] != ack.ID ||
		log.Topics[1] != common.BytesToHash(validator[:]) || !bytes.Equal(log.Data, data) {
		return errors.New("invalid aggregate DEL penalty acknowledgement")
	}
	return nil
}
