package authority

import (
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func sessionContext() sdk.Context {
	return testutil.DefaultContext(storetypes.NewKVStoreKey("authority"), storetypes.NewTransientStoreKey("authority-t")).WithChainID("mintcandy_990001-1").WithBlockHeight(10)
}

func TestGovernanceWorkSessionClosesAndSharesFailedCacheReservations(t *testing.T) {
	ctx := sessionContext()
	var escaped sdk.Context
	require.Error(t, reserve(ctx, 1))
	require.NoError(t, RunGovernance(ctx, func(c sdk.Context) error {
		escaped = c
		require.Error(t, RunGovernance(c, func(sdk.Context) error { return nil }))
		child, _ := c.CacheContext()
		require.NoError(t, reserve(child, MaxBlockWork-1))
		// Discarding the child does not restore this in-memory work reservation.
		require.NoError(t, reserve(c, 1))
		require.ErrorContains(t, reserve(c, 1), "budget exhausted")
		require.Error(t, reserve(c.WithBlockHeight(11), 0))
		require.Error(t, reserve(c.WithChainID("other"), 0))
		return nil
	}))
	require.Error(t, reserve(escaped, 0))
	require.NoError(t, RunGovernance(ctx, func(c sdk.Context) error { return reserve(c, MaxBlockWork) }))
}
