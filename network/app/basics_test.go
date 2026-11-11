package app

import (
	"testing"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	authoritytypes "github.com/maxwell2010/go-smart-node/network/authority/types"
	"github.com/stretchr/testify/require"
)

func TestEncodingConfigDecodesCompletedAuthorityProposal(t *testing.T) {
	msg := &authoritytypes.MsgExecuteSystemContract{Authority: "governance-module"}
	packed, err := codectypes.NewAnyWithValue(msg)
	require.NoError(t, err)
	genesis := govtypes.DefaultGenesisState()
	genesis.StartingProposalId = 2
	genesis.Proposals = []*govtypes.Proposal{{Id: 1, Status: govtypes.StatusPassed, Messages: []*codectypes.Any{packed}}}
	data, err := MakeEncodingConfig(990001).Codec.MarshalJSON(genesis)
	require.NoError(t, err)

	var decoded govtypes.GenesisState
	require.NoError(t, MakeEncodingConfig(990001).Codec.UnmarshalJSON(data, &decoded))
	require.Len(t, decoded.Proposals, 1)
	require.Len(t, decoded.Proposals[0].Messages, 1)
	require.Equal(t, msg, decoded.Proposals[0].Messages[0].GetCachedValue())
}
