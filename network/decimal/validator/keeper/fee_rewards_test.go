package keeper

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestNativeFeeRewardsUseCommitAndActiveDELExactlyOnce(t *testing.T) {
	ctx, _, k := environment(t)
	g := genesis(t)
	first, second := g.Validators[0], g.Validators[1]
	zero := inactiveValidator(t, 17)
	g.Validators = append(g.Validators, zero)
	alice := addr(t, identity.Bech32PrefixAccAddr, 3)
	bob := addr(t, identity.Bech32PrefixAccAddr, 4)
	coin := func(amount int64) types.Stake {
		return types.NewStakeCoin(sdk.NewInt64Coin(identity.BaseDenom, amount))
	}
	held := coin(3)
	held.Type = types.StakeType_CoinHold
	held.Holds = []*types.StakeHold{{Amount: sdkmath.NewInt(3), HoldStartTime: 10, HoldEndTime: 20}}
	nft := types.NewStakeNFT("reward-nft", []uint32{1, 2}, sdk.NewInt64Coin(identity.BaseDenom, 5))
	nft.Holds = []*types.StakeHold{{Amount: sdkmath.NewInt(2), HoldStartTime: 10, HoldEndTime: 20, SubTokenIDs: []uint32{1}}}
	g.Delegations = []types.Delegation{
		{Delegator: alice, Validator: first.OperatorAddress, Stake: coin(2)},
		{Delegator: alice, Validator: first.OperatorAddress, Stake: held},
		{Delegator: alice, Validator: first.OperatorAddress, Stake: nft},
		{Delegator: bob, Validator: first.OperatorAddress, Stake: coin(10)},
		{Delegator: alice, Validator: second.OperatorAddress, Stake: coin(17)},
	}
	g.Undelegations = []types.Undelegation{{
		Delegator: bob, Validator: first.OperatorAddress, Entries: []types.UndelegationEntry{{
			CreationHeight: 1, CompletionTime: time.Unix(1000, 0).UTC(), Stake: coin(800),
		}},
	}}
	g.Redelegations = nil
	ctx, _ = ctx.CacheContext()
	require.NoError(t, k.InitGenesis(ctx, g))
	allow := func(sdk.AccAddress) bool { return true }
	require.NoError(t, k.ValidateNativeFeeRewardAdmission(ctx, allow))
	require.ErrorContains(t, k.ValidateNativeFeeRewardAdmission(ctx, func(a sdk.AccAddress) bool {
		return !bytes.Equal(a, bytes.Repeat([]byte{4}, 20))
	}), "owner address is not admitted")
	require.ErrorContains(t, k.ValidateNativeFeeRewardAdmission(ctx, func(a sdk.AccAddress) bool {
		return !bytes.Equal(a, bytes.Repeat([]byte{1}, 20))
	}), "commission address is not admitted")

	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	votes := []NativeFeeRewardVote{{first.OperatorAddress, 1}, {second.OperatorAddress, 2}, {zero.OperatorAddress, 10_000}}
	fees := sdkmath.NewInt(101)
	payouts, err := k.PlanNativeFeeRewards(ctx, fees, votes)
	require.NoError(t, err)
	require.Len(t, payouts, 4)
	// First operator receives 34 atoms, of which 3 are commission. The
	// equal owner weights split 31 as 16/15. Second receives 67 less 6.
	for i, want := range []int64{3, 6, 77, 15} {
		require.Equal(t, sdk.AccAddress(bytes.Repeat([]byte{byte(i + 1)}, 20)), payouts[i].Recipient)
		require.True(t, payouts[i].Amount.Equal(sdkmath.NewInt(want)))
	}
	reversed, err := k.PlanNativeFeeRewards(ctx, fees, []NativeFeeRewardVote{votes[2], votes[1], votes[0]})
	require.NoError(t, err)
	require.Equal(t, payouts, reversed)
	requireEmpty, err := k.PlanNativeFeeRewards(ctx, fees, votes[2:])
	require.NoError(t, err)
	require.Empty(t, requireEmpty)
	_, err = k.PlanNativeFeeRewards(ctx, fees, []NativeFeeRewardVote{votes[0], votes[0]})
	require.ErrorContains(t, err, "duplicate")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
}

func TestNativeFeeRewardsRejectOversizedActivePositions(t *testing.T) {
	for _, test := range []struct {
		name string
		rows int
		size int
	}{
		{name: "rows", rows: maxNativeFeeRewardRows + 1},
		{name: "bytes", rows: 20, size: 60 << 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _, k := environment(t)
			g := genesis(t)
			operator := g.Validators[0]
			g.Delegations = nil
			for i := 0; i < test.rows; i++ {
				id := fmt.Sprintf("%s-%d", strings.Repeat("r", test.size), i)
				stake := types.NewStakeNFT(id, []uint32{1}, sdk.NewInt64Coin(identity.BaseDenom, 1))
				g.Delegations = append(g.Delegations, types.Delegation{
					Delegator: operator.RewardAddress, Validator: operator.OperatorAddress, Stake: stake,
				})
			}
			require.NoError(t, k.InitGenesis(ctx, g))
			err := k.ValidateNativeFeeRewardAdmission(ctx, func(sdk.AccAddress) bool { return true })
			require.ErrorIs(t, err, ErrNativeFeeRewardLimit)
			payouts, err := k.PlanNativeFeeRewards(ctx, sdkmath.OneInt(), []NativeFeeRewardVote{{operator.OperatorAddress, 1}})
			require.ErrorIs(t, err, ErrNativeFeeRewardLimit)
			require.Nil(t, payouts)
		})
	}
}

func TestNativeFeeRewardsFullWidthRounding(t *testing.T) {
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	shares := splitNativeFeeRewards(max, map[string]*big.Int{"b": max, "a": max})
	require.Equal(t, "a", shares[0].recipient)
	require.Equal(t, "b", shares[1].recipient)
	require.Zero(t, new(big.Int).Add(shares[0].amount, shares[1].amount).Cmp(max))
	require.Zero(t, new(big.Int).Sub(shares[0].amount, shares[1].amount).Cmp(big.NewInt(1)))
}
