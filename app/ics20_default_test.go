//go:build !experimental_ibc

package app

import (
	transfer "github.com/cosmos/ibc-go/v6/modules/apps/transfer/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDefaultBinaryDoesNotEnableICS20(t *testing.T) {
	require.NotContains(t, ModuleBasics, transfer.ModuleName)
	require.NotContains(t, maccPerms, transfer.ModuleName)
	dsc, _, _, _ := reliabilityNew(t)
	require.Nil(t, dsc.GetKey(transfer.StoreKey))
}
