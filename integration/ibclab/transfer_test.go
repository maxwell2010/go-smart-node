package ibclab_test

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	transfer "github.com/cosmos/ibc-go/v6/modules/apps/transfer/types"
	client "github.com/cosmos/ibc-go/v6/modules/core/02-client/types"
	channel "github.com/cosmos/ibc-go/v6/modules/core/04-channel/types"
	ibctesting "github.com/cosmos/ibc-go/v6/testing"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// This protocol lab uses two real SDK applications and light-client proofs.
// It is not a claim that Decimal's custom staking/ante/EVM integration is ready.
func setup(t *testing.T) (*ibctesting.Coordinator, *ibctesting.Path) {
	c := ibctesting.NewCoordinator(t, 2)
	p := ibctesting.NewPath(c.GetChain(ibctesting.GetChainID(1)), c.GetChain(ibctesting.GetChainID(2)))
	for _, e := range []*ibctesting.Endpoint{p.EndpointA, p.EndpointB} {
		e.ChannelConfig.PortID = transfer.PortID
		e.ChannelConfig.Version = transfer.Version
		e.ChannelConfig.Order = channel.UNORDERED
	}
	c.Setup(p)
	return c, p
}

func send(t *testing.T, from, to *ibctesting.Endpoint, coin sdk.Coin, receiver string, timestamp uint64) channel.Packet {
	height := to.Chain.GetTimeoutHeight()
	if timestamp != 0 {
		height = client.ZeroHeight()
	}
	msg := transfer.NewMsgTransfer(from.ChannelConfig.PortID, from.ChannelID, coin, from.Chain.SenderAccount.GetAddress().String(), receiver, height, timestamp, "")
	result, err := from.Chain.SendMsgs(msg)
	require.NoError(t, err)
	packet, err := ibctesting.ParsePacketFromEvents(result.GetEvents())
	require.NoError(t, err)
	return packet
}

func TestICS20SendReturnAndDuplicate(t *testing.T) {
	_, p := setup(t)
	a, b := p.EndpointA, p.EndpointB
	sender := a.Chain.SenderAccount.GetAddress()
	receiver := b.Chain.SenderAccount.GetAddress()
	bankA := a.Chain.GetSimApp().BankKeeper
	bankB := b.Chain.GetSimApp().BankKeeper
	amount := sdk.NewInt64Coin(sdk.DefaultBondDenom, 100)
	before := bankA.GetBalance(a.Chain.GetContext(), sender, amount.Denom)
	packet := send(t, a, b, amount, receiver.String(), 0)
	require.NoError(t, p.RelayPacket(packet))
	escrow := transfer.GetEscrowAddress(a.ChannelConfig.PortID, a.ChannelID)
	require.Equal(t, amount, bankA.GetBalance(a.Chain.GetContext(), escrow, amount.Denom))
	voucher := transfer.GetTransferCoin(b.ChannelConfig.PortID, b.ChannelID, amount.Denom, amount.Amount)
	require.Equal(t, voucher, bankB.GetBalance(b.Chain.GetContext(), receiver, voucher.Denom))
	require.Error(t, p.RelayPacket(packet))
	require.Equal(t, voucher, bankB.GetBalance(b.Chain.GetContext(), receiver, voucher.Denom))
	back := send(t, b, a, voucher, sender.String(), 0)
	require.NoError(t, p.RelayPacket(back))
	require.Equal(t, before, bankA.GetBalance(a.Chain.GetContext(), sender, amount.Denom))
	require.True(t, bankA.GetBalance(a.Chain.GetContext(), escrow, amount.Denom).IsZero())
	require.True(t, bankB.GetSupply(b.Chain.GetContext(), voucher.Denom).IsZero())
}

func TestICS20ErrorAcknowledgementRefund(t *testing.T) {
	_, p := setup(t)
	a, b := p.EndpointA, p.EndpointB
	sender := a.Chain.SenderAccount.GetAddress()
	coin := sdk.NewInt64Coin(sdk.DefaultBondDenom, 100)
	bank := a.Chain.GetSimApp().BankKeeper
	before := bank.GetBalance(a.Chain.GetContext(), sender, coin.Denom)
	packet := send(t, a, b, coin, "not-an-address", 0)
	require.NoError(t, p.RelayPacket(packet))
	require.Equal(t, before, bank.GetBalance(a.Chain.GetContext(), sender, coin.Denom))
	require.Error(t, p.RelayPacket(packet))
	require.Equal(t, before, bank.GetBalance(a.Chain.GetContext(), sender, coin.Denom))
}

func TestICS20TimeoutRefund(t *testing.T) {
	c, p := setup(t)
	a, b := p.EndpointA, p.EndpointB
	sender := a.Chain.SenderAccount.GetAddress()
	coin := sdk.NewInt64Coin(sdk.DefaultBondDenom, 100)
	bank := a.Chain.GetSimApp().BankKeeper
	before := bank.GetBalance(a.Chain.GetContext(), sender, coin.Denom)
	packet := send(t, a, b, coin, b.Chain.SenderAccount.GetAddress().String(), uint64(c.CurrentTime.Add(time.Minute).UnixNano()))
	c.IncrementTimeBy(2 * time.Minute)
	c.CommitBlock(b.Chain)
	require.NoError(t, a.UpdateClient())
	require.NoError(t, a.TimeoutPacket(packet))
	require.Equal(t, before, bank.GetBalance(a.Chain.GetContext(), sender, coin.Denom))
	// Duplicate timeout may be accepted as an idempotent no-op by this IBC version.
	_ = a.TimeoutPacket(packet)
	require.Equal(t, before, bank.GetBalance(a.Chain.GetContext(), sender, coin.Denom))
}
