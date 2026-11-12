package keeper

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

type activeVotingService struct {
	store.KVStoreService
	livePrefixes [][]byte
}

func (s activeVotingService) OpenKVStore(ctx context.Context) store.KVStore {
	return activeVotingStore{s.KVStoreService.OpenKVStore(ctx), s.livePrefixes}
}

type activeVotingStore struct {
	store.KVStore
	livePrefixes [][]byte
}

func (s activeVotingStore) Iterator(start, end []byte) (store.Iterator, error) {
	if s.livePrefixes != nil {
		for _, prefix := range s.livePrefixes {
			if bytes.Equal(start, prefix) && bytes.Equal(end, storetypes.PrefixEndBytes(prefix)) {
				return s.KVStore.Iterator(start, end)
			}
		}
		return nil, errors.New("native voting attempted a traversal outside the live operator indexes")
	}
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

	reader, err := NewStateKeeperWithDomain(activeVotingService{KVStoreService: service}, bindingTestChain, 990001)
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
	require.ErrorContains(t, err, "differ from indexed DEL ownership")
}

func TestActiveDELVotingStateReadsOnlyLiveValidatorIndexes(t *testing.T) {
	ctx, keeper, retiredStake := delProvenanceGenesis(t)
	liveStake := retiredStake
	liveStake.Validator = common.BytesToAddress(stateAddress(2))
	liveStake.Beneficiary = common.BytesToAddress(stateAddress(7))
	liveStake.StakeID = crypto.Keccak256Hash(liveStake.Validator[:], liveStake.Beneficiary[:], liveStake.WrappedDEL[:], make([]byte, 64))
	liveStake.Amount = sdkmath.NewInt(19)
	require.NoError(t, keeper.ApplyGatewayDELDelegation(ctx, liveStake, liveStake.Amount))
	_, liveOperator, err := liveStake.nativePair()
	require.NoError(t, err)
	retiredOwner, retiredOperator, err := retiredStake.nativePair()
	require.NoError(t, err)

	legacy, err := keeper.ReadActiveDELVotingState(ctx)
	require.NoError(t, err)
	require.Len(t, legacy.Delegations, 2)
	require.Len(t, legacy.EVMStakeBindings, 2)
	require.NoError(t, keeper.InitializeNativeLiveValidatorRoster(ctx.WithBlockHeight(0)))
	bothLive, err := keeper.ReadActiveDELVotingState(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, legacy, bothLive)

	ctx = ctx.WithExecMode(sdk.ExecModeFinalize).WithBlockHeight(10).WithBlockTime(time.Unix(2010, 0).UTC())
	retired, err := keeper.GetValidator(ctx, retiredOperator)
	require.NoError(t, err)
	consensus, err := retired.GetConsAddr()
	require.NoError(t, err)
	candidate := queuedDuplicateVote(t, 1, 1, 4)
	candidate.ConsensusAddress = consensus
	_, err = keeper.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{candidate})
	require.NoError(t, err)
	jobs, err := keeper.PendingNativeDuplicateVotePenalties(ctx, 1)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.NoError(t, keeper.FinalizeNativeDuplicateVotePenalty(ctx, jobs[0]))

	service := activeVotingService{
		KVStoreService: keeper.service,
		livePrefixes: [][]byte{
			composite(validatorDelegationPrefix, liveOperator),
			append([]byte{activePenaltyBindingPrefix}, liveStake.Validator[:]...),
		},
	}
	reader, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	cached, _ := ctx.CacheContext()
	snapshot, err := reader.ReadActiveDELVotingState(cached)
	require.NoError(t, err)
	require.Len(t, snapshot.Validators, 1)
	require.Equal(t, liveOperator, snapshot.Validators[0].OperatorAddress)
	require.Len(t, snapshot.Delegations, 1)
	require.Equal(t, liveOperator, snapshot.Delegations[0].Validator)
	require.Equal(t, liveStake.Amount, snapshot.Delegations[0].Stake.Stake.Amount)
	require.Len(t, snapshot.EVMStakeBindings, 1)
	require.Equal(t, liveStake.StakeID.Bytes(), snapshot.EVMStakeBindings[0].StakeID)

	stillRetired, err := keeper.GetValidator(ctx, retiredOperator)
	require.NoError(t, err)
	require.True(t, stillRetired.Jailed)
	claim, err := keeper.GetDelegation(ctx, retiredOwner, retiredOperator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, retiredStake.Amount, claim.Stake.Stake.Amount)
	binding, err := keeper.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, retiredStake.Gateway[:], retiredStake.StakeID[:])
	require.NoError(t, err)
	require.True(t, binding.Active)
	require.Equal(t, retiredStake.Amount, binding.RawAmount)

	record, err := recordFromStake(&snapshot.EVMStakeBindings[0])
	require.NoError(t, err)
	liveBinding := record.stake
	require.NoError(t, keeper.service.OpenKVStore(cached).Delete(activePenaltyBindingKey(liveBinding.Validator, liveBinding.PenaltyCursor, record.key)))
	_, err = reader.ReadActiveDELVotingState(cached)
	require.ErrorContains(t, err, "native delegation has no complete active source bindings")
}
