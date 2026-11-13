//go:build mcnd_native_sdk_migration_v1 && mcnd_native_coin_index_migration_ci

package keeper

import (
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const coinIndexMigrationEnabled = true

// MigrateAddressIndexV1ToV2 replaces the textual DRC20 secondary index with
// fixed-width EVM addresses. Primary coin rows and balances are preserved.
func (k *StateKeeper) MigrateAddressIndexV1ToV2(ctx sdk.Context) error {
	child, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	index, err := activeAddressIndex(state)
	if err != nil {
		return err
	}
	if index.prefix != legacyAddressIndexPrefix {
		return errors.New("coin address index migration requires schema v1")
	}
	genesis, err := k.ExportGenesis(child)
	if err != nil {
		return err
	}
	compact := addressIndex{prefix: compactAddressIndexPrefix}
	for _, coin := range genesis.Coins {
		if coin.DRC20Contract == "" {
			continue
		}
		if err := state.Set(compact.key(coin.DRC20Contract), []byte(coin.Denom)); err != nil {
			return err
		}
		if err := state.Delete(index.key(coin.DRC20Contract)); err != nil {
			return err
		}
	}
	if err := initAddressIndex(state); err != nil {
		return err
	}
	commit()
	return nil
}
