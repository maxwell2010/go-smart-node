package compat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	"github.com/cosmos/gogoproto/types"
	"github.com/stretchr/testify/require"
)

func TestParamsFollowRealSDKCacheContext(t *testing.T) {
	key := storetypes.NewKVStoreKey("financial-params")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient"))
	service := runtime.NewKVStoreService(key)
	parameterKey := []byte{0x01}
	params, err := NewParamsStore(service, parameterKey, &types.StringValue{}, validateString)
	require.NoError(t, err)
	_, err = params.Get(ctx)
	require.ErrorIs(t, err, ErrMissingParams)
	require.NoError(t, params.Set(ctx, &types.StringValue{Value: "committed"}))
	parameterKey[0] = 0xff // the caller cannot move a keeper's parameter key.

	cached, write := ctx.CacheContext()
	require.NoError(t, params.Set(cached, &types.StringValue{Value: "pending"}))
	value, err := params.Get(cached)
	require.NoError(t, err)
	require.Equal(t, "pending", value.Value)
	value.Value = "mutated returned object"
	value, err = params.Get(cached)
	require.NoError(t, err)
	require.Equal(t, "pending", value.Value)
	value, err = params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, "committed", value.Value, "uncommitted parameters must not leak through keeper memory")

	discarded, _ := ctx.CacheContext()
	require.NoError(t, params.Set(discarded, &types.StringValue{Value: "discarded"}))
	write()
	value, err = params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, "pending", value.Value, "only the committed context controls future reads")
	require.ErrorIs(t, params.Set(ctx, &types.StringValue{}), ErrInvalidParams)
	require.ErrorIs(t, params.Set(ctx, (*types.StringValue)(nil)), ErrInvalidParams)
	value, err = params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, "pending", value.Value)

	require.NoError(t, service.OpenKVStore(ctx).Set([]byte{0x01}, []byte{0xff}))
	_, err = params.Get(ctx)
	require.Error(t, err, "corrupt committed bytes cannot silently become defaults")
}

func TestParamsStorageErrorsAndBounds(t *testing.T) {
	key := storetypes.NewKVStoreKey("financial-params")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient"))
	service := runtime.NewKVStoreService(key)
	for _, writeFailure := range []bool{false, true} {
		params, err := NewParamsStore(failedService{base: service, write: writeFailure}, []byte{0x01}, &types.StringValue{}, validateString)
		require.NoError(t, err)
		if writeFailure {
			require.ErrorContains(t, params.Set(ctx, &types.StringValue{Value: "value"}), "storage failure")
		} else {
			_, err = params.Get(ctx)
			require.ErrorContains(t, err, "storage failure")
		}
	}
	params, err := NewParamsStore(service, []byte{0x01}, &types.StringValue{}, validateString)
	require.NoError(t, err)
	require.ErrorIs(t, params.Set(ctx, &types.StringValue{Value: strings.Repeat("x", MaxParamsBytes)}), ErrParamsTooLarge)
	require.NoError(t, service.OpenKVStore(ctx).Set([]byte{0x01}, make([]byte, MaxParamsBytes+1)))
	_, err = params.Get(ctx)
	require.ErrorIs(t, err, ErrParamsTooLarge)
	_, err = NewParamsStore(service, []byte{0x01}, (*types.StringValue)(nil), validateString)
	require.Error(t, err)
}

func validateString(value *types.StringValue) error {
	if value.Value == "" {
		return errors.New("empty value")
	}
	return nil
}

type failedService struct {
	base  store.KVStoreService
	write bool
}

func (s failedService) OpenKVStore(ctx context.Context) store.KVStore {
	return failedStore{KVStore: s.base.OpenKVStore(ctx), write: s.write}
}

type failedStore struct {
	store.KVStore
	write bool
}

func (s failedStore) Get(key []byte) ([]byte, error) {
	if !s.write {
		return nil, errors.New("storage failure")
	}
	return s.KVStore.Get(key)
}
func (s failedStore) Set(key, value []byte) error {
	if s.write {
		return errors.New("storage failure")
	}
	return s.KVStore.Set(key, value)
}
