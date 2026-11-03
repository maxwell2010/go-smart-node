package nativeexec

import (
	"errors"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	coinkeeper "github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

func TestReserveAdapterSharesRealMetadataAndLaterStageRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "late failure"}[fail], func(t *testing.T) {
			ctx, key, execution := fixture()
			keeper, err := coinkeeper.NewStateKeeper(runtime.NewKVStoreService(key))
			require.NoError(t, err)
			require.NoError(t, keeper.SetParams(ctx, cointypes.DefaultParams()))
			creator, err := bech32.ConvertAndEncode("d0", execution.Sender.Bytes())
			require.NoError(t, err)
			unit := sdkmath.NewIntWithDecimal(1, 18)
			coin := cointypes.Coin{Denom: "adaptercoin", Title: "Registered adapter fixture", Creator: creator,
				CRR: 50, Volume: unit.MulRaw(10), Reserve: unit.MulRaw(5), MinVolume: sdkmath.ZeroInt(),
				LimitVolume: unit.MulRaw(100), DRC20Contract: "0x1234567890123456789012345678901234567890"}
			require.NoError(t, keeper.SetCoin(ctx, coin))
			compiled, err := contracts.CompiledABI("DRC20")
			require.NoError(t, err)
			volume, reserve := coin.Volume.AddRaw(7), coin.Reserve.AddRaw(3)
			reads := 0
			// The real authenticated EVM reader has separate signed App tests.
			// Here this explicit final-state provider isolates stage composition.
			hook, err := coinkeeper.NewReserveHook(keeper, compiled, func(sdk.Context, common.Address) (sdkmath.Int, sdkmath.Int, error) {
				reads++
				return volume, reserve, nil
			}, 8)
			require.NoError(t, err)
			assets, err := NewReservePreparer(hook)
			require.NoError(t, err)
			event := compiled.Events["ReserveUpdated"]
			payload, err := event.Inputs.Pack(reserve.BigInt(), volume.BigInt(), big.NewInt(1))
			require.NoError(t, err)
			execution.Receipt.Logs = []*ethtypes.Log{{Address: common.HexToAddress(coin.DRC20Contract), Topics: []common.Hash{event.ID}, Data: payload}}
			probe := func(name string, reject bool) Preparer {
				return prepareFunc(func(sdk.Context, Execution) (Prepared, error) {
					return &plan{validate: func(sdk.Context) error { return nil }, apply: func(c sdk.Context) error {
						c.KVStore(key).Set([]byte(name), []byte{1})
						c.EventManager().EmitEvent(sdk.NewEvent(name))
						if reject {
							return errors.New("later validator plan rejected")
						}
						return nil
					}}, nil
				})
			}
			coordinator, err := New(assets, probe("nft-probe", false), probe("validator-probe", fail), finalFunc(func(sdk.Context, Execution) error { return nil }), Limits{MaxLogs: 8, MaxBytes: 4096, MaxCopyWeight: 65536})
			require.NoError(t, err)
			err = coordinator.Process(ctx, execution)
			current, readErr := keeper.GetCoin(ctx, coin.Denom)
			require.NoError(t, readErr)
			require.Equal(t, 3, reads, "prepare/validate/apply each check authoritative final backing")
			if fail {
				require.ErrorContains(t, err, "later validator")
				require.Equal(t, coin.Volume, current.Volume)
				require.Equal(t, coin.Reserve, current.Reserve)
				require.Nil(t, ctx.KVStore(key).Get([]byte("nft-probe")))
				require.Nil(t, ctx.KVStore(key).Get([]byte("validator-probe")))
				require.Empty(t, ctx.EventManager().Events())
			} else {
				require.NoError(t, err)
				require.Equal(t, volume, current.Volume)
				require.Equal(t, reserve, current.Reserve)
				require.Len(t, ctx.EventManager().Events(), 3)
			}
		})
	}
	_, err := NewReservePreparer(nil)
	require.Error(t, err)
}
