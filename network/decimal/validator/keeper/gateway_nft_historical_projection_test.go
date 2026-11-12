package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGatewayActiveHistoricalNFTPenaltyProjectsOnlyExposedReserve(t *testing.T) {
	ctx, service, k, nft, first, late := gatewayNFTPenaltyFixtureWithReserves(t, 101, 60, 11)
	require.NoError(t, service.OpenKVStore(ctx).Set([]byte{nativeStakeProvenanceMarker}, []byte{1}))
	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2002, 0).UTC())
	firstCursor := common.Hash{31: 1}
	penalty := GatewayNFTPenalty{Before: first, AfterCursor: firstCursor, RemainingReserve: sdkmath.NewInt(96)}
	wrong := penalty
	wrong.RemainingReserve = sdkmath.NewInt(95)
	require.ErrorContains(t, k.ApplyGatewayActiveHistoricalNFTPenalty(ctx, nft, wrong, 11, true), "differs from the historical exposure")
	require.ErrorContains(t, k.ApplyGatewayActiveHistoricalNFTPenalty(ctx, nft, penalty, 11, false), "differs from the historical exposure")
	require.NoError(t, k.ApplyGatewayActiveHistoricalNFTPenalty(ctx, nft, penalty, 11, true))
	row, err := k.GetEVMStakeBinding(ctx, gatewayNFTBinding, first.Companion.Gateway[:], first.StakeID[:])
	require.NoError(t, err)
	require.True(t, row.ReportedReserveAmount.Equal(penalty.RemainingReserve))
	require.Equal(t, firstCursor.Bytes(), row.PenaltyCursor)
	require.EqualValues(t, 10, row.CreatedHeight)
	lineage, err := nft.GetIsolatedLineage(ctx, first.Lineage.Vault, first.Lineage.PositionID)
	require.NoError(t, err)
	require.Equal(t, stateWord(96), lineage.Reserve.Amount)

	late.Companion.NFTBacked = sdkmath.NewInt(156)
	latePenalty := GatewayNFTPenalty{Before: late, AfterCursor: firstCursor, RemainingReserve: sdkmath.NewInt(60)}
	require.ErrorContains(t, k.ApplyGatewayActiveHistoricalNFTPenalty(ctx, nft, latePenalty, 11, true), "differs from the historical exposure")
	require.NoError(t, k.ApplyGatewayActiveHistoricalNFTPenalty(ctx, nft, latePenalty, 11, false))
	row, err = k.GetEVMStakeBinding(ctx, gatewayNFTBinding, late.Companion.Gateway[:], late.StakeID[:])
	require.NoError(t, err)
	require.True(t, row.ReportedReserveAmount.Equal(latePenalty.RemainingReserve))
	require.Equal(t, firstCursor.Bytes(), row.PenaltyCursor)
	_, operator, err := first.Companion.nativePair()
	require.NoError(t, err)
	assertActiveDELReserve(t, ctx, k, operator, 168)

	// An earlier regular penalty can leave the exact NFT receipt with no reserve.
	first.Lineage, first.Companion = lineage, late.Companion
	secondCursor, thirdCursor := common.Hash{31: 2}, common.Hash{31: 3}
	zero := GatewayNFTPenalty{Before: first, BeforeCursor: firstCursor, AfterCursor: secondCursor, RemainingReserve: sdkmath.ZeroInt()}
	require.NoError(t, k.ApplyGatewayNFTPenalty(ctx, nft, zero))
	zero.Before.Lineage.Reserve.Amount = stateWord(0)
	zero.Before.Companion.NFTBacked = sdkmath.NewInt(60)
	zero.BeforeCursor, zero.AfterCursor = secondCursor, thirdCursor
	require.ErrorContains(t, k.ApplyGatewayActiveHistoricalNFTPenalty(ctx, nft, zero, 11, false), "differs from the historical exposure")
	require.NoError(t, k.ApplyGatewayActiveHistoricalNFTPenalty(ctx, nft, zero, 11, true))
	row, err = k.GetEVMStakeBinding(ctx, gatewayNFTBinding, first.Companion.Gateway[:], first.StakeID[:])
	require.NoError(t, err)
	require.True(t, row.Active)
	require.True(t, row.ReportedReserveAmount.IsZero())
	require.True(t, row.RawAmount.Equal(sdkmath.OneInt()))
	require.Empty(t, row.CoinCompanionStakeID)
	require.Equal(t, thirdCursor.Bytes(), row.PenaltyCursor)
	assertActiveDELReserve(t, ctx, k, operator, 72)
}

func TestGatewayFrozenHistoricalNFTPenaltyProjectsMatureCustodyAtomically(t *testing.T) {
	ctx, service, k, nft, first, late := gatewayNFTPenaltyFixtureWithReserves(t, 101, 60, 11)
	require.NoError(t, service.OpenKVStore(ctx).Set([]byte{nativeStakeProvenanceMarker}, []byte{1}))
	deadline := time.Unix(2010, 0).UTC()
	firstID, lateID := common.Hash{31: 1}, common.Hash{31: 2}
	first.Companion.NFTBacked = sdkmath.NewInt(60)
	require.NoError(t, k.ApplyGatewayNFTWithdrawal(ctx, first, firstID, deadline))
	firstFrozen, err := k.GetEVMFrozenBinding(ctx, first.Companion.Gateway[:], firstID[:])
	require.NoError(t, err)
	creation, err := k.GetFrozenWithdrawalCreationBlock(ctx, firstFrozen)
	require.NoError(t, err)
	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2002, 0).UTC())
	late.Companion.NFTBacked = sdkmath.ZeroInt()
	require.NoError(t, k.ApplyGatewayNFTWithdrawal(ctx, late, lateID, deadline))
	lateFrozen, err := k.GetEVMFrozenBinding(ctx, late.Companion.Gateway[:], lateID[:])
	require.NoError(t, err)
	lateQueueHash := lateFrozen.NativeEntryHash
	firstCursor, secondCursor, thirdCursor := common.Hash{31: 1}, common.Hash{31: 2}, common.Hash{31: 3}
	ctx = ctx.WithBlockHeight(13).WithBlockTime(time.Unix(2003, 0).UTC())
	require.NoError(t, k.ApplyGatewayFrozenHistoricalNFTPenalty(ctx, nft, lateFrozen, 11, common.Hash{}, firstCursor, sdkmath.NewInt(60), false))
	lateFrozen, err = k.GetEVMFrozenBinding(ctx, late.Companion.Gateway[:], lateID[:])
	require.NoError(t, err)
	require.Equal(t, lateQueueHash, lateFrozen.NativeEntryHash)
	require.True(t, lateFrozen.ReportedReserveAmount.Equal(sdkmath.NewInt(60)))
	require.NoError(t, k.ApplyGatewayFrozenNFTPenalty(ctx, nft, lateFrozen, 12, firstCursor, secondCursor, sdkmath.ZeroInt()))
	lateFrozen, err = k.GetEVMFrozenBinding(ctx, late.Companion.Gateway[:], lateID[:])
	require.NoError(t, err)
	require.True(t, lateFrozen.ReportedReserveAmount.IsZero())
	zeroQueueHash := lateFrozen.NativeEntryHash

	ctx = ctx.WithBlockHeight(14).WithBlockTime(deadline)
	remaining := sdkmath.NewInt(96)
	require.ErrorContains(t, k.ApplyGatewayFrozenNFTPenalty(ctx, nft, firstFrozen, 11, common.Hash{}, firstCursor, remaining), "unmatured NFT queue entry")
	require.ErrorContains(t, k.ApplyGatewayFrozenHistoricalNFTPenalty(ctx, nft, firstFrozen, 11, common.Hash{}, firstCursor, remaining.SubRaw(1), true), "differs from the historical exposure")
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	nftBefore, err := nft.ExportGenesis(ctx)
	require.NoError(t, err)
	events := len(ctx.EventManager().Events())
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: bindingRevisionKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyGatewayFrozenHistoricalNFTPenalty(ctx, nft, firstFrozen, 11, common.Hash{}, firstCursor, remaining, true), "injected write failure")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	nftAfter, err := nft.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, nftBefore, nftAfter)
	require.Len(t, ctx.EventManager().Events(), events)

	require.NoError(t, k.ApplyGatewayFrozenHistoricalNFTPenalty(ctx, nft, firstFrozen, 11, common.Hash{}, firstCursor, remaining, true))
	firstFrozen, err = k.GetEVMFrozenBinding(ctx, first.Companion.Gateway[:], firstID[:])
	require.NoError(t, err)
	require.True(t, firstFrozen.ReportedReserveAmount.Equal(remaining))
	retained, err := k.GetFrozenWithdrawalCreationBlock(ctx, firstFrozen)
	require.NoError(t, err)
	require.Equal(t, creation, retained)
	firstQueueHash := firstFrozen.NativeEntryHash
	require.NoError(t, k.ApplyGatewayFrozenHistoricalNFTPenalty(ctx, nft, firstFrozen, 13, firstCursor, secondCursor, remaining, false))
	firstFrozen, err = k.GetEVMFrozenBinding(ctx, first.Companion.Gateway[:], firstID[:])
	require.NoError(t, err)
	require.Equal(t, firstQueueHash, firstFrozen.NativeEntryHash)
	require.True(t, firstFrozen.ReportedReserveAmount.Equal(remaining))
	require.ErrorContains(t, k.ApplyGatewayFrozenHistoricalNFTPenalty(ctx, nft, lateFrozen, 12, secondCursor, thirdCursor, sdkmath.ZeroInt(), false), "differs from the historical exposure")
	require.NoError(t, k.ApplyGatewayFrozenHistoricalNFTPenalty(ctx, nft, lateFrozen, 12, secondCursor, thirdCursor, sdkmath.ZeroInt(), true))
	lateFrozen, err = k.GetEVMFrozenBinding(ctx, late.Companion.Gateway[:], lateID[:])
	require.NoError(t, err)
	require.True(t, lateFrozen.Active)
	require.True(t, lateFrozen.ReportedReserveAmount.IsZero())
	require.True(t, lateFrozen.RawAmount.Equal(sdkmath.OneInt()))
	require.Equal(t, zeroQueueHash, lateFrozen.NativeEntryHash)
	storedCursor, err := service.OpenKVStore(ctx).Get(frozenDELPenaltyCursorKey(lateFrozen.Gateway, lateFrozen.FrozenIndex))
	require.NoError(t, err)
	require.Equal(t, thirdCursor.Bytes(), storedCursor)
	require.NoError(t, k.ApplyWithdrawalCompletion(ctx, firstFrozen))
	require.NoError(t, k.ApplyWithdrawalCompletion(ctx, lateFrozen))
}
