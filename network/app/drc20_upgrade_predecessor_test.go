//go:build mintcandy_mainnet && !mintcandy_drc20_issuance

package app

import (
	"testing"

	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestDRC20IssuanceBinaryRole(t *testing.T) {
	app := testApp(t, identity.MainnetChainID, identity.MainnetEVMChainID)
	require.True(t, app.UpgradeKeeper.HasHandler(nativeSDKMigrationPlan))
	require.False(t, app.UpgradeKeeper.HasHandler(drc20IssuancePlan))
}
