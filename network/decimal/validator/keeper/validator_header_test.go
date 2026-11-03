package keeper

import (
	"bytes"
	"testing"

	"cosmossdk.io/log"
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

func headerGenesisFixture(t *testing.T) *types.GenesisState {
	g := bindingGenesisState(t, false)
	for i, v := range g.Validators {
		raw, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).StringToBytes(v.OperatorAddress)
		require.NoError(t, err)
		h := types.EVMValidatorHeader{Master: stateAddress(9), Validator: raw, NativeOperator: v.OperatorAddress, Status: uint32(i + 1), Paused: i == 1, PenaltyCount: stateWord(0), LastMetaHash: bytes.Repeat([]byte{byte(i + 1)}, 32), CreatedHeight: 1, UpdatedHeight: 1, LastMetaHeight: 1}
		g.EVMValidatorHeaders = append(g.EVMValidatorHeaders, h)
	}
	require.NoError(t, g.Validate())
	return g
}
func copyHeader(t *testing.T, h types.EVMValidatorHeader) types.EVMValidatorHeader {
	t.Helper()
	data, err := proto.Marshal(&h)
	require.NoError(t, err)
	var next types.EVMValidatorHeader
	require.NoError(t, proto.Unmarshal(data, &next))
	return next
}
func TestValidatorHeaderLifecycleCASAndSourceOnlyEligibility(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain)
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	g := headerGenesisFixture(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	first, err := k.GetEVMValidatorHeaderByOperator(ctx, g.Validators[0].OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, uint32(1), first.Status)
	require.True(t, first.SourceEligible())
	page, next, err := k.GetSourceEligibleValidatorHeadersPage(ctx, first.Master, nil, 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Nil(t, next)
	require.Equal(t, first.Validator, page[0].Validator)
	old := g.EVMValidatorHeaders[1]
	updated := copyHeader(t, old)
	updated.Paused = false
	updated.UpdatedHeight = 2
	updated.PenaltyCount = bytes.Repeat([]byte{0xff}, 32)
	beforeVal, err := k.GetValidator(ctx, updated.NativeOperator)
	require.NoError(t, err)
	changes := []ValidatorHeaderChange{{Expected: &old, Next: &updated}}
	branch, commit := ctx.CacheContext()
	require.NoError(t, k.ApplyValidatorHeaderBatch(branch, changes))
	parent, err := k.GetEVMValidatorHeaderByOperator(ctx, old.NativeOperator)
	require.NoError(t, err)
	require.True(t, parent.Paused)
	commit()
	afterVal, err := k.GetValidator(ctx, updated.NativeOperator)
	require.NoError(t, err)
	require.Equal(t, beforeVal, afterVal, "metadata headers cannot create power or change RS/rewards")
	page, next, err = k.GetSourceEligibleValidatorHeadersPage(ctx, first.Master, nil, 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, first.Validator, next)
	second, end, err := k.GetSourceEligibleValidatorHeadersPage(ctx, first.Master, next, 1)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Nil(t, end)
	require.Equal(t, updated.Validator, second[0].Validator)
	badWriter, err := NewStateKeeperWithDomain(failingService{service: service, prefix: validatorHeaderPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.NoError(t, badWriter.ApplyValidatorHeaderBatch(ctx, changes), "exact target replay does not rewrite metadata")
	updated.LastMetaHash[0] ^= 1
	stored, err := k.GetEVMValidatorHeaderByOperator(ctx, old.NativeOperator)
	require.NoError(t, err)
	require.NotEqual(t, updated.LastMetaHash, stored.LastMetaHash)
	stale := copyHeader(t, stored)
	stale.Paused = true
	stale.UpdatedHeight = 3
	require.ErrorContains(t, k.ApplyValidatorHeaderBatch(ctx, []ValidatorHeaderChange{{Expected: &old, Next: &stale}}), "prestate changed")
	deleted := copyHeader(t, stored)
	deleted.Status = 0
	deleted.Paused = false
	deleted.UpdatedHeight = 3
	require.NoError(t, k.ApplyValidatorHeaderBatch(ctx, []ValidatorHeaderChange{{Expected: &stored, Next: &deleted}}))
	page, _, err = k.GetSourceEligibleValidatorHeadersPage(ctx, first.Master, nil, 10)
	require.NoError(t, err)
	require.Len(t, page, 1, "Deleted never eligible even with paused=false")
	require.Error(t, k.ApplyValidatorHeaderBatch(ctx, []ValidatorHeaderChange{{Expected: &deleted}}), "retain Deleted source identity tombstone")
	foreign := copyHeader(t, deleted)
	foreign.Master = stateAddress(8)
	require.Error(t, k.ApplyValidatorHeaderBatch(ctx, []ValidatorHeaderChange{{Expected: &deleted, Next: &foreign}}))
	_, _, err = k.GetSourceEligibleValidatorHeadersPage(ctx, stateAddress(8), nil, 10)
	require.Error(t, err)
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, exported.EVMValidatorHeaders, 2)
}
func TestValidatorHeaderAtomicFirstBindingAndGenesisFailure(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain)
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	g := bindingGenesisState(t, false)
	require.NoError(t, k.InitGenesis(ctx, g))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	complete := headerGenesisFixture(t)
	changes := []ValidatorHeaderChange{{Next: &complete.EVMValidatorHeaders[0]}, {Next: &complete.EVMValidatorHeaders[1]}}
	bad, err := NewStateKeeperWithDomain(failingService{service: service, prefix: validatorHeaderEligiblePrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, bad.ApplyValidatorHeaderBatch(ctx, changes), "injected write failure")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	bounded, err := NewStateKeeperWithDomain(noDelegationScanService{service}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.NoError(t, bounded.ApplyValidatorHeaderBatch(ctx, changes), "hot header batch must not scan native ledger")
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, exported.EVMValidatorHeaders, 2)
	freshCtx, freshService, _ := environment(t)
	freshCtx = freshCtx.WithChainID(bindingTestChain)
	broken, err := NewStateKeeperWithDomain(failingService{service: freshService, prefix: validatorHeaderEligiblePrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.InitGenesis(freshCtx, complete), "injected write failure")
	marker, err := freshService.OpenKVStore(freshCtx).Get([]byte{bindingDomainMarker})
	require.NoError(t, err)
	require.Nil(t, marker)
}
func TestValidatorHeaderGenesisCoverageAndOrphanIntegrity(t *testing.T) {
	complete := headerGenesisFixture(t)
	incomplete := *complete
	incomplete.EVMValidatorHeaders = incomplete.EVMValidatorHeaders[:1]
	require.Error(t, incomplete.Validate())
	mixed := *complete
	mixed.EVMValidatorHeaders = append([]types.EVMValidatorHeader(nil), complete.EVMValidatorHeaders...)
	mixed.EVMValidatorHeaders[1] = copyHeader(t, mixed.EVMValidatorHeaders[1])
	mixed.EVMValidatorHeaders[1].Master = stateAddress(8)
	require.Error(t, mixed.Validate())
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain)
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	require.NoError(t, k.InitGenesis(ctx, complete))
	orphan := append([]byte{validatorHeaderOperatorPrefix}, stateAddress(99)...)
	require.NoError(t, service.OpenKVStore(ctx).Set(orphan, []byte{1}))
	_, err = k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "orphan")
	require.NoError(t, service.OpenKVStore(ctx).Delete(orphan))
	row := complete.EVMValidatorHeaders[0]
	key, err := validatorHeaderKey(row.Master, row.Validator)
	require.NoError(t, err)
	require.NoError(t, service.OpenKVStore(ctx).Set(validatorHeaderEligibleKey(&row), []byte{1}))
	_, err = k.GetEVMValidatorHeaderByOperator(ctx, row.NativeOperator)
	require.Error(t, err)
	require.NoError(t, service.OpenKVStore(ctx).Set(validatorHeaderEligibleKey(&row), key))
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
}
func TestValidatorHeaderSurvivesRealIAVLLevelDBRestart(t *testing.T) {
	directory := t.TempDir()
	open := func() (sdk.Context, multistore.CommitMultiStore, dbm.DB, *StateKeeper) {
		db, err := dbm.NewDB("validator-header", dbm.GoLevelDBBackend, directory)
		require.NoError(t, err)
		cms := multistore.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
		key := storetypes.NewKVStoreKey("native-validator")
		cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, db)
		require.NoError(t, cms.LoadLatestVersion())
		ctx := sdk.NewContext(cms, cmtproto.Header{ChainID: bindingTestChain, Height: 10}, false, log.NewNopLogger())
		k, err := NewStateKeeperWithDomain(runtime.NewKVStoreService(key), bindingTestChain, 990001)
		require.NoError(t, err)
		return ctx, cms, db, k
	}
	ctx, cms, db, k := open()
	require.NoError(t, k.InitGenesis(ctx, headerGenesisFixture(t)))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	cms.Commit()
	require.NoError(t, db.Close())
	ctx, cms, db, k = open()
	defer func() { require.NoError(t, db.Close()) }()
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	page, _, err := k.GetSourceEligibleValidatorHeadersPage(ctx, stateAddress(9), nil, 2)
	require.NoError(t, err)
	require.Len(t, page, 1)
	cms.Commit()
}
