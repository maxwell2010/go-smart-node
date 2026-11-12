package systemcontracts_test

import (
	"errors"
	"math/big"
	"strings"
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

const (
	heldDELPenaltyPassed = "active held DEL penalty preserves each bucket and lot and burns the exact real reserve"
	heldPenaltyMode      = "held-penalty"
	heldLotBudgetMode    = "held-penalty-lot-budget"
	mixedLotBudgetMode   = "mixed-penalty-lot-budget"
	benchmarkHoldLots    = 128
)

func TestOwnedAppSignedHeldDELPenalty(t *testing.T) {
	testSignedDELFixture(t, heldPenaltyMode, heldDELPenaltyPassed)
	for _, mode := range []string{heldLotBudgetMode, mixedLotBudgetMode} {
		t.Run(mode, func(t *testing.T) {
			testSignedHoldLotBudget(t, mode, heldDELPenaltyPassed)
		})
	}
}

func testSignedHoldLotBudget(t *testing.T, mode, passed string) {
	t.Helper()
	output := testSignedDELFixture(t, mode, passed)
	const prefix = "DEL hold penalty gas lots="
	_, measurement, found := strings.Cut(output, prefix)
	require.True(t, found, "isolated fixture did not report hold lot gas")
	line, _, _ := strings.Cut(measurement, "\n")
	t.Log(prefix + strings.TrimSpace(line))
}

func fundSignedDELPenalty(t *testing.T, f signedDELFixture, executor *settlement.Executor, value sdkmath.Int, deadline int64) common.Hash {
	e, gateway := f.env, f.gateway
	keeper := e.app.NativeValidatorKeeper
	t.Helper()
	method := "delegateDEL"
	args := []interface{}{f.validator}
	if deadline != 0 {
		method = "delegateHoldDEL"
		args = append(args, big.NewInt(deadline))
	}
	data, err := gateway.ABI.Pack(method, args...)
	require.NoError(t, err)
	result := f.signed(method, value, true, args...)
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

func runSignedHeldDELPenalty(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, gateway, master, wrapper contracts.Artifact,
	reader *nftview.Reader, gov func(contracts.Artifact, common.Address, string, ...interface{}), mode string,
) {
	t.Helper()
	f := prepareSignedDELFixture(t, e, profile, gateway, master, wrapper, gov)
	f.declareValidator()
	executor, err := settlement.NewExecutor(e.app.EVMKeeper, reader, settlement.Config{EVMGas: 750_000, MaxLogs: 32, MaxLogBytes: 16384})
	require.NoError(t, err)
	keeper := e.app.NativeValidatorKeeper
	del := func(n int64) sdkmath.Int { return sdkmath.NewIntWithDecimal(n, 18) }
	mixed := mode == mixedLotBudgetMode
	lotCount, extraLots := 3, 0
	if mode != heldPenaltyMode {
		lotCount = benchmarkHoldLots
		extraLots = lotCount - 3
		if mixed {
			extraLots++
		}
	}
	firstDeadline := e.ctx().BlockTime().Unix() + int64(extraLots) + 12
	secondDeadline := firstDeadline + 200
	if mixed {
		secondDeadline = 0
	}
	firstID := fundSignedDELPenalty(t, f, executor, del(3), firstDeadline)
	require.Equal(t, firstID, fundSignedDELPenalty(t, f, executor, del(2), firstDeadline))
	// Five atoms incur an exact one-atom loss at 20% and keep each lot alive.
	extraAmount := sdkmath.NewInt(5)
	for range extraLots {
		require.Equal(t, firstID, fundSignedDELPenalty(t, f, executor, extraAmount, firstDeadline))
	}
	firstAmount := del(5).Add(extraAmount.MulRaw(int64(extraLots)))
	total := firstAmount.Add(del(7))
	secondID := fundSignedDELPenalty(t, f, executor, del(7), secondDeadline)
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
	require.Len(t, beforeLots.Holds, lotCount)
	f.activeReserve(total)
	f.backing(total)

	e.app.EVMKeeper.SetInternalCommitGuard(func(ctx sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if executor.AuthorizeMasterPenalty(ctx, msg) {
			return nil
		}
		if executor.AuthorizeHeldDELPenalty(ctx, msg) || (mixed && executor.AuthorizeOrdinaryDELPenalty(ctx, msg)) {
			return nil
		}
		return errors.New("test-only internal call lacks exact DEL penalty authorization")
	})
	const attemptGas = uint64(24_000_000)
	ctx := func() sdk.Context {
		return e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(attemptGas))
	}
	supply := func(ctx sdk.Context) sdkmath.Int {
		return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount
	}
	initialSupply := supply(ctx())
	const percentage = uint16(20)
	injected := errors.New("reject held penalty projection after writes")
	reconcile := func(reject bool) error {
		execution := ctx()
		err := executor.AppendMasterPenalty(execution, f.validator, percentage, supply, func(child sdk.Context, announcement settlement.MasterPenaltyProof) error {
			wrong := first
			wrong.NativeHoldStartTime++
			err := executor.ApplyHeldDELPenalty(child, wrong, percentage, supply, func(sdk.Context, settlement.ActiveDELPenaltyProof) error {
				return errors.New("invalid hold time reached the projector")
			})
			require.ErrorContains(t, err, "exact valid hold start and deadline")
			for _, row := range []validatortypes.EVMStakeBinding{first, second} {
				execute, project := executor.ApplyHeldDELPenalty, keeper.ApplyGatewayDELHoldPenalty
				if row.NativeTarget.StakeType == validatortypes.StakeType_Coin {
					execute, project = executor.ApplyOrdinaryDELPenalty, keeper.ApplyGatewayDELPenalty
				}
				err := execute(child, row, percentage, supply, func(projectCtx sdk.Context, proof settlement.ActiveDELPenaltyProof) error {
					require.Equal(t, row, proof.Before)
					require.Equal(t, row.HoldTimestamp, proof.After.HoldTimestamp)
					require.Equal(t, row.RawHoldStartTime, proof.After.RawHoldStartTime)
					require.Equal(t, announcement.AfterCursor, common.BytesToHash(proof.After.PenaltyCursor))
					require.True(t, row.RawAmount.QuoRaw(5).Equal(proof.Burned))
					require.NoError(t, project(projectCtx, validatorkeeper.GatewayDELStake{
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
		if err == nil {
			t.Logf("DEL hold penalty gas lots=%d sdk_gas=%d budget=%d mixed=%t", lotCount, execution.GasMeter().GasConsumed(), attemptGas, mixed)
		}
		return err
	}
	require.ErrorIs(t, reconcile(true), injected)
	require.Equal(t, first, binding(e.ctx(), firstID))
	require.Equal(t, second, binding(e.ctx(), secondID))
	require.Equal(t, beforeLots, held(e.ctx()))
	require.True(t, initialSupply.Equal(supply(ctx())))
	f.backing(total)

	require.NoError(t, reconcile(false))
	afterLots := held(e.ctx())
	require.Len(t, afterLots.Holds, len(beforeLots.Holds))
	for i, before := range beforeLots.Holds {
		after := afterLots.Holds[i]
		require.Equal(t, before.HoldStartTime, after.HoldStartTime)
		require.Equal(t, before.HoldEndTime, after.HoldEndTime)
		require.True(t, before.Amount.MulRaw(4).QuoRaw(5).Equal(after.Amount))
	}
	expected := total.MulRaw(4).QuoRaw(5)
	f.activeReserve(expected)
	f.backing(expected)
	require.True(t, initialSupply.Sub(total.QuoRaw(5)).Equal(supply(ctx())))
	penalized := binding(e.ctx(), firstID)
	for e.ctx().BlockTime().Unix() <= firstDeadline {
		e.advance()
	}
	amount := del(2)
	args := []interface{}{f.validator, f.wrapped, amount.BigInt(), big.NewInt(firstDeadline)}
	data, err := gateway.ABI.Pack("withdrawHold", args...)
	require.NoError(t, err)
	response := f.signed("withdrawHold", sdkmath.ZeroInt(), true, args...)
	receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(response.Logs)}
	msg := core.Message{From: f.user, To: &f.target, Value: new(big.Int), Data: data}
	inclusion := ctx().WithBlockHeight(e.app.LastBlockHeight()).WithBlockTime(e.ctx().BlockTime().Add(-time.Second))
	out, err := executor.VerifySignedPenalizedDELWithdrawal(inclusion, msg, receipt, penalized)
	require.NoError(t, err)
	require.Equal(t, firstID, out.StakeID)
	require.Equal(t, firstDeadline, out.HoldDeadline)
	require.Equal(t, penalized.NativeHoldStartTime, out.HoldStartTime)
	require.True(t, firstAmount.MulRaw(4).QuoRaw(5).Sub(amount).Equal(out.Remaining))
	require.NoError(t, keeper.ApplyPenalizedGatewayDELWithdrawal(inclusion, validatorkeeper.GatewayDELStake{
		Gateway: f.target, WrappedDEL: f.wrapped, Validator: f.validator, Beneficiary: f.user,
		StakeID: out.StakeID, Amount: out.Remaining, PenaltyCursor: out.PenaltyCursor,
		HoldDeadline: out.HoldDeadline, HoldStartTime: out.HoldStartTime,
	}, out.FrozenIndex, out.Amount, out.Deadline))
	frozen, err := keeper.GetEVMFrozenBinding(e.ctx(), f.target[:], out.FrozenIndex[:])
	require.NoError(t, err)
	require.Equal(t, validatortypes.StakeType_CoinHold, frozen.NativeTarget.StakeType)
	require.True(t, amount.Equal(frozen.RawAmount))
	active, err := keeper.GetActiveDELReserve(e.ctx(), f.operator)
	require.NoError(t, err)
	require.True(t, expected.Sub(amount).Equal(active))
	f.backing(expected)
	require.True(t, initialSupply.Sub(total.QuoRaw(5)).Equal(supply(ctx())), "withdrawHold must not burn extra DEL")
	t.Log(heldDELPenaltyPassed)
}
