package settlement

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

func masterRegistrationProofFixture(t *testing.T) *Executor {
	t.Helper()
	e := proofFixture(t)
	master, err := contracts.CompiledABI("NewNetworkMasterValidator")
	require.NoError(t, err)
	e.master = master
	e.roles.MasterValidator = common.HexToAddress("0x7004")
	return e
}

func TestAuthorizeNativeValidatorRegistrationBindsOneExactCall(t *testing.T) {
	e := masterRegistrationProofFixture(t)
	validator := common.Address{19: 9}
	const meta = `{"moniker":"test-validator"}`
	data, err := e.packNativeValidatorRegistration(validator, meta)
	require.NoError(t, err)
	ctx := sdk.Context{}.WithContext(context.Background()).WithBlockHeight(7).WithExecMode(sdk.ExecModeFinalize)
	scoped := ctx.WithValue(nativeValidatorRegistrationKey{}, nativeValidatorRegistrationCall{executor: e, height: 7, validator: validator, meta: meta})
	msg := core.Message{From: masterPenaltySender(), To: &e.roles.MasterValidator, Value: new(big.Int), Data: data}
	require.True(t, e.AuthorizeNativeValidatorRegistration(scoped, msg))
	require.False(t, e.AuthorizeNativeValidatorRegistration(ctx, msg))
	require.False(t, e.AuthorizeNativeValidatorRegistration(sdk.Context{}, msg))
	var absent *Executor
	require.False(t, absent.AuthorizeNativeValidatorRegistration(scoped, msg))
	require.False(t, masterRegistrationProofFixture(t).AuthorizeNativeValidatorRegistration(scoped, msg))
	require.False(t, e.AuthorizeNativeValidatorRegistration(scoped.WithBlockHeight(8), msg))
	require.False(t, e.AuthorizeNativeValidatorRegistration(scoped.WithExecMode(sdk.ExecModeSimulate), msg))

	changed := msg
	changed.Data, err = e.packNativeValidatorRegistration(validator, "different metadata")
	require.NoError(t, err)
	require.False(t, e.AuthorizeNativeValidatorRegistration(scoped, changed))
	changed.Data, err = e.master.Pack("addValidators", []struct {
		Validator common.Address
		Meta      string
	}{{Validator: validator, Meta: meta}, {Validator: common.Address{19: 10}, Meta: meta}})
	require.NoError(t, err)
	require.False(t, e.AuthorizeNativeValidatorRegistration(scoped, changed))
	changed = msg
	changed.From = validator
	require.False(t, e.AuthorizeNativeValidatorRegistration(scoped, changed))
	changed = msg
	changed.To = &validator
	require.False(t, e.AuthorizeNativeValidatorRegistration(scoped, changed))
	changed = msg
	changed.Value = big.NewInt(1)
	require.False(t, e.AuthorizeNativeValidatorRegistration(scoped, changed))
	changed = msg
	changed.SetCodeAuthorizations = []ethtypes.SetCodeAuthorization{{}}
	require.False(t, e.AuthorizeNativeValidatorRegistration(scoped, changed))
}

func TestMasterRegistrationRequiresExactOrderedLogs(t *testing.T) {
	e := masterRegistrationProofFixture(t)
	validator := common.Address{19: 9}
	const meta = `{"moniker":"Валидатор"}`
	eventLog := func(name string, args ...any) *ethtypes.Log {
		t.Helper()
		event := e.master.Events[name]
		data, err := event.Inputs.NonIndexed().Pack(args...)
		require.NoError(t, err)
		return &ethtypes.Log{Address: e.roles.MasterValidator, Topics: []common.Hash{event.ID, common.BytesToHash(validator[:])}, Data: data}
	}
	updated := eventLog("ValidatorUpdated", uint8(masterValidatorApproved), false)
	metadata := eventLog("ValidatorMetaUpdated", meta)
	verify := func(logs ...*ethtypes.Log) error {
		return e.verifyNativeValidatorRegistrationLogs(&ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: logs}, validator, meta)
	}
	require.NoError(t, verify(updated, metadata))
	require.ErrorContains(t, verify(metadata, updated), "ValidatorUpdated")
	require.ErrorContains(t, verify(updated, metadata, metadata), "registration log count")
	require.ErrorContains(t, verify(eventLog("ValidatorUpdated", uint8(masterValidatorApproved), true), metadata), "ValidatorUpdated")
	require.ErrorContains(t, verify(updated, eventLog("ValidatorMetaUpdated", "changed")), "ValidatorMetaUpdated")
	noncanonical := *metadata
	noncanonical.Data = append(bytes.Clone(metadata.Data), make([]byte, common.HashLength)...)
	require.ErrorContains(t, verify(updated, &noncanonical), "ValidatorMetaUpdated")
}
