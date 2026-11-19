//go:build appstate

package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

// These checks run in the signed native lifecycle after canonical TokenCenter
// deployment, actual backed-curve genesis and actual reserveless creation.
// They call the real EVM STATICCALL quote adapter, never a substitute oracle.
func assertNativeReserveFeeQuotes(t *testing.T, l nativeHistoricalLifecycle, profile ownedapp.WithdrawalProfile, free common.Address) {
	t.Helper()
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(l.doc.AppState, &state))
	var genesis cointypes.GenesisState
	require.NoError(t, l.app.AppCodec().UnmarshalJSON(state[cointypes.ModuleName], &genesis))
	require.Len(t, genesis.Coins, 1)
	coin := genesis.Coins[0]
	token := common.HexToAddress(coin.DRC20Contract)
	unit := func(n int64) *big.Int { return sdkmath.NewIntWithDecimal(n, 18).BigInt() }
	deadline := big.NewInt(l.snapshot().BlockTime().Unix() + 300)
	actor := sdk.AccAddress(l.expect.Actor.Bytes())
	supplySlot := common.HexToHash("0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace02")
	pausedSlot := common.HexToHash("0xcd5ed15c6e187e77e9aee88184c21f4f2182ab5827cb3b7e07fbedcd63f03300")
	require.NotEqual(t, common.Address{}, profile.TokenCenter)
	for _, name := range []string{"canonical", "reserveless", "paused", "empty-reserve", "minimum-reserve", "minimum-supply", "slippage", "fee-budget", "expired"} {
		t.Run("fee-quote/"+name, func(t *testing.T) {
			ctx, _ := l.snapshot().CacheContext()
			input, minimum, budget, due, target := unit(1), unit(1), unit(1), new(big.Int).Set(deadline), token
			switch name {
			case "reserveless":
				target = free
			case "paused":
				l.app.EVMKeeper.SetState(ctx, profile.TokenCenter, pausedSlot, common.BigToHash(big.NewInt(1)).Bytes())
			case "empty-reserve", "minimum-reserve":
				amount := l.app.BankKeeper.GetBalance(ctx, sdk.AccAddress(token.Bytes()), identity.BaseDenom).Amount
				if name == "minimum-reserve" {
					amount = amount.Sub(sdkmath.NewIntWithDecimal(1, 18))
				}
				require.NoError(t, l.app.BankKeeper.SendCoins(ctx, sdk.AccAddress(token.Bytes()), actor, sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, amount))))
			case "minimum-supply":
				input = new(big.Int).Add(new(big.Int).Sub(coin.Volume.BigInt(), coin.MinVolume.BigInt()), big.NewInt(1))
			case "slippage":
				minimum = unit(2)
			case "fee-budget":
				budget = unit(2)
			case "expired":
				due = big.NewInt(ctx.BlockTime().Unix() - 1)
			}
			beforeSupply := l.app.BankKeeper.GetSupply(ctx, identity.BaseDenom)
			beforeReserve := l.app.BankKeeper.GetBalance(ctx, sdk.AccAddress(token.Bytes()), identity.BaseDenom)
			beforeActor := l.app.BankKeeper.GetBalance(ctx, actor, identity.BaseDenom)
			beforeTokenSupply := l.app.EVMKeeper.GetState(ctx, token, supplySlot)
			beforeNonce := l.app.EVMKeeper.GetNonce(ctx, l.expect.Actor)
			beforeEvents := len(ctx.EventManager().Events())
			ctx = ctx.WithGasMeter(storetypes.NewGasMeter(10_000_000))
			quote, err := l.app.QuoteDRC20Fee(ctx, target, input, minimum, due, budget)
			if name == "canonical" {
				require.NoError(t, err)
				require.Equal(t, unit(1).String(), quote.NativeOutput.String())
				require.Equal(t, coin.Denom, quote.Symbol)
				require.Positive(t, quote.SDKGasUsed)
			} else {
				require.Error(t, err)
			}
			require.Positive(t, ctx.GasMeter().GasConsumed())
			require.Equal(t, beforeSupply, l.app.BankKeeper.GetSupply(ctx, identity.BaseDenom))
			require.Equal(t, beforeReserve, l.app.BankKeeper.GetBalance(ctx, sdk.AccAddress(token.Bytes()), identity.BaseDenom))
			require.Equal(t, beforeActor, l.app.BankKeeper.GetBalance(ctx, actor, identity.BaseDenom))
			require.Equal(t, beforeTokenSupply, l.app.EVMKeeper.GetState(ctx, token, supplySlot))
			require.Equal(t, beforeNonce, l.app.EVMKeeper.GetNonce(ctx, l.expect.Actor))
			require.Len(t, ctx.EventManager().Events(), beforeEvents)
		})
	}
}
