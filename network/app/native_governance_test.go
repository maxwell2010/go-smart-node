package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/gov"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	govlegacy "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func checkNativeGovernanceSignalRoutesAndTally(t *testing.T, a *App) {
	t.Helper()
	previous := a.withdrawals
	t.Cleanup(func() { a.withdrawals = previous })
	profile := WithdrawalProfile{EnableNativeConsensus: true, EnableNativeGovernance: true}
	a.withdrawals = &withdrawalController{profile: profile}
	circuit := nativeConsensusCircuit{app: a}
	for _, msg := range []sdk.Msg{&govv1.MsgSubmitProposal{}, &govv1.MsgDeposit{}, &govv1.MsgVote{}, &govv1.MsgVoteWeighted{}} {
		allowed, err := circuit.IsAllowed(context.Background(), sdk.MsgTypeURL(msg))
		require.NoError(t, err)
		require.True(t, allowed)
	}
	for _, msg := range []sdk.Msg{&govlegacy.MsgVote{}, &govv1.MsgExecLegacyContent{}, &govv1.MsgUpdateParams{}, &stakingtypes.MsgDelegate{}, &authz.MsgExec{}} {
		allowed, err := circuit.IsAllowed(context.Background(), sdk.MsgTypeURL(msg))
		require.NoError(t, err)
		require.False(t, allowed)
	}

	now := time.Unix(2000, 0).UTC()
	parent := a.NewUncachedContext(false, cmtproto.Header{ChainID: a.chainID, Height: 1, Time: now})
	ctx, _ := parent.CacheContext()
	operator := sdk.ValAddress(bytes.Repeat([]byte{1}, 20))
	proposer := sdk.AccAddress(operator)
	voter := sdk.AccAddress(bytes.Repeat([]byte{2}, 20))
	v, err := validatortypes.NewValidator(operator.String(), proposer.String(), ed25519.GenPrivKeyFromSecret([]byte("governance-fixture")).PubKey(),
		validatortypes.Description{Moniker: "isolated fixture"}, sdkmath.LegacyZeroDec())
	require.NoError(t, err)
	v.Status, v.Stake, v.Online = validatortypes.BondStatus_Bonded, 10, true
	validatorGenesis := validatortypes.DefaultGenesisState()
	validatorGenesis.Validators = []validatortypes.Validator{v}
	require.NoError(t, a.NativeValidatorKeeper.InitGenesis(ctx, validatorGenesis))
	unit := sdkmath.NewInt(nativekeeper.NativeDELAtomsPerPower)
	for _, row := range []struct {
		owner  sdk.AccAddress
		amount int64
		hold   bool
	}{{proposer, 6, false}, {voter, 2, false}, {voter, 2, true}} {
		position := nativekeeper.GatewayDELStake{Gateway: common.HexToAddress("0x1111"), WrappedDEL: common.HexToAddress("0x2222"),
			Beneficiary: common.BytesToAddress(row.owner), Validator: common.BytesToAddress(operator), Amount: unit.MulRaw(row.amount)}
		var words [64]byte
		if row.hold {
			position.HoldStartTime, position.HoldDeadline = now.Unix(), now.Add(time.Hour).Unix()
			binary.BigEndian.PutUint64(words[56:], uint64(position.HoldDeadline))
		}
		position.StakeID = crypto.Keccak256Hash(position.Validator[:], position.Beneficiary[:], position.WrappedDEL[:], words[:])
		require.NoError(t, a.NativeValidatorKeeper.ApplyGatewayDELDelegation(ctx, position, position.Amount))
	}

	defaults := a.DefaultGenesis()
	var govGenesis govv1.GenesisState
	a.appCodec.MustUnmarshalJSON(defaults[govtypes.ModuleName], &govGenesis)
	require.NoError(t, a.validateNativeGovernanceGenesis(defaults))
	deposit := sdk.NewCoins(govGenesis.Params.MinDeposit...)
	var authGenesis authtypes.GenesisState
	a.appCodec.MustUnmarshalJSON(defaults[authtypes.ModuleName], &authGenesis)
	a.AccountKeeper.InitGenesis(ctx, authGenesis)
	var bankGenesis banktypes.GenesisState
	a.appCodec.MustUnmarshalJSON(defaults[banktypes.ModuleName], &bankGenesis)
	bankGenesis.Balances = []banktypes.Balance{{Address: proposer.String(), Coins: deposit}}
	a.BankKeeper.InitGenesis(ctx, &bankGenesis)
	gov.InitGenesis(ctx, a.AccountKeeper, a.BankKeeper, &a.GovKeeper, &govGenesis)

	send, err := govv1.NewMsgSubmitProposal([]sdk.Msg{&banktypes.MsgSend{
		FromAddress: authtypes.NewModuleAddress(govtypes.ModuleName).String(), ToAddress: proposer.String(), Amount: deposit,
	}}, deposit, proposer.String(), "ipfs://signal", "Signal", "Test signal", false)
	require.NoError(t, err)
	_, err = a.MsgServiceRouter().Handler(send)(ctx, send)
	require.ErrorContains(t, err, "no executable messages")
	id, err := a.GovKeeper.ProposalID.Peek(ctx)
	require.NoError(t, err)
	require.Equal(t, govGenesis.StartingProposalId, id)

	signal, err := govv1.NewMsgSubmitProposal(nil, deposit, proposer.String(), "ipfs://signal", "Signal", "Test signal", false)
	require.NoError(t, err)
	_, err = a.MsgServiceRouter().Handler(signal)(ctx, signal)
	require.NoError(t, err)
	for _, msg := range []*govv1.MsgVote{
		govv1.NewMsgVote(proposer, id, govv1.OptionYes, ""),
		govv1.NewMsgVote(voter, id, govv1.OptionNo, ""),
	} {
		_, err := a.MsgServiceRouter().Handler(msg)(ctx, msg)
		require.NoError(t, err)
	}
	query := func(path string, req, resp proto.Message) {
		t.Helper()
		result, err := a.GRPCQueryRouter().Route(path)(ctx, &abci.RequestQuery{Path: path, Data: a.appCodec.MustMarshal(req)})
		require.NoError(t, err)
		require.NoError(t, a.appCodec.Unmarshal(result.Value, resp))
	}
	for range 2 {
		var live govv1.QueryTallyResultResponse
		query("/cosmos.gov.v1.Query/TallyResult", &govv1.QueryTallyResultRequest{ProposalId: id}, &live)
		require.Equal(t, unit.MulRaw(6).String(), live.Tally.YesCount)
		require.Equal(t, unit.MulRaw(4).String(), live.Tally.NoCount)
	}
	var legacy govlegacy.QueryTallyResultResponse
	query("/cosmos.gov.v1beta1.Query/TallyResult", &govlegacy.QueryTallyResultRequest{ProposalId: id}, &legacy)
	require.Equal(t, unit.MulRaw(4).String(), legacy.Tally.No.String())

	proposal, err := a.GovKeeper.Proposals.Get(ctx, id)
	require.NoError(t, err)
	closing := ctx.WithBlockHeight(2).WithBlockTime(proposal.VotingEndTime.Add(time.Second))
	require.NoError(t, a.nativeGovernance.EndBlock(closing, &a.GovKeeper))
	proposal, err = a.GovKeeper.Proposals.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, govv1.StatusPassed, proposal.Status)
	require.Equal(t, unit.MulRaw(4).String(), proposal.FinalTallyResult.NoCount)
	require.Equal(t, deposit.AmountOf(identity.BaseDenom), a.BankKeeper.GetBalance(ctx, proposer, identity.BaseDenom).Amount)
	require.True(t, a.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(govtypes.ModuleName), identity.BaseDenom).IsZero())
}
