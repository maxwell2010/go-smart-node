package ante

import (
	"bitbucket.org/decimalteam/go-smart-node/integrationconfig"
	fee "bitbucket.org/decimalteam/go-smart-node/x/fee/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	bank "github.com/cosmos/cosmos-sdk/x/bank/types"
	upgrade "github.com/cosmos/cosmos-sdk/x/upgrade/types"
	transfer "github.com/cosmos/ibc-go/v6/modules/apps/transfer/types"
	client "github.com/cosmos/ibc-go/v6/modules/core/02-client/types"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestExperimentalIBCAdmissionAllMessages(t *testing.T) {
	valid := &client.MsgUpdateClient{}
	transferMsg := func(denom string) *transfer.MsgTransfer {
		return &transfer.MsgTransfer{Token: sdk.NewInt64Coin(denom, 1)}
	}
	require.True(t, experimentalNativeMessagesAllowed([]sdk.Msg{valid, transferMsg("del")}))
	require.True(t, experimentalNativeMessagesAllowed([]sdk.Msg{transferMsg("ibc/" + strings.Repeat("A", 64))}))
	for _, msgs := range [][]sdk.Msg{nil, {}, {valid, &bank.MsgSend{}}, {&upgrade.MsgCancelUpgrade{}, &authz.MsgExec{}}, {transferMsg("mintcandy")}, {transferMsg("ibc/" + strings.Repeat("a", 64))}, {nil}} {
		require.False(t, experimentalNativeMessagesAllowed(msgs))
	}
}
func TestExperimentalIBCFeeScheduleIsBuildGated(t *testing.T) {
	amount, err := CalculateFee(nil, []sdk.Msg{&client.MsgUpdateClient{}}, 0, sdk.OneDec(), fee.DefaultParams())
	if integrationconfig.Enabled {
		require.NoError(t, err)
		require.True(t, amount.IsPositive())
	} else {
		require.Error(t, err)
	}
}
