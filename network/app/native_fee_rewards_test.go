package app

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

// Exercise real bank keepers in a discarded cache of the initialized app.
// Test funding is minted once before the supply-invariance baseline.
func checkNativeFeeRewardEscrow(t *testing.T, original *App) {
	t.Helper()
	ctx := original.NewUncachedContext(false, cmtproto.Header{ChainID: original.chainID, Height: 119, Time: time.Unix(5000, 0)})
	ctx, _ = ctx.CacheContext()
	app := *original
	owner := sdk.AccAddress(make([]byte, 20))
	owner[18], owner[19] = 0x41, 1
	rawOperator := make([]byte, 20)
	rawOperator[18], rawOperator[19] = 0x41, 2
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(rawOperator)
	require.NoError(t, err)
	reward, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(owner)
	require.NoError(t, err)
	validator, err := validatortypes.NewValidator(operator, reward, ed25519.GenPrivKey().PubKey(), validatortypes.Description{Moniker: "reward-bank-regression"}, sdkmath.LegacyNewDecWithPrec(25, 2))
	require.NoError(t, err)
	require.NoError(t, app.NativeValidatorKeeper.SetValidator(ctx, validator))
	require.NoError(t, app.NativeValidatorKeeper.ApplyDelegationBatch(ctx, []validatorkeeper.DelegationChange{{Next: &validatortypes.Delegation{
		Delegator: reward, Validator: operator, Stake: validatortypes.NewStakeCoin(sdk.NewInt64Coin(identity.BaseDenom, 100)),
	}}}))
	app.withdrawals = &withdrawalController{keeper: app.NativeValidatorKeeper, delRecipientAllowed: func(a sdk.AccAddress) bool { return !app.BankKeeper.BlockedAddr(a) }}
	amount := sdk.NewCoins(sdk.NewInt64Coin(identity.BaseDenom, 1000))
	require.NoError(t, app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewInt64Coin(identity.BaseDenom, 2000))))
	require.NoError(t, app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, authtypes.FeeCollectorName, amount))
	collector := authtypes.NewModuleAddress(authtypes.FeeCollectorName)
	escrow := authtypes.NewModuleAddress(nativeFeeRewardEscrowModule)
	fees := app.BankKeeper.GetBalance(ctx, collector, identity.BaseDenom).Amount
	supply := app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount
	before := app.BankKeeper.GetBalance(ctx, owner, identity.BaseDenom).Amount
	commit := validatorkeeper.VerifiedABCICommit{Height: 118, Votes: []validatorkeeper.VerifiedABCIVote{{Operator: operator, Power: 1, Flag: cmtproto.BlockIDFlagCommit}}}
	require.NoError(t, app.distributeNativeFees(ctx, commit))
	require.True(t, app.BankKeeper.GetBalance(ctx, collector, identity.BaseDenom).IsZero())
	require.True(t, app.BankKeeper.GetBalance(ctx, escrow, identity.BaseDenom).Amount.Equal(fees))
	require.True(t, app.BankKeeper.GetBalance(ctx, owner, identity.BaseDenom).Amount.Equal(before), "no early payout before the automatic tick")
	liability, err := app.NativeValidatorKeeper.AggregateNativeFeeRewardLiability(ctx)
	require.NoError(t, err)
	require.True(t, liability.Equal(fees))
	ctx = ctx.WithBlockHeight(120)
	require.NoError(t, app.distributeNativeFees(ctx, commit))
	require.True(t, app.BankKeeper.GetBalance(ctx, owner, identity.BaseDenom).Amount.Equal(before.Add(fees)))
	require.True(t, app.BankKeeper.GetBalance(ctx, escrow, identity.BaseDenom).IsZero())
	require.True(t, app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount.Equal(supply), "reward payout must not mint DEL")
	require.NoError(t, app.distributeNativeFees(ctx, commit))
	require.True(t, app.BankKeeper.GetBalance(ctx, owner, identity.BaseDenom).Amount.Equal(before.Add(fees)), "repeated page must not pay twice")
	// Surplus cannot manufacture a liability or make the next block fail.
	require.NoError(t, app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, nativeFeeRewardEscrowModule, amount))
	require.NoError(t, app.checkNativeFeeRewardEscrow(ctx))
	liability, err = app.NativeValidatorKeeper.AggregateNativeFeeRewardLiability(ctx)
	require.NoError(t, err)
	require.True(t, liability.IsZero())
	require.True(t, app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount.Equal(supply))
}
