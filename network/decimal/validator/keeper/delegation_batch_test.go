package keeper

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func copyDelegation(t *testing.T, value types.Delegation) types.Delegation {
	t.Helper()
	data, err := proto.Marshal(&value)
	require.NoError(t, err)
	var out types.Delegation
	require.NoError(t, proto.Unmarshal(data, &out))
	return out
}

func delegationSnapshot(t *testing.T, ctx sdk.Context, k *StateKeeper) []byte {
	t.Helper()
	g, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	data, err := proto.Marshal(g)
	require.NoError(t, err)
	return data
}

func TestDelegationBatchReassignsAtFullLimitAndRejectsStaleReplay(t *testing.T) {
	ctx, _, k := environment(t)
	g := genesis(t)
	g.Params.MaxDelegations = 1
	a := copyDelegation(t, g.Delegations[0])
	b := copyDelegation(t, a)
	b.Delegator, b.Validator, b.Stake.ID = g.Validators[1].RewardAddress, g.Validators[1].OperatorAddress, "other-nft"
	g.Delegations = append(g.Delegations, b)
	require.NoError(t, k.InitGenesis(ctx, g))
	nextA, nextB := copyDelegation(t, a), copyDelegation(t, b)
	nextA.Validator, nextB.Validator = b.Validator, a.Validator
	changes := []DelegationChange{{Expected: &a, Next: &nextA}, {Expected: &b, Next: &nextB}}
	branch, commit := ctx.CacheContext()
	before := delegationSnapshot(t, ctx, k)
	require.NoError(t, k.ApplyDelegationBatch(branch, changes))
	require.Equal(t, before, delegationSnapshot(t, ctx, k), "outer cache controls final commit")
	commit()
	for _, d := range []types.Delegation{nextA, nextB} {
		count, err := k.delegationCount(ctx, d.Validator)
		require.NoError(t, err)
		require.Equal(t, uint32(1), count)
		index, err := k.service.OpenKVStore(ctx).Get(nftDelegationKeys(d)[0])
		require.NoError(t, err)
		require.Equal(t, delegationKey(d), index)
	}
	after := delegationSnapshot(t, ctx, k)
	malformed := copyDelegation(t, nextA)
	malformed.Stake.Stake.Amount = sdkmath.Int{}
	require.NotPanics(t, func() {
		require.Error(t, k.ApplyDelegationBatch(ctx, []DelegationChange{{Expected: &nextA, Next: &malformed}}))
	})
	require.Equal(t, after, delegationSnapshot(t, ctx, k))
	require.ErrorContains(t, k.ApplyDelegationBatch(ctx, changes), "prestate changed")
	require.Equal(t, after, delegationSnapshot(t, ctx, k))
	// An existing unit cannot be claimed by an unrelated new record.
	duplicate := copyDelegation(t, nextA)
	duplicate.Stake.ID = nextB.Stake.ID
	require.Error(t, k.ApplyDelegationBatch(ctx, []DelegationChange{{Expected: &nextA, Next: &duplicate}}))
	require.Equal(t, after, delegationSnapshot(t, ctx, k))
}

func TestDelegationBatchHotPathDoesNotScanUnrelatedState(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	bounded, err := NewStateKeeper(noDelegationScanService{service})
	require.NoError(t, err)
	next := copyDelegation(t, g.Delegations[0])
	next.Stake.Stake.Amount = next.Stake.Stake.Amount.AddRaw(7)
	require.NoError(t, bounded.SetDelegation(ctx, next), "normal update must use point reads, not ExportGenesis")
	point, err := bounded.GetDelegation(ctx, next.Delegator, next.Validator, next.Stake.Type, next.Stake.ID)
	require.NoError(t, err)
	require.True(t, point.Stake.Stake.Amount.Equal(sdkmath.NewInt(107)))
	got, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.True(t, got.Delegations[0].Stake.Stake.Amount.Equal(sdkmath.NewInt(107)))
}

func TestDelegationBatchLateIndexFailureRollsBackDeletesAndWrites(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	before := delegationSnapshot(t, ctx, k)
	bad, err := NewStateKeeper(failingService{service: service, prefix: delegationCountPrefix})
	require.NoError(t, err)
	next := copyDelegation(t, g.Delegations[0])
	next.Validator = g.Validators[1].OperatorAddress
	require.ErrorContains(t, bad.ApplyDelegationBatch(ctx, []DelegationChange{{Expected: &g.Delegations[0], Next: &next}}), "injected write failure")
	require.Equal(t, before, delegationSnapshot(t, ctx, k), "late counter failure must restore primary and both reverse indexes")
	// Removing the last position also removes empty counters and all indexes.
	require.NoError(t, k.ApplyDelegationBatch(ctx, []DelegationChange{{Expected: &g.Delegations[0]}}))
	got, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, got.Delegations)
}

func TestDelegationIndexesDetectOrphansAndPartialGenesisWrites(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	bad, err := NewStateKeeper(failingService{service: service, prefix: delegatedNFTPrefix})
	require.NoError(t, err)
	require.ErrorContains(t, bad.InitGenesis(ctx, g), "injected write failure")
	_, err = k.GetParams(ctx)
	require.Error(t, err)
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, service.OpenKVStore(ctx).Set(composite(delegationCountPrefix, "orphan"), []byte{0, 0, 0, 1}))
	_, err = k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "orphan native delegation index")
}

type noDelegationScanService struct{ store.KVStoreService }

func (s noDelegationScanService) OpenKVStore(ctx context.Context) store.KVStore {
	return noDelegationScanStore{s.KVStoreService.OpenKVStore(ctx)}
}

type noDelegationScanStore struct{ store.KVStore }

func (s noDelegationScanStore) Iterator(_, _ []byte) (store.Iterator, error) {
	return nil, errors.New("unexpected full-state scan on native delegation hot path")
}
func (s noDelegationScanStore) ReverseIterator(_, _ []byte) (store.Iterator, error) {
	return nil, errors.New("unexpected full-state reverse scan on native delegation hot path")
}
