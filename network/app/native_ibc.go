package app

import (
	"errors"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	porttypes "github.com/cosmos/ibc-go/v10/modules/core/05-port/types"
	ibcexported "github.com/cosmos/ibc-go/v10/modules/core/exported"
)

// Keep this list explicit. IBC namespaces also contain administrative routes
// and v2 messages whose execution is outside the native ICS-20 v1 profile.
func nativeIBCV1Message(typeURL string) bool {
	switch typeURL {
	case "/ibc.core.client.v1.MsgCreateClient",
		"/ibc.core.client.v1.MsgUpdateClient",
		"/ibc.core.client.v1.MsgUpgradeClient",
		"/ibc.core.connection.v1.MsgConnectionOpenInit",
		"/ibc.core.connection.v1.MsgConnectionOpenTry",
		"/ibc.core.connection.v1.MsgConnectionOpenAck",
		"/ibc.core.connection.v1.MsgConnectionOpenConfirm",
		"/ibc.core.channel.v1.MsgChannelOpenInit",
		"/ibc.core.channel.v1.MsgChannelOpenTry",
		"/ibc.core.channel.v1.MsgChannelOpenAck",
		"/ibc.core.channel.v1.MsgChannelOpenConfirm",
		"/ibc.core.channel.v1.MsgChannelCloseInit",
		"/ibc.core.channel.v1.MsgChannelCloseConfirm",
		"/ibc.core.channel.v1.MsgRecvPacket",
		"/ibc.core.channel.v1.MsgAcknowledgement",
		"/ibc.core.channel.v1.MsgTimeout",
		"/ibc.core.channel.v1.MsgTimeoutOnClose",
		"/ibc.applications.transfer.v1.MsgTransfer":
		return true
	}
	return false
}

func nativeIBCWithdrawalSeconds(duration time.Duration) (int64, error) {
	if duration <= 0 || duration%time.Second != 0 {
		return 0, errors.New("native IBC v1 requires a positive whole-second DEL unbonding duration")
	}
	return int64(duration / time.Second), nil
}

func (app *App) verifyNativeIBCState(ctx sdk.Context, profile WithdrawalProfile) error {
	if !profile.EnableNativeIBCV1 {
		return nil
	}
	if !profile.EnableNativeConsensus {
		return errors.New("native IBC v1 requires native consensus")
	}
	native, err := app.NativeValidatorKeeper.GetParams(ctx)
	if err != nil {
		return err
	}
	if _, err := nativeIBCWithdrawalSeconds(native.UndelegationTime); err != nil {
		return err
	}
	staking, err := app.StakingKeeper.GetParams(ctx)
	if err != nil {
		return err
	}
	if staking.UnbondingTime != native.UndelegationTime {
		return errors.New("SDK staking query differs from the attested native unbonding duration")
	}
	return nil
}

// Native ICS-20 uses the bank transfer callbacks. The normal route includes
// automatic ERC-20 registration and EVM contract callbacks, neither of which
// is admitted by the native profile. Channel negotiation remains common.
type nativeIBCTransferModule struct {
	porttypes.Middleware
	bank porttypes.IBCModule
	app  *App
}

var _ porttypes.Middleware = nativeIBCTransferModule{}
var _ porttypes.PacketDataUnmarshaler = nativeIBCTransferModule{}

func (m nativeIBCTransferModule) packetModule(ctx sdk.Context) porttypes.IBCModule {
	if m.app.usesNativeConsensus(ctx) && m.app.withdrawals != nil && m.app.withdrawals.profile.EnableNativeIBCV1 {
		return m.bank
	}
	return m.Middleware
}

func (m nativeIBCTransferModule) OnRecvPacket(ctx sdk.Context, version string, packet channeltypes.Packet, relayer sdk.AccAddress) ibcexported.Acknowledgement {
	return m.packetModule(ctx).OnRecvPacket(ctx, version, packet, relayer)
}

func (m nativeIBCTransferModule) OnAcknowledgementPacket(ctx sdk.Context, version string, packet channeltypes.Packet, acknowledgement []byte, relayer sdk.AccAddress) error {
	return m.packetModule(ctx).OnAcknowledgementPacket(ctx, version, packet, acknowledgement, relayer)
}

func (m nativeIBCTransferModule) OnTimeoutPacket(ctx sdk.Context, version string, packet channeltypes.Packet, relayer sdk.AccAddress) error {
	return m.packetModule(ctx).OnTimeoutPacket(ctx, version, packet, relayer)
}

func (m nativeIBCTransferModule) UnmarshalPacketData(ctx sdk.Context, portID, channelID string, data []byte) (any, string, error) {
	module, ok := m.packetModule(ctx).(porttypes.PacketDataUnmarshaler)
	if !ok {
		return nil, "", errors.New("ICS-20 transfer route does not support packet decoding")
	}
	return module.UnmarshalPacketData(ctx, portID, channelID, data)
}
