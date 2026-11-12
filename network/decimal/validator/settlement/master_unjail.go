package settlement

import (
	"bytes"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// VerifySignedDowntimeUnjail accepts the operator's exact unpauseSelf call.
// The native keeper separately checks the active jail, time and position cursors.
func (e *Executor) VerifySignedDowntimeUnjail(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) (common.Hash, error) {
	var zero common.Hash
	if e == nil || msg.To == nil || *msg.To != e.roles.MasterValidator || msg.From == (common.Address{}) ||
		msg.Value == nil || msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 {
		return zero, errors.New("native unjail requires the operator's signed MasterValidator call")
	}
	data, err := e.master.Pack("unpauseSelf")
	if err != nil || !bytes.Equal(msg.Data, data) {
		return zero, errors.New("native unjail requires canonical unpauseSelf calldata")
	}
	markers, err := e.completions(receipt)
	if err != nil {
		return zero, err
	}
	if len(markers) != 0 || len(receipt.Logs) != 1 {
		return zero, errors.New("native unjail requires exactly one validator state log")
	}
	if err := signedMetadata(ctx, receipt); err != nil {
		return zero, err
	}
	if _, err := e.auth.AuthenticateStakeProfile(ctx); err != nil {
		return zero, err
	}
	if err := e.requireInert(ctx, msg.From); err != nil {
		return zero, err
	}
	raw, err := e.read(ctx, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, msg.From)
	if err != nil {
		return zero, err
	}
	status := common.BytesToHash(raw[:common.HashLength])
	paused := common.BytesToHash(raw[common.HashLength : 2*common.HashLength])
	count := common.BytesToHash(raw[2*common.HashLength:])
	if (status != smallWord(1) && status != smallWord(2)) || paused != zero || count == zero {
		return zero, errors.New("MasterValidator did not unpause a previously penalized operator")
	}
	log := receipt.Logs[0]
	if log.Address != e.roles.MasterValidator || len(log.Topics) != 2 || log.Topics[0] != e.master.Events["ValidatorUpdated"].ID ||
		log.Topics[1] != common.BytesToHash(msg.From[:]) || !bytes.Equal(log.Data, raw[:2*common.HashLength]) {
		return zero, errors.New("native unjail log differs from the exact operator state")
	}
	return count, nil
}
