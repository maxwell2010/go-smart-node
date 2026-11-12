package systemcontracts_test

import (
	"errors"
	"math/big"
	"testing"

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

const (
	activeHistoricalDELPenaltyMode   = "active-historical-del-penalty"
	activeHistoricalDELPenaltyPassed = "owned EVM historical DEL burn preserves late funds and rolls back native provenance"
)

func TestOwnedAppActiveHistoricalDELPenalty(t *testing.T) {
	testSignedDELFixture(t, activeHistoricalDELPenaltyMode, activeHistoricalDELPenaltyPassed)
}

func runActiveHistoricalDELPenalty(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile,
	gateway, master, wrapper contracts.Artifact, reader *nftview.Reader,
	gov func(contracts.Artifact, common.Address, string, ...interface{}),
) {
	t.Helper()
	f := prepareSignedDELFixture(t, e, profile, gateway, master, wrapper, gov)
	f.declareValidator()
	keeper := e.app.NativeValidatorKeeper
	executor, err := settlement.NewExecutor(e.app.EVMKeeper, reader, settlement.Config{EVMGas: 750_000, MaxLogs: 32, MaxLogBytes: 16384})
	require.NoError(t, err)
	delegate, err := gateway.ABI.Pack("delegateDEL", f.validator)
	require.NoError(t, err)
	deposit := func(amount sdkmath.Int, projection sdk.Context) validatorkeeper.GatewayDELStake {
		t.Helper()
		response := f.signed("delegateDEL", amount, true, f.validator)
		proof, err := executor.VerifySignedDEL(e.ctx(), core.Message{From: f.user, To: &f.target, Value: amount.BigInt(), Data: delegate},
			&ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(response.Logs)})
		require.NoError(t, err)
		position := validatorkeeper.GatewayDELStake{
			Gateway: f.target, WrappedDEL: f.wrapped, Beneficiary: f.user, Validator: f.validator,
			StakeID: proof.StakeID, Amount: proof.Remaining,
		}
		require.True(t, amount.Equal(proof.Amount))
		require.NoError(t, keeper.ApplyGatewayDELDelegation(projection, position, proof.Amount))
		return position
	}

	oldAmount := sdkmath.NewIntWithDecimal(6, 18)
	lateAmount := sdkmath.NewIntWithDecimal(4, 18)
	principal := oldAmount.Add(lateAmount)
	burned := sdkmath.NewIntWithDecimal(3, 17)
	remaining := principal.Sub(burned)
	// Model an attested genesis position in this already bootstrapped test
	// App. Its escrow comes from an actual signed deposit. Only its native
	// projection and provenance initialization use the fresh genesis height.
	genesis := e.ctx().WithBlockHeight(0)
	genesisPosition := deposit(oldAmount, genesis)
	require.NoError(t, keeper.InitializeDELProvenance(genesis, f.target, f.wrapped))
	lateBlock := e.ctx()
	infraction := lateBlock.BlockHeight()
	position := deposit(lateAmount, lateBlock)
	require.Equal(t, genesisPosition.StakeID, position.StakeID)
	require.True(t, principal.Equal(position.Amount))

	ctx := func() sdk.Context {
		return e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(24_000_000))
	}
	supply := func(ctx sdk.Context) sdkmath.Int {
		return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount
	}
	getStake := func(ctx sdk.Context) validatortypes.EVMStakeBinding {
		t.Helper()
		row, err := keeper.GetEVMStakeBinding(ctx, validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, f.target[:], position.StakeID[:])
		require.NoError(t, err)
		return row
	}
	assertTranches := func(ctx sdk.Context, row validatortypes.EVMStakeBinding, old sdkmath.Int) {
		t.Helper()
		lots, err := keeper.GetActiveDELProvenance(ctx, row)
		require.NoError(t, err)
		require.Len(t, lots, 2)
		require.Zero(t, lots[0].FundedHeight)
		require.True(t, old.Equal(lots[0].Amount))
		require.Equal(t, infraction, lots[1].FundedHeight)
		require.True(t, lateAmount.Equal(lots[1].Amount), "the entire late tranche must survive")
	}
	before := getStake(ctx())
	initialSupply := supply(ctx())
	assertCommittedState := func(want, old, expectedSupply sdkmath.Int, cursor common.Hash) {
		t.Helper()
		row := getStake(ctx())
		require.True(t, want.Equal(row.RawAmount))
		require.Equal(t, cursor, common.BytesToHash(row.PenaltyCursor))
		require.Zero(t, f.read(gateway, f.target, "getStakePenaltyIndex", position.StakeID).(*big.Int).Cmp(cursor.Big()))
		require.Zero(t, f.read(master, profile.MasterValidator, "getPenaltyCount", f.validator).(*big.Int).Cmp(cursor.Big()))
		require.True(t, expectedSupply.Equal(supply(ctx())))
		f.backing(want)
		f.activeReserve(want)
		assertTranches(ctx(), row, old)
	}
	assertCommittedState(principal, oldAmount, initialSupply, common.Hash{})

	var masterCommits, gatewayCommits int
	e.app.EVMKeeper.SetInternalCommitGuard(func(child sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if executor.AuthorizeMasterHistoricalPenalty(child, msg) {
			masterCommits++
			return nil
		}
		if executor.AuthorizeActiveHistoricalDELPenalty(child, msg) {
			require.False(t, executor.AuthorizeOrdinaryDELPenalty(child, msg))
			require.False(t, executor.AuthorizeHeldDELPenalty(child, msg))
			gatewayCommits++
			return nil
		}
		return errors.New("test-only internal EVM call lacks exact historical penalty authorization")
	})
	appendAndProject := func(projectErr error) error {
		t.Helper()
		return executor.AppendMasterHistoricalPenalty(ctx(), f.validator, infraction, supply, func(child sdk.Context, announced settlement.MasterPenaltyProof) error {
			plan, err := keeper.PlanActiveDELHistoricalPenalty(child, before, infraction)
			require.NoError(t, err)
			require.True(t, oldAmount.Equal(plan.Eligible))
			require.True(t, burned.Equal(plan.Loss))
			return executor.ApplyActiveHistoricalDELPenalty(child, before, announced, plan.Eligible, supply, func(child sdk.Context, proof settlement.ActiveDELPenaltyProof) error {
				require.Equal(t, before, proof.Before)
				require.True(t, remaining.Equal(proof.After.RawAmount))
				require.True(t, burned.Equal(proof.Burned))
				cursor := common.BytesToHash(proof.After.PenaltyCursor)
				require.Equal(t, announced.AfterCursor, cursor)
				require.NoError(t, keeper.ApplyGatewayActiveHistoricalDELPenalty(child, position, infraction, proof.After.RawAmount, cursor))
				projected := getStake(child)
				require.Equal(t, proof.After, projected)
				assertTranches(child, projected, oldAmount.Sub(burned))
				require.True(t, initialSupply.Sub(burned).Equal(supply(child)))
				return projectErr
			})
		})
	}
	injected := errors.New("reject historical DEL projection after native and EVM writes")
	require.ErrorIs(t, appendAndProject(injected), injected)
	require.Equal(t, 1, masterCommits)
	require.Equal(t, 1, gatewayCommits)
	require.Equal(t, before, getStake(ctx()))
	assertCommittedState(principal, oldAmount, initialSupply, common.Hash{})

	require.NoError(t, appendAndProject(nil))
	require.Equal(t, 2, masterCommits)
	require.Equal(t, 2, gatewayCommits)
	assertCommittedState(remaining, oldAmount.Sub(burned), initialSupply.Sub(burned), common.BigToHash(big.NewInt(1)))
	t.Log(activeHistoricalDELPenaltyPassed)
}
