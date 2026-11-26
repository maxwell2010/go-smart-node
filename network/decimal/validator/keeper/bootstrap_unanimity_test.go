//go:build mintcandy_bootstrap_unanimity && (mintcandy_mainnet || mintcandy_testnet)

package keeper

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBootstrapUnanimityRosterLatch(t *testing.T) {
	ctx, k, _ := delProvenanceGenesis(t)
	require.NoError(t, k.InitializeNativeLiveValidatorRoster(ctx.WithBlockHeight(0)))
	roster, err := k.NativeLiveValidatorOperators(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, roster)
	assertMinimum := func(want uint64) {
		t.Helper()
		got, err := k.BootstrapUnanimityMinimumID(ctx)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}

	before := ctx.GasMeter().GasConsumed()
	assertMinimum(0)
	require.Equal(t, before, ctx.GasMeter().GasConsumed(), "pre-upgrade operations must replay with identical gas")
	require.NoError(t, k.InitializeBootstrapUnanimity(ctx, 7))
	require.ErrorContains(t, k.InitializeBootstrapUnanimity(ctx, 8), "already consumed")
	require.Error(t, k.updateNativeLiveValidatorRoster(ctx, roster[0], true))
	assertMinimum(7)
	failed, err := NewStateKeeperWithDomain(failingService{service: k.service, prefix: bootstrapUnanimityKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, failed.updateNativeLiveValidatorRoster(ctx, roster[0], false), "injected write failure")
	still, err := k.NativeLiveValidatorOperators(ctx)
	require.NoError(t, err)
	require.Equal(t, roster, still)
	assertMinimum(7)

	require.NoError(t, k.updateNativeLiveValidatorRoster(ctx, roster[0], false))
	require.NoError(t, k.updateNativeLiveValidatorRoster(ctx, roster[0], true))
	assertMinimum(0)
	require.ErrorContains(t, k.InitializeBootstrapUnanimity(ctx, 9), "already consumed")
}
