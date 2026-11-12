package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGatewayCustodiedDELCheckpointPreservesMatureClaim(t *testing.T) {
	for _, name := range []string{"ordinary", "held"} {
		t.Run(name, func(t *testing.T) {
			ctx, k, position := delProvenanceGenesis(t)
			var hold *GatewayFrozenDELHold
			if name == "held" {
				position = gatewayDELHoldFixture(2000, 2002, 12)
				require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, position.Amount))
				hold = &GatewayFrozenDELHold{WrappedDEL: position.WrappedDEL, HoldDeadline: position.HoldDeadline, HoldStartTime: position.HoldStartTime}
			}
			owner, operator, err := position.nativePair()
			require.NoError(t, err)
			ctx = ctx.WithBlockHeight(3).WithBlockTime(time.Unix(2003, 0).UTC())
			deadline := time.Unix(2010, 0).UTC()
			index := common.Hash{31: 7}
			position.Amount = position.Amount.SubRaw(5)
			require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, index, sdkmath.NewInt(5), deadline))
			before, err := k.GetEVMFrozenBinding(ctx, position.Gateway[:], index[:])
			require.NoError(t, err)
			queueKey := composite(undelegationPrefix, owner, operator)
			provenanceKey := delProvenanceKey(frozenDELProvenancePrefix, before.Gateway, before.FrozenIndex)
			queue, err := k.service.OpenKVStore(ctx).Get(queueKey)
			require.NoError(t, err)
			provenance, err := k.service.OpenKVStore(ctx).Get(provenanceKey)
			require.NoError(t, err)
			require.NotEmpty(t, queue)
			require.NotEmpty(t, provenance)
			reserve, err := k.GetActiveDELReserve(ctx, operator)
			require.NoError(t, err)

			ctx = ctx.WithBlockHeight(5).WithBlockTime(deadline.Add(-time.Nanosecond))
			cursor := common.Hash{31: 1}
			require.ErrorContains(t, k.ApplyGatewayCustodiedDELCheckpoint(ctx, before, hold, 4, common.Hash{}, cursor), "requires a mature withdrawal claim")
			ctx = ctx.WithBlockTime(deadline)
			require.ErrorContains(t, k.ApplyGatewayCustodiedDELCheckpoint(ctx, before, hold, 3, common.Hash{}, cursor), "created before the infraction")
			require.ErrorContains(t, k.ApplyGatewayCustodiedDELCheckpoint(ctx, before, hold, 4, common.Hash{}, common.Hash{31: 2}), "advance by exactly one")
			if hold == nil {
				err = k.ApplyGatewayFrozenDELPenalty(ctx, before, 4, common.Hash{}, cursor, before.RawAmount)
			} else {
				err = k.ApplyGatewayFrozenDELHoldPenalty(ctx, before, *hold, 4, common.Hash{}, cursor, before.RawAmount)
			}
			require.ErrorContains(t, err, "unmatured claim for regular penalties")
			withoutProvenance, _ := ctx.CacheContext()
			require.NoError(t, k.service.OpenKVStore(withoutProvenance).Delete([]byte{nativeStakeProvenanceMarker}))
			require.ErrorContains(t, k.ApplyGatewayCustodiedDELCheckpoint(withoutProvenance, before, hold, 4, common.Hash{}, cursor), "requires native stake provenance")

			// An acknowledgement must not write even an identical queue entry.
			queueReadOnly, err := NewStateKeeperWithDomain(failingService{service: k.service, prefix: undelegationPrefix}, bindingTestChain, 990001)
			require.NoError(t, err)
			require.NoError(t, queueReadOnly.ApplyGatewayCustodiedDELCheckpoint(ctx, before, hold, 4, common.Hash{}, cursor))
			after, err := k.GetEVMFrozenBinding(ctx, position.Gateway[:], index[:])
			require.NoError(t, err)
			before.UpdatedHeight = ctx.BlockHeight()
			require.Equal(t, before, after)
			state := k.service.OpenKVStore(ctx)
			actualQueue, err := state.Get(queueKey)
			require.NoError(t, err)
			require.Equal(t, queue, actualQueue)
			actualProvenance, err := state.Get(provenanceKey)
			require.NoError(t, err)
			require.Equal(t, provenance, actualProvenance)
			actualReserve, err := k.GetActiveDELReserve(ctx, operator)
			require.NoError(t, err)
			require.True(t, reserve.Equal(actualReserve))
			actualCursor, err := state.Get(frozenDELPenaltyCursorKey(after.Gateway, after.FrozenIndex))
			require.NoError(t, err)
			require.Equal(t, cursor.Bytes(), actualCursor)
			require.ErrorContains(t, k.ApplyGatewayCustodiedDELCheckpoint(ctx, after, hold, 4, common.Hash{}, cursor), "cursor prestate changed")
			require.NoError(t, k.ApplyWithdrawalCompletion(ctx, after))
		})
	}
}

func TestGatewayCustodiedNFTCheckpointPreservesMatureReceipt(t *testing.T) {
	for _, name := range []string{"funded", "zero reserve"} {
		t.Run(name, func(t *testing.T) {
			ctx, service, k, nft, position, _ := gatewayNFTPenaltyFixture(t)
			require.NoError(t, service.OpenKVStore(ctx).Set([]byte{nativeStakeProvenanceMarker}, []byte{1}))
			owner, operator, err := position.Companion.nativePair()
			require.NoError(t, err)
			position.Companion.NFTBacked = position.Companion.NFTBacked.SubRaw(5)
			ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2002, 0).UTC())
			deadline := time.Unix(2010, 0).UTC()
			index := common.Hash{31: 7}
			require.NoError(t, k.ApplyGatewayNFTWithdrawal(ctx, position, index, deadline))
			before, err := k.GetEVMFrozenBinding(ctx, position.Companion.Gateway[:], index[:])
			require.NoError(t, err)
			var previousCursor common.Hash
			if name == "zero reserve" {
				previousCursor[31] = 1
				ctx = ctx.WithBlockHeight(13).WithBlockTime(time.Unix(2003, 0).UTC())
				require.NoError(t, k.ApplyGatewayFrozenNFTPenalty(ctx, nft, before, 12, common.Hash{}, previousCursor, sdkmath.ZeroInt()))
				before, err = k.GetEVMFrozenBinding(ctx, position.Companion.Gateway[:], index[:])
				require.NoError(t, err)
				require.True(t, before.ReportedReserveAmount.IsZero())
			}
			queueKey := composite(undelegationPrefix, owner, operator)
			provenanceKey := gatewayFrozenNFTProvenanceKey(&before)
			queue, err := service.OpenKVStore(ctx).Get(queueKey)
			require.NoError(t, err)
			provenance, err := service.OpenKVStore(ctx).Get(provenanceKey)
			require.NoError(t, err)
			require.NotEmpty(t, queue)
			require.NotEmpty(t, provenance)
			nftBefore, err := nft.ExportGenesis(ctx)
			require.NoError(t, err)

			ctx = ctx.WithBlockHeight(14).WithBlockTime(deadline.Add(-time.Nanosecond))
			cursor := previousCursor
			cursor[31]++
			require.ErrorContains(t, k.ApplyGatewayCustodiedNFTCheckpoint(ctx, nft, before, 13, previousCursor, cursor), "requires a mature withdrawal claim")
			ctx = ctx.WithBlockTime(deadline)
			require.ErrorContains(t, k.ApplyGatewayCustodiedNFTCheckpoint(ctx, nft, before, 12, previousCursor, cursor), "created before the infraction")
			require.ErrorContains(t, k.ApplyGatewayFrozenNFTPenalty(ctx, nft, before, 13, previousCursor, cursor, before.ReportedReserveAmount), "unmatured NFT queue entry")
			withoutProvenance, _ := ctx.CacheContext()
			require.NoError(t, service.OpenKVStore(withoutProvenance).Delete([]byte{nativeStakeProvenanceMarker}))
			require.ErrorIs(t, k.ApplyGatewayCustodiedNFTCheckpoint(withoutProvenance, nft, before, 13, previousCursor, cursor), ErrNotFound)

			queueReadOnly, err := NewStateKeeperWithDomain(failingService{service: service, prefix: undelegationPrefix}, bindingTestChain, 990001)
			require.NoError(t, err)
			require.NoError(t, queueReadOnly.ApplyGatewayCustodiedNFTCheckpoint(ctx, nft, before, 13, previousCursor, cursor))
			after, err := k.GetEVMFrozenBinding(ctx, position.Companion.Gateway[:], index[:])
			require.NoError(t, err)
			before.UpdatedHeight = ctx.BlockHeight()
			require.Equal(t, before, after)
			state := service.OpenKVStore(ctx)
			actualQueue, err := state.Get(queueKey)
			require.NoError(t, err)
			require.Equal(t, queue, actualQueue)
			actualProvenance, err := state.Get(provenanceKey)
			require.NoError(t, err)
			require.Equal(t, provenance, actualProvenance)
			nftAfter, err := nft.ExportGenesis(ctx)
			require.NoError(t, err)
			require.Equal(t, nftBefore, nftAfter)
			actualCursor, err := state.Get(frozenDELPenaltyCursorKey(after.Gateway, after.FrozenIndex))
			require.NoError(t, err)
			require.Equal(t, cursor.Bytes(), actualCursor)
			require.NoError(t, k.ApplyWithdrawalCompletion(ctx, after))
		})
	}
}
