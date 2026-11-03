package evmview_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/evmview"
	coinkeeper "github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
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
