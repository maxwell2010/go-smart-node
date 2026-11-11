package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func gatewayDELFixture() GatewayDELStake {
	next := GatewayDELStake{
		Validator: common.BytesToAddress(stateAddress(1)), Beneficiary: common.BytesToAddress(stateAddress(2)),
		Gateway: common.BytesToAddress(stateAddress(3)), WrappedDEL: common.BytesToAddress(stateAddress(4)),
		Amount: sdkmath.NewInt(12),
	}
	next.StakeID = crypto.Keccak256Hash(next.Validator[:], next.Beneficiary[:], next.WrappedDEL[:], make([]byte, 64))
	return next
}

func TestGatewayDELNativeLifecycle(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	ctx = ctx.WithEventManager(sdk.NewEventManager())
	position := gatewayDELFixture()
	amount := position.Amount
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, amount))
	require.ErrorContains(t, k.ApplyGatewayDELDelegation(ctx, position, amount), "already has a native binding")
	initial, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, initial.EVMStakeBindings, 1)
	require.Len(t, initial.Delegations, 1)
	require.Equal(t, amount, initial.Delegations[0].Stake.Stake.Amount)
	require.Zero(t, initial.Validators[0].Stake, "financial projection does not change consensus power")

	position.Amount = sdkmath.ZeroInt()
	id := common.Hash{} // The first frozen Gateway array index is valid.
	deadline := time.Unix(2010, 0).UTC()
	require.ErrorContains(t, k.ApplyGatewayDELWithdrawal(ctx, position, id, amount.SubRaw(1), deadline), "differs from the active native binding")
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, id, amount, deadline))
	pending, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, pending.Delegations)
	require.Empty(t, pending.EVMStakeBindings)
	require.Len(t, pending.Undelegations, 1)
	require.Len(t, pending.Undelegations[0].Entries, 1)
	require.Len(t, pending.EVMFrozenBindings, 1)
	frozen := pending.EVMFrozenBindings[0]
	require.Equal(t, amount, frozen.RawAmount)
	require.Equal(t, position.StakeID.Bytes(), frozen.SourceStakeID)

	ctx = ctx.WithBlockHeight(11).WithBlockTime(deadline)
	result, err := k.SettleMatureWithdrawals(ctx, 1_000_000, k.ApplyWithdrawalCompletion)
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Settled: 1}, result)
	result, err = k.SettleMatureWithdrawals(ctx, 1_000_000, k.ApplyWithdrawalCompletion)
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{}, result)
	require.Len(t, ctx.EventManager().Events(), 3)
	require.Equal(t, []string{"decimal_del_delegated", "decimal_del_withdrawal_requested", "decimal_withdrawal_completed"},
		[]string{ctx.EventManager().Events()[0].Type, ctx.EventManager().Events()[1].Type, ctx.EventManager().Events()[2].Type})

	position.Amount = amount
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, amount))
	position.Amount = sdkmath.ZeroInt()
	require.ErrorContains(t, k.ApplyGatewayDELWithdrawal(ctx, position, id, amount, deadline), "already has a native binding")
	restored, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, restored.EVMStakeBindings, 1)
	require.Len(t, restored.EVMFrozenBindings, 1)
	require.False(t, restored.EVMFrozenBindings[0].Active)
	require.Empty(t, restored.Undelegations)
}

func TestGatewayDELQueueWriteRollback(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	ctx = ctx.WithEventManager(sdk.NewEventManager())
	position := gatewayDELFixture()
	amount := position.Amount
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, amount))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: undelegationPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	position.Amount = sdkmath.ZeroInt()
	require.ErrorContains(t, broken.ApplyGatewayDELWithdrawal(ctx, position, common.Hash{}, amount, time.Unix(2010, 0)), "injected write failure")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Len(t, ctx.EventManager().Events(), 1)
	owner, operator, err := position.nativePair()
	require.NoError(t, err)
	_, err = k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
}
