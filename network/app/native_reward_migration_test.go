package app

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

type nativeRewardMigrationCheckpoint struct {
	rows                 [][]byte
	owed, escrow, supply sdkmath.Int
	owners               []sdk.AccAddress
	balances             []sdkmath.Int
}

func nativeRewardMigrationRows(t *testing.T, app *App, ctx sdk.Context) [][]byte {
	t.Helper()
	it := ctx.KVStore(app.keys[validatortypes.StoreKey]).Iterator([]byte{0x73}, []byte{0x80})
	var rows [][]byte
	for ; it.Valid(); it.Next() {
		require.NoError(t, it.Error())
		rows = append(rows, append([]byte(nil), it.Key()...), append([]byte(nil), it.Value()...))
	}
	require.NoError(t, it.Close())
	return rows
}

func nativeRewardMigrationPay(app *App) func(sdk.Context, sdk.AccAddress, sdkmath.Int) error {
	return func(ctx sdk.Context, owner sdk.AccAddress, n sdkmath.Int) error {
		return app.BankKeeper.SendCoinsFromModuleToAccount(ctx, nativeFeeRewardEscrowModule, owner, sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, n)))
	}
}

func prepareNativeRewardMigrationCheckpoint(t *testing.T, app *App, ctx sdk.Context, proposer, voter sdk.AccAddress) nativeRewardMigrationCheckpoint {
	t.Helper()
	validators, err := app.NativeValidatorKeeper.GetValidators(ctx)
	require.NoError(t, err)
	require.Len(t, validators, 1)
	funds := sdk.NewCoins(sdk.NewInt64Coin(identity.BaseDenom, 1000))
	require.NoError(t, app.BankKeeper.SendCoinsFromAccountToModule(ctx, proposer, nativeFeeRewardEscrowModule, funds))
	require.NoError(t, app.NativeValidatorKeeper.AccrueNativeFeeRewards(ctx.WithBlockHeight(119), sdkmath.NewInt(1000), []validatorkeeper.NativeFeeRewardVote{{Operator: validators[0].OperatorAddress, Power: 10}}))
	partial, err := app.NativeValidatorKeeper.ProcessNativeFeeRewards(ctx.WithBlockHeight(120), 120, 2, nativeRewardMigrationPay(app))
	require.NoError(t, err)
	require.True(t, partial.Active)
	require.Equal(t, 1, partial.Paid)
	require.True(t, partial.Amount.IsPositive())
	p := nativeRewardMigrationCheckpoint{rows: nativeRewardMigrationRows(t, app, ctx), owners: []sdk.AccAddress{proposer, voter}}
	p.owed, err = app.NativeValidatorKeeper.AggregateNativeFeeRewardLiability(ctx)
	require.NoError(t, err)
	require.True(t, p.owed.IsPositive())
	require.True(t, p.owed.LT(sdkmath.NewInt(1000)))
	p.escrow = app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(nativeFeeRewardEscrowModule), identity.BaseDenom).Amount
	require.True(t, p.owed.Equal(p.escrow))
	p.supply = app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount
	for _, owner := range p.owners {
		p.balances = append(p.balances, app.BankKeeper.GetBalance(ctx, owner, identity.BaseDenom).Amount)
	}
	return p
}

func (p nativeRewardMigrationCheckpoint) assertUnchanged(t *testing.T, app *App, ctx sdk.Context) {
	t.Helper()
	require.Equal(t, p.rows, nativeRewardMigrationRows(t, app, ctx), "module migration must retain credits, compressed tree, payout cursor and recipient ownership byte-for-byte")
	owed, err := app.NativeValidatorKeeper.AggregateNativeFeeRewardLiability(ctx)
	require.NoError(t, err)
	require.True(t, p.owed.Equal(owed))
	require.True(t, p.escrow.Equal(app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(nativeFeeRewardEscrowModule), identity.BaseDenom).Amount))
	require.True(t, p.supply.Equal(app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount))
	for i, owner := range p.owners {
		require.True(t, p.balances[i].Equal(app.BankKeeper.GetBalance(ctx, owner, identity.BaseDenom).Amount))
	}
}

func (p nativeRewardMigrationCheckpoint) drainOnce(t *testing.T, app *App, ctx sdk.Context) {
	t.Helper()
	total := sdkmath.ZeroInt()
	for i := 0; i < 10; i++ {
		stats, err := app.NativeValidatorKeeper.ProcessNativeFeeRewards(ctx, ctx.BlockHeight(), 64, nativeRewardMigrationPay(app))
		require.NoError(t, err)
		total = total.Add(stats.Amount)
		if !stats.Active {
			break
		}
		require.Less(t, i, 9)
	}
	require.True(t, p.owed.Equal(total))
	owed, err := app.NativeValidatorKeeper.AggregateNativeFeeRewardLiability(ctx)
	require.NoError(t, err)
	require.True(t, owed.IsZero())
	require.True(t, app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(nativeFeeRewardEscrowModule), identity.BaseDenom).IsZero())
	require.True(t, p.supply.Equal(app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount))
	before := sdkmath.ZeroInt()
	for _, owner := range p.owners {
		before = before.Add(app.BankKeeper.GetBalance(ctx, owner, identity.BaseDenom).Amount)
	}
	stats, err := app.NativeValidatorKeeper.ProcessNativeFeeRewards(ctx, ctx.BlockHeight()+120, 64, nativeRewardMigrationPay(app))
	require.NoError(t, err)
	require.True(t, stats.Amount.IsZero())
	after := sdkmath.ZeroInt()
	for _, owner := range p.owners {
		after = after.Add(app.BankKeeper.GetBalance(ctx, owner, identity.BaseDenom).Amount)
	}
	require.True(t, before.Equal(after), "reopening a sweep after migration cannot pay the same DEL twice")
}
