package ante

import (
	cmdcfg "bitbucket.org/decimalteam/go-smart-node/cmd/config"
	feetypes "bitbucket.org/decimalteam/go-smart-node/x/fee/types"
	validatortypes "bitbucket.org/decimalteam/go-smart-node/x/validator/types"
	"encoding/hex"
	sdk "github.com/cosmos/cosmos-sdk/types"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"
	transfer "github.com/cosmos/ibc-go/v6/modules/apps/transfer/types"
	client "github.com/cosmos/ibc-go/v6/modules/core/02-client/types"
	connection "github.com/cosmos/ibc-go/v6/modules/core/03-connection/types"
	channel "github.com/cosmos/ibc-go/v6/modules/core/04-channel/types"
	"strings"
)

func experimentalIBCMessage(msg sdk.Msg) bool {
	switch m := msg.(type) {
	case *transfer.MsgTransfer:
		if m==nil {return false}
        if m.Token.Denom == cmdcfg.BaseDenom {
			return true
		}
		if !strings.HasPrefix(m.Token.Denom, "ibc/") || len(m.Token.Denom) != 68 {
			return false
		}
		_, err := hex.DecodeString(m.Token.Denom[4:])
		return err == nil && m.Token.Denom[4:] == strings.ToUpper(m.Token.Denom[4:])
	case *client.MsgCreateClient, *client.MsgUpdateClient, *client.MsgUpgradeClient, *client.MsgSubmitMisbehaviour,
		*connection.MsgConnectionOpenInit, *connection.MsgConnectionOpenTry, *connection.MsgConnectionOpenAck, *connection.MsgConnectionOpenConfirm,
		*channel.MsgChannelOpenInit, *channel.MsgChannelOpenTry, *channel.MsgChannelOpenAck, *channel.MsgChannelOpenConfirm,
		*channel.MsgChannelCloseInit, *channel.MsgChannelCloseConfirm, *channel.MsgRecvPacket, *channel.MsgAcknowledgement, *channel.MsgTimeout, *channel.MsgTimeoutOnClose:
		return true
	}
	return false
}

// No allowed sibling can admit an unapproved or nested message on isolated chains.
func experimentalNativeMessagesAllowed(msgs []sdk.Msg) bool {
	if len(msgs) == 0 {
		return false
	}
	for _, msg := range msgs {
		if experimentalIBCMessage(msg) {
			continue
		}
		switch msg.(type) {
		case *upgradetypes.MsgSoftwareUpgrade, *upgradetypes.MsgCancelUpgrade, *validatortypes.MsgSetOnline,
			*validatortypes.MsgHaltChain, *validatortypes.MsgResumeChain, *validatortypes.MsgFreezeChain, *validatortypes.MsgUnfreezeChain, *feetypes.MsgUpdateCoinPrices:
		default:
			return false
		}
	}
	return true
}
