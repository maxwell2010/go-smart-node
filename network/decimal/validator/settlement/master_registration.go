package settlement

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

const (
	masterValidatorApproved      = 2
	maxMasterRegistrationMetaLen = 512
)

type nativeValidatorRegistrationKey struct{}

type nativeValidatorRegistrationCall struct {
	executor  *Executor
	height    int64
	validator common.Address
	meta      string
}

// AuthorizeNativeValidatorRegistration approves the exact internal
// single-validator Master call produced by RegisterNativeValidator.
func (e *Executor) AuthorizeNativeValidatorRegistration(ctx sdk.Context, msg core.Message) bool {
	if e == nil || ctx.Context() == nil {
		return false
	}
	call, ok := ctx.Value(nativeValidatorRegistrationKey{}).(nativeValidatorRegistrationCall)
	if !ok || call.executor != e || call.height != ctx.BlockHeight() ||
		ctx.ExecMode() != sdk.ExecModeFinalize || msg.From != masterPenaltySender() ||
		msg.To == nil || *msg.To != e.roles.MasterValidator || msg.Value == nil ||
		msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 {
		return false
	}
	data, err := e.packNativeValidatorRegistration(call.validator, call.meta)
	return err == nil && bytes.Equal(data, msg.Data)
}

func (e *Executor) packNativeValidatorRegistration(validator common.Address, meta string) ([]byte, error) {
	if e.master.Methods["addValidators"].Sig != "addValidators((address,string)[])" {
		return nil, errors.New("compiler Master validator registration ABI differs")
	}
	return e.master.Pack("addValidators", []struct {
		Validator common.Address
		Meta      string
	}{{Validator: validator, Meta: meta}})
}

// RegisterNativeValidator proves one approved Master registration and applies
// its native projection in the same cache. The caller owns proposal approval
// and consensus-key uniqueness checks.
func (e *Executor) RegisterNativeValidator(ctx sdk.Context, validator common.Address, canonicalMeta string,
	nativeSupply func(sdk.Context) sdkmath.Int, project func(sdk.Context) error,
) error {
	if e == nil || e.evm == nil || e.auth == nil || ctx.Context() == nil || ctx.MultiStore() == nil ||
		ctx.ExecMode() != sdk.ExecModeFinalize || ctx.BlockHeight() <= 0 || ctx.BlockTime().Unix() <= 0 ||
		validator == (common.Address{}) || nativeSupply == nil || project == nil {
		return errors.New("Master registration requires a finalized block, validator, supply and native projection")
	}
	if len(canonicalMeta) == 0 || len(canonicalMeta) > maxMasterRegistrationMetaLen || !utf8.ValidString(canonicalMeta) {
		return errors.New("invalid Master validator registration metadata")
	}
	data, err := e.packNativeValidatorRegistration(validator, canonicalMeta)
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
	var emptyState [3 * common.HashLength]byte
	if !bytes.Equal(state, emptyState[:]) {
		return errors.New("Master registration requires an empty validator state")
	}
	before, err := e.readMasterPenaltyBalances(child, validator, nativeSupply)
	if err != nil {
		return err
	}
	authorized := child.WithValue(nativeValidatorRegistrationKey{}, nativeValidatorRegistrationCall{
		executor: e, height: child.BlockHeight(), validator: validator, meta: canonicalMeta,
	})
	result, err := e.invokePenalty(authorized, sender, e.roles.MasterValidator, data)
	if err != nil {
		return err
	}
	if len(result.Ret) != 0 {
		return errors.New("Master registration returned unexpected data")
	}
	receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(result.Logs)}
	if err := e.verifyNativeValidatorRegistrationLogs(receipt, validator, canonicalMeta); err != nil {
		return err
	}
	state, err = e.read(child, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, validator)
	if err != nil {
		return err
	}
	approvedState := emptyState
	approvedState[common.HashLength-1] = masterValidatorApproved
	if !bytes.Equal(state, approvedState[:]) {
		return errors.New("Master registration requires an approved unpaused validator with zero penalties")
	}
	if err := project(child); err != nil {
		return fmt.Errorf("project Master validator registration: %w", err)
	}
	after, err := e.readMasterPenaltyBalances(child, validator, nativeSupply)
	if err != nil {
		return err
	}
	if before.del != after.del || before.wdel != after.wdel || before.wdelSupply != after.wdelSupply || !before.bankSupply.Equal(after.bankSupply) {
		return errors.New("Master registration changed native DEL or WDEL")
	}
	afterSource, err := e.auth.AuthenticateStakeProfile(child)
	if err != nil {
		return err
	}
	if afterSource != source {
		return errors.New("authenticated system roles changed during Master registration")
	}
	write()
	return nil
}

func (e *Executor) verifyNativeValidatorRegistrationLogs(receipt *ethtypes.Receipt, validator common.Address, meta string) error {
	markers, err := e.completions(receipt)
	if err != nil {
		return err
	}
	if len(markers) != 0 || len(receipt.Logs) != 2 {
		return errors.New("unexpected Master validator registration log count")
	}
	identity := common.BytesToHash(validator[:])
	expected := []struct {
		name string
		args []any
	}{
		{name: "ValidatorUpdated", args: []any{uint8(masterValidatorApproved), false}},
		{name: "ValidatorMetaUpdated", args: []any{meta}},
	}
	for i, spec := range expected {
		event, ok := e.master.Events[spec.name]
		if !ok {
			return errors.New("compiler Master validator registration event ABI differs")
		}
		data, err := event.Inputs.NonIndexed().Pack(spec.args...)
		if err != nil {
			return err
		}
		log := receipt.Logs[i]
		if log.Address != e.roles.MasterValidator || len(log.Topics) != 2 || log.Topics[0] != event.ID ||
			log.Topics[1] != identity || !bytes.Equal(log.Data, data) {
			return fmt.Errorf("Master registration log does not match %s", spec.name)
		}
	}
	return nil
}
