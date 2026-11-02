//go:build test

package app

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	transfer "github.com/cosmos/ibc-go/v10/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	host "github.com/cosmos/ibc-go/v10/modules/core/24-host"
	"github.com/maxwell2010/go-smart-node/network/identity"
	ibctesting "github.com/maxwell2010/go-smart-node/network/internal/ibcharness"
	"github.com/stretchr/testify/require"
)

// The official EVM test tag permits multiple process-global chain configs.
// It does not replace the separate production-binary process/relayer gate.
type fundedIBCConfig struct {
	client.TxConfig
	evmID uint64
}
type fundedIBCBuilder struct{ client.TxBuilder }

var nativeIBCTransactionFee = sdk.NewInt64Coin(identity.BaseDenom, 10_000_000_000_000_000)

func (c fundedIBCConfig) NewTxBuilder() client.TxBuilder {
	if err := evmtypes.SetChainConfig(evmtypes.DefaultChainConfig(c.evmID)); err != nil {
		panic(err)
	}
	return fundedIBCBuilder{c.TxConfig.NewTxBuilder()}
}
func (b fundedIBCBuilder) SetFeeAmount(_ sdk.Coins) {
	b.TxBuilder.SetFeeAmount(sdk.NewCoins(nativeIBCTransactionFee))
}

func newOwnIBCChain(t *testing.T, coordinator *ibctesting.Coordinator, evmID uint64) *ibctesting.TestChain {
	t.Helper()
	chainID := fmt.Sprintf("mintcandy_%d-1", evmID)
	a := testApp(t, chainID, evmID)
	_, pv := cmttypes.RandValidator(false, 1)
	pub, err := pv.GetPubKey()
	require.NoError(t, err)
	vals := cmttypes.NewValidatorSet([]*cmttypes.Validator{cmttypes.NewValidator(pub, 1)})
	// Native Cosmos secp256k1, as used by ordinary Cosmos relayers, is exercised
	// independently of the Ethereum signer used by the EVM transaction test.
	key := secp256k1.GenPrivKey()
	account := authtypes.NewBaseAccount(key.PubKey().Address().Bytes(), key.PubKey(), 0, 0)
	relayerKey := secp256k1.GenPrivKey()
	relayerAccount := authtypes.NewBaseAccount(relayerKey.PubKey().Address().Bytes(), relayerKey.PubKey(), 1, 0)
	funds := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, math.NewInt(1_000_000).Mul(math.NewInt(1_000_000_000_000_000_000))))
	genesis := fundedTestGenesis(t, a, vals, []authtypes.GenesisAccount{account, relayerAccount}, banktypes.Balance{Address: account.GetAddress().String(), Coins: funds}, banktypes.Balance{Address: relayerAccount.GetAddress().String(), Coins: funds})
	data, err := json.Marshal(genesis)
	require.NoError(t, err)
	_, err = a.InitChain(&abci.RequestInitChain{ChainId: chainID, Time: coordinator.CurrentTime, AppStateBytes: data, ConsensusParams: simtestutil.DefaultConsensusParams})
	require.NoError(t, err)
	chain := &ibctesting.TestChain{
		TB: t, Coordinator: coordinator, App: a, ChainID: chainID,
		ProposedHeader: cmtproto.Header{ChainID: chainID, Height: 1, Time: coordinator.CurrentTime},
		TxConfig:       fundedIBCConfig{a.TxConfig(), evmID}, Codec: a.AppCodec(), Vals: vals, NextVals: vals,
		Signers: map[string]cmttypes.PrivValidator{pub.Address().String(): pv}, TrustedValidators: map[uint64]*cmttypes.ValidatorSet{},
		SenderPrivKey: key, SenderAccount: account, SenderAccounts: []ibctesting.SenderAccount{{SenderPrivKey: key, SenderAccount: account}, {SenderPrivKey: relayerKey, SenderAccount: relayerAccount}},
	}
	coordinator.Chains[chainID] = chain
	chain.NextBlock()
	return chain
}

func TestOwnApplicationICS20TwoChains(t *testing.T) {
	coordinator := &ibctesting.Coordinator{T: t, CurrentTime: time.Unix(1900000000, 0).UTC(), Chains: map[string]*ibctesting.TestChain{}}
	a := newOwnIBCChain(t, coordinator, 990001)
	b := newOwnIBCChain(t, coordinator, 990002)
	appA := a.App.(*App)
	appB := b.App.(*App)
	beforeHandshake := appA.BankKeeper.GetBalance(a.GetContext(), a.SenderAccount.GetAddress(), identity.BaseDenom).Amount
	path := ibctesting.NewPath(a, b)
	path.EndpointA.ChannelConfig.PortID = transfer.PortID
	path.EndpointB.ChannelConfig.PortID = transfer.PortID
	path.EndpointA.ChannelConfig.Version = transfer.V1
	path.EndpointB.ChannelConfig.Version = transfer.V1
	coordinator.Setup(path)
	require.True(t, appA.BankKeeper.GetBalance(a.GetContext(), a.SenderAccount.GetAddress(), identity.BaseDenom).Amount.LT(beforeHandshake), "signed native relayer handshake must pay real del fees")
	escrow := transfer.GetEscrowAddress(transfer.PortID, path.EndpointA.ChannelID)
	coin := sdk.NewInt64Coin(identity.BaseDenom, 100)
	msg := transfer.NewMsgTransfer(transfer.PortID, path.EndpointA.ChannelID, coin, a.SenderAccount.GetAddress().String(), b.SenderAccount.GetAddress().String(), b.GetTimeoutHeight(), 0, "")
	beforeSend := appA.BankKeeper.GetBalance(a.GetContext(), a.SenderAccount.GetAddress(), identity.BaseDenom).Amount
	result, err := a.SendMsgs(msg)
	require.NoError(t, err)
	require.Equal(t, coin.Amount.Add(nativeIBCTransactionFee.Amount), beforeSend.Sub(appA.BankKeeper.GetBalance(a.GetContext(), a.SenderAccount.GetAddress(), identity.BaseDenom).Amount))
	packet, err := ibctesting.ParsePacketFromEvents(result.Events)
	require.NoError(t, err)
	require.NoError(t, path.RelayPacket(packet))
	denom := transfer.NewDenom(identity.BaseDenom, transfer.NewHop(transfer.PortID, path.EndpointB.ChannelID)).IBCDenom()
	require.Equal(t, coin.Amount, appB.BankKeeper.GetBalance(b.GetContext(), b.SenderAccount.GetAddress(), denom).Amount)
	require.Equal(t, coin.Amount, appA.BankKeeper.GetBalance(a.GetContext(), escrow, identity.BaseDenom).Amount)
	returnRecipient := sdk.AccAddress([]byte("independent-return01"))
	beforeReturn := appA.BankKeeper.GetBalance(a.GetContext(), returnRecipient, identity.BaseDenom).Amount
	msg = transfer.NewMsgTransfer(transfer.PortID, path.EndpointB.ChannelID, sdk.NewCoin(denom, coin.Amount), b.SenderAccount.GetAddress().String(), returnRecipient.String(), a.GetTimeoutHeight(), 0, "")
	result, err = b.SendMsgs(msg)
	require.NoError(t, err)
	packet, err = ibctesting.ParsePacketFromEvents(result.Events)
	require.NoError(t, err)
	require.NoError(t, path.RelayPacket(packet))
	require.Equal(t, coin.Amount, appA.BankKeeper.GetBalance(a.GetContext(), returnRecipient, identity.BaseDenom).Amount.Sub(beforeReturn))
	require.True(t, appA.BankKeeper.GetBalance(a.GetContext(), escrow, identity.BaseDenom).IsZero())
	require.True(t, appB.BankKeeper.GetBalance(b.GetContext(), b.SenderAccount.GetAddress(), denom).IsZero())
	// Use an independent relayer on the source so exact user refunds are not
	// obscured by client-update/acknowledgement/timeout transaction fees.
	primary := a.SenderAccounts[0]
	relayer := a.SenderAccounts[1]
	setSigner := func(sender ibctesting.SenderAccount) {
		a.SenderAccount = sender.SenderAccount
		a.SenderPrivKey = sender.SenderPrivKey
	}
	beforeError := appA.BankKeeper.GetBalance(a.GetContext(), primary.SenderAccount.GetAddress(), identity.BaseDenom).Amount
	msg = transfer.NewMsgTransfer(transfer.PortID, path.EndpointA.ChannelID, coin, primary.SenderAccount.GetAddress().String(), "invalid-destination", b.GetTimeoutHeight(), 0, "")
	result, err = a.SendMsgs(msg)
	require.NoError(t, err)
	packet, err = ibctesting.ParsePacketFromEvents(result.Events)
	require.NoError(t, err)
	setSigner(relayer)
	_, ackBytes, err := path.RelayPacketWithResults(packet)
	require.NoError(t, err)
	var acknowledgement channeltypes.Acknowledgement
	require.NoError(t, a.Codec.UnmarshalJSON(ackBytes, &acknowledgement))
	require.False(t, acknowledgement.Success())
	require.Equal(t, beforeError.Sub(nativeIBCTransactionFee.Amount), appA.BankKeeper.GetBalance(a.GetContext(), primary.SenderAccount.GetAddress(), identity.BaseDenom).Amount, "error ack must refund all user principal; only send fee belongs to the user")
	require.True(t, appA.BankKeeper.GetBalance(a.GetContext(), escrow, identity.BaseDenom).IsZero())
	setSigner(primary)
	beforeTimeout := appA.BankKeeper.GetBalance(a.GetContext(), primary.SenderAccount.GetAddress(), identity.BaseDenom).Amount
	deadline := uint64(coordinator.CurrentTime.Add(time.Minute).UnixNano())
	msg = transfer.NewMsgTransfer(transfer.PortID, path.EndpointA.ChannelID, coin, primary.SenderAccount.GetAddress().String(), b.SenderAccount.GetAddress().String(), clienttypes.ZeroHeight(), deadline, "")
	result, err = a.SendMsgs(msg)
	require.NoError(t, err)
	packet, err = ibctesting.ParsePacketFromEvents(result.Events)
	require.NoError(t, err)
	coordinator.IncrementTimeBy(2 * time.Minute)
	b.NextBlock()
	setSigner(relayer)
	require.NoError(t, path.EndpointA.UpdateClient())
	require.NoError(t, path.EndpointA.TimeoutPacket(packet))
	require.Equal(t, beforeTimeout.Sub(nativeIBCTransactionFee.Amount), appA.BankKeeper.GetBalance(a.GetContext(), primary.SenderAccount.GetAddress(), identity.BaseDenom).Amount, "timeout must refund exact principal")
	require.True(t, appA.BankKeeper.GetBalance(a.GetContext(), escrow, identity.BaseDenom).IsZero())
	require.NoError(t, path.EndpointA.TimeoutPacket(packet), "IBC treats repeated timeout delivery as an idempotent no-op")
	require.Equal(t, beforeTimeout.Sub(nativeIBCTransactionFee.Amount), appA.BankKeeper.GetBalance(a.GetContext(), primary.SenderAccount.GetAddress(), identity.BaseDenom).Amount)
	setSigner(primary)
	beforeInvalidProof := appA.BankKeeper.GetBalance(a.GetContext(), primary.SenderAccount.GetAddress(), identity.BaseDenom).Amount
	msg = transfer.NewMsgTransfer(transfer.PortID, path.EndpointA.ChannelID, coin, primary.SenderAccount.GetAddress().String(), "invalid-destination", b.GetTimeoutHeight(), 0, "")
	result, err = a.SendMsgs(msg)
	require.NoError(t, err)
	packet, err = ibctesting.ParsePacketFromEvents(result.Events)
	require.NoError(t, err)
	require.NoError(t, path.EndpointB.UpdateClient())
	proof, proofHeight := a.QueryProof(host.PacketCommitmentKey(packet.SourcePort, packet.SourceChannel, packet.Sequence))
	altered := packet
	altered.Data = append(append([]byte(nil), packet.Data...), byte(' '))
	_, err = b.SendMsgs(channeltypes.NewMsgRecvPacket(altered, proof, proofHeight, b.SenderAccount.GetAddress().String()))
	require.Error(t, err, "a real membership proof cannot authorize altered packet bytes")
	_, received := appB.IBCKeeper.ChannelKeeper.GetPacketReceipt(b.GetContext(), packet.DestinationPort, packet.DestinationChannel, packet.Sequence)
	require.False(t, received)
	setSigner(relayer)
	require.NoError(t, path.RelayPacket(packet), "original proved packet must remain relayable after rejected tampering")
	require.Equal(t, beforeInvalidProof.Sub(nativeIBCTransactionFee.Amount), appA.BankKeeper.GetBalance(a.GetContext(), primary.SenderAccount.GetAddress(), identity.BaseDenom).Amount)
	require.True(t, appA.BankKeeper.GetBalance(a.GetContext(), escrow, identity.BaseDenom).IsZero())
}
