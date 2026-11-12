package keeper

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativeEvidenceClockCommitsExactPreviousHeader(t *testing.T) {
	ctx, keeper, _ := delProvenanceGenesis(t)
	first := ctx.WithBlockTime(time.Unix(2001, 234).UTC())
	second := first.WithBlockHeight(2).WithBlockTime(first.BlockTime().Add(time.Nanosecond))
	third := second.WithBlockHeight(3).WithBlockTime(second.BlockTime().Add(time.Nanosecond))

	failed, _ := first.CacheContext()
	require.NoError(t, keeper.RecordNativeEvidenceBlock(failed))
	_, err := keeper.PreviousNativeEvidenceBlock(second)
	require.ErrorIs(t, err, ErrNotFound)

	child, write := first.CacheContext()
	require.NoError(t, keeper.RecordNativeEvidenceBlock(child))
	write()
	previous, err := keeper.PreviousNativeEvidenceBlock(second)
	require.NoError(t, err)
	require.Equal(t, CommittedEvidenceBlock{Height: 1, Time: first.BlockTime()}, previous)
	require.ErrorContains(t, keeper.RecordNativeEvidenceBlock(first), "already initialized")
	require.ErrorContains(t, keeper.RecordNativeEvidenceBlock(third), "disagrees")
	require.ErrorContains(t, keeper.RecordNativeEvidenceBlock(second.WithBlockTime(first.BlockTime())), "disagrees")

	require.NoError(t, keeper.RecordNativeEvidenceBlock(second))
	previous, err = keeper.PreviousNativeEvidenceBlock(third)
	require.NoError(t, err)
	require.Equal(t, CommittedEvidenceBlock{Height: 2, Time: second.BlockTime()}, previous)
}

func TestNativeEvidenceClockRejectsDisabledAndCorruptState(t *testing.T) {
	ctx, _, keeper := environment(t)
	ctx = ctx.WithBlockHeight(1).WithBlockTime(time.Unix(2000, 0).UTC())
	require.ErrorContains(t, keeper.RecordNativeEvidenceBlock(ctx), "requires stake provenance")
	store := keeper.service.OpenKVStore(ctx)
	require.NoError(t, store.Set([]byte{committedEvidenceClockKey}, []byte{1}))
	_, err := keeper.PreviousNativeEvidenceBlock(ctx.WithBlockHeight(2))
	require.ErrorContains(t, err, "invalid native evidence clock encoding")
}
