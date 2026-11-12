package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestFrozenDELPenaltyRekeysOrClosesOnlyTheMatchedClaim(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain).WithBlockHeight(10).WithBlockTime(time.Unix(1000, 0))
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	g := bindingGenesisState(t, false)
	before := withdrawalGenesis(t, g, stateWord(4), 2000)
	other := withdrawalGenesis(t, g, stateWord(5), 2100)
	pair := &g.Undelegations[0]
	pair.Entries[0].Stake = types.NewStakeCoin(sdk.NewInt64Coin(identity.BaseDenom, 10))
	before.RawAmount = sdkmath.NewInt(10)
	before.CreatedHeight, before.UpdatedHeight = 7, 7
	before.NativeEntryHash, err = types.NativeQueueEntryHash(1, pair.Delegator, pair.Validator, "", &pair.Entries[0])
	require.NoError(t, err)
	g.EVMFrozenBindings[0] = before
	require.NoError(t, k.InitGenesis(ctx, g))
	cursor := func(n uint64) common.Hash { return common.BytesToHash(stateWord(n)) }
	load := func(id []byte) types.EVMFrozenBinding {
		row, err := k.GetEVMFrozenBinding(ctx, before.Gateway, id)
		require.NoError(t, err)
		return row
	}
	require.ErrorContains(t, k.ApplyGatewayFrozenDELPenalty(ctx, before, 8, cursor(0), cursor(1), sdkmath.NewInt(9)), "predates")
	require.NoError(t, k.ApplyGatewayFrozenDELPenalty(ctx, before, 8, cursor(0), cursor(1), before.RawAmount))
	checkpoint := load(before.FrozenIndex)
	require.Equal(t, before.NativeEntryHash, checkpoint.NativeEntryHash)
	require.ErrorContains(t, k.ApplyGatewayFrozenDELPenalty(ctx, checkpoint, 7, cursor(0), cursor(1), sdkmath.NewInt(9)), "cursor prestate")
	require.NoError(t, k.ApplyGatewayFrozenDELPenalty(ctx, checkpoint, 7, cursor(1), cursor(2), sdkmath.NewInt(9)))
	partial := load(before.FrozenIndex)
	require.NotEqual(t, before.NativeEntryHash, partial.NativeEntryHash)
	require.Equal(t, other, load(other.FrozenIndex))
	current, err := k.GetUndelegation(ctx, pair.Delegator, pair.Validator)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(9), current.Entries[0].Stake.Stake.Amount)
	require.ErrorContains(t, k.ApplyGatewayFrozenDELPenalty(ctx.WithBlockTime(time.Unix(2000, 0)), partial, 7, cursor(2), cursor(3), sdkmath.ZeroInt()), "unmatured")
	require.NoError(t, k.ApplyGatewayFrozenDELPenalty(ctx, partial, 7, cursor(2), cursor(3), sdkmath.ZeroInt()))
	closed := load(before.FrozenIndex)
	require.False(t, closed.Active)
	require.True(t, closed.RawAmount.IsZero())
	require.Equal(t, other, load(other.FrozenIndex))
	current, err = k.GetUndelegation(ctx, pair.Delegator, pair.Validator)
	require.NoError(t, err)
	require.Equal(t, pair.Entries[1:], current.Entries)
	rows, next, err := k.GetValidatorFrozenBindingsPage(ctx, before.SourceValidator, nil, 4)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Equal(t, []types.EVMFrozenBinding{other}, rows)
}
