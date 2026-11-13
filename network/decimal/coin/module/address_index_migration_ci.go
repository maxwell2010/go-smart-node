//go:build mcnd_native_sdk_migration_v1 && mcnd_native_coin_index_migration_ci

package module

import (
	sdkmodule "github.com/cosmos/cosmos-sdk/types/module"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
)

const addressIndexConsensusVersion = 2

func (m AppModule) registerAddressIndexMigration(cfg sdkmodule.Configurator) {
	if err := cfg.RegisterMigration(types.ModuleName, 1, m.keeper.MigrateAddressIndexV1ToV2); err != nil {
		panic(err)
	}
}
