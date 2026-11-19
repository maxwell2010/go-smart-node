package main

import (
	"encoding/json"
	"testing"

	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/stretchr/testify/require"
)

func TestPrepareGraceGenesisPreservesStateAndRestrictsDomain(t *testing.T) {
	profile := ownedapp.WithdrawalProfile{EnableNativeConsensus: true, EnableNativeOrdinaryDELDowntime: true, EnableNativeDuplicateVote: true}
	fixture := func(chain string) []byte {
		raw, err := json.Marshal(map[string]interface{}{"chain_id": chain, "initial_height": "1", "validators": []string{"unchanged"}, "app_state": map[string]interface{}{ownedapp.WithdrawalGenesisKey: profile, "bank": map[string]interface{}{"sentinel": "unchanged"}}})
		require.NoError(t, err)
		return raw
	}
	input := fixture("mintcandy_990001-1")
	output, err := prepareTestGraceGenesis(input, 990001, 100)
	require.NoError(t, err)
	var before, after map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(input, &before))
	require.NoError(t, json.Unmarshal(output, &after))
	require.JSONEq(t, string(before["validators"]), string(after["validators"]))
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(after["app_state"], &state))
	require.JSONEq(t, `{"sentinel":"unchanged"}`, string(state["bank"]))
	var got ownedapp.WithdrawalProfile
	require.NoError(t, json.Unmarshal(state[ownedapp.WithdrawalGenesisKey], &got))
	require.Equal(t, int64(100), got.TestGraceUntilHeight)
	got.TestGraceUntilHeight = 0
	require.Equal(t, profile, got)
	for _, until := range []int64{-1, 0} {
		_, err := prepareTestGraceGenesis(input, 990001, until)
		require.Error(t, err)
	}
	for _, evm := range []uint64{75, 20202020, 990100, 9905401} {
		_, err := prepareTestGraceGenesis(input, evm, 100)
		require.Error(t, err)
	}
	_, err = prepareTestGraceGenesis(fixture("mintcandy_990002-1"), 990001, 100)
	require.Error(t, err)
}
