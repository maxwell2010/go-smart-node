package app

import (
	"bytes"
	"testing"
	"time"

	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

// Exercise the real App EndBlock and bank stores. The fixture creates due SDK
// entries through keeper APIs, then funds only enough to pay the first entry.
func checkMatureSDKUnbondRetry(t *testing.T, a *App, funder sdk.AccAddress, vals *cmttypes.ValidatorSet, start time.Time) {
	t.Helper()
	validator := sdk.ValAddress(vals.Validators[0].Address)
	bad := sdk.AccAddress(bytes.Repeat([]byte{0xa1}, 20))
	good := sdk.AccAddress(bytes.Repeat([]byte{0xa2}, 20))
	pool := a.AccountKeeper.GetModuleAddress(stakingtypes.NotBondedPoolName)
	due := start.Add(time.Hour)
	ctx := func(at time.Time) sdk.Context {
		return a.NewUncachedContext(false, cmtproto.Header{ChainID: a.chainID, Height: a.LastBlockHeight() + 1, Time: at, ProposerAddress: vals.Proposer.Address})
	}
	balance := func(c sdk.Context, addr sdk.AccAddress) math.Int {
		return a.BankKeeper.GetBalance(c, addr, identity.BaseDenom).Amount
	}
	fundPool := func(c sdk.Context, n int64) {
		t.Helper()
		require.NoError(t, a.BankKeeper.SendCoinsFromAccountToModule(c, funder, stakingtypes.NotBondedPoolName, sdk.NewCoins(sdk.NewInt64Coin(identity.BaseDenom, n))))
	}
	fixture := ctx(due.Add(-time.Second))
	require.True(t, balance(fixture, pool).IsZero())
	for _, recipient := range []sdk.AccAddress{bad, good} {
		require.Nil(t, a.AccountKeeper.GetAccount(fixture, recipient))
		a.AccountKeeper.SetAccount(fixture, a.AccountKeeper.NewAccountWithAddress(fixture, recipient))
	}
	var expected stakingtypes.UnbondingDelegation
	for i, n := range []int64{7, 11, 3} {
		var err error
		expected, err = a.StakingKeeper.SetUnbondingDelegationEntry(fixture, bad, validator, int64(i+1), due, math.NewInt(n))
		require.NoError(t, err)
		// Repeated time-slice references are normal for multiple entries of
		// the same pair. There must be only one retry reference per pair.
		require.NoError(t, a.StakingKeeper.InsertUBDQueue(fixture, expected, due))
	}
	heldID := expected.Entries[2].UnbondingId
	require.NoError(t, a.StakingKeeper.PutUnbondingOnHold(fixture, heldID))
	expected, err := a.StakingKeeper.GetUnbondingDelegation(fixture, bad, validator)
	require.NoError(t, err)
	healthy, err := a.StakingKeeper.SetUnbondingDelegationEntry(fixture, good, validator, 1, due, math.NewInt(5))
	require.NoError(t, err)
	require.NoError(t, a.StakingKeeper.InsertUBDQueue(fixture, healthy, due))
	fundPool(fixture, 7)

	finalize := func(at time.Time) *abci.ResponseFinalizeBlock {
		t.Helper()
		result, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: a.LastBlockHeight() + 1, Time: at, NextValidatorsHash: vals.Hash(), ProposerAddress: vals.Proposer.Address})
		require.NoError(t, err)
		_, err = a.Commit()
		require.NoError(t, err)
		return result
	}
	completions := func(result *abci.ResponseFinalizeBlock, recipient sdk.AccAddress, amount int64) {
		t.Helper()
		count := 0
		for _, event := range result.Events {
			if event.Type != stakingtypes.EventTypeCompleteUnbonding {
				continue
			}
			attributes := map[string]string{}
			for _, attribute := range event.Attributes {
				attributes[attribute.Key] = attribute.Value
			}
			require.Equal(t, recipient.String(), attributes[stakingtypes.AttributeKeyDelegator])
			require.Equal(t, sdk.NewInt64Coin(identity.BaseDenom, amount).String(), attributes[sdk.AttributeKeyAmount])
			count++
		}
		if recipient == nil {
			require.Zero(t, count)
		} else {
			require.Equal(t, 1, count)
		}
	}
	completions(finalize(due.Add(-time.Second)), nil, 0)
	failed := finalize(due)
	completions(failed, good, 5)
	for _, event := range failed.Events {
		for _, attribute := range event.Attributes {
			require.NotEqual(t, bad.String(), attribute.Value, "failed completion emitted an SDK event")
		}
	}
	assertRetry := func(c sdk.Context, at time.Time) {
		t.Helper()
		require.True(t, balance(c, bad).IsZero(), "the first payment of a failed pair must roll back")
		require.Equal(t, math.NewInt(5), balance(c, good), "the healthy pair completes exactly once")
		require.Equal(t, math.NewInt(2), balance(c, pool))
		saved, err := a.StakingKeeper.GetUnbondingDelegation(c, bad, validator)
		require.NoError(t, err)
		require.Equal(t, expected, saved)
		for _, entry := range expected.Entries {
			_, err := a.StakingKeeper.GetUnbondingDelegationByUnbondingID(c, entry.UnbondingId)
			require.NoError(t, err, "failed attempts must retain the entry index")
		}
		queued, err := a.StakingKeeper.GetUBDQueueTimeSlice(c, at)
		require.NoError(t, err)
		require.Equal(t, []stakingtypes.DVPair{{DelegatorAddress: expected.DelegatorAddress, ValidatorAddress: expected.ValidatorAddress}}, queued)
	}
	committed, err := a.CreateQueryContext(a.LastBlockHeight(), false)
	require.NoError(t, err)
	assertRetry(committed, due)
	next := due.Add(time.Second)
	completions(finalize(next), nil, 0)
	assertRetry(ctx(next), next)

	// Restore the missing funds. The due entries pay once, while the SDK's
	// explicit hold remains in force and retains its index until release.
	fundPool(ctx(next), 19)
	retryTime := next.Add(time.Second)
	completions(finalize(retryTime), bad, 18)
	settled := ctx(retryTime)
	require.Equal(t, math.NewInt(18), balance(settled, bad))
	require.Equal(t, math.NewInt(5), balance(settled, good))
	require.Equal(t, math.NewInt(3), balance(settled, pool))
	remaining, err := a.StakingKeeper.GetUnbondingDelegation(settled, bad, validator)
	require.NoError(t, err)
	require.Equal(t, expected.Entries[2:], remaining.Entries)
	for _, entry := range expected.Entries[:2] {
		_, err := a.StakingKeeper.GetUnbondingDelegationByUnbondingID(settled, entry.UnbondingId)
		require.ErrorIs(t, err, stakingtypes.ErrNoUnbondingDelegation)
	}
	require.NoError(t, a.StakingKeeper.UnbondingCanComplete(settled, heldID))
	finalTime := retryTime.Add(time.Second)
	completions(finalize(finalTime), nil, 0)
	require.Equal(t, math.NewInt(21), balance(ctx(finalTime), bad))
	require.Equal(t, math.NewInt(5), balance(ctx(finalTime), good))
	require.True(t, balance(ctx(finalTime), pool).IsZero())
	_, err = a.StakingKeeper.GetUnbondingDelegation(ctx(finalTime), bad, validator)
	require.ErrorIs(t, err, stakingtypes.ErrNoUnbondingDelegation)
}
