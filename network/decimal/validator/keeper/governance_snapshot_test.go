package keeper

import (
	"context"
	"errors"
	"testing"
	"time"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

type activeVotingService struct{ store.KVStoreService }

func (s activeVotingService) OpenKVStore(ctx context.Context) store.KVStore {
	return activeVotingStore{s.KVStoreService.OpenKVStore(ctx)}
}

type activeVotingStore struct{ store.KVStore }

func (s activeVotingStore) Iterator(start, end []byte) (store.Iterator, error) {
	if len(start) == 1 && len(end) == 1 && end[0] == start[0]+1 {
		switch start[0] {
		case validatorPrefix, delegationPrefix, stakeBindingPrefix:
			return s.KVStore.Iterator(start, end)
		}
	}
	return nil, errors.New("native voting attempted a non-active traversal")
}

func (s activeVotingStore) ReverseIterator(_, _ []byte) (store.Iterator, error) {
	return nil, errors.New("native voting attempted a non-active traversal")
}

func (s activeVotingStore) Get(key []byte) ([]byte, error) {
	if len(key) != 0 {
		switch key[0] {
		case frozenBindingPrefix, undelegationPrefix, redelegationPrefix, nativeQueueBindingPrefix,
			validatorFrozenBindingPrefix, withdrawalDuePrefix, historyPrefix:
			return nil, errors.New("native voting attempted a historical lookup")
		}
	}
	return s.KVStore.Get(key)
}

func TestActiveDELVotingStateExcludesPendingAndCompletedHistory(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	position, _ := seedGatewayDELNFTCompanion(t, ctx, k)
	position.Amount = sdkmath.NewInt(12)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, position.Amount))
	held := gatewayDELHoldFixture(2000, 2020, 8)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	_, operator, err := position.nativePair()
	require.NoError(t, err)

	firstDue, secondDue := time.Unix(2010, 0).UTC(), time.Unix(2030, 0).UTC()
	first, second := common.Hash{}, common.BytesToHash([]byte{1})
	position.Amount = position.Amount.SubRaw(3)
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, first, sdkmath.NewInt(3), firstDue))
	position.Amount = position.Amount.SubRaw(2)
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, second, sdkmath.NewInt(2), secondDue))

	reader, err := NewStateKeeperWithDomain(activeVotingService{service}, bindingTestChain, 990001)
	require.NoError(t, err)
	ctx = ctx.WithBlockHeight(11).WithBlockTime(firstDue)
	before, err := reader.ReadActiveDELVotingState(ctx)
	require.NoError(t, err)
	require.Len(t, before.Delegations, 3)
	require.Len(t, before.EVMStakeBindings, 3)
	require.Empty(t, before.Undelegations)
	require.Empty(t, before.EVMFrozenBindings)
	assertActiveDELReserve(t, ctx, k, operator, 35)

	frozen, err := k.GetEVMFrozenBinding(ctx, position.Gateway[:], first[:])
	require.NoError(t, err)
	require.NoError(t, k.ApplyWithdrawalCompletion(ctx, frozen))
	complete, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, complete.EVMFrozenBindings, 2)
	require.False(t, complete.EVMFrozenBindings[0].Active)
	require.True(t, complete.EVMFrozenBindings[1].Active)
	require.Len(t, complete.Undelegations, 1)
	require.NotZero(t, complete.BindingRevision)
	after, err := reader.ReadActiveDELVotingState(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	require.Equal(t, complete.Delegations, after.Delegations)
	require.Equal(t, complete.EVMStakeBindings, after.EVMStakeBindings)
	_, err = reader.ExportGenesis(ctx)
	require.ErrorContains(t, err, "non-active traversal")

	key, err := operatorKey(activeDELReservePrefix, operator)
	require.NoError(t, err)
	require.NoError(t, service.OpenKVStore(ctx).Set(key, make([]byte, activeDELReserveBytes)))
	_, err = reader.ReadActiveDELVotingState(ctx)
	require.ErrorContains(t, err, "differs from indexed DEL ownership")
}
