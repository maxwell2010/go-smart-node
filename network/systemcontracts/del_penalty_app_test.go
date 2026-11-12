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

const delPenaltyPassed = "ordinary DEL penalty realized exactly once with native supply, WDEL and projection rollback"

func TestOwnedAppSignedOrdinaryDELPenalty(t *testing.T) {
	testSignedDELFixture(t, "penalty", delPenaltyPassed)
}

func runSignedOrdinaryDELPenalty(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, gateway, master, wrapper contracts.Artifact,
	reader *nftview.Reader, gov func(contracts.Artifact, common.Address, string, ...interface{}),
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

	e.app.EVMKeeper.SetInternalCommitGuard(func(ctx sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if !executor.AuthorizeMasterPenalty(ctx, msg) && !executor.AuthorizeOrdinaryDELPenalty(ctx, msg) {
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
	t.Log(delPenaltyPassed)
}
