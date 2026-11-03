package keeper

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	"cosmossdk.io/core/store"
	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	multistore "cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const bindingTestChain = "mintcandy_990001-1"

func stateWord(value uint64) []byte {
	out := make([]byte, 32)
	binary.BigEndian.PutUint64(out[24:], value)
	return out
}
func stateAddress(value byte) []byte { return bytes.Repeat([]byte{value}, 20) }
func bindingNativeFixture() types.EVMStakeBinding {
	pos := stateWord(1)
	pos[5] = 1
	origin := &types.EVMNFTReference{Contract: stateAddress(7), TokenID: make([]byte, 32), NativeTokenID: "origin-native", NativeSubTokenIDs: []uint32{2, 8}, Standard: 1155}
	receipt := &types.EVMNFTReference{Contract: stateAddress(8), TokenID: bytes.Clone(pos), NativeTokenID: "receipt-native", NativeSubTokenIDs: []uint32{1}, Standard: 721}
	return types.EVMStakeBinding{Kind: types.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT, Gateway: stateAddress(3), StakeID: stateWord(30), Validator: stateAddress(1), Beneficiary: stateAddress(2), TokenContract: stateAddress(8), TokenID: bytes.Clone(pos), HoldTimestamp: stateWord(0), PenaltyCursor: stateWord(0), RawAmount: sdkmath.OneInt(), RawNftBackedAmount: sdkmath.ZeroInt(), ReportedReserveAmount: sdkmath.NewInt(20), ReserveAsset: &types.EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom}, NFTPosition: &types.NFTPositionIdentity{Mode: types.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT, Origin: origin, Custody: receipt, OriginQuantity: sdkmath.NewInt(2), ReceiptQuantity: sdkmath.OneInt(), Vault: stateAddress(8), PositionID: bytes.Clone(pos), ParentPositionID: stateWord(1)}, NativeTarget: &types.NativeStakeTarget{StakeType: types.StakeType_NFT, ID: "receipt-native", SubTokenIDs: []uint32{1}}, Active: true, CreatedHeight: 7, UpdatedHeight: 8}
}
func normalizedBindingDelegation(t *testing.T, b types.EVMStakeBinding) types.Delegation {
	t.Helper()
	owner, operator, err := bindingPairForTest(t, b.Beneficiary, b.Validator)
	require.NoError(t, err)
	return types.Delegation{Delegator: owner, Validator: operator, Stake: types.NewStakeNFT(b.NativeTarget.ID, b.NativeTarget.SubTokenIDs, sdk.NewCoin(b.ReserveAsset.NativeDenom, b.ReportedReserveAmount))}
}
func bindingPairForTest(t *testing.T, owner, operator []byte) (string, string, error) {
	t.Helper()
	o, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(owner)
	if err != nil {
		return "", "", err
	}
	v, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(operator)
	return o, v, err
}
func bindingGenesisState(t *testing.T, active bool) *types.GenesisState {
	t.Helper()
	g := types.DefaultGenesisState()
	g.BindingDomain = &types.EVMSourceDomain{ChainID: bindingTestChain, EVMChainID: 990001}
	g.Validators = []types.Validator{validator(t, 1, 0), validator(t, 2, 0)}
	if active {
		b := bindingNativeFixture()
		g.EVMStakeBindings = []types.EVMStakeBinding{b}
		g.Delegations = []types.Delegation{normalizedBindingDelegation(t, b)}
	}
	require.NoError(t, g.Validate())
	return g
}
func bindingEnvironment(t *testing.T, active bool) (sdk.Context, store.KVStoreService, *StateKeeper) {
	t.Helper()
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain).WithBlockHeight(10).WithBlockTime(time.Unix(2000, 0).UTC())
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	require.NoError(t, k.InitGenesis(ctx, bindingGenesisState(t, active)))
	return ctx, service, k
}
func copyNativeBinding(t *testing.T, b types.EVMStakeBinding) types.EVMStakeBinding {
	t.Helper()
	data, err := proto.Marshal(&b)
	require.NoError(t, err)
	var out types.EVMStakeBinding
	require.NoError(t, proto.Unmarshal(data, &out))
	return out
}
func readNativeBinding(t *testing.T, ctx sdk.Context, k *StateKeeper, b types.EVMStakeBinding) types.EVMStakeBinding {
	t.Helper()
	out, err := k.GetEVMStakeBinding(ctx, b.Kind, b.Gateway, b.StakeID)
	require.NoError(t, err)
	return out
}

func TestBindingConfiguredEmptyDomainAndNoGenesisPermissionBypass(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain)
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	input := types.DefaultGenesisState()
	require.NoError(t, k.InitGenesis(ctx, input))
	require.Nil(t, input.BindingDomain, "caller genesis is not mutated")
	domain, err := k.GetEVMSourceDomain(ctx)
	require.NoError(t, err)
	require.Equal(t, bindingTestChain, domain.ChainID)
	require.EqualValues(t, 990001, domain.EVMChainID)
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.NotNil(t, exported.BindingDomain)
	_, err = k.GetEVMSourceDomain(ctx.WithChainID("another-network"))
	require.ErrorIs(t, err, ErrBindingDomain)
	other, err := NewStateKeeperWithDomain(service, bindingTestChain, 990002)
	require.NoError(t, err)
	_, err = other.ExportGenesis(ctx)
	require.ErrorIs(t, err, ErrBindingDomain)
	unbound, err := NewStateKeeper(service)
	require.NoError(t, err)
	_, err = unbound.ExportGenesis(ctx)
	require.ErrorIs(t, err, ErrBindingDomain)
	domain.ChainID = "mutated"
	again, err := k.GetEVMSourceDomain(ctx)
	require.NoError(t, err)
	require.Equal(t, bindingTestChain, again.ChainID)
	ctx2, service2, _ := environment(t)
	ctx2 = ctx2.WithChainID(bindingTestChain)
	bound2, err := NewStateKeeperWithDomain(service2, bindingTestChain, 990001)
	require.NoError(t, err)
	wrong := bindingGenesisState(t, true)
	wrong.BindingDomain.ChainID = "another-network"
	require.ErrorIs(t, bound2.InitGenesis(ctx2, wrong), ErrBindingDomain)
	marker, err := service2.OpenKVStore(ctx2).Get([]byte{bindingDomainMarker})
	require.NoError(t, err)
	require.Nil(t, marker)

}
func TestBindingPlansReplayDeepCopiesAndAtomicReassignment(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, true)
	old := bindingNativeFixture()
	first, err := k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &old}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(ctx, first))
	revision, err := k.BindingRevision(ctx)
	require.NoError(t, err)
	require.Zero(t, revision, "identical target replay is a no-op")
	next := copyNativeBinding(t, old)
	next.StakeID = stateWord(31)
	next.Beneficiary = stateAddress(4)
	next.Validator = stateAddress(2)
	next.UpdatedHeight = 10
	_, err = k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &next}})
	require.ErrorContains(t, err, "another active/frozen")
	remove := BindingReference{Kind: old.Kind, Gateway: old.Gateway, ID: old.StakeID}
	plan, err := k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &next}, {Remove: &remove}})
	require.NoError(t, err)
	// Both native projection and source metadata transition in one outer cache.
	child, commit := ctx.CacheContext()
	before := normalizedBindingDelegation(t, old)
	after := normalizedBindingDelegation(t, next)
	require.NoError(t, k.ApplyDelegationBatch(child, []DelegationChange{{Expected: &before, Next: &after}}))
	targetBeforeMutation := copyNativeBinding(t, next)
	next.StakeID[0] = 99
	next.NFTPosition.Origin.NativeSubTokenIDs[0] = 99
	targets, _, err := k.BindingPlanTargets(plan)
	require.NoError(t, err)
	targets[0].Beneficiary[0] = 77
	targets[0].NFTPosition.Origin.NativeSubTokenIDs[0] = 88
	require.NoError(t, k.ApplyBindingPlan(child, plan))
	_, err = k.GetEVMStakeBinding(ctx, targetBeforeMutation.Kind, targetBeforeMutation.Gateway, targetBeforeMutation.StakeID)
	require.ErrorIs(t, err, ErrNotFound, "outer cache not committed")
	commit()
	require.NoError(t, k.ApplyBindingPlan(ctx, plan), "same captured plan exact target replay does not write twice")
	got := readNativeBinding(t, ctx, k, targetBeforeMutation)
	require.Equal(t, stateAddress(4), got.Beneficiary)
	require.Equal(t, []uint32{2, 8}, got.NFTPosition.Origin.NativeSubTokenIDs)
	got.NFTPosition.Custody.TokenID[0] = 9
	got.NFTPosition.Origin.NativeSubTokenIDs[0] = 1
	again := readNativeBinding(t, ctx, k, targetBeforeMutation)
	require.Equal(t, targetBeforeMutation.TokenID, again.NFTPosition.Custody.TokenID)
	require.Equal(t, []uint32{2, 8}, again.NFTPosition.Origin.NativeSubTokenIDs)
	assignment, err := k.LookupCustodyUnitBinding(ctx, "receipt-native", 1)
	require.NoError(t, err)
	require.Equal(t, stateAddress(4), assignment.Stake.Beneficiary)
	assignment, err = k.LookupCustodyNFTBinding(ctx, again.TokenContract, again.TokenID)
	require.NoError(t, err)
	require.Equal(t, stateAddress(2), assignment.Stake.Validator)
	_, err = k.GetEVMStakeBinding(ctx, old.Kind, old.Gateway, old.StakeID)
	require.ErrorIs(t, err, ErrNotFound)
	revision, err = k.BindingRevision(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, revision)
	export, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, export.EVMStakeBindings, 1)
	page, cursor, err := k.GetEVMStakeBindingsPage(ctx, nil, 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Nil(t, cursor)
}
func TestBindingStaleForeignContextsAndLateRevisionFailureRollback(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, true)
	old := bindingNativeFixture()
	a := copyNativeBinding(t, old)
	a.ReportedReserveAmount = sdkmath.NewInt(19)
	stale, err := k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &a}})
	require.NoError(t, err)
	b := copyNativeBinding(t, old)
	b.ReportedReserveAmount = sdkmath.NewInt(18)
	current, err := k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &b}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(ctx, current))
	require.ErrorIs(t, k.ApplyBindingPlan(ctx, stale), ErrStaleBindingPlan)
	require.Error(t, k.ApplyBindingPlan(ctx.WithBlockHeight(11), current))
	require.Error(t, k.ApplyBindingPlan(ctx.WithBlockTime(time.Unix(2001, 0)), current))
	require.Error(t, k.ApplyBindingPlan(ctx.WithChainID("other"), current))
	foreign, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	require.Error(t, foreign.ApplyBindingPlan(ctx, current))
	require.Error(t, k.ApplyBindingPlan(ctx, &BindingPlan{}))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	bad, err := NewStateKeeperWithDomain(failingService{service: service, prefix: bindingRevisionKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	a.ReportedReserveAmount = sdkmath.NewInt(17)
	failed, err := bad.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &a}})
	require.NoError(t, err)
	require.ErrorContains(t, bad.ApplyBindingPlan(ctx, failed), "injected write failure")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	require.Equal(t, sdkmath.NewInt(18), readNativeBinding(t, ctx, k, old).ReportedReserveAmount)
}
func TestBindingGenesisImportRollbackAndIncompleteCoverage(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain)
	bound, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	incomplete := bindingGenesisState(t, true)
	incomplete.EVMStakeBindings = nil
	require.Error(t, bound.InitGenesis(ctx, incomplete))
	marker, err := service.OpenKVStore(ctx).Get([]byte{bindingDomainMarker})
	require.NoError(t, err)
	require.Nil(t, marker)
	bad, err := NewStateKeeperWithDomain(failingService{service: service, prefix: bindingRevisionKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, bad.InitGenesis(ctx, bindingGenesisState(t, true)), "injected write failure")
	marker, err = service.OpenKVStore(ctx).Get([]byte{bindingDomainMarker})
	require.NoError(t, err)
	require.Nil(t, marker)
	require.NoError(t, bound.InitGenesis(ctx, bindingGenesisState(t, true)))
	export, err := bound.ExportGenesis(ctx)
	require.NoError(t, err)
	ctx2, service2, _ := environment(t)
	ctx2 = ctx2.WithChainID(bindingTestChain)
	again, err := NewStateKeeperWithDomain(service2, bindingTestChain, 990001)
	require.NoError(t, err)
	require.NoError(t, again.InitGenesis(ctx2, export))
	roundTrip, err := again.ExportGenesis(ctx2)
	require.NoError(t, err)
	assertGenesisBytes(t, export, roundTrip)
}
func TestBindingIdenticalNativeQueueEntriesHaveDistinctFullFrozenIndexes(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain).WithBlockHeight(10)
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	g := bindingGenesisState(t, false)
	owner, operator := addr(t, identity.Bech32PrefixAccAddr, 2), addr(t, identity.Bech32PrefixValAddr, 1)
	entry := types.UndelegationEntry{CreationHeight: 7, CompletionTime: time.Unix(2000, 0).UTC(), Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, sdkmath.NewInt(1)))}
	hash, err := types.NativeQueueEntryHash(1, owner, operator, "", &entry)
	require.NoError(t, err)
	g.Undelegations = []types.Undelegation{{Delegator: owner, Validator: operator, Entries: []types.UndelegationEntry{entry, entry}}}
	for _, i := range []uint64{0, 1} {
		g.EVMFrozenBindings = append(g.EVMFrozenBindings, types.EVMFrozenBinding{Gateway: stateAddress(3), FrozenIndex: stateWord(i), SourceKind: types.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, SourceStakeID: stateWord(30), SourceValidator: stateAddress(1), Beneficiary: stateAddress(2), FreezeType: 1, CompletionTimestamp: stateWord(2000), RawAmount: sdkmath.OneInt(), ReportedReserveAmount: sdkmath.ZeroInt(), ReserveAsset: &types.EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom}, NativeTarget: &types.NativeStakeTarget{StakeType: types.StakeType_Coin, ID: identity.BaseDenom}, NativeEntryHash: bytes.Clone(hash), Active: true})
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	first, next, err := k.GetNativeQueueBindingsPage(ctx, hash, nil, 1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, stateWord(0), first[0].FrozenIndex)
	require.Len(t, next, 86)
	second, end, err := k.GetNativeQueueBindingsPage(ctx, hash, next, 1)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, stateWord(1), second[0].FrozenIndex)
	require.Nil(t, end)
	export, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, export.EVMFrozenBindings, 2)
	orphan := append([]byte{nativeQueueBindingPrefix}, hash...)
	orphan = append(orphan, frozenBindingPrefix)
	orphan = append(orphan, second[0].Gateway...)
	orphan = append(orphan, stateWord(9)...)
	require.NoError(t, service.OpenKVStore(ctx).Set(orphan, []byte{1}))
	_, err = k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "orphan")
}
func TestBindingsSurviveRealIAVLLevelDBRestart(t *testing.T) {
	directory := t.TempDir()
	open := func() (sdk.Context, multistore.CommitMultiStore, dbm.DB, *StateKeeper) {
		database, err := dbm.NewDB("native-bindings", dbm.GoLevelDBBackend, directory)
		require.NoError(t, err)
		cms := multistore.NewCommitMultiStore(database, log.NewNopLogger(), metrics.NewNoOpMetrics())
		key := storetypes.NewKVStoreKey("native-validator")
		cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, database)
		require.NoError(t, cms.LoadLatestVersion())
		k, err := NewStateKeeperWithDomain(runtime.NewKVStoreService(key), bindingTestChain, 990001)
		require.NoError(t, err)
		ctx := sdk.NewContext(cms, cmtproto.Header{ChainID: bindingTestChain, Height: 10, Time: time.Unix(2000, 0).UTC()}, false, log.NewNopLogger())
		return ctx, cms, database, k
	}
	ctx, cms, db, k := open()
	require.NoError(t, k.InitGenesis(ctx, bindingGenesisState(t, true)))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	b := bindingNativeFixture()
	plan, err := k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &b}})
	require.NoError(t, err)
	cms.Commit()
	require.NoError(t, db.Close())
	ctx, cms, db, k = open()
	defer func() { require.NoError(t, db.Close()) }()
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	assignment, err := k.LookupCustodyNFTBinding(ctx, b.TokenContract, b.TokenID)
	require.NoError(t, err)
	require.Equal(t, b.TokenID, assignment.Stake.TokenID)
	require.Error(t, k.ApplyBindingPlan(ctx, plan), "old process/keeper plan cannot be applied after restart")
	cms.Commit()
}

type typedNilBindingService struct{}

func (*typedNilBindingService) OpenKVStore(context.Context) store.KVStore {
	panic("typed nil must not be invoked")
}
func TestBindingConstructorRejectsTypedNilService(t *testing.T) {
	var service *typedNilBindingService
	_, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.Error(t, err)
}

func TestBindingActiveReceiptFreezesAndCompletesWithoutUnitReselection(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, true)
	b := bindingNativeFixture()
	d := normalizedBindingDelegation(t, b)
	entry := types.UndelegationEntry{CreationHeight: 10, CompletionTime: time.Unix(3000, 0).UTC(), Stake: d.Stake}
	hash, err := types.NativeQueueEntryHash(1, d.Delegator, d.Validator, "", &entry)
	require.NoError(t, err)
	frozen := types.EVMFrozenBinding{Gateway: b.Gateway, FrozenIndex: stateWord(0), SourceKind: b.Kind, SourceStakeID: b.StakeID, SourceValidator: b.Validator, Beneficiary: b.Beneficiary, FreezeType: 1, CompletionTimestamp: stateWord(3000), RawAmount: sdkmath.OneInt(), ReportedReserveAmount: b.ReportedReserveAmount, ReserveAsset: b.ReserveAsset, NFTPosition: b.NFTPosition, NativeTarget: b.NativeTarget, NativeEntryHash: hash, Active: true, CreatedHeight: 10, UpdatedHeight: 10}
	old := BindingReference{Kind: b.Kind, Gateway: b.Gateway, ID: b.StakeID}
	child, commit := ctx.CacheContext()
	require.NoError(t, k.ApplyDelegationBatch(child, []DelegationChange{{Expected: &d}}))
	u := types.Undelegation{Delegator: d.Delegator, Validator: d.Validator, Entries: []types.UndelegationEntry{entry}}
	require.NoError(t, k.put(child, composite(undelegationPrefix, u.Delegator, u.Validator), &u))
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{{Frozen: &frozen}, {Remove: &old}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(child, plan))
	commit()
	assignment, err := k.LookupCustodyUnitBinding(ctx, "receipt-native", 1)
	require.NoError(t, err)
	require.Nil(t, assignment.Stake)
	require.Equal(t, stateWord(0), assignment.Frozen.FrozenIndex)
	reference, err := assignment.Reference()
	require.NoError(t, err)
	require.True(t, reference.Frozen)
	require.Equal(t, stateWord(0), reference.ID)
	assignment.Frozen.NFTPosition.Origin.NativeSubTokenIDs[0] = 99
	restored, err := k.LookupCustodyNFTBinding(ctx, b.TokenContract, b.TokenID)
	require.NoError(t, err)
	require.Equal(t, []uint32{2, 8}, restored.Frozen.NFTPosition.Origin.NativeSubTokenIDs)
	rows, cursor, err := k.GetNativeQueueBindingsPage(ctx, hash, nil, 256)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Nil(t, cursor)
	require.Equal(t, b.NativeTarget.SubTokenIDs, rows[0].NativeTarget.SubTokenIDs, "exact captured receipt unit survives active-to-frozen handoff")
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
	child, commit = ctx.CacheContext()
	require.NoError(t, service.OpenKVStore(child).Delete(composite(undelegationPrefix, u.Delegator, u.Validator)))
	complete, err := k.PrepareBindingPlan(child, []BindingMutation{{Remove: &reference}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(child, complete))
	commit()
	_, err = k.LookupCustodyUnitBinding(ctx, "receipt-native", 1)
	require.ErrorIs(t, err, ErrNotFound)
	export, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, export.EVMStakeBindings)
	require.Empty(t, export.EVMFrozenBindings)
	require.Empty(t, export.Undelegations)
	revision, err := k.BindingRevision(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, revision)
}
