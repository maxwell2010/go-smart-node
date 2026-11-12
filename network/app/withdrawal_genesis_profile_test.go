package app

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestWithdrawalGenesisProfileAcceptsBoundedIndentation(t *testing.T) {
	profile := WithdrawalProfile{
		Registry: common.HexToAddress("0x01"), Gateway: common.HexToAddress("0x02"),
		Library: common.HexToAddress("0x03"), NFTCenter: common.HexToAddress("0x04"),
		PositionVault: common.HexToAddress("0x05"), MasterValidator: common.HexToAddress("0x06"),
		WrappedDEL: common.HexToAddress("0x07"), SettlementOnly: true,
		EnableDELDelegation: true, EnableDELHold: true, EnableDELHoldReset: true, EnableNFTDelegation: true,
		EnableNativeConsensus: true, EnableNativeFeeRewards: true, EnableNativeIBCV1: true,
		EnableNativeGovernance: true, EnableNativeUpgradeGovernance: true,
		EnableNativeOrdinaryDELDowntime: true, EnableNativeHeldDELDowntime: true,
		EnableNativeNFTDowntime: true, EnableNativeDuplicateVote: true,
	}
	compact, err := json.Marshal(profile)
	require.NoError(t, err)
	require.LessOrEqual(t, len(compact), maxWithdrawalProfileJSON)
	document, err := json.MarshalIndent(map[string]map[string]WithdrawalProfile{
		"app_state": {WithdrawalGenesisKey: profile},
	}, "", "  ")
	require.NoError(t, err)
	var decoded map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document, &decoded))
	pretty := decoded["app_state"][WithdrawalGenesisKey]
	require.Greater(t, len(pretty), maxWithdrawalProfileJSON, "exercise the normal formatting that formerly failed validation")
	got, err := parseWithdrawalGenesisProfile(pretty)
	require.NoError(t, err)
	require.Equal(t, profile, *got)

	excessWhitespace := append(bytes.Repeat([]byte(" "), 4*maxWithdrawalProfileJSON), compact...)
	_, err = parseWithdrawalGenesisProfile(excessWhitespace)
	require.ErrorContains(t, err, "exceeds size limit")

	excessContent := append([]byte("{"), bytes.Repeat([]byte(`"settlement_only":true,`), 64)...)
	excessContent = append(excessContent, []byte(`"settlement_only":true}`)...)
	_, err = parseWithdrawalGenesisProfile(excessContent)
	require.ErrorContains(t, err, "exceeds size limit")
}
