package app

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestNativeLiquidityPoolUpgradePin(t *testing.T) {
	mainnetPin := NativeLiquidityPoolProfile{
		Address:     nativeLiquidityPoolUpgradeAddress(identity.MainnetChainID),
		CUSDT:       common.HexToAddress("0x16c1e12e5f0f338e9521082fd671559845406b96"),
		CUSDTOwner:  common.HexToAddress("0x40900a48273644768c09183e00e43528c17a29f6"),
		Initializer: common.HexToAddress("0x40900a48273644768c09183e00e43528c17a29f6"),
	}
	mainnetProfile := WithdrawalProfile{
		WrappedDEL:                    common.HexToAddress("0xDF85fb0aE97E5A753A732a8Eb0c6180282e274AC"),
		TokenCenter:                   common.HexToAddress("0x1001"),
		EnableNativeConsensus:         true,
		EnableNativeUpgradeGovernance: true,
	}
	testnetProfile := mainnetProfile
	testnetProfile.WrappedDEL = common.HexToAddress("0x2001")
	testnetPin := NativeLiquidityPoolProfile{
		Address:     nativeLiquidityPoolUpgradeAddress(identity.TestnetChainID),
		CUSDT:       common.HexToAddress("0x2002"),
		CUSDTOwner:  common.HexToAddress("0x2003"),
		Initializer: common.HexToAddress("0x2003"),
	}
	require.NotEqual(t, mainnetPin.Address, testnetPin.Address)
	require.Error(t, validateNativeLiquidityPoolUpgradePin("mintcandy_990098-2", testnetProfile, testnetPin))
	switch identity.ProfileChainID {
	case identity.MainnetChainID:
		require.NoError(t, validateNativeLiquidityPoolUpgradePin(identity.MainnetChainID, mainnetProfile, mainnetPin))
		require.Error(t, validateNativeLiquidityPoolUpgradePin(identity.TestnetChainID, testnetProfile, testnetPin))

		wrong := mainnetPin
		wrong.CUSDT = testnetPin.CUSDT
		require.Error(t, validateNativeLiquidityPoolUpgradePin(identity.MainnetChainID, mainnetProfile, wrong))
		wrong = mainnetPin
		wrong.CUSDTOwner, wrong.Initializer = testnetPin.CUSDTOwner, testnetPin.CUSDTOwner
		require.Error(t, validateNativeLiquidityPoolUpgradePin(identity.MainnetChainID, mainnetProfile, wrong))
		wrongProfile := mainnetProfile
		wrongProfile.WrappedDEL = testnetProfile.WrappedDEL
		require.Error(t, validateNativeLiquidityPoolUpgradePin(identity.MainnetChainID, wrongProfile, mainnetPin))
	case identity.TestnetChainID:
		require.NoError(t, validateNativeLiquidityPoolUpgradePin(identity.TestnetChainID, testnetProfile, testnetPin))
		require.Error(t, validateNativeLiquidityPoolUpgradePin(identity.MainnetChainID, mainnetProfile, mainnetPin))
		wrong := testnetPin
		wrong.CUSDT = common.Address{}
		require.Error(t, validateNativeLiquidityPoolUpgradePin(identity.TestnetChainID, testnetProfile, wrong))
		wrong = testnetPin
		wrong.Address = mainnetPin.Address
		require.Error(t, validateNativeLiquidityPoolUpgradePin(identity.TestnetChainID, testnetProfile, wrong))
		wrong = testnetPin
		wrong.Initializer = mainnetPin.Initializer
		require.Error(t, validateNativeLiquidityPoolUpgradePin(identity.TestnetChainID, testnetProfile, wrong))
		testnetProfile.NativePool = testnetPin
		require.Error(t, validateNativeLiquidityPoolUpgradePin(identity.TestnetChainID, testnetProfile, testnetPin))
	default:
		require.Error(t, validateNativeLiquidityPoolUpgradePin(identity.MainnetChainID, mainnetProfile, mainnetPin))
		require.Error(t, validateNativeLiquidityPoolUpgradePin(identity.TestnetChainID, testnetProfile, testnetPin))
	}
}
