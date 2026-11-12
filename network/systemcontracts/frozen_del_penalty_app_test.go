package systemcontracts_test

import (
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

const (
	frozenDELPenaltyPassed  = "frozen DEL checkpoint, penalty burn and final claim settlement passed"
	frozenHeldLotBudgetMode = "frozen-held-penalty-lot-budget"
)

func TestOwnedAppSignedFrozenDELPenalty(t *testing.T) {
	for _, mode := range []string{"frozen-penalty-partial", "frozen-penalty-full", "frozen-held-penalty", "frozen-held-penalty-full"} {
		t.Run(mode, func(t *testing.T) { testSignedDELFixture(t, mode, frozenDELPenaltyPassed) })
	}
	t.Run(frozenHeldLotBudgetMode, func(t *testing.T) {
		testSignedHoldLotBudget(t, frozenHeldLotBudgetMode, frozenDELPenaltyPassed)
	})
}

func runSignedFrozenDELPenalty(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, gateway, master, wrapper contracts.Artifact,
	reader *nftview.Reader, gov func(contracts.Artifact, common.Address, string, ...interface{}), full, held, benchmark bool,
) {
	t.Helper()
	f := prepareSignedDELFixture(t, e, profile, gateway, master, wrapper, gov)
	f.declareValidator()
	executor, err := settlement.NewExecutor(e.app.EVMKeeper, reader, settlement.Config{EVMGas: 750_000, MaxLogs: 32, MaxLogBytes: 16384})
	require.NoError(t, err)
	keeper := e.app.NativeValidatorKeeper
	amount := func(n int64) sdkmath.Int { return sdkmath.NewIntWithDecimal(n, 18) }
	inclusion := func() sdk.Context {
		// The fixture read context otherwise points to the next block.
		return e.ctx().WithBlockHeight(e.app.LastBlockHeight()).WithBlockTime(e.ctx().BlockTime().Add(-time.Second))
	}
	signed := func(method string, value sdkmath.Int, args ...interface{}) settlement.DELTransition {
		t.Helper()
		data, err := gateway.ABI.Pack(method, args...)
		require.NoError(t, err)
		result := f.signed(method, value, true, args...)
		msg := core.Message{From: f.user, To: &f.target, Value: value.BigInt(), Data: data}
		receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(result.Logs)}
		proof, err := executor.VerifySignedDEL(inclusion(), msg, receipt)
		require.NoError(t, err)
		return proof
	}
	position := func(proof settlement.DELTransition) validatorkeeper.GatewayDELStake {
		return validatorkeeper.GatewayDELStake{
			Gateway: f.target, WrappedDEL: f.wrapped, Validator: f.validator, Beneficiary: f.user,
			StakeID: proof.StakeID, Amount: proof.Remaining, HoldStartTime: proof.HoldStartTime, HoldDeadline: proof.HoldDeadline,
		}
	}
	oldAmount, eligibleAmount := int64(4), int64(6)
	deposits := []int64{3, 7}
	if benchmark {
		oldAmount, eligibleAmount = 1, benchmarkHoldLots-1
		deposits = make([]int64, benchmarkHoldLots)
		for i := range deposits {
			deposits[i] = 1
		}
	}
	total := amount(oldAmount + eligibleAmount)
	var holdDeadline, holdStart int64
	if held {
		holdDeadline = e.ctx().BlockTime().Unix() + int64(len(deposits)) + 2
		for _, quantity := range deposits {
			transition := signed("delegateHoldDEL", amount(quantity), f.validator, big.NewInt(holdDeadline))
			if holdStart == 0 {
				holdStart = transition.HoldStartTime
			}
			require.Equal(t, holdStart, transition.HoldStartTime)
			require.NoError(t, keeper.ApplyGatewayDELDelegation(inclusion(), position(transition), transition.Amount))
		}
		for e.ctx().BlockTime().Unix() <= holdDeadline {
			e.advance()
		}
	} else {
		funding := signed("delegateDEL", total, f.validator)
		require.NoError(t, keeper.ApplyGatewayDELDelegation(inclusion(), position(funding), funding.Amount))
	}
	frozen := func(request settlement.DELTransition) validatortypes.EVMFrozenBinding {
		row, err := keeper.GetEVMFrozenBinding(e.ctx(), f.target[:], request.FrozenIndex[:])
		require.NoError(t, err)
		return row
	}
	withdraw := func(n int64) settlement.DELTransition {
		t.Helper()
		method := "withdraw"
		args := []interface{}{f.validator, f.wrapped, amount(n).BigInt()}
		if held {
			method = "withdrawHold"
			args = append(args, big.NewInt(holdDeadline))
		}
		out := signed(method, sdkmath.ZeroInt(), args...)
		require.Equal(t, holdStart, out.HoldStartTime)
		require.Equal(t, holdDeadline, out.HoldDeadline)
		require.NoError(t, keeper.ApplyGatewayDELWithdrawal(inclusion(), position(out), out.FrozenIndex, out.Amount, out.Deadline))
		return out
	}
	oldRequest, eligibleRequest := withdraw(oldAmount), withdraw(eligibleAmount)
	oldRow, eligibleRow := frozen(oldRequest), frozen(eligibleRequest)
	require.Equal(t, oldRow.CreatedHeight+1, eligibleRow.CreatedHeight)
	f.activeReserve(sdkmath.ZeroInt())
	f.backing(total)
	percent, wantLoss := uint16(50), amount(eligibleAmount).QuoRaw(2)
	if full {
		percent, wantLoss = 100, amount(eligibleAmount)
	}
	infraction := eligibleRow.CreatedHeight
	oldRemaining := oldRow.RawAmount
	if benchmark {
		infraction = oldRow.CreatedHeight
		oldRemaining = oldRemaining.QuoRaw(2)
		pending, err := keeper.GetUndelegation(e.ctx(), f.beneficiary, f.operator)
		require.NoError(t, err)
		require.Len(t, pending.Entries, 2)
		require.Len(t, pending.Entries[0].Stake.Holds, 1)
		require.Len(t, pending.Entries[1].Stake.Holds, benchmarkHoldLots-1)
	}
	totalLoss := oldRow.RawAmount.Sub(oldRemaining).Add(wantLoss)
	f.signed("applyFrozenDELPenalty", sdkmath.ZeroInt(), false, eligibleRequest.FrozenIndex.Big(),
		big.NewInt(eligibleRow.CreatedHeight), eligibleRow.RawAmount.BigInt(), new(big.Int))
	e.app.EVMKeeper.SetInternalCommitGuard(func(ctx sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if !executor.AuthorizeMasterPenalty(ctx, msg) && !executor.AuthorizeFrozenDELPenalty(ctx, msg) {
			return errors.New("test-only internal call lacks exact frozen penalty authorization")
		}
		return nil
	})
	const attemptGas = uint64(24_000_000)
	ctx := func() sdk.Context {
		return e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(attemptGas))
	}
	supply := func(ctx sdk.Context) sdkmath.Int {
		return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount
	}
	initialSupply := supply(ctx())
	count := func() int64 {
		return f.read(master, profile.MasterValidator, "getPenaltyCount", f.validator).(*big.Int).Int64()
	}
	rejected := errors.New("reject frozen queue projection after writes")
	reconcile := func(fail bool) error {
		execution := ctx()
		err := executor.AppendMasterPenalty(execution, f.validator, percent, supply, func(child sdk.Context, announcement settlement.MasterPenaltyProof) error {
			for _, row := range []validatortypes.EVMFrozenBinding{oldRow, eligibleRow} {
				isEligible := row.CreatedHeight >= infraction
				err := executor.ApplyFrozenDELPenalty(child, row, infraction, percent, supply,
					func(projectCtx sdk.Context, proof settlement.FrozenDELPenaltyProof) error {
						require.Equal(t, isEligible, proof.Eligible)
						require.Equal(t, announcement.BeforeCursor, proof.BeforeCursor)
						require.Equal(t, announcement.AfterCursor, proof.AfterCursor)
						require.Equal(t, holdStart, proof.HoldStartTime)
						require.Equal(t, holdDeadline, proof.HoldDeadline)
						expectedLoss := sdkmath.ZeroInt()
						if isEligible {
							expectedLoss = row.RawAmount.MulRaw(int64(percent)).QuoRaw(100)
						}
						require.True(t, expectedLoss.Equal(proof.Burned))
						var err error
						if held {
							err = keeper.ApplyGatewayFrozenDELHoldPenalty(projectCtx, row, validatorkeeper.GatewayFrozenDELHold{
								WrappedDEL: f.wrapped, HoldStartTime: proof.HoldStartTime, HoldDeadline: proof.HoldDeadline,
							}, proof.InfractionHeight, proof.BeforeCursor, proof.AfterCursor, proof.Remaining)
						} else {
							err = keeper.ApplyGatewayFrozenDELPenalty(projectCtx, row, proof.InfractionHeight,
								proof.BeforeCursor, proof.AfterCursor, proof.Remaining)
						}
						if err != nil {
							return err
						}
						if row.CreatedHeight == eligibleRow.CreatedHeight && fail {
							return rejected
						}
						return nil
					})
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil && benchmark {
			t.Logf("DEL hold penalty gas lots=%d sdk_gas=%d budget=%d frozen=true",
				benchmarkHoldLots, execution.GasMeter().GasConsumed(), attemptGas)
		}
		return err
	}
	require.ErrorIs(t, reconcile(true), rejected)
	require.Zero(t, count())
	require.Equal(t, oldRow, frozen(oldRequest))
	require.Equal(t, eligibleRow, frozen(eligibleRequest))
	require.True(t, initialSupply.Equal(supply(ctx())))
	f.backing(total)

	require.NoError(t, reconcile(false))
	require.Equal(t, int64(1), count())
	require.True(t, oldRemaining.Equal(frozen(oldRequest).RawAmount))
	after := frozen(eligibleRequest)
	require.True(t, eligibleRow.RawAmount.Sub(wantLoss).Equal(after.RawAmount))
	require.Equal(t, eligibleRow.CompletionTimestamp, after.CompletionTimestamp)
	require.Equal(t, !full, after.Active)
	f.backing(total.Sub(totalLoss))
	f.activeReserve(sdkmath.ZeroInt())
	require.True(t, initialSupply.Sub(totalLoss).Equal(supply(ctx())))

	for e.ctx().BlockTime().Before(eligibleRequest.Deadline) {
		e.advance()
	}
	if full {
		f.signed("complete", sdkmath.ZeroInt(), false, []*big.Int{eligibleRequest.FrozenIndex.Big()})
		require.True(t, frozen(eligibleRequest).RawAmount.IsZero())
		f.backing(oldRemaining)
	} else {
		err := executor.ApplyFrozenDELPenalty(ctx(), after, eligibleRow.CreatedHeight, percent, supply,
			func(sdk.Context, settlement.FrozenDELPenaltyProof) error {
				return errors.New("mature claim reached penalty projection")
			})
		require.ErrorContains(t, err, "unmatured frozen DEL binding")
		complete := func(request settlement.DELTransition, claim validatortypes.EVMFrozenBinding) {
			t.Helper()
			e.credit.Set(claim.RawAmount.BigInt())
			response := f.signed("complete", sdkmath.ZeroInt(), true, []*big.Int{request.FrozenIndex.Big()})
			receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(response.Logs)}
			_, err := executor.ValidateSigned(e.ctx(), claim, receipt)
			require.NoError(t, err)
			require.NoError(t, keeper.ApplyWithdrawalCompletion(e.ctx(), claim))
			require.False(t, frozen(request).Active)
		}
		complete(eligibleRequest, after)
		f.backing(oldRemaining)
		if benchmark {
			complete(oldRequest, frozen(oldRequest))
			f.backing(sdkmath.ZeroInt())
		}
	}
	require.True(t, initialSupply.Sub(totalLoss).Equal(supply(ctx())))
	t.Log(frozenDELPenaltyPassed)
}
