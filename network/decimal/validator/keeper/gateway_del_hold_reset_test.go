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

func TestGatewayDELHoldResetKeepsSiblingAndPendingState(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	ctx = ctx.WithEventManager(sdk.NewEventManager())
	held := gatewayDELHoldFixture(2000, 2020, 12)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	sibling := gatewayDELHoldFixture(2000, 2040, 7)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, sibling, sibling.Amount))
	plain := gatewayDELFixture()
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, plain, plain.Amount))
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2001, 0))
	held.Amount = sdkmath.NewInt(20)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, sdkmath.NewInt(8)))
	plain.Amount = sdkmath.NewInt(9)
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, plain, common.Hash{}, sdkmath.NewInt(3), time.Unix(2030, 0)))

	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	owner, operator, err := held.nativePair()
	require.NoError(t, err)
	moved := held.Amount
	held.Amount = sdkmath.ZeroInt()
	plain.Amount = plain.Amount.Add(moved)
	require.ErrorContains(t, k.ApplyGatewayDELHoldReset(ctx, held, plain, moved), "hold has not expired")
	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(held.HoldDeadline, 0))
	require.ErrorContains(t, k.ApplyGatewayDELHoldReset(ctx, held, plain, moved.SubRaw(1)), "differs from the active native binding")
	wrongPlain := plain
	wrongPlain.Amount = wrongPlain.Amount.AddRaw(1)
	require.ErrorContains(t, k.ApplyGatewayDELHoldReset(ctx, held, wrongPlain, moved), "differs from the active native binding")
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: stakeBindingPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	events := len(ctx.EventManager().Events())
	require.ErrorContains(t, broken.ApplyGatewayDELHoldReset(ctx, held, plain, moved), "injected write failure")
	unchanged, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, unchanged)
	require.Len(t, ctx.EventManager().Events(), events)

	require.NoError(t, k.ApplyGatewayDELHoldReset(ctx, held, plain, moved))
	require.ErrorIs(t, k.ApplyGatewayDELHoldReset(ctx, held, plain, moved), ErrNotFound)
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before.Undelegations, after.Undelegations)
	require.Equal(t, before.EVMFrozenBindings, after.EVMFrozenBindings)
	require.Equal(t, before.Validators, after.Validators)
	require.Equal(t, before.BindingRevision+1, after.BindingRevision)
	heldRow, err := k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, sibling.Amount, heldRow.Stake.Stake.Amount)
	require.Len(t, heldRow.Stake.Holds, 1)
	require.Equal(t, sibling.HoldDeadline, heldRow.Stake.Holds[0].HoldEndTime)
	plainRow, err := k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, plain.Amount, plainRow.Stake.Stake.Amount)
	require.Empty(t, plainRow.Stake.Holds)
	binding, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, plain.Gateway[:], plain.StakeID[:])
	require.NoError(t, err)
	require.EqualValues(t, 10, binding.CreatedHeight)
	require.EqualValues(t, 12, binding.UpdatedHeight)
	require.Len(t, ctx.EventManager().Events(), events+1)
	require.Equal(t, "decimal_del_hold_reset", ctx.EventManager().Events()[events].Type)
}

func TestGatewayDELHoldResetCreatesPlainPosition(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	held := gatewayDELHoldFixture(2000, 2020, 12)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	owner, operator, err := held.nativePair()
	require.NoError(t, err)
	plain := gatewayDELFixture()
	plain.Amount = held.Amount
	held.Amount = sdkmath.ZeroInt()
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2021, 0))
	require.NoError(t, k.ApplyGatewayDELHoldReset(ctx, held, plain, plain.Amount))
	_, err = k.GetDelegation(ctx, owner, operator, types.StakeType_CoinHold, identity.BaseDenom)
	require.ErrorIs(t, err, ErrNotFound)
	plainRow, err := k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, plain.Amount, plainRow.Stake.Stake.Amount)
}
