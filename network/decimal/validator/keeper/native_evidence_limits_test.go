package keeper

import (
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"
)

func TestNativeEvidenceLimitsPinAndCache(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	original := ctx.WithBlockHeight(0)
	params := &cmtproto.EvidenceParams{
		MaxAgeNumBlocks: 100, MaxAgeDuration: 2*time.Hour + time.Nanosecond, MaxBytes: 1024,
	}
	want := NativeEvidenceAgeLimits{MaxAgeNumBlocks: params.MaxAgeNumBlocks, MaxAgeDuration: params.MaxAgeDuration}
	require.ErrorContains(t, k.InitializeNativeEvidenceLimits(ctx, params), "fresh height-one genesis")
	require.ErrorContains(t, k.InitializeNativeEvidenceLimits(original.WithBlockTime(time.Time{}), params), "fresh height-one genesis")
	require.ErrorIs(t, k.VerifyNativeEvidenceLimits(original, params), ErrNotFound)

	discarded, _ := original.CacheContext()
	require.NoError(t, k.InitializeNativeEvidenceLimits(discarded, params))
	got, err := k.NativeEvidenceLimits(discarded)
	require.NoError(t, err)
	require.Equal(t, want, got)
	_, err = k.NativeEvidenceLimits(original)
	require.ErrorIs(t, err, ErrNotFound)

	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: nativeEvidenceLimitsKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.InitializeNativeEvidenceLimits(original, params), "injected write failure")
	_, err = k.NativeEvidenceLimits(original)
	require.ErrorIs(t, err, ErrNotFound)

	child, commit := original.CacheContext()
	require.NoError(t, k.InitializeNativeEvidenceLimits(child, params))
	commit()
	reopened, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	got, err = reopened.NativeEvidenceLimits(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.NoError(t, reopened.VerifyNativeEvidenceLimits(ctx, params))

	changedBytes := *params
	changedBytes.MaxBytes++
	require.NoError(t, reopened.VerifyNativeEvidenceLimits(ctx, &changedBytes))
	changedBlocks := *params
	changedBlocks.MaxAgeNumBlocks++
	require.ErrorContains(t, reopened.VerifyNativeEvidenceLimits(ctx, &changedBlocks), "differ from genesis")
	require.ErrorContains(t, reopened.InitializeNativeEvidenceLimits(original, &changedBlocks), "already initialized")
	changedTime := *params
	changedTime.MaxAgeDuration--
	require.ErrorContains(t, reopened.VerifyNativeEvidenceLimits(ctx, &changedTime), "differ from genesis")
}

func TestNativeEvidenceLimitsRejectInvalidParametersAndRecord(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	ctx = ctx.WithBlockHeight(0)
	valid := cmtproto.EvidenceParams{MaxAgeNumBlocks: 1, MaxAgeDuration: time.Second, MaxBytes: 1}
	for _, bad := range []*cmtproto.EvidenceParams{
		nil,
		{MaxAgeDuration: valid.MaxAgeDuration, MaxBytes: valid.MaxBytes},
		{MaxAgeNumBlocks: valid.MaxAgeNumBlocks, MaxAgeDuration: -time.Second, MaxBytes: valid.MaxBytes},
		{MaxAgeNumBlocks: valid.MaxAgeNumBlocks, MaxAgeDuration: valid.MaxAgeDuration},
	} {
		require.ErrorContains(t, k.InitializeNativeEvidenceLimits(ctx, bad), "positive age limits and evidence bytes")
		require.ErrorContains(t, k.VerifyNativeEvidenceLimits(ctx, bad), "positive age limits and evidence bytes")
	}
	require.NoError(t, k.InitializeNativeEvidenceLimits(ctx, &valid))
	child, _ := ctx.CacheContext()
	state := service.OpenKVStore(child)
	require.NoError(t, state.Set([]byte{nativeEvidenceLimitsKey}, []byte{nativeEvidenceLimitsVersion}))
	_, err := k.NativeEvidenceLimits(child)
	require.ErrorContains(t, err, "invalid native evidence limits encoding")
	corrupt := make([]byte, nativeEvidenceLimitsSize)
	corrupt[0] = nativeEvidenceLimitsVersion
	require.NoError(t, state.Set([]byte{nativeEvidenceLimitsKey}, corrupt))
	require.ErrorContains(t, k.VerifyNativeEvidenceLimits(child, &valid), "invalid native evidence age limits")
	require.NoError(t, k.VerifyNativeEvidenceLimits(ctx, &valid))
}
