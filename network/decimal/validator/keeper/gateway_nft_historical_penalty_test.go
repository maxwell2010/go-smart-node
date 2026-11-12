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

func TestGatewayNFTHistoricalPenaltyPlansExactExposure(t *testing.T) {
	ctx, k, companion := delProvenanceGenesis(t)
	ctx = ctx.WithBlockHeight(10).WithBlockTime(time.Unix(2010, 0).UTC())
	companion.NFTBacked = sdkmath.NewInt(101)
	first := gatewayNFTFixture(t, companion, 1, 101, []uint32{2, 8})
	require.NoError(t, k.ApplyGatewayNFTDelegation(ctx, first))
	companion.NFTBacked = sdkmath.NewInt(120)
	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2011, 0).UTC())
	second := gatewayNFTFixture(t, companion, 2, 19, []uint32{9})
	require.NoError(t, k.ApplyGatewayNFTDelegation(ctx, second))
	active, err := k.GetEVMStakeBinding(ctx, gatewayNFTBinding, companion.Gateway[:], first.StakeID[:])
	require.NoError(t, err)
	late, err := k.GetEVMStakeBinding(ctx, gatewayNFTBinding, companion.Gateway[:], second.StakeID[:])
	require.NoError(t, err)
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	events := len(ctx.EventManager().Events())
	eligible := NFTHistoricalPenaltyPlan{Before: sdkmath.NewInt(101), Eligible: true, Loss: sdkmath.NewInt(5), Remaining: sdkmath.NewInt(96)}
	plan, err := k.PlanActiveNFTHistoricalPenalty(ctx, active, 11)
	require.NoError(t, err)
	require.Equal(t, eligible, plan)
	plan, err = k.PlanActiveNFTHistoricalPenalty(ctx, active, 10)
	require.NoError(t, err)
	require.Equal(t, NFTHistoricalPenaltyPlan{Before: eligible.Before, Loss: sdkmath.ZeroInt(), Remaining: eligible.Before}, plan)
	plan, err = k.PlanActiveNFTHistoricalPenalty(ctx, late, 10)
	require.NoError(t, err)
	require.Equal(t, NFTHistoricalPenaltyPlan{Before: sdkmath.NewInt(19), Loss: sdkmath.ZeroInt(), Remaining: sdkmath.NewInt(19)}, plan)
	_, err = k.PlanActiveNFTHistoricalPenalty(ctx, active, 12)
	require.ErrorContains(t, err, "invalid NFT infraction height")
	stale := active
	stale.ReportedReserveAmount = sdkmath.NewInt(100)
	_, err = k.PlanActiveNFTHistoricalPenalty(ctx, stale, 11)
	require.ErrorContains(t, err, "differs from the expected historical penalty preimage")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	require.Len(t, ctx.EventManager().Events(), events)

	child, _ := ctx.CacheContext()
	owner, operator, err := companion.nativePair()
	require.NoError(t, err)
	delegation, err := k.GetDelegation(child, owner, operator, types.StakeType_NFT, active.NativeTarget.ID)
	require.NoError(t, err)
	changed := delegation
	changed.Stake.Stake = sdk.NewInt64Coin(identity.BaseDenom, 100)
	require.NoError(t, k.ApplyDelegationBatch(child, []DelegationChange{{Expected: &delegation, Next: &changed}}))
	_, err = k.PlanActiveNFTHistoricalPenalty(child, active, 11)
	require.ErrorContains(t, err, "differs from the active NFT historical penalty reserve or receipt")
	child, _ = ctx.CacheContext()
	require.NoError(t, k.service.OpenKVStore(child).Delete([]byte{nativeStakeProvenanceMarker}))
	_, err = k.PlanActiveNFTHistoricalPenalty(child, active, 11)
	require.ErrorIs(t, err, ErrNotFound)

	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2012, 0).UTC())
	plan, err = k.PlanActiveNFTHistoricalPenalty(ctx, late, 12)
	require.NoError(t, err)
	require.Equal(t, NFTHistoricalPenaltyPlan{Before: sdkmath.NewInt(19), Eligible: true, Loss: sdkmath.ZeroInt(), Remaining: sdkmath.NewInt(19)}, plan)
	first.Companion.NFTBacked = sdkmath.NewInt(19)
	index := common.Hash{31: 7}
	require.NoError(t, k.ApplyGatewayNFTWithdrawal(ctx, first, index, time.Unix(2030, 0).UTC()))
	frozen, err := k.GetEVMFrozenBinding(ctx, companion.Gateway[:], index[:])
	require.NoError(t, err)
	ctx = ctx.WithBlockHeight(13)
	for _, test := range []struct {
		height   int64
		eligible bool
	}{
		{10, false}, // Staked at the infraction height.
		{11, true},
		{12, true}, // The withdrawal happened after the block was signed.
		{13, false},
	} {
		plan, err := k.PlanFrozenNFTHistoricalPenalty(ctx, frozen, test.height)
		require.NoError(t, err)
		if test.eligible {
			require.Equal(t, eligible, plan)
		} else {
			require.Equal(t, NFTHistoricalPenaltyPlan{Before: eligible.Before, Loss: sdkmath.ZeroInt(), Remaining: eligible.Before}, plan)
		}
	}
	child, _ = ctx.CacheContext()
	require.NoError(t, k.service.OpenKVStore(child).Delete(gatewayFrozenNFTProvenanceKey(&frozen)))
	_, err = k.PlanFrozenNFTHistoricalPenalty(child, frozen, 11)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestGatewayNFTHistoricalPenaltyReadsSurvivingReserveAfterProjection(t *testing.T) {
	ctx, service, k, nft, first, _ := gatewayNFTPenaltyFixture(t)
	// The projection fixture uses real native NFT state. Supply the provenance
	// marker for this reader test; the exposure test covers real initialization.
	require.NoError(t, service.OpenKVStore(ctx).Set([]byte{nativeStakeProvenanceMarker}, []byte{1}))
	ctx = ctx.WithBlockHeight(12)
	penalty := GatewayNFTPenalty{Before: first, AfterCursor: common.Hash{31: 1}, RemainingReserve: sdkmath.NewInt(2)}
	require.NoError(t, k.ApplyGatewayNFTPenalty(ctx, nft, penalty))
	row, err := k.GetEVMStakeBinding(ctx, gatewayNFTBinding, first.Companion.Gateway[:], first.StakeID[:])
	require.NoError(t, err)
	plan, err := k.PlanActiveNFTHistoricalPenalty(ctx, row, 11)
	require.NoError(t, err)
	require.Equal(t, NFTHistoricalPenaltyPlan{Before: penalty.RemainingReserve, Eligible: true, Loss: sdkmath.ZeroInt(), Remaining: penalty.RemainingReserve}, plan)
}
