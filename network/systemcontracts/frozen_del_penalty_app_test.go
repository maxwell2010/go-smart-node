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

const frozenDELPenaltyPassed = "frozen DEL checkpoint, penalty burn and final claim settlement passed"

func TestOwnedAppSignedFrozenDELPenalty(t *testing.T) {
	for _, mode := range []string{"frozen-penalty-partial", "frozen-penalty-full", "frozen-held-penalty", "frozen-held-penalty-full"} {
		t.Run(mode, func(t *testing.T) { testSignedDELFixture(t, mode, frozenDELPenaltyPassed) })
	}
}

func runSignedFrozenDELPenalty(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, gateway, master, wrapper contracts.Artifact,
	reader *nftview.Reader, gov func(contracts.Artifact, common.Address, string, ...interface{}), full, held bool,
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
	var holdDeadline, holdStart int64
	if held {
		holdDeadline = e.ctx().BlockTime().Unix() + 4
		first := signed("delegateHoldDEL", amount(3), f.validator, big.NewInt(holdDeadline))
		require.NoError(t, keeper.ApplyGatewayDELDelegation(inclusion(), position(first), first.Amount))
		holdStart = first.HoldStartTime
		second := signed("delegateHoldDEL", amount(7), f.validator, big.NewInt(holdDeadline))
		require.Equal(t, holdStart, second.HoldStartTime)
		require.NoError(t, keeper.ApplyGatewayDELDelegation(inclusion(), position(second), second.Amount))
		for e.ctx().BlockTime().Unix() <= holdDeadline {
			e.advance()
		}
	} else {
		funding := signed("delegateDEL", amount(10), f.validator)
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
	oldRequest, eligibleRequest := withdraw(4), withdraw(6)
	oldRow, eligibleRow := frozen(oldRequest), frozen(eligibleRequest)
	require.Equal(t, oldRow.CreatedHeight+1, eligibleRow.CreatedHeight)
	f.activeReserve(sdkmath.ZeroInt())
	f.backing(amount(10))
	percent, wantLoss := uint16(50), amount(3)
	if full {
		percent, wantLoss = 100, amount(6)
	}
	f.signed("applyFrozenDELPenalty", sdkmath.ZeroInt(), false, eligibleRequest.FrozenIndex.Big(),
		big.NewInt(eligibleRow.CreatedHeight), eligibleRow.RawAmount.BigInt(), new(big.Int))
	e.app.EVMKeeper.SetInternalCommitGuard(func(ctx sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if !executor.AuthorizeMasterPenalty(ctx, msg) && !executor.AuthorizeFrozenDELPenalty(ctx, msg) {
			return errors.New("test-only internal call lacks exact frozen penalty authorization")
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
	count := func() int64 {
		return f.read(master, profile.MasterValidator, "getPenaltyCount", f.validator).(*big.Int).Int64()
	}
	rejected := errors.New("reject frozen queue projection after writes")
	reconcile := func(fail bool) error {
		return executor.AppendMasterPenalty(ctx(), f.validator, percent, supply, func(child sdk.Context, announcement settlement.MasterPenaltyProof) error {
			for _, row := range []validatortypes.EVMFrozenBinding{oldRow, eligibleRow} {
				isEligible := row.CreatedHeight == eligibleRow.CreatedHeight
				err := executor.ApplyFrozenDELPenalty(child, row, eligibleRow.CreatedHeight, percent, supply,
					func(projectCtx sdk.Context, proof settlement.FrozenDELPenaltyProof) error {
						require.Equal(t, isEligible, proof.Eligible)
						require.Equal(t, announcement.BeforeCursor, proof.BeforeCursor)
						require.Equal(t, announcement.AfterCursor, proof.AfterCursor)
						require.Equal(t, holdStart, proof.HoldStartTime)
						require.Equal(t, holdDeadline, proof.HoldDeadline)
						expectedLoss := sdkmath.ZeroInt()
						if isEligible {
							expectedLoss = wantLoss
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
						if isEligible && fail {
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
	}
	require.ErrorIs(t, reconcile(true), rejected)
	require.Zero(t, count())
	require.Equal(t, oldRow, frozen(oldRequest))
	require.Equal(t, eligibleRow, frozen(eligibleRequest))
	require.True(t, initialSupply.Equal(supply(ctx())))
	f.backing(amount(10))

	require.NoError(t, reconcile(false))
	require.Equal(t, int64(1), count())
	require.True(t, oldRow.RawAmount.Equal(frozen(oldRequest).RawAmount))
	after := frozen(eligibleRequest)
	require.True(t, eligibleRow.RawAmount.Sub(wantLoss).Equal(after.RawAmount))
	require.Equal(t, eligibleRow.CompletionTimestamp, after.CompletionTimestamp)
	require.Equal(t, !full, after.Active)
	f.backing(amount(10).Sub(wantLoss))
	f.activeReserve(sdkmath.ZeroInt())
	require.True(t, initialSupply.Sub(wantLoss).Equal(supply(ctx())))

	for e.ctx().BlockTime().Before(eligibleRequest.Deadline) {
		e.advance()
	}
	if full {
		f.signed("complete", sdkmath.ZeroInt(), false, []*big.Int{eligibleRequest.FrozenIndex.Big()})
		require.True(t, frozen(eligibleRequest).RawAmount.IsZero())
		f.backing(amount(4))
	} else {
		err := executor.ApplyFrozenDELPenalty(ctx(), after, eligibleRow.CreatedHeight, percent, supply,
			func(sdk.Context, settlement.FrozenDELPenaltyProof) error {
				return errors.New("mature claim reached penalty projection")
			})
		require.ErrorContains(t, err, "unmatured frozen DEL binding")
		e.credit.Set(after.RawAmount.BigInt())
		response := f.signed("complete", sdkmath.ZeroInt(), true, []*big.Int{eligibleRequest.FrozenIndex.Big()})
		receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(response.Logs)}
		_, err = executor.ValidateSigned(e.ctx(), after, receipt)
		require.NoError(t, err)
		require.NoError(t, keeper.ApplyWithdrawalCompletion(e.ctx(), after))
		require.False(t, frozen(eligibleRequest).Active)
		f.backing(amount(4))
	}
	require.True(t, initialSupply.Sub(wantLoss).Equal(supply(ctx())))
	t.Log(frozenDELPenaltyPassed)
}
