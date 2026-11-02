package keeper

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) (sdk.Context, store.KVStoreService, *StateKeeper, types.Coin) {
	t.Helper()
	key := storetypes.NewKVStoreKey("coin-state")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient"))
	service := runtime.NewKVStoreService(key)
	keeper, err := NewStateKeeper(service)
	require.NoError(t, err)
	address, err := bech32.ConvertAndEncode("d0", make([]byte, 20))
	require.NoError(t, err)
	unit := sdkmath.NewIntWithDecimal(1, 18)
	coin := types.Coin{Denom: "testcoin", Title: "Test coin", Creator: address, CRR: 50,
		Volume: unit.MulRaw(10), Reserve: unit.MulRaw(5), LimitVolume: unit.MulRaw(100), MinVolume: sdkmath.ZeroInt(),
		DRC20Contract: "0x1234567890123456789012345678901234567890"}
	require.NoError(t, keeper.SetParams(ctx, types.DefaultParams()))
	return ctx, service, keeper, coin
}

func TestCoinStateAndIndexesFollowSDKTransactions(t *testing.T) {
	ctx, _, keeper, coin := fixture(t)
	require.NoError(t, keeper.SetCoin(ctx, coin))
	cached, commit := ctx.CacheContext()
	coin.Volume = coin.Volume.AddRaw(9)
	require.NoError(t, keeper.UpdateCoinVR(cached, coin.Denom, coin.Volume, coin.Reserve))
	parent, err := keeper.GetCoin(ctx, coin.Denom)
	require.NoError(t, err)
	require.Equal(t, coin.Volume.SubRaw(9), parent.Volume)
	require.Empty(t, ctx.EventManager().Events())
	pending, err := keeper.GetCoinByDRC(cached, coin.DRC20Contract)
	require.NoError(t, err)
	require.Equal(t, coin.Volume, pending.Volume)
	commit()
	parent, err = keeper.GetCoinByDRC(ctx, coin.DRC20Contract)
	require.NoError(t, err)
	require.Equal(t, coin.Volume, parent.Volume)
	require.Len(t, ctx.EventManager().Events(), 1)
	// Returned amounts cannot mutate committed state through aliased big.Ints.
	parent.Volume.BigInt().SetInt64(1)
	again, err := keeper.GetCoin(ctx, coin.Denom)
	require.NoError(t, err)
	require.Equal(t, coin.Volume, again.Volume)
	other := coin
	other.Denom = "othercoin"
	require.ErrorContains(t, keeper.SetCoin(ctx, other), "already belongs")
	_, err = keeper.GetCoin(ctx, other.Denom)
	require.ErrorIs(t, err, ErrNotFound)
	coin.DRC20Contract = ""
	require.ErrorContains(t, keeper.SetCoin(ctx, coin), "immutable")
}

func TestCoinEventFailureDiscardsFinancialStateAndEvents(t *testing.T) {
	ctx, _, keeper, coin := fixture(t)
	require.NoError(t, keeper.SetCoin(ctx, coin))
	keeper.emit = func(child sdk.Context, event proto.Message) error {
		require.NoError(t, child.EventManager().EmitTypedEvent(event))
		return errors.New("event conversion failed")
	}
	require.ErrorContains(t, keeper.UpdateCoinVR(ctx, coin.Denom, coin.Volume.AddRaw(1), coin.Reserve.SubRaw(1)), "event conversion failed")
	stored, err := keeper.GetCoin(ctx, coin.Denom)
	require.NoError(t, err)
	require.Equal(t, coin.Volume, stored.Volume)
	require.Equal(t, coin.Reserve, stored.Reserve)
	require.Empty(t, ctx.EventManager().Events())
	require.Error(t, keeper.UpdateCoinVR(ctx, coin.Denom, sdkmath.Int{}, coin.Reserve))
}

func TestCoinIndexWriteFailureLeavesNoMetadata(t *testing.T) {
	ctx, service, _, coin := fixture(t)
	keeper, err := NewStateKeeper(indexFailureService{service})
	require.NoError(t, err)
	require.ErrorContains(t, keeper.SetCoin(ctx, coin), "index write failed")
	_, err = keeper.GetCoin(ctx, coin.Denom)
	require.ErrorIs(t, err, ErrNotFound)
	raw, err := service.OpenKVStore(ctx).Get(addressKey(coin.DRC20Contract))
	require.NoError(t, err)
	require.Nil(t, raw)
}

func TestCoinParamsDoNotLeakFromDiscardedContext(t *testing.T) {
	ctx, _, keeper, _ := fixture(t)
	child, _ := ctx.CacheContext()
	params := types.DefaultParams()
	params.BaseTitle = "discarded"
	require.NoError(t, keeper.SetParams(child, params))
	parent, err := keeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultBaseTitle, parent.BaseTitle)
	params.BaseDenom = "other"
	require.ErrorContains(t, keeper.SetParams(ctx, params), "immutable")
}

type indexFailureService struct{ store.KVStoreService }

func (s indexFailureService) OpenKVStore(ctx context.Context) store.KVStore {
	return indexFailureStore{s.KVStoreService.OpenKVStore(ctx)}
}

type indexFailureStore struct{ store.KVStore }

func (s indexFailureStore) Set(key, value []byte) error {
	if len(key) > 0 && key[0] == 0x13 {
		return errors.New("index write failed")
	}
	return s.KVStore.Set(key, value)
}
