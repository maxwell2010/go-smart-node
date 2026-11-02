//go:build test

package app

import evmtypes "github.com/cosmos/evm/x/vm/types"

func resetEVMConfigurationForTest() { evmtypes.NewEVMConfigurator().ResetTestConfig() }
