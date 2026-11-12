package keeper

import (
	"bytes"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestNativeDowntimeOutcomeIsAtomicAndRemovalTakesEffectAtHPlusTwo(t *testing.T) {
	ctx, service, k := environment(t)
	g := types.DefaultGenesisState()
	g.Params.MaxValidators = 2
	g.Params.SignedBlocksWindow = 1
	g.Params.MinSignedPerWindow = sdkmath.LegacyOneDec()
	g.Params.DowntimeJailDuration = time.Minute + 321*time.Millisecond
	var eligible []string
	for i, power := range []int64{2, 5, 1} {
		v := validator(t, byte(i+1), 0)
		v.Status, v.Online = types.BondStatus_Unbonded, true
		g.Validators = append(g.Validators, v)
		eligible = append(eligible, v.OperatorAddress)
		g.Delegations = append(g.Delegations, types.Delegation{Delegator: v.RewardAddress, Validator: v.OperatorAddress,
			Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, sdkmath.NewInt(power).MulRaw(NativeDELAtomsPerPower)))})
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	_, err := k.BootstrapStakeABCIConsensus(ctx, 1, eligible)
	require.NoError(t, err)
	rows, err := k.readScheduledPair(ctx.WithBlockHeight(1))
	require.NoError(t, err)
	initial, err := ConsensusSet(rows[0].Validators)
	require.NoError(t, err)
	operator := eligible[0]
	consensus, err := g.Validators[0].GetConsAddr()
	require.NoError(t, err)
	block := func(height int64, current, next, previous *cmttypes.ValidatorSet) sdk.Context {
		prior := cometPreviousCommit(height, 1, previous)
		if height == 2 {
			for i := range prior.Votes {
				if bytes.Equal(prior.Votes[i].Validator.Address, consensus) {
					prior.Votes[i].BlockIdFlag = cmtproto.BlockIDFlagAbsent
				}
			}
		}
		return ctx.WithExecMode(sdk.ExecModeFinalize).WithBlockHeader(cmtproto.Header{
			ChainID: "native-downtime-outcome-1", Height: height, Time: time.Unix(2000+height, 456789).UTC(),
			NextValidatorsHash: next.Hash(), ProposerAddress: current.Validators[0].Address,
		}).WithHeaderHash(bytes.Repeat([]byte{byte(height)}, 32)).WithVoteInfos(prior.Votes)
	}
	h1 := block(1, initial, initial, nil)
	_, err = k.RecordABCICommit(h1)
	require.NoError(t, err)
	_, err = k.ScheduledStakeABCIEndBlock(h1, eligible)
	require.NoError(t, err)
	h2 := block(2, initial, initial, initial)
	_, err = k.RecordABCICommit(h2)
	require.NoError(t, err)
	planned, err := k.PlanNativeDowntimePenalties(h2)
	require.NoError(t, err)
	require.Len(t, planned, 1)
	candidate := planned[0]
	require.Equal(t, operator, candidate.Operator)
	require.EqualValues(t, 1, candidate.InfractionHeight)
	require.EqualValues(t, 2, candidate.ObservedPower)
	before, err := k.GetValidator(h2, operator)
	require.NoError(t, err)
	baseline, err := k.previousPositivePowers(h2)
	require.NoError(t, err)
	schedule, err := k.readScheduledPair(h2)
	require.NoError(t, err)

	wrong := candidate
	wrong.JailUntil = wrong.JailUntil.Add(time.Nanosecond)
	require.ErrorContains(t, k.ApplyNativeDowntimeOutcome(h2, wrong), "differs from the authenticated current candidate")
	broken, err := NewStateKeeper(failingService{service: service, prefix: nativeInfractionPrefix})
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyNativeDowntimeOutcome(h2, candidate), "injected write failure")
	unchanged, err := k.GetValidator(h2, operator)
	require.NoError(t, err)
	require.Equal(t, before, unchanged)
	_, err = k.GetNativeDowntimeOutcome(h2, consensus, candidate.InfractionHeight)
	require.ErrorIs(t, err, ErrNotFound)

	discarded, _ := h2.CacheContext()
	require.NoError(t, k.ApplyNativeDowntimeOutcome(discarded, candidate))
	_, err = k.GetNativeDowntimeOutcome(h2, consensus, candidate.InfractionHeight)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, k.ApplyNativeDowntimeOutcome(h2, candidate))
	outcome, err := k.GetNativeDowntimeOutcome(h2, consensus, candidate.InfractionHeight)
	require.NoError(t, err)
	require.Equal(t, NativeDowntimeOutcome{
		Reason: "downtime", ConsensusAddress: consensus, InfractionHeight: 1, JailUntil: candidate.JailUntil,
	}, outcome)
	require.ErrorIs(t, k.ApplyNativeDowntimeOutcome(h2, candidate), ErrNativeDowntimeAlreadyApplied)
	jailed, err := k.GetValidator(h2, operator)
	require.NoError(t, err)
	before.Jailed = true
	require.Equal(t, before, jailed)
	powers, err := k.previousPositivePowers(h2)
	require.NoError(t, err)
	require.Equal(t, baseline, powers)
	effective, err := k.readScheduledPair(h2)
	require.NoError(t, err)
	require.Equal(t, schedule, effective)
	window, err := k.GetSigningWindow(h2, operator)
	require.NoError(t, err)
	require.Equal(t, candidate.Window, window)

	updates, err := k.ScheduledStakeABCIEndBlock(h2, eligible)
	require.NoError(t, err)
	require.Len(t, updates, 2)
	changes, err := cmttypes.PB2TM.ValidatorUpdates(updates)
	require.NoError(t, err)
	future := initial.Copy()
	require.NoError(t, future.UpdateWithChangeSet(changes))
	require.False(t, future.HasAddress(consensus))
	replacement, err := g.Validators[2].GetConsAddr()
	require.NoError(t, err)
	require.True(t, future.HasAddress(replacement))
	jailed, err = k.GetValidator(h2, operator)
	require.NoError(t, err)
	require.True(t, jailed.Jailed)
	require.Equal(t, types.BondStatus_Unbonded, jailed.Status)
	require.NoError(t, k.VerifyABCIConsensusStartup(h2, 2))
	for _, next := range []sdk.Context{block(3, initial, future, initial), block(4, future, future, initial)} {
		_, err := k.RecordABCICommit(next)
		require.NoError(t, err)
		updates, err := k.ScheduledStakeABCIEndBlock(next, eligible)
		require.NoError(t, err)
		require.Empty(t, updates)
	}
	for height, set := range map[int64]*cmttypes.ValidatorSet{2: initial, 3: initial, 4: future} {
		info, err := k.GetHistoricalInfo(h2, height)
		require.NoError(t, err)
		require.Equal(t, set.Hash(), info.Header.ValidatorsHash)
	}
	require.ErrorIs(t, k.ApplyNativeDowntimeOutcome(h2, candidate), ErrNativeDowntimeAlreadyApplied)
}
