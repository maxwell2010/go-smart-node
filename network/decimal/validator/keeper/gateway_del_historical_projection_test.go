package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestGatewayActiveHistoricalDELPenaltyProjectsExactPrincipalAtomically(t *testing.T) {
	ctx, k, position := delProvenanceGenesis(t)
	position.Amount = position.Amount.AddRaw(48)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(48)))
	ctx = ctx.WithBlockHeight(2).WithBlockTime(time.Unix(2001, 0).UTC())
	position.Amount = position.Amount.AddRaw(100)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(100)))
	ctx = ctx.WithBlockHeight(3).WithBlockTime(time.Unix(2002, 0).UTC())
	position.Amount = position.Amount.AddRaw(40)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(40)))
	position.NFTBacked = sdkmath.NewInt(17)
	nft := gatewayNFTFixture(t, position, 1, 17, []uint32{2})
	require.NoError(t, k.ApplyGatewayNFTDelegation(ctx, nft))
	ctx = ctx.WithBlockHeight(4).WithBlockTime(time.Unix(2003, 0).UTC())
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	events := len(ctx.EventManager().Events())
	key := delProvenanceKey(activeDELProvenancePrefix, position.Gateway[:], position.StakeID[:])
	provenance, err := k.service.OpenKVStore(ctx).Get(key)
	require.NoError(t, err)
	cursor := common.Hash{31: 1}
	remaining := sdkmath.NewInt(197)
	require.ErrorContains(t, k.ApplyGatewayActiveHistoricalDELPenalty(ctx, position, 2, remaining.SubRaw(1), cursor), "differs from the historical exposure")
	require.ErrorContains(t, k.ApplyGatewayActiveHistoricalDELPenalty(ctx, position, 2, remaining, common.Hash{31: 2}), "advance exactly once")
	require.ErrorContains(t, k.ApplyGatewayActiveHistoricalDELPenalty(ctx, position, 0, remaining, cursor), "invalid DEL historical penalty infraction height")

	broken, err := NewStateKeeperWithDomain(failingService{service: k.service, prefix: bindingRevisionKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyGatewayActiveHistoricalDELPenalty(ctx, position, 2, remaining, cursor), "injected write failure")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	unchanged, err := k.service.OpenKVStore(ctx).Get(key)
	require.NoError(t, err)
	require.Equal(t, provenance, unchanged)
	require.Len(t, ctx.EventManager().Events(), events)

	require.NoError(t, k.ApplyGatewayActiveHistoricalDELPenalty(ctx, position, 2, remaining, cursor))
	require.ErrorContains(t, k.ApplyGatewayActiveHistoricalDELPenalty(ctx, position, 2, remaining, cursor), "differs from the active native binding")
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, position), []int64{0, 1, 2, 3}, []int64{12, 45, 100, 40})
	row, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	require.True(t, row.RawAmount.Equal(remaining.Add(position.NFTBacked)))
	require.True(t, row.RawNftBackedAmount.Equal(position.NFTBacked))
	require.Equal(t, cursor.Bytes(), row.PenaltyCursor)
	owner, operator, err := position.nativePair()
	require.NoError(t, err)
	delegation, err := k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.True(t, delegation.Stake.Stake.Amount.Equal(remaining))
	assertActiveDELReserve(t, ctx, k, operator, 214)
}

func TestGatewayActiveHistoricalDELHoldPenaltyPreservesOriginalLots(t *testing.T) {
	ctx, k, _ := delProvenanceGenesis(t)
	heldPosition := func(start, deadline, amount int64) GatewayDELStake {
		position := gatewayDELHoldFixture(start, deadline, amount)
		position.Validator = common.BytesToAddress(stateAddress(2))
		position.StakeID = crypto.Keccak256Hash(position.Validator[:], position.Beneficiary[:], position.WrappedDEL[:], stateWord(0), stateWord(uint64(deadline)))
		return position
	}
	held := heldPosition(2000, 2020, 20)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	ctx = ctx.WithBlockHeight(2).WithBlockTime(time.Unix(2000, 500).UTC())
	held.Amount = held.Amount.AddRaw(60)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, sdkmath.NewInt(60)))
	ctx = ctx.WithBlockHeight(3).WithBlockTime(time.Unix(2001, 0).UTC())
	held.Amount = held.Amount.AddRaw(21)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, sdkmath.NewInt(21)))
	sibling := heldPosition(2001, 2040, 13)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, sibling, sibling.Amount))
	ctx = ctx.WithBlockHeight(4).WithBlockTime(time.Unix(2002, 0).UTC())
	owner, operator, err := held.nativePair()
	require.NoError(t, err)
	before, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, []*types.StakeHold{
		{Amount: sdkmath.NewInt(80), HoldStartTime: 2000, HoldEndTime: 2020},
		{Amount: sdkmath.NewInt(21), HoldStartTime: 2001, HoldEndTime: 2020},
		{Amount: sibling.Amount, HoldStartTime: 2001, HoldEndTime: 2040},
	}, before.Stake.Holds)

	// A matching total alone cannot authorize changes to the original lots.
	child, _ := ctx.CacheContext()
	changed := before
	changed.Stake = gatewayDELHoldStake(before.Stake.Stake.Amount, []*types.StakeHold{
		{Amount: sdkmath.NewInt(79), HoldStartTime: 2000, HoldEndTime: 2020},
		{Amount: sdkmath.NewInt(22), HoldStartTime: 2001, HoldEndTime: 2020},
		before.Stake.Holds[2],
	})
	require.NoError(t, k.ApplyDelegationBatch(child, []DelegationChange{{Expected: &before, Next: &changed}}))
	cursor := common.Hash{31: 1}
	remaining := sdkmath.NewInt(100)
	require.ErrorContains(t, k.ApplyGatewayActiveHistoricalDELPenalty(child, held, 2, remaining, cursor), "provenance differs from a native hold lot")

	require.NoError(t, k.ApplyGatewayActiveHistoricalDELPenalty(ctx, held, 2, remaining, cursor))
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, held), []int64{1, 2, 3}, []int64{19, 60, 21})
	requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, sibling), []int64{3}, []int64{13})
	after, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, []*types.StakeHold{
		{Amount: sdkmath.NewInt(79), HoldStartTime: 2000, HoldEndTime: 2020},
		before.Stake.Holds[1], before.Stake.Holds[2],
	}, after.Stake.Holds)
	assertActiveDELReserve(t, ctx, k, operator, 113)
	row, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, held.Gateway[:], held.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, stateWord(2000), row.RawHoldStartTime)
	require.Equal(t, stateWord(2020), row.HoldTimestamp)
	require.Equal(t, cursor.Bytes(), row.PenaltyCursor)

	// A later historical penalty on only 19 exposed atoms rounds to zero.
	held.Amount, held.PenaltyCursor = remaining, cursor
	key := delProvenanceKey(activeDELProvenancePrefix, held.Gateway[:], held.StakeID[:])
	prior, err := k.service.OpenKVStore(ctx).Get(key)
	require.NoError(t, err)
	nextCursor := common.Hash{31: 2}
	require.NoError(t, k.ApplyGatewayActiveHistoricalDELPenalty(ctx, held, 2, remaining, nextCursor))
	row, err = k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, held.Gateway[:], held.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, nextCursor.Bytes(), row.PenaltyCursor)
	actual, err := k.service.OpenKVStore(ctx).Get(key)
	require.NoError(t, err)
	require.Equal(t, prior, actual)
	unmodified, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, after, unmodified)
}
