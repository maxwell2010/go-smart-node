package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	transfertypes "github.com/cosmos/ibc-go/v10/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	connectiontypes "github.com/cosmos/ibc-go/v10/modules/core/03-connection/types"
	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	channelv2types "github.com/cosmos/ibc-go/v10/modules/core/04-channel/v2/types"
	porttypes "github.com/cosmos/ibc-go/v10/modules/core/05-port/types"
	ibcexported "github.com/cosmos/ibc-go/v10/modules/core/exported"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestNativeIBCV1CircuitAllowsOnlyProtocolMessages(t *testing.T) {
	profile := WithdrawalProfile{EnableNativeConsensus: true, EnableNativeIBCV1: true}
	app := &App{withdrawals: &withdrawalController{profile: profile}}
	circuit := nativeConsensusCircuit{app: app}
	ctx := context.Background()
	messages := []sdk.Msg{
		&clienttypes.MsgCreateClient{}, &clienttypes.MsgUpdateClient{}, &clienttypes.MsgUpgradeClient{},
		&connectiontypes.MsgConnectionOpenInit{}, &connectiontypes.MsgConnectionOpenTry{},
		&connectiontypes.MsgConnectionOpenAck{}, &connectiontypes.MsgConnectionOpenConfirm{},
		&channeltypes.MsgChannelOpenInit{}, &channeltypes.MsgChannelOpenTry{},
		&channeltypes.MsgChannelOpenAck{}, &channeltypes.MsgChannelOpenConfirm{},
		&channeltypes.MsgChannelCloseInit{}, &channeltypes.MsgChannelCloseConfirm{},
		&channeltypes.MsgRecvPacket{}, &channeltypes.MsgAcknowledgement{},
		&channeltypes.MsgTimeout{}, &channeltypes.MsgTimeoutOnClose{},
		&transfertypes.MsgTransfer{},
	}
	for _, msg := range messages {
		url := sdk.MsgTypeURL(msg)
		allowed, err := circuit.IsAllowed(ctx, url)
		require.NoError(t, err)
		require.True(t, allowed, "%s", url)
	}
	for _, url := range []string{
		sdk.MsgTypeURL(&clienttypes.MsgRecoverClient{}),
		sdk.MsgTypeURL(&clienttypes.MsgIBCSoftwareUpgrade{}),
		sdk.MsgTypeURL(&clienttypes.MsgUpdateParams{}),
		sdk.MsgTypeURL(&stakingtypes.MsgDelegate{}),
		sdk.MsgTypeURL(&govtypes.MsgSubmitProposal{}),
		sdk.MsgTypeURL(&channelv2types.MsgSendPacket{}),
		sdk.MsgTypeURL(&channelv2types.MsgRecvPacket{}),
		"/ibc.applications.transfer.v1.MsgUpdateParams",
		"/ibc.core.client.v1.MsgArbitraryFutureOperation",
	} {
		allowed, err := circuit.IsAllowed(ctx, url)
		require.NoError(t, err)
		require.False(t, allowed, "%s", url)
	}
	app.withdrawals.profile.EnableNativeIBCV1 = false
	for _, msg := range messages {
		allowed, err := circuit.IsAllowed(ctx, sdk.MsgTypeURL(msg))
		require.NoError(t, err)
		require.False(t, allowed, "%T", msg)
	}
}

func TestNativeIBCV1GenesisRequiresNativeConsensusAndWholeSecondDelay(t *testing.T) {
	profile := WithdrawalProfile{
		Registry: common.HexToAddress("0x01"), Gateway: common.HexToAddress("0x02"),
		Library: common.HexToAddress("0x03"), NFTCenter: common.HexToAddress("0x04"),
		PositionVault: common.HexToAddress("0x05"), MasterValidator: common.HexToAddress("0x06"),
		WrappedDEL: common.HexToAddress("0x07"), EnableDELDelegation: true, EnableNativeIBCV1: true,
	}
	raw, err := json.Marshal(profile)
	require.NoError(t, err)
	_, err = parseWithdrawalGenesisProfile(raw)
	require.ErrorContains(t, err, "requires native consensus")
	_, err = (&App{}).newWithdrawalController(profile)
	require.ErrorContains(t, err, "requires native consensus")

	profile.EnableNativeConsensus = true
	raw, err = json.Marshal(profile)
	require.NoError(t, err)
	decoded, err := parseWithdrawalGenesisProfile(raw)
	require.NoError(t, err)
	require.True(t, decoded.EnableNativeIBCV1)

	seconds, err := nativeIBCWithdrawalSeconds(28 * 24 * time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 28*24*60*60, seconds)
	for _, delay := range []time.Duration{0, -time.Second, 1500 * time.Millisecond} {
		_, err := nativeIBCWithdrawalSeconds(delay)
		require.ErrorContains(t, err, "positive whole-second")
	}
}

type nativeIBCProbeModule struct {
	porttypes.Middleware
	calls []string
}

func (p *nativeIBCProbeModule) OnRecvPacket(sdk.Context, string, channeltypes.Packet, sdk.AccAddress) ibcexported.Acknowledgement {
	p.calls = append(p.calls, "receive")
	return channeltypes.NewResultAcknowledgement([]byte{1})
}

func (p *nativeIBCProbeModule) OnAcknowledgementPacket(sdk.Context, string, channeltypes.Packet, []byte, sdk.AccAddress) error {
	p.calls = append(p.calls, "acknowledge")
	return nil
}

func (p *nativeIBCProbeModule) OnTimeoutPacket(sdk.Context, string, channeltypes.Packet, sdk.AccAddress) error {
	p.calls = append(p.calls, "timeout")
	return nil
}

func (p *nativeIBCProbeModule) UnmarshalPacketData(sdk.Context, string, string, []byte) (any, string, error) {
	p.calls = append(p.calls, "decode")
	return nil, "ics20-1", nil
}

func TestNativeIBCV1PacketRouteSelectsBankCallbacks(t *testing.T) {
	app := &App{}
	legacy, bank := &nativeIBCProbeModule{}, &nativeIBCProbeModule{}
	module := nativeIBCTransferModule{Middleware: legacy, bank: bank, app: app}
	ctx := sdk.Context{}.WithContext(context.Background())
	packet := channeltypes.Packet{}
	exercise := func() {
		require.True(t, module.OnRecvPacket(ctx, "ics20-1", packet, nil).Success())
		require.NoError(t, module.OnAcknowledgementPacket(ctx, "ics20-1", packet, nil, nil))
		require.NoError(t, module.OnTimeoutPacket(ctx, "ics20-1", packet, nil))
		_, version, err := module.UnmarshalPacketData(ctx, "transfer", "channel-0", nil)
		require.NoError(t, err)
		require.Equal(t, "ics20-1", version)
	}
	want := []string{"receive", "acknowledge", "timeout", "decode"}
	exercise()
	require.Equal(t, want, legacy.calls)
	require.Empty(t, bank.calls)
	app.withdrawals = &withdrawalController{profile: WithdrawalProfile{EnableNativeConsensus: true, EnableNativeIBCV1: true}}
	exercise()
	require.Equal(t, want, legacy.calls)
	require.Equal(t, want, bank.calls)
}
