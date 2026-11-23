//go:build mintcandy_mainnet

package app

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	transfertypes "github.com/cosmos/ibc-go/v10/modules/apps/transfer/types"
	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	channeltypesv2 "github.com/cosmos/ibc-go/v10/modules/core/04-channel/v2/types"
	"github.com/stretchr/testify/require"
)

func TestMainnetICS20DenomAdmission(t *testing.T) {
	for _, denom := range []string{"tmsp", "transfer/channel-2/tmsp", "transfer/channel-7/transfer/channel-2/tMSP", "wtmsp"} {
		t.Run(denom, func(t *testing.T) {
			data := transfertypes.NewFungibleTokenPacketData(denom, "1", "sender", "recipient", "")
			ack := (nativeIBCTransferModule{}).OnRecvPacket(sdk.Context{}, transfertypes.V1,
				channeltypes.Packet{Data: data.GetBytes()}, nil)
			require.False(t, ack.Success())

			wire, err := transfertypes.MarshalPacketData(data, transfertypes.V1, transfertypes.EncodingProtobuf)
			require.NoError(t, err)
			result := (nativeIBCTransferModuleV2{}).OnRecvPacket(sdk.Context{}, "07-tendermint-0", "07-tendermint-1", 1,
				channeltypesv2.Payload{Value: wire, Version: transfertypes.V1, Encoding: transfertypes.EncodingProtobuf}, nil)
			require.Equal(t, channeltypesv2.PacketStatus_Failure, result.Status)
		})
	}
	for _, denom := range []string{"msp", "transfer/channel-9/msp", "transfer/channel-3/uatom"} {
		data := transfertypes.NewFungibleTokenPacketData(denom, "1", "sender", "recipient", "")
		require.NoError(t, checkMainnetICS20Denom(data.GetBytes(), transfertypes.V1, ""))
	}
}
