//go:build mcnd_native_sdk_migration_v1 && mcnd_native_coin_index_migration_ci

package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddressIndexMigrationPreservesCoinState(t *testing.T) {
	ctx, service, keeper, coin := fixture(t)
	require.NoError(t, keeper.SetCoin(ctx, coin))
	state := service.OpenKVStore(ctx)
	legacy := addressKey(coin.DRC20Contract)
	compact := addressIndex{prefix: compactAddressIndexPrefix}.key(coin.DRC20Contract)
	before, err := keeper.ExportGenesis(ctx)
	require.NoError(t, err)
	indexed, err := state.Get(legacy)
	require.NoError(t, err)
	require.Equal(t, []byte(coin.Denom), indexed)
	indexed, err = state.Get(compact)
	require.NoError(t, err)
	require.Nil(t, indexed)

	child, commit := ctx.CacheContext()
	require.NoError(t, keeper.MigrateAddressIndexV1ToV2(child))
	indexed, err = state.Get(legacy)
	require.NoError(t, err)
	require.Equal(t, []byte(coin.Denom), indexed, "migration must follow the enclosing upgrade transaction")
	commit()

	indexed, err = state.Get(legacy)
	require.NoError(t, err)
	require.Nil(t, indexed)
	indexed, err = state.Get(compact)
	require.NoError(t, err)
	require.Equal(t, []byte(coin.Denom), indexed)
	schema, err := state.Get([]byte{addressIndexSchemaPrefix})
	require.NoError(t, err)
	require.Equal(t, compactAddressIndexSchema, schema)
	stored, err := keeper.GetCoinByDRC(ctx, coin.DRC20Contract)
	require.NoError(t, err)
	require.Equal(t, coin, stored)
	after, err := keeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.ErrorContains(t, keeper.MigrateAddressIndexV1ToV2(ctx), "requires schema v1")
}
