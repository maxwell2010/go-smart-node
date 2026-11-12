package keeper

import (
	"encoding/binary"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGatewayFrozenNFTProvenanceFollowsWithdrawalLifecycle(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	require.NoError(t, service.OpenKVStore(ctx).Set([]byte{nativeStakeProvenanceMarker}, []byte{1}))
	entryHeight := ctx.BlockHeight()
	companion := gatewayDELFixture()
	companion.Amount, companion.NFTBacked = sdkmath.ZeroInt(), sdkmath.NewInt(5)
	position := gatewayNFTFixture(t, companion, 1, 5, []uint32{2, 8})
	require.NoError(t, k.ApplyGatewayNFTDelegation(ctx, position))
	position.Companion.NFTBacked = sdkmath.ZeroInt()
	index := common.Hash{31: 7}
	deadline := time.Unix(2010, 0).UTC()
	ctx = ctx.WithBlockHeight(entryHeight + 2).WithBlockTime(time.Unix(2002, 0))

	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	events := len(ctx.EventManager().Events())
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: gatewayFrozenNFTProvenancePrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyGatewayNFTWithdrawal(ctx, position, index, deadline), "injected write failure")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Len(t, ctx.EventManager().Events(), events)

	require.NoError(t, k.ApplyGatewayNFTWithdrawal(ctx, position, index, deadline))
	frozen, err := k.GetEVMFrozenBinding(ctx, companion.Gateway[:], index[:])
	require.NoError(t, err)
	require.Equal(t, ctx.BlockHeight(), frozen.CreatedHeight)
	height, err := k.GatewayFrozenNFTStakeEntryHeight(ctx, frozen)
	require.NoError(t, err)
	require.Equal(t, entryHeight, height)
	key := gatewayFrozenNFTProvenanceKey(&frozen)

	stale := frozen
	stale.ReportedReserveAmount = stale.ReportedReserveAmount.SubRaw(1)
	_, err = k.GatewayFrozenNFTStakeEntryHeight(ctx, stale)
	require.ErrorContains(t, err, "differs from the expected provenance preimage")

	t.Run("missing and malformed provenance", func(t *testing.T) {
		child, _ := ctx.CacheContext()
		state := service.OpenKVStore(child)
		require.NoError(t, state.Delete(key))
		_, err := k.GatewayFrozenNFTStakeEntryHeight(child, frozen)
		require.ErrorIs(t, err, ErrNotFound)
		require.NoError(t, state.Set(key, []byte{1}))
		_, err = k.GatewayFrozenNFTStakeEntryHeight(child, frozen)
		require.ErrorContains(t, err, "invalid frozen NFT stake provenance record")
		wrongSource := append(binary.BigEndian.AppendUint64(nil, uint64(entryHeight)), make([]byte, common.HashLength)...)
		require.NoError(t, state.Set(key, wrongSource))
		_, err = k.GatewayFrozenNFTStakeEntryHeight(child, frozen)
		require.ErrorContains(t, err, "invalid frozen NFT stake provenance record")
		wrongHeight := append(binary.BigEndian.AppendUint64(nil, uint64(frozen.CreatedHeight+1)), frozen.SourceStakeID...)
		require.NoError(t, state.Set(key, wrongHeight))
		_, err = k.GatewayFrozenNFTStakeEntryHeight(child, frozen)
		require.ErrorContains(t, err, "invalid frozen NFT stake provenance height")
	})
	t.Run("native queue mismatch", func(t *testing.T) {
		child, _ := ctx.CacheContext()
		owner, operator, err := companion.nativePair()
		require.NoError(t, err)
		pending, err := k.GetUndelegation(child, owner, operator)
		require.NoError(t, err)
		pending.Entries[0].CreationHeight++
		data, err := encodeWithdrawalPair(pending)
		require.NoError(t, err)
		require.NoError(t, service.OpenKVStore(child).Set(composite(undelegationPrefix, owner, operator), data))
		_, err = k.GatewayFrozenNFTStakeEntryHeight(child, frozen)
		require.ErrorContains(t, err, "no matching native undelegation")
	})

	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(deadline)
	events = len(ctx.EventManager().Events())
	broken, err = NewStateKeeperWithDomain(failingService{service: service, prefix: gatewayFrozenNFTProvenancePrefix, deleteOnly: true}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyWithdrawalCompletion(ctx, frozen), "injected delete failure")
	height, err = k.GatewayFrozenNFTStakeEntryHeight(ctx, frozen)
	require.NoError(t, err)
	require.Equal(t, entryHeight, height)
	require.Len(t, ctx.EventManager().Events(), events)
	require.NoError(t, k.ApplyWithdrawalCompletion(ctx, frozen))
	exists, err := service.OpenKVStore(ctx).Has(key)
	require.NoError(t, err)
	require.False(t, exists)
	require.ErrorIs(t, k.ApplyWithdrawalCompletion(ctx, frozen), ErrWithdrawalAlreadyCompleted)
}
