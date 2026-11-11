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

func gatewayDELHoldFixture(start, end, amount int64) GatewayDELStake {
	position := gatewayDELFixture()
	position.HoldStartTime, position.HoldDeadline = start, end
	position.Amount = sdkmath.NewInt(amount)
	position.StakeID = crypto.Keccak256Hash(position.Validator[:], position.Beneficiary[:], position.WrappedDEL[:], stateWord(0), stateWord(uint64(end)))
	return position
}

func TestGatewayDELHoldsPreserveBucketsAndFIFOLots(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	first := gatewayDELHoldFixture(2000, 2020, 12)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, first, first.Amount))
	plain := gatewayDELFixture()
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, plain, plain.Amount))
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2001, 0))
	second := gatewayDELHoldFixture(2001, 2040, 7)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, second, second.Amount))
	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2002, 0))
	first.Amount = sdkmath.NewInt(20)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, first, sdkmath.NewInt(8)))

	owner, operator, err := first.nativePair()
	require.NoError(t, err)
	held, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(27), held.Stake.Stake.Amount)
	require.Equal(t, []*types.StakeHold{
		{Amount: sdkmath.NewInt(12), HoldStartTime: 2000, HoldEndTime: 2020},
		{Amount: sdkmath.NewInt(8), HoldStartTime: 2002, HoldEndTime: 2020},
		{Amount: sdkmath.NewInt(7), HoldStartTime: 2001, HoldEndTime: 2040},
	}, held.Stake.Holds)
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, before.EVMStakeBindings, 3)

	first.Amount = sdkmath.NewInt(5)
	part := sdkmath.NewInt(15)
	index := common.Hash{}
	due := time.Unix(2030, 0).UTC()
	ctx = ctx.WithBlockHeight(13).WithBlockTime(time.Unix(2020, 0))
	require.ErrorContains(t, k.ApplyGatewayDELWithdrawal(ctx, first, index, part, due), "hold has not expired")
	ctx = ctx.WithBlockHeight(14).WithBlockTime(time.Unix(2021, 0))
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: undelegationPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyGatewayDELWithdrawal(ctx, first, index, part, due), "injected write failure")
	unchanged, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, unchanged)
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, first, index, part, due))

	queued, err := k.GetUndelegation(ctx, owner, operator)
	require.NoError(t, err)
	require.Len(t, queued.Entries, 1)
	require.Equal(t, part, queued.Entries[0].Stake.Stake.Amount)
	require.Equal(t, types.StakeType_CoinHold, queued.Entries[0].Stake.Type)
	require.Equal(t, []*types.StakeHold{
		{Amount: sdkmath.NewInt(12), HoldStartTime: 2000, HoldEndTime: 2020},
		{Amount: sdkmath.NewInt(3), HoldStartTime: 2002, HoldEndTime: 2020},
	}, queued.Entries[0].Stake.Holds)
	held, err = k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(12), held.Stake.Stake.Amount)
	require.Equal(t, []*types.StakeHold{
		{Amount: sdkmath.NewInt(5), HoldStartTime: 2002, HoldEndTime: 2020},
		{Amount: sdkmath.NewInt(7), HoldStartTime: 2001, HoldEndTime: 2040},
	}, held.Stake.Holds)
	plainRow, err := k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, plain.Amount, plainRow.Stake.Stake.Amount)

	ctx = ctx.WithBlockHeight(15).WithBlockTime(due)
	result, err := k.SettleMatureWithdrawals(ctx, 1_000_000, k.ApplyWithdrawalCompletion)
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Settled: 1}, result)
	_, err = k.GetUndelegation(ctx, owner, operator)
	require.ErrorIs(t, err, ErrNotFound)
	after, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, held, after)

	first.Amount = sdkmath.ZeroInt()
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, first, common.BytesToHash([]byte{1}), sdkmath.NewInt(5), due.Add(time.Second)))
	_, err = k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, first.Gateway[:], first.StakeID[:])
	require.ErrorIs(t, err, ErrNotFound)
	held, err = k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, second.Amount, held.Stake.Stake.Amount)
	require.Len(t, held.Stake.Holds, 1)
	require.Equal(t, second.HoldDeadline, held.Stake.Holds[0].HoldEndTime)
}
