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

func TestGatewayDELPenaltyReconcilesPartialRoundedAndFullLoss(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	ctx = ctx.WithEventManager(sdk.NewEventManager())
	position := gatewayDELFixture()
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, position.Amount))
	sibling := position
	sibling.Beneficiary = common.BytesToAddress(stateAddress(6))
	sibling.StakeID = crypto.Keccak256Hash(sibling.Validator[:], sibling.Beneficiary[:], sibling.WrappedDEL[:], make([]byte, 64))
	sibling.Amount = sdkmath.NewInt(5)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, sibling, sibling.Amount))
	position.Amount = position.Amount.SubRaw(3)
	index := common.Hash{}
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, index, sdkmath.NewInt(3), time.Unix(2010, 0)))
	owner, operator, err := position.nativePair()
	require.NoError(t, err)
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	siblingBinding, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, sibling.Gateway[:], sibling.StakeID[:])
	require.NoError(t, err)

	ctx = ctx.WithBlockHeight(11)
	cursor := common.Hash{5: 1}
	reduced := sdkmath.NewInt(7)
	wrongAmount := position
	wrongAmount.Amount = position.Amount.AddRaw(1)
	require.ErrorContains(t, k.ApplyGatewayDELPenalty(ctx, wrongAmount, reduced, cursor), "differs from the active native binding")
	require.ErrorContains(t, k.ApplyGatewayDELPenalty(ctx, position, position.Amount.AddRaw(1), cursor), "invalid ordinary")
	require.ErrorContains(t, k.ApplyGatewayDELPenalty(ctx, position, reduced, common.Hash{}), "cursor must strictly advance")
	missingIndex, _ := ctx.CacheContext()
	require.NoError(t, service.OpenKVStore(missingIndex).Delete([]byte{activeDELReserveMarker}))
	require.ErrorIs(t, k.ApplyGatewayDELPenalty(missingIndex, position, reduced, cursor), ErrActiveDELReserveIndexUnavailable)
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: bindingRevisionKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyGatewayDELPenalty(ctx, position, reduced, cursor), "injected write failure")
	unchanged, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, unchanged)
	require.Len(t, ctx.EventManager().Events(), 3)

	require.NoError(t, k.ApplyGatewayDELPenalty(ctx, position, reduced, cursor))
	require.ErrorContains(t, k.ApplyGatewayDELPenalty(ctx, position, reduced, cursor), "differs from the active native binding")
	after, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, cursor.Bytes(), after.PenaltyCursor)
	require.Equal(t, reduced, after.RawAmount)
	require.EqualValues(t, 10, after.CreatedHeight)
	require.EqualValues(t, 11, after.UpdatedHeight)
	delegation, err := k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, reduced, delegation.Stake.Stake.Amount)
	reserve, err := k.GetActiveDELReserve(ctx, operator)
	require.NoError(t, err)
	require.Equal(t, reduced.Add(sibling.Amount), reserve)
	maximum, found, err := k.extremeActivePenaltyCursor(ctx, position.Validator[:], true)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, cursor.Bytes(), maximum)

	position.Amount, position.PenaltyCursor = reduced, cursor
	lowerCursor := common.Hash{31: 255}
	require.ErrorContains(t, k.ApplyGatewayDELPenalty(ctx, position, reduced, lowerCursor), "cursor must strictly advance")
	cursor[31]++
	require.NoError(t, k.ApplyGatewayDELPenalty(ctx, position, reduced, cursor))
	position.PenaltyCursor = cursor
	cursor[31]++
	require.NoError(t, k.ApplyGatewayDELPenalty(ctx, position, sdkmath.ZeroInt(), cursor))
	require.ErrorIs(t, k.ApplyGatewayDELPenalty(ctx, position, sdkmath.ZeroInt(), cursor), ErrNotFound)

	final, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, []types.EVMStakeBinding{siblingBinding}, final.EVMStakeBindings)
	require.Len(t, final.Delegations, 1)
	require.Equal(t, sibling.Amount, final.Delegations[0].Stake.Stake.Amount)
	require.Equal(t, before.EVMFrozenBindings, final.EVMFrozenBindings)
	require.Equal(t, before.Undelegations, final.Undelegations)
	require.Equal(t, before.LastValidatorPowers, final.LastValidatorPowers)
	require.Equal(t, before.BindingRevision+3, final.BindingRevision)
	reserve, err = k.GetActiveDELReserve(ctx, operator)
	require.NoError(t, err)
	require.Equal(t, sibling.Amount, reserve)
	maximum, found, err = k.extremeActivePenaltyCursor(ctx, position.Validator[:], true)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, common.Hash{}.Bytes(), maximum)
	events := ctx.EventManager().Events()
	require.Len(t, events, 6)
	require.Equal(t, "decimal_del_penalty_applied", events[3].Type)
	amount, found := events[4].GetAttribute("amount")
	require.True(t, found)
	require.Equal(t, "0", amount.Value)
}

func TestGatewayDELPenaltyRejectsHoldAndNFTCompanion(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	held := gatewayDELHoldFixture(2000, 2020, 12)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	position, _ := seedGatewayDELNFTCompanion(t, ctx, k)
	position.Amount = sdkmath.NewInt(12)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, position.Amount))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	cursor := common.Hash{31: 1}
	require.ErrorContains(t, k.ApplyGatewayDELPenalty(ctx, held, sdkmath.OneInt(), cursor), "invalid ordinary")
	require.ErrorContains(t, k.ApplyGatewayDELPenalty(ctx, position, sdkmath.OneInt(), cursor), "invalid ordinary")
	position.NFTBacked = sdkmath.ZeroInt()
	require.ErrorContains(t, k.ApplyGatewayDELPenalty(ctx, position, sdkmath.OneInt(), cursor), "differs from the active native binding")
	unchanged, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, unchanged)
}
