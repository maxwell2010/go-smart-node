package governance

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	nativetypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

type nativeReadCount struct{ gets int }
type countingService struct {
	store.KVStoreService
	count *nativeReadCount
}

func (s countingService) OpenKVStore(ctx context.Context) store.KVStore {
	return countingStore{KVStore: s.KVStoreService.OpenKVStore(ctx), count: s.count}
}

type countingStore struct {
	store.KVStore
	count *nativeReadCount
}

func (s countingStore) Get(key []byte) ([]byte, error) { s.count.gets++; return s.KVStore.Get(key) }

func TestScopedSessionsReuseIndexedOwnershipWithoutNativeReadsPerVoter(t *testing.T) {
	ctx, service, _, _, scope := sessionNativeFixture(t)
	count := &nativeReadCount{}
	native, e := nativekeeper.NewStateKeeper(countingService{service, count})
	require.NoError(t, e)
	calls := 0
	reader, e := NewSessions(native, sourceFn(func(c context.Context, r Request) (Weights, error) {
		calls++
		return baseSource().SnapshotWeights(c, r)
	}), sdkmath.OneInt(), scope)
	require.NoError(t, e)
	require.NoError(t, reader.Run(ctx, func(sc sdk.Context) error {
		if e := reader.IterateBondedValidatorsByPower(sc, func(int64, stakingtypes.ValidatorI) bool { return false }); e != nil {
			return e
		}
		before := count.gets
		for i := 0; i < 100; i++ {
			owner := sdk.AccAddress(bytes.Repeat([]byte{byte(i + 1)}, 20))
			if e := reader.IterateDelegations(sc, owner, func(int64, stakingtypes.DelegationI) bool { return false }); e != nil {
				return e
			}
		}
		require.Equal(t, before, count.gets, "per-voter reads must use the frozen ownership index")
		total, e := reader.TotalBondedTokens(sc)
		require.NoError(t, e)
		require.True(t, total.Equal(sdkmath.NewInt(100)))
		require.Greater(t, count.gets, before, "close must fingerprint native state again")
		return e
	}))
	require.Equal(t, 1, calls)
}

func TestScopedSessionsRefreshEachTallyAndNeverKeepGlobalWeights(t *testing.T) {
	ctx, _, native, g, scope := sessionNativeFixture(t)
	calls := 0
	reader, e := NewSessions(native, sourceFn(func(c context.Context, r Request) (Weights, error) {
		calls++
		return baseSource().SnapshotWeights(c, r)
	}), sdkmath.OneInt(), scope)
	require.NoError(t, e)
	require.NoError(t, reader.Run(ctx, func(sc sdk.Context) error {
		for _, power := range []int64{100, 200} {
			v := g.Validators[0]
			v.Stake = power
			if e := native.SetValidator(sc, v); e != nil {
				return e
			}
			if e := reader.IterateBondedValidatorsByPower(sc, func(_ int64, v stakingtypes.ValidatorI) bool {
				require.True(t, v.(stakingtypes.Validator).Tokens.Equal(sdkmath.NewInt(power)))
				return false
			}); e != nil {
				return e
			}
			total, e := reader.TotalBondedTokens(sc)
			if e != nil {
				return e
			}
			require.True(t, total.Equal(sdkmath.NewInt(power)))
		}
		return nil
	}))
	require.Equal(t, 2, calls)
	stored, e := native.GetValidator(ctx, g.Validators[0].OperatorAddress)
	require.NoError(t, e)
	require.EqualValues(t, 200, stored.Stake)
}

func TestScopedSessionsFailClosedOnWrongOrderCacheAndLeakedContext(t *testing.T) {
	ctx, _, native, _, scope := sessionNativeFixture(t)
	reader, e := NewSessions(native, baseSource(), sdkmath.OneInt(), scope)
	require.NoError(t, e)
	_, e = reader.TotalBondedTokens(context.Background())
	require.ErrorContains(t, e, "missing")
	_, e = reader.TotalBondedTokens(ctx)
	require.ErrorContains(t, e, "missing")
	require.ErrorContains(t, reader.Run(ctx, func(sc sdk.Context) error { _, e := reader.TotalBondedTokens(sc); return e }), "not started")
	require.ErrorContains(t, reader.Run(ctx, func(sc sdk.Context) error {
		return reader.IterateBondedValidatorsByPower(sc, func(int64, stakingtypes.ValidatorI) bool { return false })
	}), "did not close")
	require.ErrorContains(t, reader.Run(ctx, func(sc sdk.Context) error {
		require.NoError(t, reader.IterateBondedValidatorsByPower(sc, func(int64, stakingtypes.ValidatorI) bool { return false }))
		other, _ := sc.CacheContext()
		return reader.IterateDelegations(other, sdk.AccAddress(bytes.Repeat([]byte{2}, 20)), func(int64, stakingtypes.DelegationI) bool { return false })
	}), "cache contexts")
	var leaked sdk.Context
	require.NoError(t, reader.ReadOnly(ctx, func(sc sdk.Context) error { leaked = sc; return nil }))
	require.ErrorContains(t, reader.IterateBondedValidatorsByPower(leaked, func(int64, stakingtypes.ValidatorI) bool { return false }), "closed")
}

func TestScopedSessionsAbortChangedOrFailedStateAndDiscardQueries(t *testing.T) {
	ctx, _, native, g, scope := sessionNativeFixture(t)
	reader, e := NewSessions(native, baseSource(), sdkmath.OneInt(), scope)
	require.NoError(t, e)
	require.ErrorContains(t, reader.Run(ctx, func(sc sdk.Context) error {
		require.NoError(t, reader.IterateBondedValidatorsByPower(sc, func(int64, stakingtypes.ValidatorI) bool { return false }))
		v := g.Validators[0]
		v.Stake = 200
		require.NoError(t, native.SetValidator(sc, v))
		_, e := reader.TotalBondedTokens(sc)
		return e
	}), "changed after")
	stored, e := native.GetValidator(ctx, g.Validators[0].OperatorAddress)
	require.NoError(t, e)
	require.EqualValues(t, 100, stored.Stake)
	require.ErrorContains(t, reader.Run(ctx, func(sc sdk.Context) error {
		v := g.Validators[0]
		v.Stake = 300
		require.NoError(t, native.SetValidator(sc, v))
		return errors.New("callback aborted")
	}), "callback aborted")
	require.NoError(t, reader.ReadOnly(ctx, func(sc sdk.Context) error { v := g.Validators[0]; v.Stake = 400; return native.SetValidator(sc, v) }))
	stored, e = native.GetValidator(ctx, g.Validators[0].OperatorAddress)
	require.NoError(t, e)
	require.EqualValues(t, 100, stored.Stake)
	// An outer application cache can still discard a successful scoped tally.
	outer, _ := ctx.CacheContext()
	require.NoError(t, reader.Run(outer, func(sc sdk.Context) error {
		v := g.Validators[0]
		v.Stake = 500
		require.NoError(t, native.SetValidator(sc, v))
		require.NoError(t, reader.IterateBondedValidatorsByPower(sc, func(int64, stakingtypes.ValidatorI) bool { return false }))
		_, e := reader.TotalBondedTokens(sc)
		return e
	}))
	stored, e = native.GetValidator(ctx, g.Validators[0].OperatorAddress)
	require.NoError(t, e)
	require.EqualValues(t, 100, stored.Stake)
}

func TestScopedSessionsRejectReentrantReadsWithoutDeadlock(t *testing.T) {
	ctx, _, native, _, scope := sessionNativeFixture(t)
	reader, e := NewSessions(native, baseSource(), sdkmath.OneInt(), scope)
	require.NoError(t, e)
	require.ErrorContains(t, reader.Run(ctx, func(sc sdk.Context) error {
		return reader.IterateBondedValidatorsByPower(sc, func(int64, stakingtypes.ValidatorI) bool {
			_, e := reader.TotalBondedTokens(sc)
			require.ErrorContains(t, e, "reentrant")
			return false
		})
	}), "reentrant")
}

func TestScopedSessionsRejectInvalidIdentityAndChangedContext(t *testing.T) {
	ctx, _, native, _, scope := sessionNativeFixture(t)
	_, e := NewSessions(native, baseSource(), sdkmath.OneInt(), nil)
	require.Error(t, e)
	for _, identity := range []func(sdk.Context) any{func(sdk.Context) any { return nil }, func(sdk.Context) any { return map[string]int{} }, func(sdk.Context) any { panic("unmounted key") }} {
		reader, e := NewSessions(native, baseSource(), sdkmath.OneInt(), identity)
		require.NoError(t, e)
		require.Error(t, reader.Run(ctx, func(sc sdk.Context) error {
			return reader.IterateBondedValidatorsByPower(sc, func(int64, stakingtypes.ValidatorI) bool { return false })
		}))
	}
	reader, e := NewSessions(native, baseSource(), sdkmath.OneInt(), scope)
	require.NoError(t, e)
	for _, alter := range []func(sdk.Context) sdk.Context{func(c sdk.Context) sdk.Context { return c.WithBlockHeight(2) }, func(c sdk.Context) sdk.Context { return c.WithChainID("other") }, func(c sdk.Context) sdk.Context { return c.WithBlockTime(c.BlockTime().Add(time.Second)) }} {
		require.ErrorContains(t, reader.Run(ctx, func(sc sdk.Context) error {
			return reader.IterateBondedValidatorsByPower(alter(sc), func(int64, stakingtypes.ValidatorI) bool { return false })
		}), "context changed")
	}
	other, e := NewSessions(native, baseSource(), sdkmath.OneInt(), scope)
	require.NoError(t, e)
	require.ErrorContains(t, reader.Run(ctx, func(sc sdk.Context) error {
		return other.IterateBondedValidatorsByPower(sc, func(int64, stakingtypes.ValidatorI) bool { return false })
	}), "another reader")
}

func sessionNativeFixture(t *testing.T) (sdk.Context, store.KVStoreService, *nativekeeper.StateKeeper, *nativetypes.GenesisState, func(sdk.Context) any) {
	t.Helper()
	_, _, _, g := nativeFixture(t)
	key := storetypes.NewKVStoreKey("native-session")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient")).WithBlockHeight(1).WithChainID("mintcandy_300001-1")
	service := runtime.NewKVStoreService(key)
	native, e := nativekeeper.NewStateKeeper(service)
	require.NoError(t, e)
	require.NoError(t, native.InitGenesis(ctx, g))
	return ctx, service, native, g, func(c sdk.Context) any { return c.MultiStore().GetKVStore(key) }
}
