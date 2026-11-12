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
	masterHistoricalPenaltyMode   = "master-historical-penalty"
	masterHistoricalPenaltyPassed = "owned EVM historical Master append, rollback, and typed settlement gate passed"
)

func TestOwnedAppMasterHistoricalPenalty(t *testing.T) {
	testSignedDELFixture(t, masterHistoricalPenaltyMode, masterHistoricalPenaltyPassed)
}

func runMasterHistoricalPenalty(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile,
	gateway, master, wrapper contracts.Artifact, reader *nftview.Reader,
	gov func(contracts.Artifact, common.Address, string, ...interface{}),
) {
	t.Helper()
	f := prepareSignedDELFixture(t, e, profile, gateway, master, wrapper, gov)
	f.declareValidator()
	executor, err := settlement.NewExecutor(e.app.EVMKeeper, reader, settlement.Config{EVMGas: 750_000, MaxLogs: 32, MaxLogBytes: 16384})
	require.NoError(t, err)
	principal := sdkmath.NewIntWithDecimal(10, 18)
	response := f.signed("delegateDEL", principal, true, f.validator)
	delegate, err := gateway.ABI.Pack("delegateDEL", f.validator)
	require.NoError(t, err)
	funding, err := executor.VerifySignedDEL(e.ctx(), core.Message{From: f.user, To: &f.target, Value: principal.BigInt(), Data: delegate},
		&ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(response.Logs)})
	require.NoError(t, err)
	keeper := e.app.NativeValidatorKeeper
	require.NoError(t, keeper.ApplyGatewayDELDelegation(e.ctx(), validatorkeeper.GatewayDELStake{
		Gateway: f.target, WrappedDEL: f.wrapped, Beneficiary: f.user, Validator: f.validator,
		StakeID: funding.StakeID, Amount: funding.Remaining,
	}, funding.Amount))
	getStake := func() validatortypes.EVMStakeBinding {
		row, err := keeper.GetEVMStakeBinding(e.ctx(), validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, f.target[:], funding.StakeID[:])
		require.NoError(t, err)
		return row
	}
	beforeStake := getStake()
	ctx := func() sdk.Context {
		return e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(24_000_000))
	}
	supply := func(ctx sdk.Context) sdkmath.Int {
		return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount
	}
	initialSupply := supply(ctx())
	assertNoBurn := func() {
		t.Helper()
		require.Equal(t, initialSupply, supply(ctx()))
		require.Equal(t, beforeStake, getStake())
		require.Zero(t, f.read(gateway, f.target, "getStakePenaltyIndex", funding.StakeID).(*big.Int).Sign())
		f.backing(principal)
		f.activeReserve(principal)
	}
	readMaster := func(ctx sdk.Context, method string, args ...interface{}) []interface{} {
		t.Helper()
		data, err := master.ABI.Pack(method, args...)
		require.NoError(t, err)
		child, _ := ctx.CacheContext()
		child = child.WithGasMeter(storetypes.NewGasMeter(3_000_000))
		msg := core.Message{From: f.user, To: &profile.MasterValidator, Value: new(big.Int), Data: data, GasLimit: 500_000,
			GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), SkipNonceChecks: true, SkipFromEOACheck: true}
		result, err := e.app.EVMKeeper.ApplyMessage(child, statedb.New(child, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), msg, nil, false, false, true)
		require.NoError(t, err)
		require.Empty(t, result.VmError)
		values, err := master.ABI.Unpack(method, result.Ret)
		require.NoError(t, err)
		return values
	}
	status := readMaster(ctx(), "getValidatorState", f.validator)[0]
	assertState := func(ctx sdk.Context, paused bool, count int64) {
		t.Helper()
		state := readMaster(ctx, "getValidatorState", f.validator)
		require.Len(t, state, 3)
		require.Equal(t, status, state[0])
		require.Equal(t, paused, state[1])
		require.Zero(t, state[2].(*big.Int).Cmp(big.NewInt(count)))
	}
	const rate uint16 = 5
	assertEntry := func(ctx sdk.Context, index, height int64) {
		t.Helper()
		entry := readMaster(ctx, "getDuplicateVotePenalty", f.validator, big.NewInt(index))
		require.Len(t, entry, 2)
		require.Zero(t, entry[0].(*big.Int).Cmp(big.NewInt(height)))
		require.Equal(t, rate, entry[1])
	}
	historicalCommits, ordinaryCommits := 0, 0
	e.app.EVMKeeper.SetInternalCommitGuard(func(ctx sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if executor.AuthorizeMasterHistoricalPenalty(ctx, msg) {
			require.False(t, executor.AuthorizeMasterPenalty(ctx, msg), "the regular append guard must not authorize a historical entry")
			historicalCommits++
			return nil
		}
		if executor.AuthorizeOrdinaryDELPenalty(ctx, msg) {
			ordinaryCommits++
			return nil
		}
		return errors.New("test-only internal EVM call lacks exact penalty authorization")
	})
	assertState(ctx(), false, 0)
	assertNoBurn()
	require.Greater(t, ctx().BlockHeight(), int64(2))
	firstHeight := ctx().BlockHeight() - 2
	appendAndCheck := func(height, before int64, reconcileErr error) error {
		t.Helper()
		return executor.AppendMasterHistoricalPenalty(ctx(), f.validator, height, supply, func(child sdk.Context, proof settlement.MasterPenaltyProof) error {
			require.Equal(t, f.validator, proof.Validator)
			require.Equal(t, rate, proof.Percentage)
			require.Equal(t, height, proof.InfractionHeight)
			require.Equal(t, common.BigToHash(big.NewInt(before)), proof.BeforeCursor)
			require.Equal(t, common.BigToHash(big.NewInt(before+1)), proof.AfterCursor)
			assertState(child, true, before+1)
			assertEntry(child, before, height)
			require.Equal(t, initialSupply, supply(child))
			return reconcileErr
		})
	}

	injected := errors.New("reject historical Master reconciliation after verified append")
	require.ErrorIs(t, appendAndCheck(firstHeight, 0, injected), injected)
	require.Equal(t, 1, historicalCommits)
	assertState(ctx(), false, 0)
	assertNoBurn()

	require.NoError(t, appendAndCheck(firstHeight, 0, nil))
	require.Equal(t, 2, historicalCommits)
	assertState(ctx(), true, 1)
	assertEntry(ctx(), 0, firstHeight)
	assertNoBurn()

	ordinaryProjected := false
	err = executor.ApplyOrdinaryDELPenalty(ctx(), beforeStake, rate, supply, func(sdk.Context, settlement.OrdinaryDELPenaltyProof) error {
		ordinaryProjected = true
		return nil
	})
	require.ErrorContains(t, err, "HistoricalPenaltyRequiresSettlement")
	require.False(t, ordinaryProjected)
	require.Zero(t, ordinaryCommits, "typed entries are rejected before an ordinary balance-changing call")
	assertNoBurn()

	replayed := false
	err = executor.AppendMasterHistoricalPenalty(ctx(), f.validator, firstHeight, supply, func(sdk.Context, settlement.MasterPenaltyProof) error {
		replayed = true
		return nil
	})
	require.Error(t, err)
	require.False(t, replayed)
	assertState(ctx(), true, 1)
	assertEntry(ctx(), 0, firstHeight)

	secondHeight := firstHeight + 1
	require.NoError(t, appendAndCheck(secondHeight, 1, nil), "a distinct infraction can be appended while the validator is paused")
	assertState(ctx(), true, 2)
	assertEntry(ctx(), 0, firstHeight)
	assertEntry(ctx(), 1, secondHeight)
	assertNoBurn()
	t.Log(masterHistoricalPenaltyPassed)
}
