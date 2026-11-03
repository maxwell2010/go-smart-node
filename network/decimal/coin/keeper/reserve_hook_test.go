package keeper

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func reserveABI(t *testing.T) abi.ABI {
	t.Helper()
	a, err := abi.JSON(strings.NewReader(`[{"type":"event","name":"ReserveUpdated","anonymous":false,"inputs":[{"name":"newReserve","type":"uint256","indexed":false},{"name":"newSupply","type":"uint256","indexed":false},{"name":"newPrice","type":"uint256","indexed":false}]}]`))
	require.NoError(t, err)
	return a
}

func reserveLog(t *testing.T, a abi.ABI, address string, reserve, volume sdkmath.Int) *ethtypes.Log {
	t.Helper()
	event := a.Events["ReserveUpdated"]
	data, err := event.Inputs.Pack(reserve.BigInt(), volume.BigInt(), big.NewInt(1))
	require.NoError(t, err)
	return &ethtypes.Log{Address: common.HexToAddress(address), Topics: []common.Hash{event.ID}, Data: data}
}

func TestReserveHookUsesFinalPerTokenStateAndDiscardsReaderWrites(t *testing.T) {
	ctx, _, k, coin := fixture(t)
	require.NoError(t, k.SetCoin(ctx, coin))
	a := reserveABI(t)
	finalVolume, finalReserve := coin.Volume.AddRaw(9), coin.Reserve.AddRaw(3)
	reads := 0
	hook, err := NewReserveHook(k, a, func(c sdk.Context, addr common.Address) (sdkmath.Int, sdkmath.Int, error) {
		reads++
		require.Equal(t, common.HexToAddress(coin.DRC20Contract), addr)
		changed := coin
		changed.Title = "discard this reader write"
		require.NoError(t, k.SetCoin(c, changed))
		return finalVolume, finalReserve, nil
	}, 8)
	require.NoError(t, err)
	unknown := reserveLog(t, a, "0x9999999999999999999999999999999999999999", coin.Reserve, coin.Volume)
	unknown.Data = nil // A foreign emitter cannot force native decoding failure.
	receipt := &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{
		unknown, {Address: common.HexToAddress(coin.DRC20Contract)},
		reserveLog(t, a, coin.DRC20Contract, sdkmath.ZeroInt(), sdkmath.ZeroInt()),
		reserveLog(t, a, coin.DRC20Contract, finalReserve, finalVolume),
	}}
	cached, commit := ctx.CacheContext()
	require.NoError(t, hook.PostTxProcessing(cached, common.Address{}, core.Message{}, receipt))
	before, err := k.GetCoin(ctx, coin.Denom)
	require.NoError(t, err)
	require.Equal(t, coin.Volume, before.Volume)
	require.Empty(t, ctx.EventManager().Events())
	commit()
	after, err := k.GetCoin(ctx, coin.Denom)
	require.NoError(t, err)
	require.Equal(t, finalVolume, after.Volume)
	require.Equal(t, finalReserve, after.Reserve)
	require.Equal(t, coin.Title, after.Title)
	require.Equal(t, 2, reads, "prepare and standalone apply each verify actual final backing")
	require.Len(t, ctx.EventManager().Events(), 1)
}

func TestReserveHookLaterEventErrorRollsBackWholeReceipt(t *testing.T) {
	ctx, _, k, first := fixture(t)
	second := first
	second.Denom, second.DRC20Contract = "secondcoin", "0x2222222222222222222222222222222222222222"
	require.NoError(t, k.SetCoin(ctx, first))
	require.NoError(t, k.SetCoin(ctx, second))
	a := reserveABI(t)
	wantVolume, wantReserve := first.Volume.AddRaw(5), first.Reserve.AddRaw(7)
	hook, err := NewReserveHook(k, a, func(sdk.Context, common.Address) (sdkmath.Int, sdkmath.Int, error) {
		return wantVolume, wantReserve, nil
	}, 4)
	require.NoError(t, err)
	emits := 0
	k.emit = func(c sdk.Context, event proto.Message) error {
		emits++
		if emits == 2 {
			return errors.New("later native event rejected")
		}
		return c.EventManager().EmitTypedEvent(event)
	}
	receipt := &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{
		reserveLog(t, a, first.DRC20Contract, wantReserve, wantVolume),
		reserveLog(t, a, second.DRC20Contract, wantReserve, wantVolume),
	}}
	require.ErrorContains(t, hook.PostTxProcessing(ctx, common.Address{}, core.Message{}, receipt), "later native event rejected")
	for _, coin := range []string{first.Denom, second.Denom} {
		unchanged, err := k.GetCoin(ctx, coin)
		require.NoError(t, err)
		require.Equal(t, first.Volume, unchanged.Volume)
		require.Equal(t, first.Reserve, unchanged.Reserve)
	}
	require.Empty(t, ctx.EventManager().Events())
}

func TestReserveHookRejectsMalformedOrUnbackedEventsAndSkipsFailedReceipt(t *testing.T) {
	for _, mode := range []string{"short", "trailing", "topics", "removed", "backing", "reader", "limit", "failed", "nil", "base"} {
		t.Run(mode, func(t *testing.T) {
			ctx, _, k, coin := fixture(t)
			if mode == "base" {
				coin.Denom = "del"
			}
			require.NoError(t, k.SetCoin(ctx, coin))
			a := reserveABI(t)
			reads := 0
			hook, err := NewReserveHook(k, a, func(sdk.Context, common.Address) (sdkmath.Int, sdkmath.Int, error) {
				reads++
				if mode == "reader" {
					return sdkmath.Int{}, sdkmath.Int{}, errors.New("backing read failed")
				}
				if mode == "backing" {
					return coin.Volume.AddRaw(1), coin.Reserve, nil
				}
				return coin.Volume, coin.Reserve, nil
			}, 1)
			require.NoError(t, err)
			log := reserveLog(t, a, coin.DRC20Contract, coin.Reserve, coin.Volume)
			receipt := &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{log}}
			switch mode {
			case "short":
				log.Data = log.Data[:32]
			case "trailing":
				log.Data = append(log.Data, 0)
			case "topics":
				log.Topics = append(log.Topics, common.Hash{})
			case "removed":
				log.Removed = true
			case "limit":
				receipt.Logs = append(receipt.Logs, log)
			case "nil":
				receipt.Logs[0] = nil
			case "failed":
				receipt.Status = 0
			}
			err = hook.PostTxProcessing(ctx, common.Address{}, core.Message{}, receipt)
			if mode == "failed" {
				require.NoError(t, err)
				require.Zero(t, reads)
			} else {
				require.Error(t, err)
			}
			unchanged, err := k.GetCoin(ctx, coin.Denom)
			require.NoError(t, err)
			require.Equal(t, coin.Volume, unchanged.Volume)
			require.Equal(t, coin.Reserve, unchanged.Reserve)
			require.Empty(t, ctx.EventManager().Events())
		})
	}
}
