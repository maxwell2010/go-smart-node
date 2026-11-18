package client

import (
	"bytes"
	"context"
	corestore "cosmossdk.io/core/store"
	"cosmossdk.io/log"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/crypto/tmhash"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmtversion "github.com/cometbft/cometbft/proto/tendermint/version"
	"github.com/cometbft/cometbft/types"
	"github.com/cometbft/cometbft/version"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/bridge/finality"
	"github.com/maxwell2010/go-smart-node/network/bridge/workgas"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

// Disposable canonical signatures are test-only and are never operator keys.
type testKeys map[string]ed25519.PrivKey

func testValidators(t *testing.T, names []string, powers []int64) (*types.ValidatorSet, testKeys) {
	t.Helper()
	keys := make(testKeys)
	validators := make([]*types.Validator, len(names))
	for index, name := range names {
		private := ed25519.GenPrivKeyFromSecret([]byte("disposable-light-client-test/" + name))
		public := private.PubKey()
		keys[string(public.Address())] = private
		validators[index] = types.NewValidator(public, powers[index])
	}
	return types.NewValidatorSet(validators), keys
}

func testBlockID(text string) types.BlockID {
	return types.BlockID{Hash: tmhash.Sum([]byte(text)),
		PartSetHeader: types.PartSetHeader{Total: 1, Hash: tmhash.Sum([]byte("parts/" + text))}}
}

func signTestBlock(t *testing.T, block *types.LightBlock, keys testKeys, chainID string,
	absent, nilVotes map[int]bool) {
	t.Helper()
	block.Commit = &types.Commit{Height: block.Height, Round: 2,
		BlockID: types.BlockID{Hash: block.Header.Hash(),
			PartSetHeader: types.PartSetHeader{Total: 1, Hash: tmhash.Sum([]byte(fmt.Sprint("parts/", block.Height)))}}}
	for index, validator := range block.ValidatorSet.Validators {
		if absent[index] {
			block.Commit.Signatures = append(block.Commit.Signatures, types.NewCommitSigAbsent())
			continue
		}
		flag := types.BlockIDFlagCommit
		blockID := block.Commit.BlockID
		if nilVotes[index] {
			flag, blockID = types.BlockIDFlagNil, types.BlockID{}
		}
		stamp := block.Time.Add(time.Duration(index) * time.Millisecond)
		vote := cmtproto.Vote{Type: cmtproto.PrecommitType, Height: block.Height,
			Round: block.Commit.Round, BlockID: blockID.ToProto(), Timestamp: stamp}
		private := keys[string(validator.Address)]
		signature, err := private.Sign(types.VoteSignBytes(chainID, &vote))
		if err != nil {
			t.Fatal(err)
		}
		block.Commit.Signatures = append(block.Commit.Signatures, types.CommitSig{
			BlockIDFlag: flag, ValidatorAddress: validator.Address, Timestamp: stamp, Signature: signature})
	}
}

func testBlock(t *testing.T, height int64, stamp time.Time, vals, next *types.ValidatorSet,
	keys testKeys, previous types.BlockID) *types.LightBlock {
	t.Helper()
	block := &types.LightBlock{SignedHeader: &types.SignedHeader{Header: &types.Header{
		Version: cmtversion.Consensus{Block: version.BlockProtocol}, ChainID: "bridge_fixture-1",
		Height: height, Time: stamp, LastBlockID: previous,
		LastCommitHash: tmhash.Sum([]byte("last-commit")), DataHash: tmhash.Sum([]byte("data")),
		ValidatorsHash: vals.Hash(), NextValidatorsHash: next.Hash(), ConsensusHash: tmhash.Sum([]byte("params")),
		AppHash:         tmhash.Sum([]byte(fmt.Sprint("application-state/", height))),
		LastResultsHash: tmhash.Sum([]byte("results")), EvidenceHash: tmhash.Sum([]byte("evidence")),
		ProposerAddress: vals.Proposer.Address,
	}}, ValidatorSet: vals}
	signTestBlock(t, block, keys, block.ChainID, nil, nil)
	return block
}

func testFixture(t *testing.T) (finality.Checkpoint, *types.LightBlock, *types.LightBlock, time.Time, finality.Policy, testKeys) {
	t.Helper()
	vals, keys := testValidators(t, []string{"a", "b", "c", "d"}, []int64{1, 1, 1, 1})
	base := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	trusted := testBlock(t, 10, base, vals, vals, keys, testBlockID("block9"))
	candidateVals, _ := testValidators(t, []string{"a", "b", "c", "d"}, []int64{1, 1, 1, 1})
	candidate := testBlock(t, 11, base.Add(time.Minute), candidateVals, candidateVals, keys, trusted.Commit.BlockID)
	anchor := finality.Checkpoint{ChainID: trusted.ChainID, Height: trusted.Height}
	copy(anchor.Hash[:], trusted.Hash())
	policy := finality.Policy{ChainID: trusted.ChainID, TrustingPeriod: time.Hour,
		UnbondingPeriod: 7 * 24 * time.Hour, MaxClockDrift: 30 * time.Second, MaxValidators: 64}
	return anchor, trusted, candidate, base.Add(5 * time.Minute), policy, keys
}

func clientEnvironment(t *testing.T) (sdk.Context, *Keeper, corestore.KVStoreService) {
	t.Helper()
	database := dbm.NewMemDB()
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	cms := store.NewCommitMultiStore(database, log.NewNopLogger(), metrics.NewNoOpMetrics())
	key := storetypes.NewKVStoreKey("bridge-client")
	cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, database)
	require.NoError(t, cms.LoadLatestVersion())
	service := runtime.NewKVStoreService(key)
	keeper, err := NewKeeper(service)
	require.NoError(t, err)
	_, _, _, now, _, _ := testFixture(t)
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1, Time: now}, false, log.NewNopLogger())
	return ctx, keeper, service
}

func storedBytes(t *testing.T, ctx sdk.Context, service corestore.KVStoreService, id string) []byte {
	t.Helper()
	key, err := clientKey(id)
	require.NoError(t, err)
	value, err := service.OpenKVStore(ctx).Get(key)
	require.NoError(t, err)
	return bytes.Clone(value)
}

func TestClientInitUpdateLatestOnlyAndDefensiveGetters(t *testing.T) {
	ctx, k, service := clientEnvironment(t)
	anchor, trusted, candidate, _, policy, _ := testFixture(t)
	require.NoError(t, k.InitClient(ctx, "decimal", anchor, trusted, policy))
	initial, err := k.LatestCheckpoint(ctx, "decimal")
	require.NoError(t, err)
	require.Equal(t, anchor, initial.Checkpoint)
	gotPolicy, err := k.ClientPolicy(ctx, "decimal")
	require.NoError(t, err)
	require.Equal(t, policy, gotPolicy)
	gotPolicy.ChainID = "forged-1"
	block, err := k.LatestLightBlock(ctx, "decimal")
	require.NoError(t, err)
	block.AppHash[0] ^= 1
	again, err := k.LatestCheckpoint(ctx, "decimal")
	require.NoError(t, err)
	require.Equal(t, initial, again)
	before := storedBytes(t, ctx, service, "decimal")
	verified, err := k.Update(ctx, "decimal", candidate)
	require.NoError(t, err)
	require.Equal(t, int64(11), verified.Height)
	latest, err := k.LatestCheckpoint(ctx, "decimal")
	require.NoError(t, err)
	require.Equal(t, verified, latest)
	require.NotEqual(t, before, storedBytes(t, ctx, service, "decimal"))
	stored, _, err := k.load(ctx, "decimal")
	require.NoError(t, err)
	require.Equal(t, anchor, stored.BootstrapAnchor)
	require.Equal(t, policy, stored.Policy, "source policy must be immutable through updates")
	_, err = k.Update(ctx, "decimal", candidate)
	require.Error(t, err, "same-height replay cannot replace current root")
	_, err = k.Update(ctx, "decimal", trusted)
	require.Error(t, err, "older root cannot be reinstated")
	latest, err = k.LatestCheckpoint(ctx, "decimal")
	require.NoError(t, err)
	require.Equal(t, verified, latest)
}

func TestInvalidUpdateAndBootstrapLeaveStateUnchanged(t *testing.T) {
	ctx, k, service := clientEnvironment(t)
	anchor, trusted, candidate, _, policy, keys := testFixture(t)
	forged := anchor
	forged.Hash[0] ^= 1
	require.Error(t, k.InitClient(ctx, "decimal", forged, trusted, policy))
	require.Nil(t, storedBytes(t, ctx, service, "decimal"))
	require.NoError(t, k.InitClient(ctx, "decimal", anchor, trusted, policy))
	before := storedBytes(t, ctx, service, "decimal")
	candidate.Commit.Signatures[3].Signature[0] ^= 1
	got, err := k.Update(ctx, "decimal", candidate)
	require.Error(t, err)
	require.Equal(t, finality.VerifiedCheckpoint{}, got)
	require.Equal(t, before, storedBytes(t, ctx, service, "decimal"))
	require.ErrorIs(t, k.InitClient(ctx, "decimal", anchor, trusted, policy), ErrAlreadyInitialized)
	require.Equal(t, before, storedBytes(t, ctx, service, "decimal"))
	signTestBlock(t, candidate, keys, policy.ChainID, nil, nil)
	_, err = k.Update(ctx, "decimal", candidate)
	require.NoError(t, err, "valid retry survives failed signature")
}

func TestClientExpiryAlwaysUsesSDKBlockTime(t *testing.T) {
	ctx, k, service := clientEnvironment(t)
	anchor, trusted, candidate, _, policy, _ := testFixture(t)
	require.NoError(t, k.InitClient(ctx, "decimal", anchor, trusted, policy))
	before := storedBytes(t, ctx, service, "decimal")
	expired := ctx.WithBlockTime(trusted.Time.Add(policy.TrustingPeriod))
	_, err := k.LatestCheckpoint(expired, "decimal")
	require.Error(t, err)
	_, err = k.LatestLightBlock(expired, "decimal")
	require.Error(t, err)
	_, err = k.ClientPolicy(expired, "decimal")
	require.Error(t, err)
	got, err := k.Update(expired, "decimal", candidate)
	require.Error(t, err)
	require.Equal(t, finality.VerifiedCheckpoint{}, got)
	require.Equal(t, before, storedBytes(t, ctx, service, "decimal"))
	require.Error(t, k.InitClient(expired, "expired", anchor, trusted, policy))
	require.Nil(t, storedBytes(t, ctx, service, "expired"))
}

func TestClientOuterCacheRollbackAndCommit(t *testing.T) {
	ctx, k, service := clientEnvironment(t)
	anchor, trusted, candidate, _, policy, _ := testFixture(t)
	outer, _ := ctx.CacheContext()
	require.NoError(t, k.InitClient(outer, "decimal", anchor, trusted, policy))
	_, err := k.LatestCheckpoint(ctx, "decimal")
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, k.InitClient(ctx, "decimal", anchor, trusted, policy))
	before := storedBytes(t, ctx, service, "decimal")
	outer, _ = ctx.CacheContext()
	_, err = k.Update(outer, "decimal", candidate)
	require.NoError(t, err)
	observed, err := k.LatestCheckpoint(outer, "decimal")
	require.NoError(t, err)
	require.Equal(t, int64(11), observed.Height)
	require.Equal(t, before, storedBytes(t, ctx, service, "decimal"), "discarded outer cache must discard update")
	outer, commit := ctx.CacheContext()
	_, err = k.Update(outer, "decimal", candidate)
	require.NoError(t, err)
	commit()
	observed, err = k.LatestCheckpoint(ctx, "decimal")
	require.NoError(t, err)
	require.Equal(t, int64(11), observed.Height)
}

type failingService struct{ corestore.KVStoreService }
type failingStore struct{ corestore.KVStore }

func (s failingService) OpenKVStore(ctx context.Context) corestore.KVStore {
	return failingStore{s.KVStoreService.OpenKVStore(ctx)}
}
func (s failingStore) Set(key, value []byte) error {
	if err := s.KVStore.Set(key, value); err != nil {
		return err
	}
	return errors.New("injected write failure after child cache write")
}

func TestStoreFailureDiscardsChildWrite(t *testing.T) {
	ctx, k, service := clientEnvironment(t)
	anchor, trusted, candidate, _, policy, _ := testFixture(t)
	broken, err := NewKeeper(failingService{service})
	require.NoError(t, err)
	require.ErrorContains(t, broken.InitClient(ctx, "decimal", anchor, trusted, policy), "injected write failure")
	require.Nil(t, storedBytes(t, ctx, service, "decimal"))
	require.NoError(t, k.InitClient(ctx, "decimal", anchor, trusted, policy))
	before := storedBytes(t, ctx, service, "decimal")
	_, err = broken.Update(ctx, "decimal", candidate)
	require.ErrorContains(t, err, "injected write failure")
	require.Equal(t, before, storedBytes(t, ctx, service, "decimal"))
}

func TestClientCorruptStoreCannotBeReadUpdatedOrReinitialized(t *testing.T) {
	for _, mode := range []string{"json", "unknown_field", "duplicate_field", "version", "checkpoint", "policy", "protobuf", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			ctx, k, service := clientEnvironment(t)
			anchor, trusted, candidate, _, policy, _ := testFixture(t)
			require.NoError(t, k.InitClient(ctx, "decimal", anchor, trusted, policy))
			raw := storedBytes(t, ctx, service, "decimal")
			var record persistedRecord
			require.NoError(t, json.Unmarshal(raw, &record))
			switch mode {
			case "json":
				raw = []byte("broken")
			case "unknown_field":
				raw = append(raw[:len(raw)-1], []byte(",\"unknown\":1}")...)
			case "duplicate_field":
				raw = append(raw[:len(raw)-1], []byte(",\"version\":1}")...)
			case "version":
				record.Version = 2
			case "checkpoint":
				record.Latest.AppHash[0] ^= 1
			case "policy":
				record.Policy.TrustingPeriod = 0
			case "protobuf":
				record.LightBlock = []byte{0xff}
			case "oversized":
				raw = make([]byte, maxRecordBytes+1)
			}
			if mode == "version" || mode == "checkpoint" || mode == "policy" || mode == "protobuf" {
				var err error
				raw, err = encodeRecord(record)
				require.NoError(t, err)
			}
			key, err := clientKey("decimal")
			require.NoError(t, err)
			require.NoError(t, service.OpenKVStore(ctx).Set(key, raw))
			_, err = k.LatestCheckpoint(ctx, "decimal")
			require.ErrorIs(t, err, ErrCorruptStore)
			_, err = k.Update(ctx, "decimal", candidate)
			require.ErrorIs(t, err, ErrCorruptStore)
			require.ErrorIs(t, k.InitClient(ctx, "decimal", anchor, trusted, policy), ErrAlreadyInitialized)
			require.Equal(t, raw, storedBytes(t, ctx, service, "decimal"))
		})
	}
}

func TestClientIdentifiersAndMissingState(t *testing.T) {
	ctx, k, _ := clientEnvironment(t)
	anchor, trusted, candidate, _, policy, _ := testFixture(t)
	for _, id := range []string{"", "UPPER", "a/b", "a\x00b", strings.Repeat("a", 65)} {
		require.ErrorIs(t, k.InitClient(ctx, id, anchor, trusted, policy), ErrInvalidClientID)
		_, err := k.Update(ctx, id, candidate)
		require.ErrorIs(t, err, ErrInvalidClientID)
		_, err = k.LatestCheckpoint(ctx, id)
		require.ErrorIs(t, err, ErrInvalidClientID)
	}
	_, err := k.LatestCheckpoint(ctx, "missing")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = k.Update(ctx, "missing", candidate)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = NewKeeper(nil)
	require.Error(t, err)
}

func TestClientLevelDBCommitAndReopen(t *testing.T) {
	directory := t.TempDir()
	_, _, _, now, _, _ := testFixture(t)
	open := func() (sdk.Context, store.CommitMultiStore, dbm.DB, *Keeper) {
		database, err := dbm.NewDB("bridge-client", dbm.GoLevelDBBackend, directory)
		require.NoError(t, err)
		cms := store.NewCommitMultiStore(database, log.NewNopLogger(), metrics.NewNoOpMetrics())
		key := storetypes.NewKVStoreKey("bridge-client")
		cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, database)
		require.NoError(t, cms.LoadLatestVersion())
		keeper, err := NewKeeper(runtime.NewKVStoreService(key))
		require.NoError(t, err)
		return sdk.NewContext(cms, cmtproto.Header{Time: now}, false, log.NewNopLogger()), cms, database, keeper
	}
	anchor, trusted, candidate, _, policy, _ := testFixture(t)
	ctx, cms, db, k := open()
	require.NoError(t, k.InitClient(ctx, "decimal", anchor, trusted, policy))
	cms.Commit()
	require.NoError(t, db.Close())
	ctx, cms, db, k = open()
	cp, err := k.LatestCheckpoint(ctx, "decimal")
	require.NoError(t, err)
	require.Equal(t, anchor, cp.Checkpoint)
	cp, err = k.Update(ctx, "decimal", candidate)
	require.NoError(t, err)
	cms.Commit()
	require.NoError(t, db.Close())
	ctx, _, db, k = open()
	defer func() { require.NoError(t, db.Close()) }()
	after, err := k.LatestCheckpoint(ctx, "decimal")
	require.NoError(t, err)
	require.Equal(t, cp, after)
	require.Equal(t, int64(11), after.Height)
}

func TestFiniteGasChargesBootstrapUpdateAndEveryGetter(t *testing.T) {
	ctx, k, service := clientEnvironment(t)
	anchor, trusted, candidate, _, policy, _ := testFixture(t)
	limited := ctx.WithGasMeter(storetypes.NewGasMeter(20_000_000))
	priced := storetypes.NewInfiniteGasMeter()
	require.NoError(t, workgas.ChargeBootstrap(priced, trusted))
	require.NoError(t, k.InitClient(limited, "decimal", anchor, trusted, policy))
	require.GreaterOrEqual(t, limited.GasMeter().GasConsumed(), priced.GasConsumed())
	stored := storedBytes(t, ctx, service, "decimal")
	priced = storetypes.NewInfiniteGasMeter()
	require.NoError(t, workgas.ChargeDecode(priced, len(stored)))
	require.NoError(t, workgas.ChargeBootstrap(priced, trusted)) // load integrity
	require.NoError(t, workgas.ChargeBootstrap(priced, trusted)) // current-time check
	for _, name := range []string{"checkpoint", "block", "policy"} {
		getter := ctx.WithGasMeter(storetypes.NewGasMeter(20_000_000))
		switch name {
		case "checkpoint":
			_, err := k.LatestCheckpoint(getter, "decimal")
			require.NoError(t, err)
		case "block":
			_, err := k.LatestLightBlock(getter, "decimal")
			require.NoError(t, err)
		case "policy":
			_, err := k.ClientPolicy(getter, "decimal")
			require.NoError(t, err)
		}
		require.GreaterOrEqual(t, getter.GasMeter().GasConsumed(), priced.GasConsumed(), name)
	}
	priced = storetypes.NewInfiniteGasMeter()
	require.NoError(t, workgas.ChargeUpdateInput(priced, candidate))
	require.NoError(t, workgas.ChargeDecode(priced, len(stored)))
	require.NoError(t, workgas.ChargeBootstrap(priced, trusted))
	require.NoError(t, workgas.ChargeUpdate(priced, trusted, candidate))
	limited = ctx.WithGasMeter(storetypes.NewGasMeter(20_000_000))
	_, err := k.Update(limited, "decimal", candidate)
	require.NoError(t, err)
	require.GreaterOrEqual(t, limited.GasMeter().GasConsumed(), priced.GasConsumed())
}

func recoverOutOfGas(t *testing.T, fn func()) {
	t.Helper()
	var recovered any
	func() { defer func() { recovered = recover() }(); fn() }()
	require.IsType(t, storetypes.ErrorOutOfGas{}, recovered, "SDK out-of-gas must reach the transaction boundary")
}

func TestOutOfGasCannotInitializeOrAdvanceClientAndGasIsNotRefunded(t *testing.T) {
	ctx, k, service := clientEnvironment(t)
	anchor, trusted, candidate, _, policy, _ := testFixture(t)
	priced := storetypes.NewInfiniteGasMeter()
	require.NoError(t, workgas.ChargeBootstrap(priced, trusted))
	require.Positive(t, priced.GasConsumed())
	limited := ctx.WithGasMeter(storetypes.NewGasMeter(priced.GasConsumed() - 1))
	recoverOutOfGas(t, func() { _ = k.InitClient(limited, "decimal", anchor, trusted, policy) })
	require.Greater(t, limited.GasMeter().GasConsumed(), limited.GasMeter().Limit())
	require.Nil(t, storedBytes(t, ctx, service, "decimal"))
	require.NoError(t, k.InitClient(ctx, "decimal", anchor, trusted, policy))
	before := storedBytes(t, ctx, service, "decimal")
	// Measure the real pre-verification path including SDK store gas. Allow
	// that work, but leave one unit less than the explicit update crypto price.
	probe := ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	require.NoError(t, workgas.ChargeUpdateInput(probe.GasMeter(), candidate))
	_, _, err := k.load(probe, "decimal")
	require.NoError(t, err)
	priced = storetypes.NewInfiniteGasMeter()
	require.NoError(t, workgas.ChargeUpdate(priced, trusted, candidate))
	limit := probe.GasMeter().GasConsumed() + priced.GasConsumed() - 1
	limited = ctx.WithGasMeter(storetypes.NewGasMeter(limit))
	recoverOutOfGas(t, func() { _, _ = k.Update(limited, "decimal", candidate) })
	require.Greater(t, limited.GasMeter().GasConsumed(), limit)
	require.Equal(t, before, storedBytes(t, ctx, service, "decimal"))
	_, err = k.Update(ctx, "decimal", candidate)
	require.NoError(t, err, "fresh meter can retry the same candidate")
}

func TestFailingAndCorruptGettersConsumeWorkGasWithoutChangingState(t *testing.T) {
	ctx, k, service := clientEnvironment(t)
	anchor, trusted, _, _, policy, _ := testFixture(t)
	require.NoError(t, k.InitClient(ctx, "decimal", anchor, trusted, policy))
	before := storedBytes(t, ctx, service, "decimal")
	priced := storetypes.NewInfiniteGasMeter()
	require.NoError(t, workgas.ChargeDecode(priced, len(before)))
	require.NoError(t, workgas.ChargeBootstrap(priced, trusted))
	require.NoError(t, workgas.ChargeBootstrap(priced, trusted))
	expired := ctx.WithBlockTime(trusted.Time.Add(policy.TrustingPeriod)).WithGasMeter(storetypes.NewGasMeter(20_000_000))
	_, err := k.LatestCheckpoint(expired, "decimal")
	require.Error(t, err)
	require.GreaterOrEqual(t, expired.GasMeter().GasConsumed(), priced.GasConsumed())
	require.Equal(t, before, storedBytes(t, ctx, service, "decimal"))
	key, err := clientKey("decimal")
	require.NoError(t, err)
	require.NoError(t, service.OpenKVStore(ctx).Set(key, []byte("broken")))
	priced = storetypes.NewInfiniteGasMeter()
	require.NoError(t, workgas.ChargeDecode(priced, len("broken")))
	corrupt := ctx.WithGasMeter(storetypes.NewGasMeter(20_000_000))
	_, err = k.LatestCheckpoint(corrupt, "decimal")
	require.ErrorIs(t, err, ErrCorruptStore)
	require.GreaterOrEqual(t, corrupt.GasMeter().GasConsumed(), priced.GasConsumed())
	require.Equal(t, []byte("broken"), storedBytes(t, ctx, service, "decimal"))
}

type readTrackingService struct {
	corestore.KVStoreService
	reads *int
}
type readTrackingStore struct {
	corestore.KVStore
	reads *int
}

func (s readTrackingService) OpenKVStore(ctx context.Context) corestore.KVStore {
	return readTrackingStore{s.KVStoreService.OpenKVStore(ctx), s.reads}
}
func (s readTrackingStore) Get(key []byte) ([]byte, error) {
	*s.reads++
	return s.KVStore.Get(key)
}

func TestMalformedTypedCandidateChargedAndRejectedBeforeStoredState(t *testing.T) {
	ctx, _, service := clientEnvironment(t)
	reads := 0
	k, err := NewKeeper(readTrackingService{service, &reads})
	require.NoError(t, err)
	for _, candidate := range []*types.LightBlock{nil, {}, {SignedHeader: &types.SignedHeader{}}} {
		limited := ctx.WithGasMeter(storetypes.NewGasMeter(20_000_000))
		var got finality.VerifiedCheckpoint
		require.NotPanics(t, func() { got, err = k.Update(limited, "decimal", candidate) })
		require.Error(t, err)
		require.Equal(t, finality.VerifiedCheckpoint{}, got)
		require.Zero(t, reads, "candidate shape must be checked before loading trusted state")
		require.Positive(t, limited.GasMeter().GasConsumed())
	}
}
