package keeper

import (
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func transitionContext(t *testing.T, ctx sdk.Context, height int64, vals []types.Validator) sdk.Context {
	t.Helper()
	set, err := ConsensusSet(vals)
	require.NoError(t, err)
	return ctx.WithBlockHeader(cmtproto.Header{Height: height, ChainID: "mintcandy_300001-1", Time: time.Unix(height, 0), ValidatorsHash: set.Hash()})
}

func TestNativeValidatorTransitionPreservesActualHPlusTwoHistory(t *testing.T) {
	ctx, _, k := environment(t)
	g := genesis(t)
	g.Params.HistoricalEntries = 10
	require.NoError(t, k.InitGenesis(ctx, g))
	effective := append([]types.Validator(nil), g.Validators...)
	first := g.Validators[0]
	first.Stake = 150
	removed := g.Validators[1]
	removed.Jailed = true
	added := validator(t, 3, 50)
	for _, v := range []types.Validator{first, removed, added} {
		require.NoError(t, k.SetValidator(ctx, v))
	}
	ctx = transitionContext(t, ctx, 1, effective)
	updates, err := k.EndBlockValidatorUpdates(ctx, effective, []string{first.OperatorAddress, removed.OperatorAddress, added.OperatorAddress})
	require.NoError(t, err)
	require.Len(t, updates, 3)
	comet, err := ConsensusSet(effective)
	require.NoError(t, err)
	h1hash := comet.Hash()
	// The same effective set remains active atH+1. The H updates are applied
	// only to the set used atH+2, as Comet's real change-set operation does.
	ctx = transitionContext(t, ctx, 2, effective)
	repeated, err := k.EndBlockValidatorUpdates(ctx, effective, []string{first.OperatorAddress, removed.OperatorAddress, added.OperatorAddress})
	require.NoError(t, err)
	require.Empty(t, repeated)
	require.Equal(t, h1hash, comet.Hash())
	changes, err := cmttypes.PB2TM.ValidatorUpdates(updates)
	require.NoError(t, err)
	require.NoError(t, comet.UpdateWithChangeSet(changes))
	next := []types.Validator{first, added}
	selected, err := ConsensusSet(next)
	require.NoError(t, err)
	require.Equal(t, selected.Hash(), comet.Hash())
	ctx = transitionContext(t, ctx, 3, next)
	repeated, err = k.EndBlockValidatorUpdates(ctx, next, []string{first.OperatorAddress, removed.OperatorAddress, added.OperatorAddress})
	require.NoError(t, err)
	require.Empty(t, repeated)
	h1, err := k.GetHistoricalInfo(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, h1hash, h1.Header.ValidatorsHash)
	h3, err := k.GetHistoricalInfo(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, comet.Hash(), h3.Header.ValidatorsHash)
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 200, exported.LastTotalPower)
	require.Len(t, exported.LastValidatorPowers, 2)
}

func TestNativeValidatorTransitionRollsBackWhenHistoricalWriteFails(t *testing.T) {
	ctx, _, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	ctx = transitionContext(t, ctx, 1, g.Validators)
	_, err := k.EndBlockValidatorUpdates(ctx, g.Validators, []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress})
	require.NoError(t, err)
	original, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	changed := g.Validators[0]
	changed.Stake = 250
	require.NoError(t, k.SetValidator(ctx, changed))
	ctx = ctx.WithBlockTime(ctx.BlockTime().Add(time.Second)) // Different immutable history row.
	_, err = k.EndBlockValidatorUpdates(ctx, g.Validators, []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress})
	require.ErrorContains(t, err, "immutable")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, original.LastValidatorPowers, after.LastValidatorPowers)
	require.Equal(t, original.LastTotalPower, after.LastTotalPower)
}

func TestNativeValidatorTransitionRefusesEmptyOrWrongEffectiveSet(t *testing.T) {
	ctx, _, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	ctx = transitionContext(t, ctx, 1, g.Validators)
	wrong := append([]types.Validator(nil), g.Validators...)
	wrong[0].Stake++
	_, err := k.EndBlockValidatorUpdates(ctx, wrong, []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress})
	require.ErrorContains(t, err, "header")
	for _, v := range g.Validators {
		v.Jailed = true
		require.NoError(t, k.SetValidator(ctx, v))
	}
	_, err = k.EndBlockValidatorUpdates(ctx, g.Validators, []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress})
	require.ErrorContains(t, err, "empty")
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, g.LastValidatorPowers, exported.LastValidatorPowers)
}

func TestNativeValidatorSelectionCanReenterFromUnbondedMetadata(t *testing.T) {
	ctx, _, k := environment(t)
	g := genesis(t)
	g.Params.MaxValidators = 1
	effective := append([]types.Validator(nil), g.Validators...)
	g.Validators[0].Status = types.BondStatus_Unbonded
	require.NoError(t, k.InitGenesis(ctx, g))
	eligible := []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress}
	ctx = transitionContext(t, ctx, 1, effective)
	updates, err := k.EndBlockValidatorUpdates(ctx, effective, eligible)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.Zero(t, updates[0].Power)
	first, err := k.GetValidator(ctx, g.Validators[0].OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, types.BondStatus_Unbonded, first.Status)
	// Authentic contract eligibility is independent of topN BondStatus.
	first.Stake = 300
	require.NoError(t, k.SetValidator(ctx, first))
	ctx = transitionContext(t, ctx, 2, effective)
	updates, err = k.EndBlockValidatorUpdates(ctx, effective, eligible)
	require.NoError(t, err)
	require.Len(t, updates, 2)
	first, err = k.GetValidator(ctx, first.OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, types.BondStatus_Bonded, first.Status)
	second, err := k.GetValidator(ctx, g.Validators[1].OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, types.BondStatus_Unbonded, second.Status)
}

func TestNativeValidatorTransitionRejectsCorruptPreviousTotal(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	ctx = transitionContext(t, ctx, 1, g.Validators)
	require.NoError(t, service.OpenKVStore(ctx).Set([]byte{3}, make([]byte, 8)))
	_, err := k.EndBlockValidatorUpdates(ctx, g.Validators, []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress})
	require.ErrorContains(t, err, "total power mismatch")
}
