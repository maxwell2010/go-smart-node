package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGatewayCustodiedDELPenaltyAppliesExactMatureClaimLoss(t *testing.T) {
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

			const percentage uint16 = 40
			cursor := common.Hash{31: 1}
			jump, _ := ctx.WithBlockHeight(4).WithBlockTime(deadline.Add(time.Second)).CacheContext()
			// The immediate next block jumped past nominal maturity. A claim
			// created at the infraction still owes the ordinary 40% loss.
			require.ErrorContains(t, k.ApplyGatewayCustodiedDELPenalty(jump, before, hold, 3, percentage, common.Hash{}, cursor, before.RawAmount), "exact eligible loss")
			remaining := before.RawAmount.SubRaw(2)
			require.NoError(t, k.ApplyGatewayCustodiedDELPenalty(jump, before, hold, 3, percentage, common.Hash{}, cursor, remaining))
			charged, err := k.GetEVMFrozenBinding(jump, position.Gateway[:], index[:])
			require.NoError(t, err)
			require.True(t, remaining.Equal(charged.RawAmount))
			require.NotEqual(t, before.NativeEntryHash, charged.NativeEntryHash)
			pending, err := k.GetUndelegation(jump, owner, operator)
			require.NoError(t, err)
			require.Len(t, pending.Entries, 1)
			require.True(t, remaining.Equal(pending.Entries[0].Stake.Stake.Amount))
			if hold != nil {
				require.Len(t, pending.Entries[0].Stake.Holds, 1)
				require.True(t, remaining.Equal(pending.Entries[0].Stake.Holds[0].Amount))
			}
			lots, _, err := k.GetFrozenDELProvenance(jump, charged)
			require.NoError(t, err)
			require.Len(t, lots, 1)
			require.True(t, remaining.Equal(lots[0].Amount))
			chargedReserve, err := k.GetActiveDELReserve(jump, operator)
			require.NoError(t, err)
			require.True(t, reserve.Equal(chargedReserve))

			ctx = ctx.WithBlockHeight(5).WithBlockTime(deadline.Add(-time.Nanosecond))
			require.ErrorContains(t, k.ApplyGatewayCustodiedDELPenalty(ctx, before, hold, 4, percentage, common.Hash{}, cursor, before.RawAmount), "requires a mature withdrawal claim")
			ctx = ctx.WithBlockTime(deadline)
			require.ErrorContains(t, k.ApplyGatewayCustodiedDELPenalty(ctx, before, hold, 4, percentage, common.Hash{}, cursor, remaining), "exact eligible loss")
			require.ErrorContains(t, k.ApplyGatewayCustodiedDELPenalty(ctx, before, hold, 4, percentage, common.Hash{}, common.Hash{31: 2}, before.RawAmount), "advance by exactly one")
			if hold == nil {
				err = k.ApplyGatewayFrozenDELPenalty(ctx, before, 4, common.Hash{}, cursor, before.RawAmount)
			} else {
				err = k.ApplyGatewayFrozenDELHoldPenalty(ctx, before, *hold, 4, common.Hash{}, cursor, before.RawAmount)
			}
			require.ErrorContains(t, err, "unmatured claim for regular penalties")
			withoutProvenance, _ := ctx.CacheContext()
			require.NoError(t, k.service.OpenKVStore(withoutProvenance).Delete([]byte{nativeStakeProvenanceMarker}))
			require.ErrorContains(t, k.ApplyGatewayCustodiedDELPenalty(withoutProvenance, before, hold, 4, percentage, common.Hash{}, cursor, before.RawAmount), "requires native stake provenance")

			// An acknowledgement must not write even an identical queue entry.
			queueReadOnly, err := NewStateKeeperWithDomain(failingService{service: k.service, prefix: undelegationPrefix}, bindingTestChain, 990001)
			require.NoError(t, err)
			require.NoError(t, queueReadOnly.ApplyGatewayCustodiedDELPenalty(ctx, before, hold, 4, percentage, common.Hash{}, cursor, before.RawAmount))
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
			require.ErrorContains(t, k.ApplyGatewayCustodiedDELPenalty(ctx, after, hold, 4, percentage, common.Hash{}, cursor, before.RawAmount), "cursor prestate changed")
			require.NoError(t, k.ApplyWithdrawalCompletion(ctx, after))
		})
	}
}

func TestGatewayCustodiedNFTPenaltyAppliesExactMatureReserveLoss(t *testing.T) {
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

			const percentage uint16 = 40
			cursor := previousCursor
			cursor[31]++
			if name == "funded" {
				jump, _ := ctx.WithBlockHeight(13).WithBlockTime(deadline.Add(time.Second)).CacheContext()
				remaining := before.ReportedReserveAmount.SubRaw(2)
				require.ErrorContains(t, k.ApplyGatewayCustodiedNFTPenalty(jump, nft, before, 12, percentage, previousCursor, cursor, before.ReportedReserveAmount), "exact eligible loss")
				require.NoError(t, k.ApplyGatewayCustodiedNFTPenalty(jump, nft, before, 12, percentage, previousCursor, cursor, remaining))
				charged, err := k.GetEVMFrozenBinding(jump, position.Companion.Gateway[:], index[:])
				require.NoError(t, err)
				require.True(t, remaining.Equal(charged.ReportedReserveAmount))
				require.Equal(t, before.NFTPosition, charged.NFTPosition)
				require.True(t, charged.Active)
				require.NotEqual(t, before.NativeEntryHash, charged.NativeEntryHash)
				pending, err := k.GetUndelegation(jump, owner, operator)
				require.NoError(t, err)
				require.Len(t, pending.Entries, 1)
				require.True(t, remaining.Equal(pending.Entries[0].Stake.Stake.Amount))
				lineage, err := nft.GetIsolatedLineage(jump, charged.NFTPosition.Vault, charged.NFTPosition.PositionID)
				require.NoError(t, err)
				require.Equal(t, common.BigToHash(remaining.BigInt()).Bytes(), lineage.Reserve.Amount)
			}
			ctx = ctx.WithBlockHeight(14).WithBlockTime(deadline.Add(-time.Nanosecond))
			require.ErrorContains(t, k.ApplyGatewayCustodiedNFTPenalty(ctx, nft, before, 13, percentage, previousCursor, cursor, before.ReportedReserveAmount), "requires a mature withdrawal claim")
			ctx = ctx.WithBlockTime(deadline)
			require.ErrorContains(t, k.ApplyGatewayFrozenNFTPenalty(ctx, nft, before, 13, previousCursor, cursor, before.ReportedReserveAmount), "unmatured NFT queue entry")
			withoutProvenance, _ := ctx.CacheContext()
			require.NoError(t, service.OpenKVStore(withoutProvenance).Delete([]byte{nativeStakeProvenanceMarker}))
			require.ErrorIs(t, k.ApplyGatewayCustodiedNFTPenalty(withoutProvenance, nft, before, 13, percentage, previousCursor, cursor, before.ReportedReserveAmount), ErrNotFound)

			queueReadOnly, err := NewStateKeeperWithDomain(failingService{service: service, prefix: undelegationPrefix}, bindingTestChain, 990001)
			require.NoError(t, err)
			require.NoError(t, queueReadOnly.ApplyGatewayCustodiedNFTPenalty(ctx, nft, before, 13, percentage, previousCursor, cursor, before.ReportedReserveAmount))
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
