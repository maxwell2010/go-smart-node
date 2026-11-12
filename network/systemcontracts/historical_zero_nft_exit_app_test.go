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
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const historicalZeroNFTExitMode = "historical-zero-nft-exit"
const historicalZeroNFTExitPassed = "zero-reserve historical NFT exits after acknowledging its empty DEL companion"

func TestOwnedAppHistoricalZeroNFTExit(t *testing.T) {
	testSignedDELFixture(t, historicalZeroNFTExitMode, historicalZeroNFTExitPassed)
}

func runHistoricalZeroNFTExit(t *testing.T, f signedDELFixture, reader *nftview.Reader, executor *settlement.Executor,
	isolation settlement.NFTIsolation, principal, reserve sdkmath.Int,
	inclusion func(*evmtypes.MsgEthereumTxResponse) (sdk.Context, *ethtypes.Receipt),
) {
	t.Helper()
	e := f.env
	keeper, nft := e.app.NativeValidatorKeeper, e.app.NativeNFTKeeper
	ctx := func() sdk.Context {
		return e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(24_000_000))
	}
	supply := func(ctx sdk.Context) sdkmath.Int { return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount }
	position := func(proof settlement.DELTransition, cursor common.Hash) validatorkeeper.GatewayDELStake {
		return validatorkeeper.GatewayDELStake{Gateway: f.target, WrappedDEL: f.wrapped, Validator: proof.Validator,
			Beneficiary: proof.Beneficiary, StakeID: proof.StakeID, Amount: proof.Remaining, NFTBacked: proof.NFTBacked, PenaltyCursor: cursor}
	}
	coinID, positionID := isolation.Companion.StakeID, isolation.Position.Asset.TokenID
	getNFT := func(ctx sdk.Context) validatortypes.EVMStakeBinding {
		t.Helper()
		row, err := keeper.GetEVMStakeBinding(ctx, validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT, f.target[:], isolation.StakeID[:])
		require.NoError(t, err)
		return row
	}
	getCoin := func(ctx sdk.Context) (validatortypes.EVMStakeBinding, error) {
		return keeper.GetEVMStakeBinding(ctx, validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, f.target[:], coinID[:])
	}
	projectNFT := func(ctx sdk.Context, proof settlement.ActiveNFTPenaltyProof) validatorkeeper.GatewayNFTPenalty {
		t.Helper()
		lineage, err := nft.GetIsolatedLineage(ctx, isolation.Position.Asset.Contract[:], positionID[:])
		require.NoError(t, err)
		return validatorkeeper.GatewayNFTPenalty{
			Before:       validatorkeeper.GatewayNFTPosition{Companion: position(proof.Companion, proof.CompanionCursor), StakeID: isolation.StakeID, Lineage: lineage},
			BeforeCursor: proof.BeforeCursor, AfterCursor: proof.AfterCursor, RemainingReserve: proof.RemainingReserve,
		}
	}
	e.app.EVMKeeper.SetInternalCommitGuard(func(ctx sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if executor.AuthorizeMasterPenalty(ctx, msg) || executor.AuthorizeActiveNFTPenalty(ctx, msg) ||
			executor.AuthorizeOrdinaryDELPenalty(ctx, msg) || executor.AuthorizeMasterHistoricalPenalty(ctx, msg) ||
			executor.AuthorizeActiveHistoricalNFTPenalty(ctx, msg) {
			return nil
		}
		return errors.New("test-only internal call lacks exact penalty authorization")
	})
	initialSupply := supply(ctx())
	require.NoError(t, executor.AppendMasterPenalty(ctx(), f.validator, 100, supply, func(child sdk.Context, master settlement.MasterPenaltyProof) error {
		if err := executor.ApplyActiveNFTPenalty(child, getNFT(child), 100, supply, func(project sdk.Context, proof settlement.ActiveNFTPenaltyProof) error {
			return keeper.ApplyGatewayNFTPenalty(project, nft, projectNFT(project, proof))
		}); err != nil {
			return err
		}
		coin, err := getCoin(child)
		if err != nil {
			return err
		}
		return executor.ApplyOrdinaryDELPenalty(child, coin, 100, supply, func(project sdk.Context, proof settlement.ActiveDELPenaltyProof) error {
			del := validatorkeeper.GatewayDELStake{Gateway: f.target, WrappedDEL: f.wrapped, Validator: f.validator,
				Beneficiary: f.user, StakeID: coinID, Amount: proof.Before.RawAmount, PenaltyCursor: master.BeforeCursor}
			return keeper.ApplyGatewayDELPenalty(project, del, proof.After.RawAmount, master.AfterCursor)
		})
	}))
	wantSupply := initialSupply.Sub(principal).Sub(reserve)
	require.True(t, wantSupply.Equal(supply(ctx())))
	_, err := getCoin(ctx())
	require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
	before := getNFT(ctx())
	require.True(t, before.ReportedReserveAmount.IsZero())
	infraction := ctx().BlockHeight() - 1
	require.NoError(t, executor.AppendMasterHistoricalPenalty(ctx(), f.validator, infraction, supply, func(child sdk.Context, master settlement.MasterPenaltyProof) error {
		plan, err := keeper.PlanActiveNFTHistoricalPenalty(child, before, infraction)
		if err != nil {
			return err
		}
		require.True(t, plan.Loss.IsZero())
		return executor.ApplyActiveHistoricalNFTPenalty(child, before, master, plan.Eligible, supply, func(project sdk.Context, proof settlement.ActiveNFTPenaltyProof) error {
			return keeper.ApplyGatewayActiveHistoricalNFTPenalty(project, nft, projectNFT(project, proof), infraction, proof.Eligible)
		})
	}))
	before = getNFT(ctx())
	require.Equal(t, common.BigToHash(big.NewInt(2)).Bytes(), before.PenaltyCursor)
	require.True(t, wantSupply.Equal(supply(ctx())))
	data, err := f.gateway.ABI.Pack("withdrawNFT", f.validator, isolation.Position.Asset.Contract, positionID.Big())
	require.NoError(t, err)
	msg := core.Message{From: f.user, To: &f.target, Value: new(big.Int), Data: data}
	response := f.signed("withdrawNFT", sdkmath.ZeroInt(), true, f.validator, isolation.Position.Asset.Contract, positionID.Big())
	committed, receipt := inclusion(response)
	out, err := executor.VerifySignedPenalizedNFTWithdrawal(committed, msg, receipt, before)
	require.NoError(t, err)
	require.True(t, out.CompanionCursorAcknowledged)
	lineage, err := nft.GetIsolatedLineage(committed, isolation.Position.Asset.Contract[:], positionID[:])
	require.NoError(t, err)
	require.NoError(t, keeper.ApplyGatewayPenalizedNFTWithdrawal(committed, validatorkeeper.GatewayNFTPosition{
		Companion: position(out.Companion, out.Companion.PenaltyCursor), StakeID: out.StakeID, Lineage: lineage,
	}, out.FrozenIndex, out.Deadline, out.PenaltyCursor, out.CompanionCursorAcknowledged))
	frozen, err := keeper.GetEVMFrozenBinding(ctx(), f.target[:], out.FrozenIndex[:])
	require.NoError(t, err)
	completePenalizedNFTExit(t, f, reader, executor, frozen, out, lineage, inclusion, 0)
	require.True(t, wantSupply.Equal(supply(ctx())))
	t.Log(historicalZeroNFTExitPassed)
}
