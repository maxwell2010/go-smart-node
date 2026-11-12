package keeper

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestRetiredNativeValidatorPreservesHistoricalPenaltyLiability(t *testing.T) {
	ctx, keeper, _ := delProvenanceGenesis(t)
	require.NoError(t, keeper.InitializeNativeLiveValidatorRoster(ctx.WithBlockHeight(0)))
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize).WithBlockHeight(10).WithBlockTime(time.Unix(2010, 0).UTC())
	operator := addr(t, identity.Bech32PrefixValAddr, 2)
	v, err := keeper.GetValidator(ctx, operator)
	require.NoError(t, err)
	v.Status, v.Online = types.BondStatus_Unbonded, true
	require.NoError(t, keeper.SetValidator(ctx, v))
	consensus, err := v.GetConsAddr()
	require.NoError(t, err)
	remaining, err := keeper.GetValidator(ctx, addr(t, identity.Bech32PrefixValAddr, 1))
	require.NoError(t, err)
	remaining.Status, remaining.Stake = types.BondStatus_Bonded, 1
	for _, height := range []int64{11, 12} {
		row, err := canonicalScheduledSet(height, []types.Validator{remaining})
		require.NoError(t, err)
		require.NoError(t, keeper.writeScheduledSet(ctx, row))
	}

	candidate := queuedDuplicateVote(t, 2, 2, 4)
	candidate.ConsensusAddress = consensus
	pending, _ := ctx.CacheContext()
	_, err = keeper.enqueueNativeDuplicateVoteCandidates(pending, []NativeDuplicateVoteCandidate{candidate})
	require.NoError(t, err)
	require.ErrorContains(t, keeper.RetireNativeValidator(pending, operator), "no unresolved historical penalty")
	before, err := keeper.NativeLiveValidatorOperators(pending)
	require.NoError(t, err)
	require.Contains(t, before, operator)

	require.NoError(t, keeper.RetireNativeValidator(ctx, operator))
	retired, err := keeper.IsNativeValidatorRetired(ctx, operator)
	require.NoError(t, err)
	require.True(t, retired)
	roster, err := keeper.NativeLiveValidatorOperators(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{remaining.OperatorAddress}, roster)
	require.ErrorIs(t, keeper.SetValidator(ctx, v), ErrNativeValidatorRetired)

	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2011, 0).UTC())
	_, err = keeper.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{candidate})
	require.NoError(t, err)
	jobs, err := keeper.PendingNativeDuplicateVotePenalties(ctx, 1)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.NoError(t, keeper.FinalizeNativeDuplicateVotePenalty(ctx, jobs[0]))
	history, err := keeper.GetNativeDuplicateVoteExclusion(ctx, consensus)
	require.NoError(t, err)
	require.Equal(t, NativeDuplicateVoteExclusion{InfractionHeight: 4, SettledHeight: 11}, history)
	actual, err := keeper.GetValidatorByConsAddr(ctx, consensus)
	require.NoError(t, err)
	require.False(t, actual.Online)
	require.True(t, actual.Jailed)
	unchanged, err := keeper.NativeLiveValidatorOperators(ctx)
	require.NoError(t, err)
	require.Equal(t, roster, unchanged)
	require.ErrorIs(t, keeper.ReleaseNativeDowntimeJail(ctx, operator), ErrNativeValidatorRetired)
}
