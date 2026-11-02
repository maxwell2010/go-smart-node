package types

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	"github.com/maxwell2010/go-smart-node/network/decimal/compat"
	"github.com/stretchr/testify/require"
)

func TestNativeCoinParamsRespectTransactionRollback(t *testing.T) {
	key := storetypes.NewKVStoreKey(ModuleName)
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient"))
	params, err := compat.NewParamsStore(runtime.NewKVStoreService(key), []byte{0}, &Params{}, func(p *Params) error { return p.Validate() })
	require.NoError(t, err)
	initial := DefaultParams()
	require.NoError(t, params.Set(ctx, &initial))
	cached, _ := ctx.CacheContext()
	changed := NewParams("mcn", "Pending network coin", sdkmath.NewInt(500))
	require.NoError(t, params.Set(cached, &changed))
	pending, err := params.Get(cached)
	require.NoError(t, err)
	require.Equal(t, "mcn", pending.BaseDenom)
	stored, err := params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, initial.BaseDenom, stored.BaseDenom)
	require.True(t, stored.BaseVolume.IsZero(), "configuration must not invent a mainnet allocation")
	for _, volume := range []sdkmath.Int{{}, sdkmath.NewInt(-1)} {
		invalid := NewParams(initial.BaseDenom, initial.BaseTitle, volume)
		require.Error(t, params.Set(ctx, &invalid))
	}
	stored, err = params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, initial.BaseTitle, stored.BaseTitle)
}
