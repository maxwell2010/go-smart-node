package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func TestReservePlanCapturesInputsAndCommitsOnceThroughOuterCache(t *testing.T) {
	ctx, _, k, coin := fixture(t)
	ctx = ctx.WithChainID("plan-local-1").WithBlockHeight(3).WithBlockTime(time.Unix(100, 0)).WithTxBytes([]byte("ordinary-local-transaction"))
	require.NoError(t, k.SetCoin(ctx, coin))
	a := reserveABI(t)
	volume, reserve := coin.Volume.AddRaw(1), coin.Reserve.AddRaw(2)
	reads := 0
	hook, err := NewReserveHook(k, a, func(read sdk.Context, _ common.Address) (sdkmath.Int, sdkmath.Int, error) {
		reads++
		changed := coin
		changed.Title = "discarded reader mutation"
		require.NoError(t, k.SetCoin(read, changed))
		return volume, reserve, nil
	}, 4)
	require.NoError(t, err)
	log := reserveLog(t, a, coin.DRC20Contract, reserve, volume)
	plan, err := hook.Prepare(ctx, &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{log}})
	require.NoError(t, err)
	require.False(t, plan.IsNoop())
	log.Data[0] ^= 1 // Caller-owned receipt bytes cannot change the prepared update.
	require.NoError(t, plan.Validate(ctx))
	before, err := k.GetCoin(ctx, coin.Denom)
	require.NoError(t, err)
	require.Equal(t, coin, before)
	require.Empty(t, ctx.EventManager().Events())
	copyOfPlan := *plan
	child, commit := ctx.CacheContext()
	require.NoError(t, plan.Apply(child))
	require.Equal(t, 3, reads, "each phase rechecks authoritative backing")
	require.ErrorContains(t, copyOfPlan.Apply(child), "consumed")
	require.ErrorContains(t, plan.Validate(child), "consumed")
	before, err = k.GetCoin(ctx, coin.Denom)
	require.NoError(t, err)
	require.Equal(t, coin, before)
	require.Empty(t, ctx.EventManager().Events())
	commit()
	after, err := k.GetCoin(ctx, coin.Denom)
	require.NoError(t, err)
	require.Equal(t, volume, after.Volume)
	require.Equal(t, reserve, after.Reserve)
	require.Equal(t, coin.Title, after.Title)
	require.Len(t, ctx.EventManager().Events(), 1)
}

func TestReservePlanRejectsChangedPrestateAndConsumesFailedApply(t *testing.T) {
	for _, mode := range []string{"native row", "parameters", "chain", "height", "time", "proposer", "header hash", "transaction", "backing before validate", "backing after validate"} {
		t.Run(mode, func(t *testing.T) {
			ctx, _, k, coin := fixture(t)
			ctx = ctx.WithChainID("plan-local-1").WithBlockHeight(3).WithBlockTime(time.Unix(100, 0)).WithTxBytes([]byte("ordinary-local-transaction"))
			require.NoError(t, k.SetCoin(ctx, coin))
			a := reserveABI(t)
			actual := coin.Reserve
			hook, err := NewReserveHook(k, a, func(sdk.Context, common.Address) (sdkmath.Int, sdkmath.Int, error) {
				return coin.Volume, actual, nil
			}, 4)
			require.NoError(t, err)
			plan, err := hook.Prepare(ctx, &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{reserveLog(t, a, coin.DRC20Contract, coin.Reserve, coin.Volume)}})
			require.NoError(t, err)
			changed := ctx
			switch mode {
			case "native row":
				coin.Title = "changed after prepare"
				require.NoError(t, k.SetCoin(ctx, coin))
			case "parameters":
				params, err := k.GetParams(ctx)
				require.NoError(t, err)
				params.BaseVolume = params.BaseVolume.AddRaw(1)
				require.NoError(t, k.SetParams(ctx, params))
			case "chain":
				changed = ctx.WithChainID("another-local-chain")
			case "height":
				changed = ctx.WithBlockHeight(4)
			case "time":
				changed = ctx.WithBlockTime(ctx.BlockTime().Add(time.Second))
			case "proposer":
				changed = ctx.WithProposer(sdk.ConsAddress([]byte("different-proposer")))
			case "header hash":
				changed = ctx.WithHeaderHash([]byte("different-header-hash"))
			case "transaction":
				changed = ctx.WithTxBytes([]byte("different-local-transaction"))
			case "backing before validate":
				actual = actual.AddRaw(1)
			case "backing after validate":
				require.NoError(t, plan.Validate(ctx))
				actual = actual.AddRaw(1)
			}
			if mode != "backing after validate" {
				require.Error(t, plan.Validate(changed))
			}
			require.Error(t, plan.Apply(changed))
			require.ErrorContains(t, plan.Apply(ctx), "consumed")
			require.Empty(t, ctx.EventManager().Events())
		})
	}
}

func TestReservePlanApplyInheritsGasFailureWithoutWriting(t *testing.T) {
	ctx, _, k, coin := fixture(t)
	require.NoError(t, k.SetCoin(ctx, coin))
	a := reserveABI(t)
	hook, err := NewReserveHook(k, a, func(sdk.Context, common.Address) (sdkmath.Int, sdkmath.Int, error) {
		return coin.Volume, coin.Reserve, nil
	}, 4)
	require.NoError(t, err)
	plan, err := hook.Prepare(ctx, &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{reserveLog(t, a, coin.DRC20Contract, coin.Reserve, coin.Volume)}})
	require.NoError(t, err)
	limited := ctx.WithGasMeter(storetypes.NewGasMeter(1))
	var recovered interface{}
	func() {
		defer func() { recovered = recover() }()
		_ = plan.Apply(limited)
	}()
	require.IsType(t, storetypes.ErrorOutOfGas{}, recovered, "inherited OOG must reach the transaction boundary")
	require.ErrorContains(t, plan.Apply(ctx), "consumed")
	unchanged, err := k.GetCoin(ctx, coin.Denom)
	require.NoError(t, err)
	require.Equal(t, coin, unchanged)
	require.Empty(t, ctx.EventManager().Events())
}

func TestReservePlanNoopDoesNotReadNativeStateOrBacking(t *testing.T) {
	ctx, _, _, _ := fixture(t)
	// An uninitialized native store would fail on any KV read. There is no
	// matching topic in these receipts, so neither KV nor reader is consulted.
	hook, err := NewReserveHook(&StateKeeper{}, reserveABI(t), func(sdk.Context, common.Address) (sdkmath.Int, sdkmath.Int, error) {
		t.Fatal("no-op must not invoke backing reader")
		return sdkmath.Int{}, sdkmath.Int{}, nil
	}, 4)
	require.NoError(t, err)
	for _, receipt := range []*ethtypes.Receipt{
		{Status: 0}, {Status: 1}, {Status: 1, Logs: []*ethtypes.Log{{Topics: []common.Hash{common.HexToHash("0x1234")}}}},
	} {
		plan, err := hook.Prepare(ctx, receipt)
		require.NoError(t, err)
		require.True(t, plan.IsNoop())
		require.NoError(t, plan.Validate(ctx))
		require.NoError(t, plan.Apply(ctx))
		require.ErrorContains(t, plan.Apply(ctx), "consumed")
	}
	var absent *ReservePlan
	require.False(t, absent.IsNoop())
	require.Error(t, absent.Validate(ctx))
	require.Error(t, new(ReservePlan).Apply(ctx))
}
