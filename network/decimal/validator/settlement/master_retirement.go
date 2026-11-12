package settlement

import (
	"bytes"
	"errors"
	"fmt"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

type nativeValidatorRetirementKey struct{}
type nativeValidatorRetirementCall struct {
	executor  *Executor
	height    int64
	validator common.Address
}

func (e *Executor) AuthorizeNativeValidatorRetirement(ctx sdk.Context, msg core.Message) bool {
	if e == nil || ctx.Context() == nil {
		return false
	}
	call, ok := ctx.Value(nativeValidatorRetirementKey{}).(nativeValidatorRetirementCall)
	if !ok || call.executor != e || call.height != ctx.BlockHeight() ||
		ctx.ExecMode() != sdk.ExecModeFinalize || msg.From != masterPenaltySender() ||
		msg.To == nil || *msg.To != e.roles.MasterValidator || msg.Value == nil ||
		msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 {
		return false
	}
	data, err := e.packNativeValidatorRetirement(call.validator)
	return err == nil && bytes.Equal(data, msg.Data)
}

func (e *Executor) packNativeValidatorRetirement(validator common.Address) ([]byte, error) {
	if e.master.Methods["pauseValidator"].Sig != "pauseValidator(address)" {
		return nil, errors.New("compiler Master validator pause ABI differs")
	}
	return e.master.Pack("pauseValidator", validator)
}

// RetireNativeValidator proves a Master pause for an operator with no Gateway
// reserve. The Master status and penalty cursor are retained for later evidence.
func (e *Executor) RetireNativeValidator(ctx sdk.Context, validator common.Address,
	nativeSupply func(sdk.Context) sdkmath.Int, project func(sdk.Context) error,
) error {
	if e == nil || e.evm == nil || e.auth == nil || ctx.Context() == nil || ctx.MultiStore() == nil ||
		ctx.ExecMode() != sdk.ExecModeFinalize || ctx.BlockHeight() <= 0 ||
		validator == (common.Address{}) || nativeSupply == nil || project == nil {
		return errors.New("Master retirement requires a finalized block, validator, supply and native projection")
	}
	data, err := e.packNativeValidatorRetirement(validator)
	if err != nil {
		return err
	}
	child, write := ctx.CacheContext()
	source, err := e.auth.AuthenticateStakeProfile(child)
	if err != nil {
		return err
	}
	sender := masterPenaltySender()
	if err := e.requireInert(child, sender); err != nil {
		return err
	}
	state, err := e.read(child, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, validator)
	if err != nil {
		return err
	}
	status := common.BytesToHash(state[:common.HashLength])
	if (status != smallWord(1) && status != smallWord(2)) ||
		common.BytesToHash(state[common.HashLength:2*common.HashLength]) != (common.Hash{}) {
		return errors.New("Master retirement requires an unpaused candidate or approved validator")
	}
	reserve, err := e.read(child, e.roles.Gateway, e.gateway, "getValidatorReserve", 2*common.HashLength, validator, e.roles.WrappedDEL)
	if err != nil {
		return err
	}
	if common.BytesToHash(reserve[common.HashLength:]) != (common.Hash{}) {
		return errors.New("Master retirement requires an empty Gateway WDEL reserve")
	}
	before, err := e.readMasterPenaltyBalances(child, validator, nativeSupply)
	if err != nil {
		return err
	}
	authorized := child.WithValue(nativeValidatorRetirementKey{}, nativeValidatorRetirementCall{executor: e, height: child.BlockHeight(), validator: validator})
	result, err := e.invokePenalty(authorized, sender, e.roles.MasterValidator, data)
	if err != nil {
		return err
	}
	if len(result.Ret) != 0 {
		return errors.New("Master retirement returned unexpected data")
	}
	want := bytes.Clone(state)
	want[2*common.HashLength-1] = 1
	receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(result.Logs)}
	markers, err := e.completions(receipt)
	if err != nil {
		return err
	}
	if len(markers) != 0 || len(receipt.Logs) != 1 {
		return errors.New("Master retirement requires exactly one validator state log")
	}
	log := receipt.Logs[0]
	event, ok := e.master.Events["ValidatorUpdated"]
	if !ok || log.Address != e.roles.MasterValidator || len(log.Topics) != 2 || log.Topics[0] != event.ID ||
		log.Topics[1] != common.BytesToHash(validator[:]) || !bytes.Equal(log.Data, want[:2*common.HashLength]) {
		return errors.New("Master retirement log differs from the exact operator pause")
	}
	state, err = e.read(child, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, validator)
	if err != nil {
		return err
	}
	if !bytes.Equal(state, want) {
		return errors.New("Master retirement changed the operator status or penalty count")
	}
	if err := project(child); err != nil {
		return fmt.Errorf("project Master retirement: %w", err)
	}
	after, err := e.readMasterPenaltyBalances(child, validator, nativeSupply)
	if err != nil {
		return err
	}
	if before.del != after.del || before.wdel != after.wdel || before.wdelSupply != after.wdelSupply || !before.bankSupply.Equal(after.bankSupply) {
		return errors.New("Master retirement changed native DEL or WDEL")
	}
	if err := e.confirmPenaltySource(child, &source); err != nil {
		return err
	}
	write()
	return nil
}
