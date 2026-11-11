package keeper

import (
	"bytes"
	"math/big"
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

func TestStakeDerivedABCIConsensusKeepsEffectivePowerUntilHPlusTwo(t *testing.T) {
	ctx, service, k := environment(t)
	g := types.DefaultGenesisState()
	g.Params.HistoricalEntries = 5
	for id := byte(1); id <= 5; id++ {
		v := validator(t, id, 0)
		v.Status, v.Online = types.BondStatus_Unbonded, true
		g.Validators = append(g.Validators, v)
	}
	g.Validators[2].Online = false
	g.Validators[3].Jailed = true
	a, b, offline, jailed, unapproved := g.Validators[0], g.Validators[1], g.Validators[2], g.Validators[3], g.Validators[4]
	eligible := []string{b.OperatorAddress, offline.OperatorAddress, a.OperatorAddress, jailed.OperatorAddress}
	unit := sdkmath.NewInt(NativeDELAtomsPerPower)
	coin := func(v types.Validator, denom string, amount sdkmath.Int) types.Delegation {
		return types.Delegation{Delegator: v.RewardAddress, Validator: v.OperatorAddress, Stake: types.NewStakeCoin(sdk.NewCoin(denom, amount))}
	}
	aCoin := coin(a, identity.BaseDenom, unit.MulRaw(7).QuoRaw(10))
	aHold := coin(a, identity.BaseDenom, unit.MulRaw(4).QuoRaw(10))
	aHold.Stake.Type = types.StakeType_CoinHold
	aHold.Stake.Holds = []*types.StakeHold{{Amount: aHold.Stake.Stake.Amount, HoldStartTime: 10, HoldEndTime: 20}}
	aNFT := types.Delegation{Delegator: a.RewardAddress, Validator: a.OperatorAddress,
		Stake: types.NewStakeNFT("native-nft", []uint32{1}, sdk.NewCoin(identity.BaseDenom, unit.QuoRaw(5)))}
	bCoin := coin(b, identity.BaseDenom, unit.MulRaw(9).QuoRaw(10))
	g.Delegations = []types.Delegation{aCoin, aHold, aNFT, bCoin, coin(a, "custom", unit.MulRaw(100)),
		coin(offline, identity.BaseDenom, unit.MulRaw(8)), coin(jailed, identity.BaseDenom, unit.MulRaw(9)),
		coin(unapproved, identity.BaseDenom, unit.MulRaw(10))}
	g.Undelegations = []types.Undelegation{{Delegator: b.RewardAddress, Validator: b.OperatorAddress, Entries: []types.UndelegationEntry{{
		CreationHeight: 1, CompletionTime: time.Unix(3000, 0).UTC(), Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, unit.MulRaw(100))),
	}}}}
	require.NoError(t, k.InitGenesis(ctx, g))
	initial, err := k.BootstrapStakeABCIConsensus(ctx, 1, eligible)
	require.NoError(t, err)
	require.Len(t, initial, 1)
	require.EqualValues(t, 1, initial[0].Power, "round the sum of direct, held and NFT reserve once")
	storedA, err := k.GetValidator(ctx, a.OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, types.BondStatus_Bonded, storedA.Status)
	require.Equal(t, a.Rewards, storedA.Rewards)
	storedB, err := k.GetValidator(ctx, b.OperatorAddress)
	require.NoError(t, err)
	require.Zero(t, storedB.Stake, "a pending DEL claim is not active reserve")
	oldSet, err := ConsensusSet([]types.Validator{storedA})
	require.NoError(t, err)

	removeA := []DelegationChange{{Expected: &aCoin}, {Expected: &aHold}}
	guard, _ := ctx.CacheContext()
	require.NoError(t, k.ApplyDelegationBatch(guard, removeA))
	require.ErrorContains(t, k.ValidateStakeConsensusSelection(guard, eligible), "effective native consensus set is empty")
	require.NoError(t, k.ValidateStakeConsensusSelection(ctx, eligible), "the read-only guard has no outer effects")
	bLarger := coin(b, identity.BaseDenom, unit.MulRaw(21).QuoRaw(10))
	require.NoError(t, k.ApplyDelegationBatch(ctx, append(removeA, DelegationChange{Expected: &bCoin, Next: &bLarger})))
	require.NoError(t, k.ValidateStakeConsensusSelection(ctx, eligible))

	block := func(base sdk.Context, height int64, current, next, previous *cmttypes.ValidatorSet) sdk.Context {
		return base.WithBlockHeader(cmtproto.Header{
			ChainID: "mintcandy_300001-1", Height: height, Time: time.Unix(2000+height, 0).UTC(),
			ProposerAddress: current.Validators[0].Address, NextValidatorsHash: next.Hash(),
		}).WithHeaderHash(bytes.Repeat([]byte{byte(height)}, 32)).WithVoteInfos(cometPreviousCommit(height, 1, previous).Votes)
	}
	h1 := block(ctx, 1, oldSet, oldSet, nil)
	badBaseline, _ := h1.CacheContext()
	require.NoError(t, k.SetLastValidatorPowers(badBaseline, []types.LastValidatorPower{{Address: a.OperatorAddress, Power: 2}}))
	_, err = k.ScheduledStakeABCIEndBlock(badBaseline, eligible)
	require.ErrorContains(t, err, "previous native powers differ from the authenticated next ABCI set")
	before, err := k.ExportGenesis(h1)
	require.NoError(t, err)
	broken, err := NewStateKeeper(failingService{service: service, prefix: schedulePrefix})
	require.NoError(t, err)
	_, err = broken.ScheduledStakeABCIEndBlock(h1, eligible)
	require.ErrorContains(t, err, "injected write failure")
	unchanged, err := k.ExportGenesis(h1)
	require.NoError(t, err)
	assertGenesisBytes(t, before, unchanged)

	updates, err := k.ScheduledStakeABCIEndBlock(h1, eligible)
	require.NoError(t, err)
	require.Len(t, updates, 2)
	cometChanges, err := cmttypes.PB2TM.ValidatorUpdates(updates)
	require.NoError(t, err)
	future := oldSet.Copy()
	require.NoError(t, future.UpdateWithChangeSet(cometChanges))
	require.Len(t, future.Validators, 1)
	bConsensus, err := b.GetConsAddr()
	require.NoError(t, err)
	require.True(t, future.HasAddress(bConsensus.Bytes()))
	require.EqualValues(t, 2, future.Validators[0].VotingPower)
	h2 := block(ctx, 2, oldSet, future, oldSet)
	repeated, err := k.ScheduledStakeABCIEndBlock(h2, eligible)
	require.NoError(t, err)
	require.Empty(t, repeated)
	h3 := block(ctx, 3, future, future, oldSet)
	repeated, err = k.ScheduledStakeABCIEndBlock(h3, eligible)
	require.NoError(t, err)
	require.Empty(t, repeated)
	for height, set := range map[int64]*cmttypes.ValidatorSet{1: oldSet, 2: oldSet, 3: future} {
		history, err := k.GetHistoricalInfo(h3, height)
		require.NoError(t, err)
		require.Equal(t, set.Hash(), history.Header.ValidatorsHash)
	}
}

func TestStakeConsensusSelectionRejectsBadIndexAndPowerBounds(t *testing.T) {
	ctx, service, k := environment(t)
	g := types.DefaultGenesisState()
	unit := big.NewInt(NativeDELAtomsPerPower)
	largest := big.NewInt(cmttypes.MaxTotalVotingPower)
	for id := byte(1); id <= 2; id++ {
		v := validator(t, id, 0)
		v.Status, v.Online = types.BondStatus_Unbonded, true
		g.Validators = append(g.Validators, v)
		amount := sdkmath.NewIntFromBigInt(new(big.Int).Mul(largest, unit))
		g.Delegations = append(g.Delegations, types.Delegation{Delegator: v.RewardAddress, Validator: v.OperatorAddress,
			Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, amount))})
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	a, b := g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress
	require.ErrorContains(t, k.ValidateStakeConsensusSelection(ctx, []string{a, a}), "duplicate eligible")
	require.ErrorContains(t, k.ValidateStakeConsensusSelection(ctx, []string{a, b}), "invalid effective native consensus power")
	require.NoError(t, k.ValidateStakeConsensusSelection(ctx, []string{a}))
	tooLarge := new(big.Int).Mul(new(big.Int).Add(largest, big.NewInt(1)), unit)
	require.NoError(t, k.writeActiveDELReserve(ctx, a, tooLarge))
	require.ErrorContains(t, k.ValidateStakeConsensusSelection(ctx, []string{a}), "exceeds the Comet limit")
	require.NoError(t, service.OpenKVStore(ctx).Delete([]byte{activeDELReserveMarker}))
	require.ErrorIs(t, k.ValidateStakeConsensusSelection(ctx, []string{a}), ErrActiveDELReserveIndexUnavailable)
}
