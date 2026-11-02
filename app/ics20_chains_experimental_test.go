//go:build experimental_ibc

package app

import (
	cmdcfg "bitbucket.org/decimalteam/go-smart-node/cmd/config"
	dsctest "bitbucket.org/decimalteam/go-smart-node/testutil/dsc"
	"encoding/json"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	transfer "github.com/cosmos/ibc-go/v6/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v6/modules/core/02-client/types"
	ibctesting "github.com/cosmos/ibc-go/v6/testing"
	ibcmock "github.com/cosmos/ibc-go/v6/testing/mock"
	ibctestingtypes "github.com/cosmos/ibc-go/v6/testing/types"
	ethsecp "github.com/decimalteam/ethermint/crypto/ethsecp256k1"
	"github.com/stretchr/testify/require"
	abci "github.com/tendermint/tendermint/abci/types"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	tmtypes "github.com/tendermint/tendermint/types"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type dscIBCApp struct{ *DSC }

func (a dscIBCApp) GetStakingKeeper() ibctestingtypes.StakingKeeper { return a.ValidatorKeeper }

func newDSCChain(t *testing.T, c *ibctesting.Coordinator, id string) *ibctesting.TestChain {
	pv := ibcmock.NewPV()
	pub, err := pv.GetPubKey()
	require.NoError(t, err)
	vals := tmtypes.NewValidatorSet([]*tmtypes.Validator{tmtypes.NewValidator(pub, 1000)})
	priv, err := ethsecp.GenerateKey()
	require.NoError(t, err)
	acc := authtypes.NewBaseAccount(priv.PubKey().Address().Bytes(), priv.PubKey(), 0, 0)
	bal := banktypes.Balance{Address: acc.GetAddress().String(), Coins: sdk.NewCoins(sdk.NewCoin(cmdcfg.BaseDenom, sdk.NewInt(1000000).Mul(sdk.NewInt(1000000000000000000))))}
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "config"), 0700))
	g := tmtypes.GenesisDoc{ChainID: id, GenesisTime: c.CurrentTime, ConsensusParams: tmtypes.DefaultConsensusParams(), AppState: json.RawMessage(`{}`)}
	require.NoError(t, g.SaveAs(filepath.Join(home, "config", "genesis.json")))
	app, _ := reliabilityOpen(t, home)
	gs, err := dsctest.GenesisStateWithValSet(app.AppCodec(), NewDefaultGenesisState(app.AppCodec()), vals, []authtypes.GenesisAccount{acc}, bal)
	require.NoError(t, err)
	state, err := json.Marshal(gs)
	require.NoError(t, err)
	app.InitChain(abci.RequestInitChain{ChainId: id, ConsensusParams: dsctest.DefaultConsensusParams, AppStateBytes: state})
	header := tmproto.Header{ChainID: id, Height: 1, Time: c.CurrentTime, ProposerAddress: pub.Address()}
	chain := &ibctesting.TestChain{T: t, Coordinator: c, App: dscIBCApp{app}, ChainID: id, CurrentHeader: header, QueryServer: app.IBCKeeper, TxConfig: app.GetTxConfig(), Codec: app.AppCodec(), Vals: vals, NextVals: vals, Signers: map[string]tmtypes.PrivValidator{pub.Address().String(): pv}, SenderPrivKey: priv, SenderAccount: acc, SenderAccounts: []ibctesting.SenderAccount{{SenderPrivKey: priv, SenderAccount: acc}}}
	app.BeginBlock(abci.RequestBeginBlock{Header: header})
	c.CommitBlock(chain)
	return chain
}

func TestExperimentalICS20TwoDSCChains(t *testing.T) {
	c := &ibctesting.Coordinator{T: t, CurrentTime: reliabilityTime, Chains: map[string]*ibctesting.TestChain{}}
	a := newDSCChain(t, c, "decimal_900001-1")
	b := newDSCChain(t, c, "decimal_900002-1")
	c.Chains[a.ChainID] = a
	c.Chains[b.ChainID] = b
	p := ibctesting.NewPath(a, b)
	for _, ep := range []*ibctesting.Endpoint{p.EndpointA, p.EndpointB} {
		ep.ClientConfig.(*ibctesting.TendermintConfig).UnbondingPeriod = ep.Counterparty.Chain.App.(dscIBCApp).ValidatorKeeper.UnbondingTime(ep.Counterparty.Chain.GetContext())
		ep.ClientConfig.(*ibctesting.TendermintConfig).TrustingPeriod = 14 * 24 * time.Hour
		ep.ChannelConfig.PortID = transfer.PortID
		ep.ChannelConfig.Version = transfer.Version
	}
	c.Setup(p)
	coin := sdk.NewInt64Coin(cmdcfg.BaseDenom, 100)
	bankA := a.App.(dscIBCApp).BankKeeper
	before := bankA.GetBalance(a.GetContext(), a.SenderAccount.GetAddress(), coin.Denom).Amount

	msg := transfer.NewMsgTransfer(transfer.PortID, p.EndpointA.ChannelID, coin, a.SenderAccount.GetAddress().String(), b.SenderAccount.GetAddress().String(), b.GetTimeoutHeight(), 0, "")
	result, err := a.SendMsgs(msg)
	require.NoError(t, err)
	after := bankA.GetBalance(a.GetContext(), a.SenderAccount.GetAddress(), coin.Denom).Amount
	require.True(t, before.Sub(after).GT(coin.Amount), "signed send must deduct a nonzero native fee in addition to escrow")
	packet, err := ibctesting.ParsePacketFromEvents(result.GetEvents())
	require.NoError(t, err)
	require.NoError(t, p.RelayPacket(packet))
	voucher := transfer.GetTransferCoin(transfer.PortID, p.EndpointB.ChannelID, coin.Denom, coin.Amount)
	require.Equal(t, voucher, b.App.(dscIBCApp).BankKeeper.GetBalance(b.GetContext(), b.SenderAccount.GetAddress(), voucher.Denom))
	returnMsg := transfer.NewMsgTransfer(transfer.PortID, p.EndpointB.ChannelID, voucher, b.SenderAccount.GetAddress().String(), a.SenderAccount.GetAddress().String(), a.GetTimeoutHeight(), 0, "")
	returned, err := b.SendMsgs(returnMsg)
	require.NoError(t, err)
	returnPacket, err := ibctesting.ParsePacketFromEvents(returned.GetEvents())
	require.NoError(t, err)
	require.NoError(t, p.RelayPacket(returnPacket))
	escrow := transfer.GetEscrowAddress(transfer.PortID, p.EndpointA.ChannelID)
	require.True(t, bankA.GetBalance(a.GetContext(), escrow, coin.Denom).IsZero())
	require.True(t, b.App.(dscIBCApp).BankKeeper.GetSupply(b.GetContext(), voucher.Denom).IsZero())

	badMsg := transfer.NewMsgTransfer(transfer.PortID, p.EndpointA.ChannelID, coin, a.SenderAccount.GetAddress().String(), "invalid-receiver", b.GetTimeoutHeight(), 0, "")
	badResult, err := a.SendMsgs(badMsg)
	require.NoError(t, err)
	badPacket, err := ibctesting.ParsePacketFromEvents(badResult.GetEvents())
	require.NoError(t, err)
	require.NoError(t, p.RelayPacket(badPacket))
	require.True(t, bankA.GetBalance(a.GetContext(), escrow, coin.Denom).IsZero())

	timedMsg := transfer.NewMsgTransfer(transfer.PortID, p.EndpointA.ChannelID, coin, a.SenderAccount.GetAddress().String(), b.SenderAccount.GetAddress().String(), clienttypes.ZeroHeight(), uint64(c.CurrentTime.Add(time.Minute).UnixNano()), "")
	timedResult, err := a.SendMsgs(timedMsg)
	require.NoError(t, err)
	timedPacket, err := ibctesting.ParsePacketFromEvents(timedResult.GetEvents())
	require.NoError(t, err)
	c.IncrementTimeBy(2 * time.Minute)
	c.CommitBlock(b)
	require.NoError(t, p.EndpointA.UpdateClient())
	require.NoError(t, p.EndpointA.TimeoutPacket(timedPacket))
	require.True(t, bankA.GetBalance(a.GetContext(), escrow, coin.Denom).IsZero())

}
