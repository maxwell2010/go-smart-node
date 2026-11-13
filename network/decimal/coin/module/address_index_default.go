//go:build !mcnd_native_sdk_migration_v1 || !mcnd_native_coin_index_migration_ci

package module

import sdkmodule "github.com/cosmos/cosmos-sdk/types/module"

const addressIndexConsensusVersion = 1

func (AppModule) registerAddressIndexMigration(sdkmodule.Configurator) {}
