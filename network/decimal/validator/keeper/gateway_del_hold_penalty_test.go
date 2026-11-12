package keeper

import (
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestGatewayDELHoldPenaltyPreservesLotsAndOtherPositions(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	first := gatewayDELHoldFixture(2000, 2020, 4)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, first, first.Amount))
	plain := gatewayDELFixture()
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, plain, plain.Amount))
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2001, 0))
	sibling := gatewayDELHoldFixture(2001, 2040, 7)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, sibling, sibling.Amount))
	for i, deposit := range []int64{5, 4} {
		ctx = ctx.WithBlockHeight(int64(12 + i)).WithBlockTime(time.Unix(int64(2002+i), 0))
		first.Amount = first.Amount.AddRaw(deposit)
		require.NoError(t, k.ApplyGatewayDELDelegation(ctx, first, sdkmath.NewInt(deposit)))
	}
	ctx = ctx.WithBlockHeight(14).WithBlockTime(time.Unix(2021, 0))
	first.Amount = first.Amount.SubRaw(1)
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, first, common.Hash{}, sdkmath.OneInt(), time.Unix(2030, 0)))
	owner, operator, err := first.nativePair()
	require.NoError(t, err)
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	ctx = ctx.WithBlockHeight(15)
	cursor := common.Hash{31: 1}
	reduced := sdkmath.NewInt(7)
	wrong := first
	wrong.HoldStartTime--
	require.ErrorContains(t, k.ApplyGatewayDELHoldPenalty(ctx, wrong, reduced, cursor), "differs from the active native binding")
	require.ErrorContains(t, k.ApplyGatewayDELHoldPenalty(ctx, plain, reduced, cursor), "invalid held")
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: bindingRevisionKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyGatewayDELHoldPenalty(ctx, first, reduced, cursor), "injected write failure")
	unchanged, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, unchanged)

	require.NoError(t, k.ApplyGatewayDELHoldPenalty(ctx, first, reduced, cursor))
	require.ErrorContains(t, k.ApplyGatewayDELHoldPenalty(ctx, first, reduced, cursor), "differs from the active native binding")
	binding, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, first.Gateway[:], first.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, stateWord(2020), binding.HoldTimestamp)
	require.Equal(t, stateWord(2000), binding.RawHoldStartTime)
	require.EqualValues(t, 2000, binding.NativeHoldStartTime)
	require.Equal(t, cursor.Bytes(), binding.PenaltyCursor)
	require.Equal(t, reduced, binding.RawAmount)
	require.EqualValues(t, 10, binding.CreatedHeight)
	require.EqualValues(t, 15, binding.UpdatedHeight)
	held, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, reduced.Add(sibling.Amount), held.Stake.Stake.Amount)
	require.Equal(t, []*types.StakeHold{
		{Amount: sdkmath.NewInt(2), HoldStartTime: 2000, HoldEndTime: 2020},
		{Amount: sdkmath.NewInt(3), HoldStartTime: 2002, HoldEndTime: 2020},
		{Amount: sdkmath.NewInt(2), HoldStartTime: 2003, HoldEndTime: 2020},
		{Amount: sibling.Amount, HoldStartTime: 2001, HoldEndTime: 2040},
	}, held.Stake.Holds, "loss of five on [3,5,4] charges [1,2,2] with exact conservation")
	reserve, err := k.GetActiveDELReserve(ctx, operator)
	require.NoError(t, err)
	require.Equal(t, reduced.Add(sibling.Amount).Add(plain.Amount), reserve)

	first.Amount, first.PenaltyCursor = reduced, cursor
	cursor[31]++
	require.NoError(t, k.ApplyGatewayDELHoldPenalty(ctx, first, reduced, cursor))
	zeroLoss, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, held, zeroLoss)
	first.PenaltyCursor = cursor
	cursor[31]++
	require.NoError(t, k.ApplyGatewayDELHoldPenalty(ctx, first, sdkmath.ZeroInt(), cursor))
	require.ErrorIs(t, k.ApplyGatewayDELHoldPenalty(ctx, first, sdkmath.ZeroInt(), cursor), ErrNotFound)
	remaining, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, sibling.Amount, remaining.Stake.Stake.Amount)
	require.Equal(t, held.Stake.Holds[3:], remaining.Stake.Holds)
	require.NoError(t, k.ApplyGatewayDELHoldPenalty(ctx, sibling, sdkmath.ZeroInt(), common.Hash{31: 1}))
	_, err = k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.ErrorIs(t, err, ErrNotFound)
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before.EVMFrozenBindings, after.EVMFrozenBindings)
	require.Equal(t, before.Undelegations, after.Undelegations)
	require.Len(t, after.EVMStakeBindings, 1)
	require.Equal(t, plain.StakeID.Bytes(), after.EVMStakeBindings[0].StakeID)
	reserve, err = k.GetActiveDELReserve(ctx, operator)
	require.NoError(t, err)
	require.Equal(t, plain.Amount, reserve)
}

func TestGatewayDELHoldPenaltyUsesWideProportionalArithmetic(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	hold := gatewayDELHoldFixture(2000, 2020, 1)
	unit := sdkmath.NewIntFromBigInt(new(big.Int).Lsh(big.NewInt(1), 250))
	hold.Amount = unit.MulRaw(3)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, hold, hold.Amount))
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2001, 0))
	hold.Amount = unit.MulRaw(8)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, hold, unit.MulRaw(5)))
	owner, operator, err := hold.nativePair()
	require.NoError(t, err)
	remaining := unit.MulRaw(4)
	require.NoError(t, k.ApplyGatewayDELHoldPenalty(ctx, hold, remaining, common.Hash{31: 1}))
	delegation, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, remaining, delegation.Stake.Stake.Amount)
	require.Equal(t, unit.MulRaw(3).QuoRaw(2), delegation.Stake.Holds[0].Amount)
	require.Equal(t, unit.MulRaw(5).QuoRaw(2), delegation.Stake.Holds[1].Amount)
}
