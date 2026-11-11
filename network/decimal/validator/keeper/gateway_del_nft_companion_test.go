package keeper

import (
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func seedGatewayDELNFTCompanion(t *testing.T, ctx sdk.Context, k *StateKeeper) (GatewayDELStake, types.EVMStakeBinding) {
	t.Helper()
	position, nft := gatewayDELFixture(), bindingNativeFixture()
	position.Amount = sdkmath.ZeroInt()
	position.NFTBacked = nft.ReportedReserveAmount
	nft.CoinCompanionStakeID = position.StakeID.Bytes()
	coin, err := position.binding(position.Amount, ctx.BlockHeight(), ctx.BlockHeight())
	require.NoError(t, err)
	plan, err := k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &coin}, {Stake: &nft}})
	require.NoError(t, err)
	nftDelegation := normalizedBindingDelegation(t, nft)
	require.NoError(t, k.ApplyDelegationBatch(ctx, []DelegationChange{{Next: &nftDelegation}}))
	require.NoError(t, k.ApplyBindingPlan(ctx, plan))
	return position, nft
}

func TestGatewayDELDirectStakePreservesNFTCompanion(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	ctx = ctx.WithEventManager(sdk.NewEventManager())
	position, nft := seedGatewayDELNFTCompanion(t, ctx, k)
	owner, operator, err := position.nativePair()
	require.NoError(t, err)
	_, err = k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.ErrorIs(t, err, ErrNotFound)

	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2001, 0))
	position.Amount = sdkmath.NewInt(12)
	mismatch := position
	mismatch.NFTBacked = mismatch.NFTBacked.AddRaw(1)
	require.ErrorContains(t, k.ApplyGatewayDELDelegation(ctx, mismatch, position.Amount), "differs from the active native binding")
	oversized := position
	oversized.Amount = sdkmath.NewIntFromBigInt(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)))
	require.ErrorContains(t, k.ApplyGatewayDELDelegation(ctx, oversized, oversized.Amount), "plus NFT backing exceeds 256 bits")
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, position.Amount))
	position.Amount = position.Amount.AddRaw(3)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, sdkmath.NewInt(3)))

	due := time.Unix(2010, 0).UTC()
	position.Amount = position.Amount.SubRaw(5)
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, common.Hash{}, sdkmath.NewInt(5), due))
	active, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, position.Amount.Add(position.NFTBacked), active.RawAmount)
	require.Equal(t, position.NFTBacked, active.RawNftBackedAmount)
	direct, err := k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, position.Amount, direct.Stake.Stake.Amount)
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)

	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2002, 0))
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: undelegationPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	amount, index := position.Amount, common.BytesToHash([]byte{1})
	position.Amount = sdkmath.ZeroInt()
	events := len(ctx.EventManager().Events())
	require.ErrorContains(t, broken.ApplyGatewayDELWithdrawal(ctx, position, index, amount, due), "injected write failure")
	unchanged, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, unchanged)
	require.Len(t, ctx.EventManager().Events(), events)

	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, index, amount, due))
	pending, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, pending.EVMStakeBindings, 2)
	require.Equal(t, []types.Delegation{normalizedBindingDelegation(t, nft)}, pending.Delegations)
	require.Len(t, pending.EVMFrozenBindings, 2)
	require.Equal(t, amount, pending.EVMFrozenBindings[1].RawAmount)
	require.Equal(t, nft, readNativeBinding(t, ctx, k, nft))
	active, err = k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, position.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, position.NFTBacked, active.RawAmount)
	require.Equal(t, position.NFTBacked, active.RawNftBackedAmount)
	require.EqualValues(t, 10, active.CreatedHeight)
	require.EqualValues(t, 12, active.UpdatedHeight)
	ctx = ctx.WithBlockHeight(13).WithBlockTime(due)
	result, err := k.SettleMatureWithdrawalsLimit(ctx, 2, 1_000_000, k.ApplyWithdrawalCompletion)
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Settled: 2}, result)
	settled, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, pending.EVMStakeBindings, settled.EVMStakeBindings)
	require.Equal(t, pending.Delegations, settled.Delegations)
	require.Empty(t, settled.Undelegations)
}

func TestGatewayDELHoldResetIntoNFTOnlyCompanion(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	ordinary, nft := seedGatewayDELNFTCompanion(t, ctx, k)
	held := gatewayDELHoldFixture(2000, 2020, 9)
	unsupported := held
	unsupported.NFTBacked = sdkmath.OneInt()
	require.ErrorContains(t, k.ApplyGatewayDELDelegation(ctx, unsupported, held.Amount), "invalid NFT backing")
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))

	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2020, 0))
	ordinary.Amount = held.Amount
	held.Amount = sdkmath.ZeroInt()
	require.NoError(t, k.ApplyGatewayDELHoldReset(ctx, held, ordinary, ordinary.Amount))
	row, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, ordinary.Gateway[:], ordinary.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, ordinary.Amount.Add(ordinary.NFTBacked), row.RawAmount)
	require.Equal(t, ordinary.NFTBacked, row.RawNftBackedAmount)
	require.EqualValues(t, 10, row.CreatedHeight)
	require.Equal(t, nft, readNativeBinding(t, ctx, k, nft))
	genesis, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, genesis.Delegations, 2)
	require.Empty(t, genesis.Undelegations)
}
