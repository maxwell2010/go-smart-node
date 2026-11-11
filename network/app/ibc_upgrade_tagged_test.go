//go:build test

package app

import (
	"context"
	"testing"
	"time"

	upgradetypes "cosmossdk.io/x/upgrade/types"
	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	transfer "github.com/cosmos/ibc-go/v10/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	ibctm "github.com/cosmos/ibc-go/v10/modules/light-clients/07-tendermint"
	"github.com/maxwell2010/go-smart-node/network/identity"
	ibctesting "github.com/maxwell2010/go-smart-node/network/internal/ibcharness"
	"github.com/stretchr/testify/require"
)

func TestOwnApplicationIBCClientUpgradePendingPacket(t *testing.T) {
	coordinator := &ibctesting.Coordinator{T: t, CurrentTime: time.Unix(1900000000, 0).UTC(), Chains: map[string]*ibctesting.TestChain{}}
	a := newOwnIBCChain(t, coordinator, 990001)
	b := newOwnIBCChain(t, coordinator, 990002)
	appA, appB := a.App.(*App), b.App.(*App)
	path := ibctesting.NewPath(a, b)
	endpointA, endpointB := path.EndpointA, path.EndpointB
	endpointA.ChannelConfig.PortID, endpointB.ChannelConfig.PortID = transfer.PortID, transfer.PortID
	endpointA.ChannelConfig.Version, endpointB.ChannelConfig.Version = transfer.V1, transfer.V1
	path.Setup()

	payer, recipient := a.SenderAccount.GetAddress(), b.SenderAccount.GetAddress()
	coin := sdk.NewInt64Coin(identity.BaseDenom, 100)
	escrow := transfer.GetEscrowAddress(transfer.PortID, endpointA.ChannelID)
	voucher := transfer.NewDenom(coin.Denom, transfer.NewHop(transfer.PortID, endpointB.ChannelID)).IBCDenom()
	payerBalance := appA.BankKeeper.GetBalance(a.GetContext(), payer, coin.Denom).Amount
	send := transfer.NewMsgTransfer(transfer.PortID, endpointA.ChannelID, coin, payer.String(), recipient.String(), b.GetTimeoutHeight(), 0, "")
	sent, err := a.SendMsgs(send)
	require.NoError(t, err)
	packet, err := ibctesting.ParsePacketFromEvents(sent.Events)
	require.NoError(t, err)
	require.Equal(t, coin.Amount.Add(nativeIBCTransactionFee.Amount), payerBalance.Sub(appA.BankKeeper.GetBalance(a.GetContext(), payer, coin.Denom).Amount))
	commitment := channeltypes.CommitPacket(packet)
	assertPending := func() {
		t.Helper()
		require.Equal(t, commitment, appA.IBCKeeper.ChannelKeeper.GetPacketCommitment(a.GetContext(), packet.SourcePort, packet.SourceChannel, packet.Sequence))
		require.Equal(t, coin, appA.BankKeeper.GetBalance(a.GetContext(), escrow, coin.Denom))
		_, received := appB.IBCKeeper.ChannelKeeper.GetPacketReceipt(b.GetContext(), packet.DestinationPort, packet.DestinationChannel, packet.Sequence)
		require.False(t, received)
		require.True(t, appB.BankKeeper.GetSupply(b.GetContext(), voucher).IsZero())
	}
	assertPending()

	stakingParams, err := appA.StakingKeeper.GetParams(a.GetContext())
	require.NoError(t, err)
	previousClient := b.GetClientState(endpointB.ClientID).(*ibctm.ClientState)
	require.Equal(t, stakingParams.UnbondingTime, previousClient.UnbondingPeriod)
	plan := upgradetypes.Plan{Name: "ibc-client-proof-rehearsal", Height: appA.LastBlockHeight() + 8}
	revision := clienttypes.ParseChainID(a.ChainID)
	oldHeight := clienttypes.NewHeight(revision, uint64(plan.Height))
	placeholderHeight := clienttypes.NewHeight(revision, uint64(plan.Height+1))
	upgradedClient := *previousClient
	upgradedClient.UnbondingPeriod += time.Hour
	upgradedClient.LatestHeight = placeholderHeight
	proposeUpgrade, err := clienttypes.NewMsgIBCSoftwareUpgrade(appA.IBCKeeper.GetAuthority(), plan, &upgradedClient)
	require.NoError(t, err)

	// Exercise actual governance execution of the authority-gated IBC message.
	// The payer owns the only SDK genesis delegation in this test chain.
	params, err := appA.GovKeeper.Params.Get(a.GetContext())
	require.NoError(t, err)
	proposalID, err := appA.GovKeeper.ProposalID.Peek(a.GetContext())
	require.NoError(t, err)
	proposalMsg, err := govv1.NewMsgSubmitProposal([]sdk.Msg{proposeUpgrade}, sdk.NewCoins(params.MinDeposit...),
		payer.String(), "", "IBC client proof rehearsal", "Change the test chain unbonding period through a scheduled upgrade.", false)
	require.NoError(t, err)
	_, err = a.SendMsgs(proposalMsg)
	require.NoError(t, err)
	proposal, err := appA.GovKeeper.Proposals.Get(a.GetContext(), proposalID)
	require.NoError(t, err)
	require.Equal(t, govv1.StatusVotingPeriod, proposal.Status)
	require.NotNil(t, proposal.VotingEndTime)
	_, err = a.SendMsgs(govv1.NewMsgVote(payer, proposalID, govv1.OptionYes, ""))
	require.NoError(t, err)
	coordinator.IncrementTimeBy(proposal.VotingEndTime.Sub(coordinator.CurrentTime) + time.Second)
	coordinator.CommitBlock(a)
	proposal, err = appA.GovKeeper.Proposals.Get(a.GetContext(), proposalID)
	require.NoError(t, err)
	require.Equal(t, govv1.StatusPassed, proposal.Status)
	scheduled, err := appA.UpgradeKeeper.GetUpgradePlan(a.GetContext())
	require.NoError(t, err)
	require.Equal(t, plan.Name, scheduled.Name)
	require.Equal(t, plan.Height, scheduled.Height)

	for a.ProposedHeader.Height < plan.Height {
		coordinator.CommitBlock(a)
	}
	lastOldCommit := appA.LastCommitID()
	require.Equal(t, plan.Height-1, lastOldCommit.Version)
	committedClientBytes, err := appA.UpgradeKeeper.GetUpgradedClient(a.GetContext(), plan.Height)
	require.NoError(t, err)
	canonical, err := clienttypes.MarshalClientState(a.Codec, upgradedClient.ZeroCustomFields())
	require.NoError(t, err)
	require.Equal(t, canonical, committedClientBytes)
	committedClient, err := clienttypes.UnmarshalClientState(a.Codec, committedClientBytes)
	require.NoError(t, err)
	committedConsensusBytes, err := appA.UpgradeKeeper.GetUpgradedConsensusState(a.GetContext(), plan.Height)
	require.NoError(t, err)
	committedConsensus, err := clienttypes.UnmarshalConsensusState(a.Codec, committedConsensusBytes)
	require.NoError(t, err)
	clientProof, clientProofHeight := a.QueryUpgradeProof(upgradetypes.UpgradedClientKey(plan.Height), uint64(plan.Height))
	consensusProof, consensusProofHeight := a.QueryUpgradeProof(upgradetypes.UpgradedConsStateKey(plan.Height), uint64(plan.Height))
	require.Equal(t, oldHeight, clientProofHeight)
	require.Equal(t, oldHeight, consensusProofHeight)

	// This is the old app at its real PreBlock boundary. Comet's height H
	// header authenticates AppHash(H-1), where both upgrade keys were stored.
	_, err = appA.FinalizeBlock(&abci.RequestFinalizeBlock{Height: plan.Height, Time: a.ProposedHeader.Time, NextValidatorsHash: a.NextVals.Hash()})
	require.ErrorContains(t, err, "UPGRADE")
	require.ErrorContains(t, err, plan.Name)
	hint, err := appA.UpgradeKeeper.ReadUpgradeInfoFromDisk()
	require.NoError(t, err)
	require.Equal(t, plan.Name, hint.Name)
	require.Equal(t, plan.Height, hint.Height)
	require.Equal(t, lastOldCommit, appA.LastCommitID())
	assertPending()

	applications := 0
	appA.UpgradeKeeper.SetUpgradeHandler(plan.Name, func(ctx context.Context, actual upgradetypes.Plan, from module.VersionMap) (module.VersionMap, error) {
		require.Equal(t, plan.Height, actual.Height)
		require.Equal(t, plan.Height, sdk.UnwrapSDKContext(ctx).BlockHeight())
		applications++
		next, err := appA.StakingKeeper.GetParams(ctx)
		if err != nil {
			return nil, err
		}
		next.UnbondingTime = upgradedClient.UnbondingPeriod
		if err := appA.StakingKeeper.SetParams(ctx, next); err != nil {
			return nil, err
		}
		return appA.ModuleManager.RunMigrations(ctx, appA.configurator, from)
	})
	coordinator.CommitBlock(a)
	require.Equal(t, 1, applications)
	appliedName, appliedHeight, err := appA.UpgradeKeeper.GetLastCompletedUpgrade(a.GetContext())
	require.NoError(t, err)
	require.Equal(t, plan.Name, appliedName)
	require.Equal(t, plan.Height, appliedHeight)
	_, err = appA.UpgradeKeeper.GetUpgradePlan(a.GetContext())
	require.ErrorIs(t, err, upgradetypes.ErrNoUpgradePlanFound)
	afterParams, err := appA.StakingKeeper.GetParams(a.GetContext())
	require.NoError(t, err)
	require.Equal(t, upgradedClient.UnbondingPeriod, afterParams.UnbondingTime)
	assertPending()

	lastOldHeader, err := a.IBCClientHeader(a.LatestCommittedHeader, previousClient.LatestHeight)
	require.NoError(t, err)
	require.Equal(t, lastOldCommit.Hash, []byte(lastOldHeader.SignedHeader.Header.AppHash))
	update, err := clienttypes.NewMsgUpdateClient(endpointB.ClientID, lastOldHeader, recipient.String())
	require.NoError(t, err)
	_, err = b.SendMsgs(update)
	require.NoError(t, err)
	require.Equal(t, oldHeight, b.GetClientState(endpointB.ClientID).(*ibctm.ClientState).LatestHeight)

	upgrade, err := clienttypes.NewMsgUpgradeClient(endpointB.ClientID, committedClient, committedConsensus, clientProof, consensusProof, recipient.String())
	require.NoError(t, err)
	_, err = b.SendMsgs(upgrade)
	require.NoError(t, err)
	updatedClient := b.GetClientState(endpointB.ClientID).(*ibctm.ClientState)
	require.Equal(t, placeholderHeight, updatedClient.LatestHeight)
	require.Equal(t, a.ChainID, updatedClient.ChainId)
	require.Equal(t, previousClient.TrustingPeriod, updatedClient.TrustingPeriod)
	require.Equal(t, afterParams.UnbondingTime, updatedClient.UnbondingPeriod)
	placeholder, found := b.GetConsensusState(endpointB.ClientID, placeholderHeight)
	require.True(t, found)
	require.Equal(t, []byte(ibctm.SentinelRoot), placeholder.(*ibctm.ConsensusState).Root.Hash)
	assertPending()

	// Advance beyond the placeholder and use a new trusted header for the
	// actual packet and acknowledgement proofs on the same connection/channel.
	require.NoError(t, evmtypes.SetChainConfig(evmtypes.DefaultChainConfig(990001)))
	coordinator.CommitBlock(a)
	relayer := a.SenderAccounts[1]
	a.SenderAccount, a.SenderPrivKey = relayer.SenderAccount, relayer.SenderPrivKey
	_, ackBytes, err := path.RelayPacketWithResults(packet)
	require.NoError(t, err)
	var ack channeltypes.Acknowledgement
	require.NoError(t, b.Codec.UnmarshalJSON(ackBytes, &ack))
	require.True(t, ack.Success())
	require.True(t, b.GetClientState(endpointB.ClientID).(*ibctm.ClientState).LatestHeight.GT(placeholderHeight))
	require.Empty(t, appA.IBCKeeper.ChannelKeeper.GetPacketCommitment(a.GetContext(), packet.SourcePort, packet.SourceChannel, packet.Sequence))
	require.Equal(t, coin, appA.BankKeeper.GetBalance(a.GetContext(), escrow, coin.Denom))
	require.Equal(t, coin.Amount, appB.BankKeeper.GetBalance(b.GetContext(), recipient, voucher).Amount)
	require.Equal(t, coin.Amount, appB.BankKeeper.GetSupply(b.GetContext(), voucher).Amount)

	beforeReturn := appA.BankKeeper.GetBalance(a.GetContext(), payer, coin.Denom).Amount
	returnTransfer := transfer.NewMsgTransfer(transfer.PortID, endpointB.ChannelID, sdk.NewCoin(voucher, coin.Amount),
		recipient.String(), payer.String(), a.GetTimeoutHeight(), 0, "")
	result, err := b.SendMsgs(returnTransfer)
	require.NoError(t, err)
	returnPacket, err := ibctesting.ParsePacketFromEvents(result.Events)
	require.NoError(t, err)
	require.NoError(t, path.RelayPacket(returnPacket))
	require.Equal(t, beforeReturn.Add(coin.Amount), appA.BankKeeper.GetBalance(a.GetContext(), payer, coin.Denom).Amount)
	require.True(t, appA.BankKeeper.GetBalance(a.GetContext(), escrow, coin.Denom).IsZero())
	require.True(t, appB.BankKeeper.GetSupply(b.GetContext(), voucher).IsZero())
	require.Empty(t, appB.IBCKeeper.ChannelKeeper.GetPacketCommitment(b.GetContext(), returnPacket.SourcePort, returnPacket.SourceChannel, returnPacket.Sequence))
	require.Equal(t, 1, applications)
	t.Logf("signed IBC client upgrade at height %d preserved the pending packet and returned its full principal", plan.Height)
}
