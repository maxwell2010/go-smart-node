package systemcontracts_test

import (
	"bytes"
	"errors"
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const delPenaltyPassed = "ordinary DEL penalty realized exactly once with native supply, WDEL and projection rollback"
const recoveredDELDepositsPassed = "signed ordinary and held DEL deposits preserve the realized Master penalty cursor"

func TestOwnedAppSignedOrdinaryDELPenalty(t *testing.T) {
	testSignedDELFixture(t, "penalty", delPenaltyPassed)
}

func TestOwnedAppSignedRecoveredDELDeposits(t *testing.T) {
	testSignedDELFixture(t, "recovered-deposits", recoveredDELDepositsPassed)
}

func runSignedOrdinaryDELPenalty(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, gateway, master, wrapper contracts.Artifact,
	reader *nftview.Reader, gov func(contracts.Artifact, common.Address, string, ...interface{}),
	recover func(*testing.T, signedDELFixture, *settlement.Executor, sdkmath.Int, sdkmath.Int),
) {
	t.Helper()
	f := prepareSignedDELFixture(t, e, profile, gateway, master, wrapper, gov)
	f.declareValidator()
	principal := sdkmath.NewIntWithDecimal(10, 18)
	burned := sdkmath.NewIntWithDecimal(1, 18)
	executor, err := settlement.NewExecutor(e.app.EVMKeeper, reader, settlement.Config{EVMGas: 750_000, MaxLogs: 32, MaxLogBytes: 16384})
	require.NoError(t, err)
	response := f.signed("delegateDEL", principal, true, f.validator)
	delegate, err := gateway.ABI.Pack("delegateDEL", f.validator)
	require.NoError(t, err)
	signed := core.Message{From: f.user, To: &f.target, Value: principal.BigInt(), Data: delegate}
	funding, err := executor.VerifySignedDEL(e.ctx(), signed, &ethtypes.Receipt{
		Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(response.Logs),
	})
	require.NoError(t, err)
	id := funding.StakeID
	keeper := e.app.NativeValidatorKeeper
	// Project the signed funding proof explicitly so the test installs its
	// one internal EVM guard only after Master setup.
	require.NoError(t, keeper.ApplyGatewayDELDelegation(e.ctx(), validatorkeeper.GatewayDELStake{
		Gateway: f.target, WrappedDEL: f.wrapped, Beneficiary: f.user, Validator: f.validator,
		StakeID: id, Amount: funding.Remaining,
	}, funding.Amount))
	f.backing(principal)
	f.activeReserve(principal)
	governed := uint16(10)
	unpause, err := master.ABI.Pack("unpauseValidator", f.validator)
	require.NoError(t, err)

	e.app.EVMKeeper.SetInternalCommitGuard(func(ctx sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if !executor.AuthorizeMasterPenalty(ctx, msg) && !executor.AuthorizeOrdinaryDELPenalty(ctx, msg) {
			if recover != nil && msg.From == e.govOwner && msg.To != nil && *msg.To == profile.MasterValidator &&
				msg.Value != nil && msg.Value.Sign() == 0 && bytes.Equal(msg.Data, unpause) {
				return nil
			}
			return errors.New("test-only internal EVM call lacks exact penalty authorization")
		}
		return nil
	})
	ctx := func() sdk.Context {
		return e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(24_000_000))
	}
	supply := func(ctx sdk.Context) sdkmath.Int {
		return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount
	}
	initialSupply := supply(ctx())
	masterCount := func() int64 {
		return f.read(master, profile.MasterValidator, "getPenaltyCount", f.validator).(*big.Int).Int64()
	}
	require.Zero(t, masterCount())
	getRow := func() validatortypes.EVMStakeBinding {
		row, err := keeper.GetEVMStakeBinding(e.ctx(), validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, f.target[:], id[:])
		require.NoError(t, err)
		return row
	}
	before := getRow()
	want := principal.Sub(burned)
	project := func(child sdk.Context, proof settlement.OrdinaryDELPenaltyProof) error {
		require.Equal(t, before, proof.Before)
		require.True(t, burned.Equal(proof.Burned))
		require.True(t, want.Equal(proof.After.RawAmount))
		require.True(t, proof.After.Active)
		return keeper.ApplyGatewayDELPenalty(child, validatorkeeper.GatewayDELStake{
			Gateway: f.target, WrappedDEL: f.wrapped, Beneficiary: f.user, Validator: f.validator,
			StakeID: id, Amount: before.RawAmount, PenaltyCursor: common.BytesToHash(before.PenaltyCursor),
		}, proof.After.RawAmount, common.BytesToHash(proof.After.PenaltyCursor))
	}
	appendAndRealize := func(project func(sdk.Context, settlement.OrdinaryDELPenaltyProof) error) error {
		return executor.AppendMasterPenalty(ctx(), f.validator, governed, supply, func(child sdk.Context, proof settlement.MasterPenaltyProof) error {
			require.Equal(t, f.validator, proof.Validator)
			require.Equal(t, governed, proof.Percentage)
			require.Equal(t, common.Hash{}, proof.BeforeCursor)
			require.Equal(t, common.BigToHash(big.NewInt(1)), proof.AfterCursor)
			return executor.ApplyOrdinaryDELPenalty(child, before, governed, supply, project)
		})
	}
	injected := errors.New("reject verified penalty projection after writes")
	err = appendAndRealize(func(child sdk.Context, proof settlement.OrdinaryDELPenaltyProof) error {
		if err := project(child, proof); err != nil {
			return err
		}
		return injected
	})
	require.ErrorIs(t, err, injected)
	require.Zero(t, masterCount())
	require.Equal(t, before, getRow())
	require.True(t, initialSupply.Equal(supply(ctx())))
	require.Zero(t, f.read(f.gateway, f.target, "getStakePenaltyIndex", id).(*big.Int).Sign())
	f.backing(principal)
	f.activeReserve(principal)

	require.NoError(t, appendAndRealize(project))
	require.Equal(t, int64(1), masterCount())
	after := getRow()
	require.True(t, want.Equal(after.RawAmount))
	require.Equal(t, common.BigToHash(big.NewInt(1)), common.BytesToHash(after.PenaltyCursor))
	require.Equal(t, int64(1), f.read(f.gateway, f.target, "getStakePenaltyIndex", id).(*big.Int).Int64())
	require.True(t, initialSupply.Sub(burned).Equal(supply(ctx())))
	f.backing(want)
	f.activeReserve(want)

	require.Error(t, executor.ApplyOrdinaryDELPenalty(ctx(), after, governed, supply,
		func(sdk.Context, settlement.OrdinaryDELPenaltyProof) error {
			t.Fatal("replayed penalty reached projection")
			return nil
		}))
	require.True(t, initialSupply.Sub(burned).Equal(supply(ctx())))
	require.Equal(t, after, getRow())
	require.ErrorContains(t, appendAndRealize(project), "unpaused candidate or approved validator")
	require.Equal(t, int64(1), masterCount())
	if recover != nil {
		f.signed("delegateDEL", sdkmath.NewIntWithDecimal(1, 18), false, f.validator)
		gov(master, profile.MasterValidator, "unpauseValidator", f.validator)
		recover(t, f, executor, want, initialSupply.Sub(burned))
		return
	}

	withdraw := func(binding validatortypes.EVMStakeBinding, amount sdkmath.Int, expectAck bool) {
		data, err := gateway.ABI.Pack("withdraw", f.validator, f.wrapped, amount.BigInt())
		require.NoError(t, err)
		msg := core.Message{From: f.user, To: &f.target, Value: new(big.Int), Data: data}
		response := f.signed("withdraw", sdkmath.ZeroInt(), true, f.validator, f.wrapped, amount.BigInt())
		receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(response.Logs)}
		if expectAck {
			require.Len(t, receipt.Logs, 4, "first withdrawal advances the aggregate reserve cursor")
			// A nonzero amount in the aggregate acknowledgement is not a
			// valid penalty-free withdrawal even when the receipt has four logs.
			bad := *receipt
			bad.Logs = append([]*ethtypes.Log(nil), receipt.Logs...)
			ack := *bad.Logs[0]
			ack.Data = append([]byte(nil), ack.Data...)
			ack.Data[len(ack.Data)-1] = 1
			bad.Logs[0] = &ack
			_, err = executor.VerifySignedPenalizedDELWithdrawal(ctx(), msg, &bad, binding)
			require.ErrorContains(t, err, "aggregate penalty acknowledgement")
		} else {
			require.Len(t, receipt.Logs, 3, "subsequent withdrawal leaves the aggregate cursor unchanged")
		}
		_, err = executor.VerifySignedDEL(ctx(), msg, receipt)
		require.Error(t, err, "ordinary admission remains closed to a penalized validator")
		badBinding := binding
		badBinding.RawAmount = badBinding.RawAmount.Add(sdkmath.OneInt())
		_, err = executor.VerifySignedPenalizedDELWithdrawal(ctx(), msg, receipt, badBinding)
		require.ErrorContains(t, err, "native active amount")
		out, err := executor.VerifySignedPenalizedDELWithdrawal(ctx(), msg, receipt, binding)
		require.NoError(t, err)
		require.Equal(t, common.BigToHash(big.NewInt(1)), out.PenaltyCursor)
		require.True(t, binding.RawAmount.Sub(amount).Equal(out.Remaining))
		require.True(t, amount.Equal(out.Amount))
		require.NoError(t, keeper.ApplyPenalizedGatewayDELWithdrawal(ctx(), validatorkeeper.GatewayDELStake{
			Gateway: f.target, WrappedDEL: f.wrapped, Beneficiary: f.user, Validator: f.validator,
			StakeID: id, Amount: out.Remaining, PenaltyCursor: out.PenaltyCursor,
		}, out.FrozenIndex, out.Amount, out.Deadline))
		frozen, err := keeper.GetEVMFrozenBinding(e.ctx(), f.target[:], out.FrozenIndex[:])
		require.NoError(t, err)
		require.True(t, amount.Equal(frozen.RawAmount))
		active, err := keeper.GetActiveDELReserve(e.ctx(), f.operator)
		require.NoError(t, err)
		require.True(t, out.Remaining.Equal(active))
		require.True(t, initialSupply.Sub(burned).Equal(supply(ctx())), "withdrawal must not implicitly burn any additional DEL")
		f.backing(want)
	}
	withdraw(after, sdkmath.NewIntWithDecimal(2, 18), true)
	last := getRow()
	withdraw(last, last.RawAmount, false)
	_, err = keeper.GetEVMStakeBinding(e.ctx(), validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, f.target[:], id[:])
	require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
	t.Log(delPenaltyPassed)
}

func verifyRecoveredDELDeposits(t *testing.T, f signedDELFixture, executor *settlement.Executor, existing, supply sdkmath.Int) {
	t.Helper()
	e := f.env
	keeper := e.app.NativeValidatorKeeper
	validator, err := keeper.GetValidator(e.ctx(), f.operator)
	require.NoError(t, err)
	validator.Online, validator.Jailed = true, true
	require.NoError(t, keeper.SetValidator(e.ctx(), validator))
	one := sdkmath.NewIntWithDecimal(1, 18)
	cursor := common.BigToHash(big.NewInt(1))
	deposit := func(route string, amount, remaining sdkmath.Int, expectAck bool, args ...interface{}) settlement.DELTransition {
		t.Helper()
		response := f.signed(route, amount, true, args...)
		data, err := f.gateway.ABI.Pack(route, args...)
		require.NoError(t, err)
		msg := core.Message{From: f.user, To: &f.target, Value: amount.BigInt(), Data: data}
		receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(response.Logs)}
		included := e.ctx().WithBlockHeight(e.app.LastBlockHeight()).WithBlockTime(e.ctx().BlockTime().Add(-time.Second)).WithExecMode(sdk.ExecModeFinalize)
		ctx := included.WithGasMeter(storetypes.NewGasMeter(24_000_000))
		if expectAck {
			require.Len(t, receipt.Logs, 4)
			bad := *receipt
			bad.Logs = append([]*ethtypes.Log(nil), receipt.Logs...)
			ack := *bad.Logs[0]
			ack.Data = append([]byte(nil), ack.Data...)
			ack.Data[len(ack.Data)-1] = 1
			bad.Logs[0] = &ack
			_, err := executor.VerifySignedDELDepositWithDowntime(included.WithGasMeter(storetypes.NewGasMeter(24_000_000)), msg, &bad)
			require.ErrorContains(t, err, "aggregate DEL penalty acknowledgement")
		} else {
			require.Len(t, receipt.Logs, 3)
		}
		out, err := executor.VerifySignedDELDepositWithDowntime(ctx, msg, receipt)
		require.NoError(t, err)
		require.Equal(t, cursor, out.PenaltyCursor)
		require.True(t, out.Remaining.Equal(remaining))
		_, err = executor.VerifySignedDEL(included.WithGasMeter(storetypes.NewGasMeter(24_000_000)), msg, receipt)
		require.Error(t, err, "the historical route must still require the zero cursor")
		position := validatorkeeper.GatewayDELStake{Gateway: f.target, WrappedDEL: f.wrapped, Beneficiary: f.user, Validator: f.validator,
			StakeID: out.StakeID, PenaltyCursor: out.PenaltyCursor, Amount: out.Remaining,
			HoldDeadline: out.HoldDeadline, HoldStartTime: out.HoldStartTime}
		if validator.Jailed {
			require.ErrorContains(t, keeper.ApplyRecoveredGatewayDELDelegation(ctx, position, out.Amount), "unjailed")
			unchanged, err := keeper.GetActiveDELReserve(ctx, f.operator)
			require.NoError(t, err)
			require.True(t, existing.Equal(unchanged))
			validator.Jailed = false
			require.NoError(t, keeper.SetValidator(e.ctx(), validator))
		}
		require.NoError(t, keeper.ApplyRecoveredGatewayDELDelegation(ctx, position, out.Amount))
		row, err := keeper.GetEVMStakeBinding(ctx, validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, f.target.Bytes(), out.StakeID.Bytes())
		require.NoError(t, err)
		require.Equal(t, cursor.Bytes(), row.PenaltyCursor)
		require.True(t, row.RawAmount.Equal(remaining))
		require.True(t, supply.Equal(e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount))
		return out
	}
	ordinary := existing.Add(one)
	deposit("delegateDEL", one, ordinary, true, f.validator)
	f.backing(ordinary)
	f.activeReserveAtCursor(ordinary, cursor)
	deadline := big.NewInt(e.ctx().BlockTime().Unix() + 120)
	held := deposit("delegateHoldDEL", one, one, false, f.validator, deadline)
	require.Equal(t, deadline.Int64(), held.HoldDeadline)
	topUp := deposit("delegateHoldDEL", one, one.Add(one), false, f.validator, deadline)
	require.Equal(t, held.HoldStartTime, topUp.HoldStartTime)
	row, err := keeper.GetDelegation(e.ctx(), f.beneficiary, f.operator, validatortypes.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Len(t, row.Stake.Holds, 2)
	require.True(t, row.Stake.Stake.Amount.Equal(one.Add(one)))
	f.backing(ordinary.Add(one).Add(one))
	f.activeReserveAtCursor(ordinary.Add(one).Add(one), cursor)
	t.Log(recoveredDELDepositsPassed)
}
