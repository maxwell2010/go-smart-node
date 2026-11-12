package systemcontracts_test

import (
	"errors"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
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

const (
	frozenHistoricalDELPenaltyMode   = "frozen-historical-del-penalty"
	frozenHistoricalDELPenaltyPassed = "owned EVM mature frozen DEL burn preserves late FIFO funds and rolls back the queue"
)

func TestOwnedAppFrozenHistoricalDELPenalty(t *testing.T) {
	testSignedDELFixture(t, frozenHistoricalDELPenaltyMode, frozenHistoricalDELPenaltyPassed)
}

func runFrozenHistoricalDELPenalty(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile,
	gateway, master, wrapper contracts.Artifact, reader *nftview.Reader,
	gov func(contracts.Artifact, common.Address, string, ...interface{}),
) {
	t.Helper()
	f := prepareSignedDELFixture(t, e, profile, gateway, master, wrapper, gov)
	f.declareValidator()
	keeper := e.app.NativeValidatorKeeper
	executor, err := settlement.NewExecutor(e.app.EVMKeeper, reader, settlement.Config{EVMGas: 750_000, MaxLogs: 32, MaxLogBytes: 16384})
	require.NoError(t, err)
	signed := func(method string, value sdkmath.Int, args ...interface{}) settlement.DELTransition {
		t.Helper()
		included := e.ctx()
		data, err := gateway.ABI.Pack(method, args...)
		require.NoError(t, err)
		response := f.signed(method, value, true, args...)
		proof, err := executor.VerifySignedDEL(included, core.Message{From: f.user, To: &f.target, Value: value.BigInt(), Data: data},
			&ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(response.Logs)})
		require.NoError(t, err)
		return proof
	}
	position := func(proof settlement.DELTransition) validatorkeeper.GatewayDELStake {
		return validatorkeeper.GatewayDELStake{Gateway: f.target, WrappedDEL: f.wrapped, Validator: f.validator, Beneficiary: f.user,
			StakeID: proof.StakeID, Amount: proof.Remaining}
	}
	amount := func(n int64) sdkmath.Int { return sdkmath.NewIntWithDecimal(n, 18) }
	oldAmount, lateAmount, claimAmount := amount(6), amount(4), amount(8)
	principal := oldAmount.Add(lateAmount)
	activeAmount := principal.Sub(claimAmount)
	queuedLate := lateAmount.Sub(activeAmount)
	burned := sdkmath.NewIntWithDecimal(3, 17)
	// Use an actual signed deposit as attested escrow for the synthetic native
	// genesis projection. The late deposit and freeze use their inclusion heights.
	genesis := e.ctx().WithBlockHeight(0)
	old := signed("delegateDEL", oldAmount, f.validator)
	require.NoError(t, keeper.ApplyGatewayDELDelegation(genesis, position(old), old.Amount))
	require.NoError(t, keeper.InitializeDELProvenance(genesis, f.target, f.wrapped))
	lateBlock := e.ctx()
	infraction := lateBlock.BlockHeight()
	late := signed("delegateDEL", lateAmount, f.validator)
	require.Equal(t, old.StakeID, late.StakeID)
	require.NoError(t, keeper.ApplyGatewayDELDelegation(lateBlock, position(late), late.Amount))
	freezeBlock := e.ctx()
	request := signed("withdraw", sdkmath.ZeroInt(), f.validator, f.wrapped, claimAmount.BigInt())
	require.True(t, claimAmount.Equal(request.Amount))
	require.NoError(t, keeper.ApplyGatewayDELWithdrawal(freezeBlock, position(request), request.FrozenIndex, request.Amount, request.Deadline))

	// Advance through real isolated fixture blocks. This fixture projects signed
	// receipts explicitly and has no automatic settlement controller installed,
	// so the nominally mature claim is still custodied at the penalty call.
	for !e.ctx().BlockTime().After(request.Deadline) {
		e.advance()
	}
	ctx := func() sdk.Context {
		return e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(24_000_000))
	}
	supply := func(ctx sdk.Context) sdkmath.Int {
		return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount
	}
	claim := func(ctx sdk.Context) validatortypes.EVMFrozenBinding {
		t.Helper()
		row, err := keeper.GetEVMFrozenBinding(ctx, f.target[:], request.FrozenIndex[:])
		require.NoError(t, err)
		return row
	}
	assertNative := func(ctx sdk.Context, claimAmount, old sdkmath.Int) {
		t.Helper()
		row := claim(ctx)
		require.True(t, row.Active)
		require.Equal(t, freezeBlock.BlockHeight(), row.CreatedHeight)
		require.True(t, claimAmount.Equal(row.RawAmount))
		require.Equal(t, request.Deadline.Unix(), common.BytesToHash(row.CompletionTimestamp).Big().Int64())
		queue, err := keeper.GetUndelegation(ctx, f.beneficiary, f.operator)
		require.NoError(t, err)
		require.Len(t, queue.Entries, 1)
		entry := queue.Entries[0]
		require.True(t, claimAmount.Equal(entry.Stake.Stake.Amount))
		require.True(t, request.Deadline.Equal(entry.CompletionTime))
		hash, err := validatortypes.NativeQueueEntryHash(1, f.beneficiary, f.operator, "", &entry)
		require.NoError(t, err)
		require.Equal(t, hash, row.NativeEntryHash)
		lots, frozenAt, err := keeper.GetFrozenDELProvenance(ctx, row)
		require.NoError(t, err)
		require.True(t, freezeBlock.BlockTime().Equal(frozenAt))
		require.Len(t, lots, 2)
		require.Zero(t, lots[0].FundedHeight)
		require.True(t, old.Equal(lots[0].Amount))
		require.Equal(t, infraction, lots[1].FundedHeight)
		require.True(t, queuedLate.Equal(lots[1].Amount), "the late portion of the FIFO claim must survive")
	}
	checkpointData, err := gateway.ABI.Pack("getFrozenDELCheckpoint", request.FrozenIndex.Big())
	require.NoError(t, err)
	assertCheckpoint := func(cursor int64) {
		t.Helper()
		child, _ := ctx().CacheContext()
		msg := core.Message{From: f.user, To: &f.target, Value: new(big.Int), Data: checkpointData, GasLimit: 500_000,
			GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), SkipNonceChecks: true, SkipFromEOACheck: true}
		result, err := e.app.EVMKeeper.ApplyMessage(child, statedb.New(child, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), msg, nil, false, false, true)
		require.NoError(t, err)
		require.Empty(t, result.VmError)
		values, err := gateway.ABI.Unpack("getFrozenDELCheckpoint", result.Ret)
		require.NoError(t, err)
		require.Len(t, values, 2)
		require.Zero(t, values[0].(*big.Int).Cmp(big.NewInt(freezeBlock.BlockHeight())))
		require.Zero(t, values[1].(*big.Int).Cmp(big.NewInt(cursor)))
	}
	initialSupply := supply(ctx())
	before := claim(ctx())
	assertCommitted := func(loss sdkmath.Int, cursor int64) {
		t.Helper()
		want := claimAmount.Sub(loss)
		assertNative(ctx(), want, oldAmount.Sub(loss))
		raw := f.read(gateway, f.target, "getFrozenStake", request.FrozenIndex.Big())
		frozen := abi.ConvertType(raw, new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal)
		require.Equal(t, uint8(1), frozen.FreezeStatus)
		require.Zero(t, frozen.Stake.Amount.Cmp(want.BigInt()))
		require.Equal(t, request.Deadline.Unix(), frozen.UnfreezeTimestamp.Int64())
		assertCheckpoint(cursor)
		require.Equal(t, cursor, f.read(master, profile.MasterValidator, "getPenaltyCount", f.validator).(*big.Int).Int64())
		require.True(t, initialSupply.Sub(loss).Equal(supply(ctx())))
		f.backing(principal.Sub(loss))
		f.activeReserve(activeAmount)
		row, err := keeper.GetEVMStakeBinding(ctx(), validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, f.target[:], request.StakeID[:])
		require.NoError(t, err)
		active, err := keeper.GetActiveDELProvenance(ctx(), row)
		require.NoError(t, err)
		require.Len(t, active, 1)
		require.Equal(t, infraction, active[0].FundedHeight)
		require.True(t, activeAmount.Equal(active[0].Amount))
	}
	assertCommitted(sdkmath.ZeroInt(), 0)

	var historicalCommits int
	e.app.EVMKeeper.SetInternalCommitGuard(func(child sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if executor.AuthorizeMasterHistoricalPenalty(child, msg) {
			return nil
		}
		if executor.AuthorizeFrozenHistoricalDELPenalty(child, msg) {
			require.False(t, executor.AuthorizeFrozenDELPenalty(child, msg))
			historicalCommits++
			return nil
		}
		return errors.New("test-only internal EVM call lacks exact historical frozen penalty authorization")
	})
	appendAndProject := func(projectErr error) error {
		t.Helper()
		return executor.AppendMasterHistoricalPenalty(ctx(), f.validator, infraction, supply, func(child sdk.Context, master settlement.MasterPenaltyProof) error {
			plan, err := keeper.PlanFrozenDELHistoricalPenalty(child, before, infraction)
			require.NoError(t, err)
			require.True(t, oldAmount.Equal(plan.Eligible))
			require.True(t, burned.Equal(plan.Loss))
			return executor.ApplyFrozenHistoricalDELPenalty(child, before, master, plan.Eligible, supply,
				func(child sdk.Context, proof settlement.FrozenDELPenaltyProof) error {
					require.Equal(t, request.FrozenIndex, proof.Index)
					require.Equal(t, infraction, proof.InfractionHeight)
					require.Equal(t, master.BeforeCursor, proof.BeforeCursor)
					require.Equal(t, master.AfterCursor, proof.AfterCursor)
					require.True(t, proof.Eligible)
					require.True(t, burned.Equal(proof.Burned))
					require.True(t, plan.Remaining.Equal(proof.Remaining))
					require.NoError(t, keeper.ApplyGatewayFrozenHistoricalDELPenalty(child, before, nil, proof.InfractionHeight, proof.BeforeCursor, proof.AfterCursor, proof.Remaining))
					assertNative(child, proof.Remaining, oldAmount.Sub(burned))
					require.True(t, initialSupply.Sub(burned).Equal(supply(child)))
					return projectErr
				})
		})
	}
	injected := errors.New("reject historical frozen DEL projection after native and EVM writes")
	require.ErrorIs(t, appendAndProject(injected), injected)
	require.Equal(t, 1, historicalCommits)
	require.Equal(t, before, claim(ctx()))
	assertCommitted(sdkmath.ZeroInt(), 0)
	require.NoError(t, appendAndProject(nil))
	require.Equal(t, 2, historicalCommits)
	assertCommitted(burned, 1)
	after := claim(ctx())
	require.ErrorContains(t, keeper.ApplyGatewayFrozenHistoricalDELPenalty(ctx(), after, nil, infraction, common.Hash{}, common.BigToHash(big.NewInt(1)), after.RawAmount), "cursor prestate changed")
	t.Log(frozenHistoricalDELPenaltyPassed)
}
