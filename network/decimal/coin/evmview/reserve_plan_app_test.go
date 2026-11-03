package evmview_test

import (
	"errors"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/evmview"
	coinkeeper "github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/nativeexec"
	"github.com/stretchr/testify/require"
)

// Reuse the package's only actual App and its genuinely deployed approved DRC20.
// Metadata installation is an explicit discarded fixture, not genesis admission.
func checkPreparedReservePlan(t *testing.T, app *ownedapp.App, parent sdk.Context, metadata cointypes.Coin, binding *fixtureBinding, compiled abi.ABI, actual cointypes.Coin, funded sdk.AccAddress) {
	t.Helper()
	ctx, _ := parent.CacheContext()
	require.NoError(t, app.NativeCoinKeeper.SetCoin(ctx, metadata))
	reader, err := evmview.New(app.NativeCoinKeeper, app.BankKeeper, app.EVMKeeper, binding, compiled, 200_000, 2_000_000)
	require.NoError(t, err)
	reads := 0
	hook, err := coinkeeper.NewReserveHook(app.NativeCoinKeeper, compiled, func(read sdk.Context, address common.Address) (sdkmath.Int, sdkmath.Int, error) {
		reads++
		require.Equal(t, common.HexToAddress(metadata.DRC20Contract), address)
		coin, err := reader.GetCoin(read, metadata.Denom)
		return coin.Volume, coin.Reserve, err
	}, 4)
	require.NoError(t, err)
	event := compiled.Events["ReserveUpdated"]
	data, err := event.Inputs.Pack(actual.Reserve.BigInt(), actual.Volume.BigInt(), sdkmath.OneInt().BigInt())
	require.NoError(t, err)
	receipt := &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{{Address: common.HexToAddress(metadata.DRC20Contract), Topics: []common.Hash{event.ID}, Data: data}}}
	gasBeforePrepare := ctx.GasMeter().GasConsumed()
	plan, err := hook.Prepare(ctx, receipt)
	require.NoError(t, err)
	gasBeforeValidate := ctx.GasMeter().GasConsumed()
	require.NoError(t, plan.Validate(ctx))
	gasBeforeApply := ctx.GasMeter().GasConsumed()
	require.NoError(t, plan.Apply(ctx))
	require.Greater(t, ctx.GasMeter().GasConsumed(), gasBeforeApply, "fresh real EVM/SDK reads consume the inherited meter")
	t.Logf("registered coin reserve plan SDK gas: prepare=%d validate=%d apply=%d total=%d",
		gasBeforeValidate-gasBeforePrepare, gasBeforeApply-gasBeforeValidate,
		ctx.GasMeter().GasConsumed()-gasBeforeApply, ctx.GasMeter().GasConsumed()-gasBeforePrepare)
	require.Equal(t, 3, reads)
	updated, err := app.NativeCoinKeeper.GetCoin(ctx, metadata.Denom)
	require.NoError(t, err)
	require.True(t, actual.Volume.Equal(updated.Volume))
	require.True(t, actual.Reserve.Equal(updated.Reserve))
	require.Len(t, ctx.EventManager().Events(), 1)
	for _, applyAfterValidate := range []bool{false, true} {
		stale, _ := parent.CacheContext()
		require.NoError(t, app.NativeCoinKeeper.SetCoin(stale, metadata))
		plan, err := hook.Prepare(stale, receipt)
		require.NoError(t, err)
		if applyAfterValidate {
			require.NoError(t, plan.Validate(stale))
		}
		// A normal bank credit changes the EVM-visible native balance without a
		// token event, height/transaction change or metadata modification.
		require.NoError(t, app.BankKeeper.SendCoins(stale, funded, sdk.AccAddress(common.HexToAddress(metadata.DRC20Contract).Bytes()), sdk.NewCoins(sdk.NewInt64Coin("del", 1))))
		beforeEvents := append(sdk.Events(nil), stale.EventManager().Events()...)
		if !applyAfterValidate {
			require.ErrorContains(t, plan.Validate(stale), "differs from final")
		}
		require.ErrorContains(t, plan.Apply(stale), "differs from final")
		unchanged, err := app.NativeCoinKeeper.GetCoin(stale, metadata.Denom)
		require.NoError(t, err)
		require.Equal(t, metadata, unchanged)
		require.Equal(t, beforeEvents, stale.EventManager().Events(), "failed apply cannot emit a coin update")
	}
	_, err = app.NativeCoinKeeper.GetCoin(parent, metadata.Denom)
	require.ErrorIs(t, err, coinkeeper.ErrNotFound, "fixture metadata and updates must remain in discarded children")
}

// These later stages are explicit composition probes, not NFT/validator receipt
// admission. Only the reserve stage uses the real keeper and EVM/bank reader.
type reserveCompositionStage struct {
	validate func(sdk.Context) error
	apply    func(sdk.Context) error
}

func (p reserveCompositionStage) Validate(ctx sdk.Context) error { return p.validate(ctx) }
func (p reserveCompositionStage) Apply(ctx sdk.Context) error    { return p.apply(ctx) }

type reserveCompositionPrepare func(sdk.Context, nativeexec.Execution) (nativeexec.Prepared, error)

func (f reserveCompositionPrepare) Prepare(ctx sdk.Context, e nativeexec.Execution) (nativeexec.Prepared, error) {
	return f(ctx, e)
}

type reserveCompositionFinal func(sdk.Context, nativeexec.Execution) error

func (f reserveCompositionFinal) ValidateFinal(ctx sdk.Context, e nativeexec.Execution) error {
	return f(ctx, e)
}

func checkCoordinatedReserveAdapter(t *testing.T, app *ownedapp.App, parent sdk.Context, metadata cointypes.Coin, binding *fixtureBinding, compiled abi.ABI, actual cointypes.Coin, receipt *ethtypes.Receipt, owner common.Address) {
	t.Helper()
	for _, mode := range []string{"commit", "late apply failure", "final failure", "backing mismatch"} {
		t.Run(mode, func(t *testing.T) {
			ctx, _ := parent.CacheContext()
			require.NoError(t, app.NativeCoinKeeper.SetCoin(ctx, metadata))
			reader, err := evmview.New(app.NativeCoinKeeper, app.BankKeeper, app.EVMKeeper, binding, compiled, 200_000, 2_000_000)
			require.NoError(t, err)
			reads := 0
			hook, err := coinkeeper.NewReserveHook(app.NativeCoinKeeper, compiled, func(read sdk.Context, address common.Address) (sdkmath.Int, sdkmath.Int, error) {
				reads++
				require.Equal(t, common.HexToAddress(metadata.DRC20Contract), address)
				coin, err := reader.GetCoin(read, metadata.Denom)
				return coin.Volume, coin.Reserve, err
			}, 32)
			require.NoError(t, err)
			assets, err := nativeexec.NewReservePreparer(hook)
			require.NoError(t, err)
			if mode == "backing mismatch" {
				require.NoError(t, app.BankKeeper.SendCoins(ctx, sdk.AccAddress(owner.Bytes()), sdk.AccAddress(common.HexToAddress(metadata.DRC20Contract).Bytes()), sdk.NewCoins(sdk.NewInt64Coin("del", 1))))
			}
			beforeEvents := append(sdk.Events(nil), ctx.EventManager().Events()...)
			balance := app.BankKeeper.GetBalance(ctx, sdk.AccAddress(owner.Bytes()), "del")
			nativeSupply := app.BankKeeper.GetSupply(ctx, "del")
			applied := 0
			stage := reserveCompositionPrepare(func(sdk.Context, nativeexec.Execution) (nativeexec.Prepared, error) {
				return reserveCompositionStage{validate: func(c sdk.Context) error {
					old, err := app.NativeCoinKeeper.GetCoin(c, metadata.Denom)
					require.NoError(t, err)
					require.Equal(t, metadata, old)
					return nil
				}, apply: func(c sdk.Context) error {
					updated, err := app.NativeCoinKeeper.GetCoin(c, metadata.Denom)
					require.NoError(t, err)
					require.Equal(t, actual.Reserve, updated.Reserve)
					applied++
					c.EventManager().EmitEvent(sdk.NewEvent("composition_probe"))
					if mode == "late apply failure" && applied == 2 {
						return errors.New("composition late error")
					}
					return nil
				}}, nil
			})
			final := reserveCompositionFinal(func(sdk.Context, nativeexec.Execution) error {
				if mode == "final failure" {
					return errors.New("composition final error")
				}
				return nil
			})
			coordinator, err := nativeexec.New(assets, stage, stage, final, nativeexec.Limits{MaxLogs: 32, MaxBytes: 16384, MaxCopyWeight: 65536})
			require.NoError(t, err)
			err = coordinator.Process(ctx, nativeexec.Execution{EntryPoint: nativeexec.SignedEthereum, Sender: owner, Message: core.Message{From: owner, GasLimit: 8_000_000}, Receipt: receipt})
			updated, readErr := app.NativeCoinKeeper.GetCoin(ctx, metadata.Denom)
			require.NoError(t, readErr)
			if mode == "commit" {
				require.NoError(t, err)
				require.Equal(t, actual.Volume, updated.Volume)
				require.Equal(t, actual.Reserve, updated.Reserve)
				require.Len(t, ctx.EventManager().Events(), len(beforeEvents)+3)
			} else {
				require.Error(t, err)
				require.Equal(t, metadata, updated)
				require.Equal(t, beforeEvents, ctx.EventManager().Events())
			}
			if mode == "backing mismatch" {
				require.Zero(t, applied)
				require.Equal(t, 1, reads)
				require.ErrorContains(t, err, "differs from final")
			} else {
				require.Equal(t, 3, reads, "all phases use real same-context EVM supply and bank DEL backing")
			}
			require.Equal(t, balance, app.BankKeeper.GetBalance(ctx, sdk.AccAddress(owner.Bytes()), "del"))
			require.Equal(t, nativeSupply, app.BankKeeper.GetSupply(ctx, "del"), "metadata projection cannot mint or burn bank DEL")
		})
	}
	_, err := app.NativeCoinKeeper.GetCoin(parent, metadata.Denom)
	require.ErrorIs(t, err, coinkeeper.ErrNotFound, "all admission and updates remain in discarded fixture caches")
}
