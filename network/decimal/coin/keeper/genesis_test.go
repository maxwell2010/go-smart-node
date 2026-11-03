package keeper

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/stretchr/testify/require"
)

func TestNativeCoinGenesisRoundTripIncludesRedeemedChecks(t *testing.T) {
	ctx, service, k, coin := fixture(t)
	require.NoError(t, service.OpenKVStore(ctx).Delete([]byte{0x01}))
	params := types.DefaultParams()
	params.BaseVolume = coin.Reserve.MulRaw(2)
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	check := types.Check{ChainID: "mintcandy_990001-1", DueBlock: 100, Coin: sdk.NewCoin(coin.Denom, sdkmath.OneInt())}
	hash := check.HashWithoutLock()
	check.Lock, err = crypto.Sign(hash[:], key)
	require.NoError(t, err)
	require.NoError(t, check.Sign(key))
	genesis := types.NewGenesisState(params, []types.Coin{coin}, []types.Check{check})
	child, commit := ctx.CacheContext()
	require.NoError(t, k.InitGenesis(child, genesis))
	_, err = k.GetParams(ctx)
	require.Error(t, err)
	exported, err := k.ExportGenesis(child)
	require.NoError(t, err)
	expectedBytes, err := proto.Marshal(genesis)
	require.NoError(t, err)
	actualBytes, err := proto.Marshal(exported)
	require.NoError(t, err)
	require.Equal(t, expectedBytes, actualBytes)
	commit()
	require.ErrorContains(t, k.InitGenesis(ctx, genesis), "empty store")
	exported, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
	other, otherService, otherKeeper, _ := fixture(t)
	require.NoError(t, otherService.OpenKVStore(other).Delete([]byte{0x01}))
	require.NoError(t, otherKeeper.InitGenesis(other, exported))
	roundTrip, err := otherKeeper.ExportGenesis(other)
	require.NoError(t, err)
	expectedBytes, err = proto.Marshal(exported)
	require.NoError(t, err)
	actualBytes, err = proto.Marshal(roundTrip)
	require.NoError(t, err)
	require.Equal(t, expectedBytes, actualBytes)
}

func TestNativeCoinGenesisIndexFailureLeavesEmptyStore(t *testing.T) {
	ctx, service, _, coin := fixture(t)
	require.NoError(t, service.OpenKVStore(ctx).Delete([]byte{0x01}))
	k, err := NewStateKeeper(indexFailureService{service})
	require.NoError(t, err)
	params := types.DefaultParams()
	params.BaseVolume = coin.Reserve
	require.ErrorContains(t, k.InitGenesis(ctx, types.NewGenesisState(params, []types.Coin{coin}, nil)), "index write failed")
	iterator, err := service.OpenKVStore(ctx).Iterator(nil, nil)
	require.NoError(t, err)
	require.False(t, iterator.Valid())
	require.NoError(t, iterator.Close())
}

func TestNativeCoinExportRejectsOrphansAndCorruptRows(t *testing.T) {
	for _, mode := range []string{"unknown", "orphan", "missing-index", "bad-check", "coin-key"} {
		t.Run(mode, func(t *testing.T) {
			ctx, service, k, coin := fixture(t)
			params := types.DefaultParams()
			params.BaseVolume = coin.Reserve
			require.NoError(t, k.SetParams(ctx, params))
			require.NoError(t, k.SetCoin(ctx, coin))
			state := service.OpenKVStore(ctx)
			switch mode {
			case "unknown":
				require.NoError(t, state.Set([]byte{0x77}, []byte{1}))
			case "orphan":
				require.NoError(t, state.Set(addressKey("0x9999999999999999999999999999999999999999"), []byte(coin.Denom)))
			case "missing-index":
				require.NoError(t, state.Delete(addressKey(coin.DRC20Contract)))
			case "bad-check":
				require.NoError(t, state.Set([]byte{0x21, 1}, []byte{255}))
			case "coin-key":
				data, err := proto.Marshal(&coin)
				require.NoError(t, err)
				require.NoError(t, state.Set(coinKey("othercoin"), data))
			}
			_, err := k.ExportGenesis(ctx)
			require.Error(t, err)
		})
	}
}
