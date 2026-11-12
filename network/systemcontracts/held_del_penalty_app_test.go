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

const heldDELPenaltyPassed = "active held DEL penalty preserves each bucket and lot and burns the exact real reserve"

func TestOwnedAppSignedHeldDELPenalty(t *testing.T) {
	testSignedDELFixture(t, "held-penalty", heldDELPenaltyPassed)
}

func runSignedHeldDELPenalty(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, gateway, master, wrapper contracts.Artifact,
	reader *nftview.Reader, gov func(contracts.Artifact, common.Address, string, ...interface{}),
) {
	t.Helper()
	f := prepareSignedDELFixture(t, e, profile, gateway, master, wrapper, gov)
	f.declareValidator()
	executor, err := settlement.NewExecutor(e.app.EVMKeeper, reader, settlement.Config{EVMGas: 750_000, MaxLogs: 32, MaxLogBytes: 16384})
	require.NoError(t, err)
	keeper := e.app.NativeValidatorKeeper
	del := func(n int64) sdkmath.Int { return sdkmath.NewIntWithDecimal(n, 18) }
	deposit := func(value sdkmath.Int, deadline int64) common.Hash {
		t.Helper()
		data, err := gateway.ABI.Pack("delegateHoldDEL", f.validator, big.NewInt(deadline))
		require.NoError(t, err)
		result := f.signed("delegateHoldDEL", value, true, f.validator, big.NewInt(deadline))
		msg := core.Message{From: f.user, To: &f.target, Value: value.BigInt(), Data: data}
		receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(result.Logs)}
		inclusion := e.ctx().WithBlockHeight(e.app.LastBlockHeight()).WithBlockTime(e.ctx().BlockTime().Add(-time.Second))
		proof, err := executor.VerifySignedDEL(inclusion, msg, receipt)
		require.NoError(t, err)
		require.NoError(t, keeper.ApplyGatewayDELDelegation(inclusion, validatorkeeper.GatewayDELStake{
			Gateway: f.target, WrappedDEL: f.wrapped, Validator: f.validator, Beneficiary: f.user,
			StakeID: proof.StakeID, Amount: proof.Remaining, HoldDeadline: proof.HoldDeadline,
			HoldStartTime: proof.HoldStartTime, NFTBacked: proof.NFTBacked,
		}, proof.Amount))
		return proof.StakeID
	}
	firstDeadline, secondDeadline := e.ctx().BlockTime().Unix()+100, e.ctx().BlockTime().Unix()+200
	firstID := deposit(del(3), firstDeadline)
	require.Equal(t, firstID, deposit(del(2), firstDeadline))
	secondID := deposit(del(7), secondDeadline)
	require.NotEqual(t, firstID, secondID)
	binding := func(ctx sdk.Context, id common.Hash) validatortypes.EVMStakeBinding {
		row, err := keeper.GetEVMStakeBinding(ctx, validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, f.target[:], id[:])
		require.NoError(t, err)
		return row
	}
	held := func(ctx sdk.Context) validatortypes.Stake {
		row, err := keeper.GetDelegation(ctx, f.beneficiary, f.operator, validatortypes.StakeType_CoinHold, identity.BaseDenom)
		require.NoError(t, err)
		return row.Stake
	}
	first, second := binding(e.ctx(), firstID), binding(e.ctx(), secondID)
	beforeLots := held(e.ctx())
	require.Len(t, beforeLots.Holds, 3)
	f.activeReserve(del(12))
	f.backing(del(12))

	e.app.EVMKeeper.SetInternalCommitGuard(func(ctx sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if executor.AuthorizeMasterPenalty(ctx, msg) {
			return nil
		}
		if executor.AuthorizeOrdinaryDELPenalty(ctx, msg) || !executor.AuthorizeHeldDELPenalty(ctx, msg) {
			return errors.New("test-only internal call lacks exact held penalty authorization")
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
	const percentage = uint16(20)
	injected := errors.New("reject held penalty projection after writes")
	reconcile := func(reject bool) error {
		return executor.AppendMasterPenalty(ctx(), f.validator, percentage, supply, func(child sdk.Context, announcement settlement.MasterPenaltyProof) error {
			wrong := first
			wrong.NativeHoldStartTime++
			err := executor.ApplyHeldDELPenalty(child, wrong, percentage, supply, func(sdk.Context, settlement.ActiveDELPenaltyProof) error {
				return errors.New("invalid hold time reached the projector")
			})
			require.ErrorContains(t, err, "exact valid hold start and deadline")
			for _, row := range []validatortypes.EVMStakeBinding{first, second} {
				err := executor.ApplyHeldDELPenalty(child, row, percentage, supply, func(projectCtx sdk.Context, proof settlement.ActiveDELPenaltyProof) error {
					require.Equal(t, row, proof.Before)
					require.Equal(t, row.HoldTimestamp, proof.After.HoldTimestamp)
					require.Equal(t, row.RawHoldStartTime, proof.After.RawHoldStartTime)
					require.Equal(t, announcement.AfterCursor, common.BytesToHash(proof.After.PenaltyCursor))
					require.True(t, row.RawAmount.QuoRaw(5).Equal(proof.Burned))
					require.NoError(t, keeper.ApplyGatewayDELHoldPenalty(projectCtx, validatorkeeper.GatewayDELStake{
						Gateway: f.target, WrappedDEL: f.wrapped, Validator: f.validator, Beneficiary: f.user,
						StakeID: common.BytesToHash(row.StakeID), Amount: row.RawAmount, PenaltyCursor: common.BytesToHash(row.PenaltyCursor),
						HoldDeadline: common.BytesToHash(row.HoldTimestamp).Big().Int64(), HoldStartTime: row.NativeHoldStartTime,
					}, proof.After.RawAmount, announcement.AfterCursor))
					if common.BytesToHash(row.StakeID) == firstID {
						require.Equal(t, second, binding(projectCtx, secondID))
						if reject {
							return injected
						}
					}
					return nil
				})
				if err != nil {
					return err
				}
			}
			return nil
		})
	}
	require.ErrorIs(t, reconcile(true), injected)
	require.Equal(t, first, binding(e.ctx(), firstID))
	require.Equal(t, second, binding(e.ctx(), secondID))
	require.Equal(t, beforeLots, held(e.ctx()))
	require.True(t, initialSupply.Equal(supply(ctx())))
	f.backing(del(12))

	require.NoError(t, reconcile(false))
	afterLots := held(e.ctx())
	require.Len(t, afterLots.Holds, len(beforeLots.Holds))
	for i, before := range beforeLots.Holds {
		after := afterLots.Holds[i]
		require.Equal(t, before.HoldStartTime, after.HoldStartTime)
		require.Equal(t, before.HoldEndTime, after.HoldEndTime)
		require.True(t, before.Amount.MulRaw(4).QuoRaw(5).Equal(after.Amount))
	}
	expected := del(12).MulRaw(4).QuoRaw(5)
	f.activeReserve(expected)
	f.backing(expected)
	require.True(t, initialSupply.Sub(del(12).QuoRaw(5)).Equal(supply(ctx())))
	t.Log(heldDELPenaltyPassed)
}
