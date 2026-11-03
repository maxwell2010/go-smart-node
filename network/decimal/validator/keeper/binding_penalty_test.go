package keeper

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func penaltyCoinFixture() types.EVMStakeBinding {
	return types.EVMStakeBinding{Kind: types.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, Gateway: stateAddress(3), StakeID: stateWord(40), Validator: stateAddress(1), Beneficiary: stateAddress(2), TokenContract: stateAddress(4), TokenID: stateWord(0), HoldTimestamp: stateWord(0), RawHoldStartTime: stateWord(0), PenaltyCursor: stateWord(0), RawAmount: sdkmath.OneInt(), RawNftBackedAmount: sdkmath.ZeroInt(), ReportedReserveAmount: sdkmath.ZeroInt(), ReserveAsset: &types.EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom}, NativeTarget: &types.NativeStakeTarget{StakeType: types.StakeType_Coin, ID: identity.BaseDenom}, Active: true}
}
func TestPenaltyCursorFullWordMinMaxCatchupAndReassignment(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain)
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	g := bindingGenesisState(t, true)
	nft := g.EVMStakeBindings[0]
	coin := penaltyCoinFixture()
	target := stateWord(3)
	target[5] = 1
	nft.PenaltyCursor = bytes.Clone(target)
	coin.PenaltyCursor = bytes.Clone(target)
	coin.PenaltyCursor[31] = 2
	g.EVMStakeBindings = []types.EVMStakeBinding{nft, coin}
	owner, operator, err := bindingPairForTest(t, coin.Beneficiary, coin.Validator)
	require.NoError(t, err)
	coinD := types.Delegation{Delegator: owner, Validator: operator, Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, sdkmath.OneInt()))}
	g.Delegations = append(g.Delegations, coinD)
	require.NoError(t, k.InitGenesis(ctx, g))
	got, found, err := k.GetMinActiveStakePenaltyCursor(ctx, coin.Validator)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, coin.PenaltyCursor, got)
	got[0] = 0xff
	require.ErrorIs(t, k.RequireActiveStakePenaltyCatchup(ctx, coin.Validator, target), ErrPendingStakePenalties)
	coin.PenaltyCursor = bytes.Clone(target)
	plan, err := k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &coin}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(ctx, plan))
	require.NoError(t, k.RequireActiveStakePenaltyCatchup(ctx, coin.Validator, target))
	coin.PenaltyCursor[31] = 4
	plan, err = k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &coin}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(ctx, plan))
	require.ErrorIs(t, k.RequireActiveStakePenaltyCatchup(ctx, coin.Validator, target), ErrSourcePenaltyCursor, "max check rejects a future cursor even while minimum equals target")
	coin.Validator = stateAddress(2)
	coin.PenaltyCursor = stateWord(0)
	nextD := copyDelegation(t, coinD)
	_, nextD.Validator, err = bindingPairForTest(t, coin.Beneficiary, coin.Validator)
	require.NoError(t, err)
	branch, commit := ctx.CacheContext()
	require.NoError(t, k.ApplyDelegationBatch(branch, []DelegationChange{{Expected: &coinD, Next: &nextD}}))
	plan, err = k.PrepareBindingPlan(branch, []BindingMutation{{Stake: &coin}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(branch, plan))
	commit()
	require.NoError(t, k.RequireActiveStakePenaltyCatchup(ctx, stateAddress(1), target))
	require.ErrorIs(t, k.RequireActiveStakePenaltyCatchup(ctx, stateAddress(2), target), ErrPendingStakePenalties)
	branch, commit = ctx.CacheContext()
	require.NoError(t, k.ApplyDelegationBatch(branch, []DelegationChange{{Expected: &g.Delegations[0]}}))
	ref := BindingReference{Kind: nft.Kind, Gateway: nft.Gateway, ID: nft.StakeID}
	plan, err = k.PrepareBindingPlan(branch, []BindingMutation{{Remove: &ref}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(branch, plan))
	commit()
	_, found, err = k.GetMinActiveStakePenaltyCursor(ctx, stateAddress(1))
	require.NoError(t, err)
	require.False(t, found)
	coin.Active = false
	branch, commit = ctx.CacheContext()
	require.NoError(t, k.ApplyDelegationBatch(branch, []DelegationChange{{Expected: &nextD}}))
	plan, err = k.PrepareBindingPlan(branch, []BindingMutation{{Stake: &coin}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(branch, plan))
	commit()
	_, found, err = k.GetMinActiveStakePenaltyCursor(ctx, stateAddress(2))
	require.NoError(t, err)
	require.False(t, found)
	export, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, export.Delegations)
}
func TestPenaltyCursorIndexIsAtomicAndRejectsCorruption(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, true)
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	b := bindingNativeFixture()
	b.PenaltyCursor = stateWord(1)
	bad, err := NewStateKeeperWithDomain(failingService{service: service, prefix: activePenaltyBindingPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	plan, err := bad.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &b}})
	require.NoError(t, err)
	require.ErrorContains(t, bad.ApplyBindingPlan(ctx, plan), "injected write failure")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	got, found, err := k.GetMinActiveStakePenaltyCursor(ctx, b.Validator)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, stateWord(0), got)
	ref := BindingReference{Kind: b.Kind, Gateway: b.Gateway, ID: stateWord(99)}
	key, err := ref.key()
	require.NoError(t, err)
	orphan := activePenaltyBindingKey(b.Validator, stateWord(0), key)
	require.NoError(t, service.OpenKVStore(ctx).Set(orphan, key))
	_, _, err = k.GetMinActiveStakePenaltyCursor(ctx, b.Validator)
	require.NoError(t, err, "hot minimum lookup does not scan an unrelated later orphan")
	// The valid first row precedes the orphan; export inspects exact coverage.
	_, err = k.ExportGenesis(ctx)
	require.Error(t, err)
	require.NoError(t, service.OpenKVStore(ctx).Delete(orphan))
	original := bindingNativeFixture()
	sourceKey, err := (BindingReference{Kind: original.Kind, Gateway: original.Gateway, ID: original.StakeID}).key()
	require.NoError(t, err)
	ownKey := activePenaltyBindingKey(original.Validator, original.PenaltyCursor, sourceKey)
	require.NoError(t, service.OpenKVStore(ctx).Set(ownKey, key))
	_, _, err = k.GetMinActiveStakePenaltyCursor(ctx, b.Validator)
	require.Error(t, err)
	_, _, err = k.GetMinActiveStakePenaltyCursor(ctx, []byte{1})
	require.Error(t, err)
	require.Error(t, k.RequireActiveStakePenaltyCatchup(ctx, b.Validator, []byte{1}))
}

type penaltySeekControl struct{ iteratorCalls, nextCalls, closeCalls int }
type penaltySeekService struct {
	store.KVStoreService
	validator []byte
	control   *penaltySeekControl
}

func (s penaltySeekService) OpenKVStore(ctx context.Context) store.KVStore {
	return penaltySeekStore{s.KVStoreService.OpenKVStore(ctx), s.validator, s.control}
}

type penaltySeekStore struct {
	store.KVStore
	validator []byte
	control   *penaltySeekControl
}

func (s penaltySeekStore) open(start, end []byte, reverse bool) (store.Iterator, error) {
	prefix := append([]byte{activePenaltyBindingPrefix}, s.validator...)
	if !bytes.Equal(start, prefix) || !bytes.Equal(end, storetypes.PrefixEndBytes(prefix)) {
		return nil, errors.New("unexpected full ledger scan in penalty catchup")
	}
	s.control.iteratorCalls++
	var it store.Iterator
	var err error
	if reverse {
		it, err = s.KVStore.ReverseIterator(start, end)
	} else {
		it, err = s.KVStore.Iterator(start, end)
	}
	if err != nil {
		return nil, err
	}
	return penaltySeekIterator{it, s.control}, nil
}
func (s penaltySeekStore) Iterator(start, end []byte) (store.Iterator, error) {
	return s.open(start, end, false)
}
func (s penaltySeekStore) ReverseIterator(start, end []byte) (store.Iterator, error) {
	return s.open(start, end, true)
}

type penaltySeekIterator struct {
	store.Iterator
	control *penaltySeekControl
}

func (i penaltySeekIterator) Next()        { i.control.nextCalls++; i.Iterator.Next() }
func (i penaltySeekIterator) Close() error { i.control.closeCalls++; return i.Iterator.Close() }
func TestPenaltyCatchupUsesOnlyTwoBoundedSeeks(t *testing.T) {
	ctx, service, _ := bindingEnvironment(t, true)
	control := &penaltySeekControl{}
	k, err := NewStateKeeperWithDomain(penaltySeekService{service, stateAddress(1), control}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.NoError(t, k.RequireActiveStakePenaltyCatchup(ctx, stateAddress(1), stateWord(0)))
	require.Equal(t, 2, control.iteratorCalls)
	require.Zero(t, control.nextCalls)
	require.Equal(t, 2, control.closeCalls)
}

func TestPenaltyIndexGenesisFailureLeavesNoPartialStore(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain)
	bad, err := NewStateKeeperWithDomain(failingService{service: service, prefix: activePenaltyBindingPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, bad.InitGenesis(ctx, bindingGenesisState(t, true)), "injected write failure")
	marker, err := service.OpenKVStore(ctx).Get([]byte{bindingDomainMarker})
	require.NoError(t, err)
	require.Nil(t, marker)
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	require.NoError(t, k.InitGenesis(ctx, bindingGenesisState(t, true)))
	cursor, found, err := k.GetMinActiveStakePenaltyCursor(ctx, stateAddress(1))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, stateWord(0), cursor)
}
