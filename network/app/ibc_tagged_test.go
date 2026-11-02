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
	transfer "github.com/cosmos/ibc-go/v11/modules/apps/transfer/types"
	ibctesting "github.com/cosmos/ibc-go/v11/testing"
	"github.com/maxwell2010/go-smart-node/network/identity"
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
	genesis, err := simtestutil.GenesisStateWithValSet(a.AppCodec(), a.DefaultGenesis(), vals, []authtypes.GenesisAccount{account}, banktypes.Balance{Address: account.GetAddress().String(), Coins: sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, math.NewInt(1_000_000).Mul(math.NewInt(1_000_000_000_000_000_000))))})
	require.NoError(t, err)
	var originalBank, fundedBank banktypes.GenesisState
	a.AppCodec().MustUnmarshalJSON(a.DefaultGenesis()[banktypes.ModuleName], &originalBank)
	a.AppCodec().MustUnmarshalJSON(genesis[banktypes.ModuleName], &fundedBank)
	fundedBank.DenomMetadata = originalBank.DenomMetadata
	genesis[banktypes.ModuleName] = a.AppCodec().MustMarshalJSON(&fundedBank)
	data, err := json.Marshal(genesis)
	require.NoError(t, err)
	_, err = a.InitChain(&abci.RequestInitChain{ChainId: chainID, Time: coordinator.CurrentTime, AppStateBytes: data, ConsensusParams: simtestutil.DefaultConsensusParams})
	require.NoError(t, err)
	chain := &ibctesting.TestChain{
		TB: t, Coordinator: coordinator, App: a, ChainID: chainID,
		ProposedHeader: cmtproto.Header{ChainID: chainID, Height: 1, Time: coordinator.CurrentTime},
		TxConfig:       fundedIBCConfig{a.TxConfig(), evmID}, Codec: a.AppCodec(), Vals: vals, NextVals: vals,
		Signers: map[string]cmttypes.PrivValidator{pub.Address().String(): pv}, TrustedValidators: map[uint64]*cmttypes.ValidatorSet{},
		SenderPrivKey: key, SenderAccount: account, SenderAccounts: []ibctesting.SenderAccount{{SenderPrivKey: key, SenderAccount: account}},
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
}
