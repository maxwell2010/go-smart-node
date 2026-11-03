package keeper

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"cosmossdk.io/core/store"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

type consensusReadCounts struct{ scheduleValues, staleKeys, reverseKeys int }
type consensusGuardService struct {
	store.KVStoreService
	counts *consensusReadCounts
}

func (s consensusGuardService) OpenKVStore(ctx context.Context) store.KVStore {
	return consensusGuardStore{s.KVStoreService.OpenKVStore(ctx), s.counts}
}

type consensusGuardStore struct {
	store.KVStore
	counts *consensusReadCounts
}

func forbiddenFinancialPrefix(prefix byte) bool {
	return prefix >= delegationPrefix && prefix <= delegationCountPrefix || prefix >= stakeBindingPrefix && prefix <= activePenaltyBindingPrefix
}

func (s consensusGuardStore) Get(key []byte) ([]byte, error) {
	if len(key) > 0 && forbiddenFinancialPrefix(key[0]) {
		return nil, errors.New("consensus hot path attempted unrelated financial read")
	}
	return s.KVStore.Get(key)
}

func (s consensusGuardStore) Iterator(start, end []byte) (store.Iterator, error) {
	if len(start) == 0 || forbiddenFinancialPrefix(start[0]) {
		return nil, errors.New("consensus hot path attempted financial or whole-store scan")
	}
	if start[0] == historyPrefix && (len(end) != 9 || end[0] != historyPrefix) {
		return nil, errors.New("consensus hot path attempted retained-history full scan")
	}
	it, err := s.KVStore.Iterator(start, end)
	if err != nil {
		return nil, err
	}
	return consensusGuardIterator{Iterator: it, counts: s.counts, prefix: start[0]}, nil
}

func (s consensusGuardStore) ReverseIterator(start, end []byte) (store.Iterator, error) {
	if !bytes.Equal(start, []byte{historyPrefix}) || !bytes.Equal(end, []byte{historyPrefix + 1}) {
		return nil, errors.New("consensus hot path attempted unexpected reverse scan")
	}
	it, err := s.KVStore.ReverseIterator(start, end)
	if err != nil {
		return nil, err
	}
	return consensusGuardIterator{Iterator: it, counts: s.counts, prefix: historyPrefix, reverse: true}, nil
}

type consensusGuardIterator struct {
	store.Iterator
	counts  *consensusReadCounts
	prefix  byte
	reverse bool
}

func (it consensusGuardIterator) Key() []byte {
	if it.prefix == historyPrefix {
		if it.reverse {
			it.counts.reverseKeys++
		} else {
			it.counts.staleKeys++
		}
	}
	return it.Iterator.Key()
}
func (it consensusGuardIterator) Value() []byte {
	if it.prefix == schedulePrefix {
		it.counts.scheduleValues++
	}
	return it.Iterator.Value()
}
func (it consensusGuardIterator) Next() {
	if it.reverse {
		panic("history cursor bootstrap must never advance beyond newest key")
	}
	it.Iterator.Next()
}

func nativeStoreSnapshot(t *testing.T, service store.KVStoreService, ctx sdk.Context) map[string]string {
	t.Helper()
	it, err := service.OpenKVStore(ctx).Iterator(nil, nil)
	require.NoError(t, err)
	out := map[string]string{}
	for ; it.Valid(); it.Next() {
		out[string(it.Key())] = string(it.Value())
	}
	require.NoError(t, it.Close())
	return out
}

func TestScheduledConsensusAvoidsFinancialAndRetainedHistoryScans(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	g.Params.HistoricalEntries, g.Params.MaxDelegations = 32, 5000
	// Large unrelated financial state is initialized and validated cold. Normal
	// block execution must not even read these delegation/index records.
	for i := uint64(1); i <= 4096; i++ {
		raw := make([]byte, 20)
		binary.BigEndian.PutUint64(raw[12:], i+1000)
		delegator, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(raw)
		require.NoError(t, err)
		g.Delegations = append(g.Delegations, types.Delegation{Delegator: delegator, Validator: g.Validators[0].OperatorAddress, Stake: types.NewStakeCoin(sdk.NewInt64Coin("del", 1))})
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators))
	counts := new(consensusReadCounts)
	guarded, err := NewStateKeeper(consensusGuardService{service, counts})
	require.NoError(t, err)
	eligible := []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress}
	for height := int64(1); height <= 70; height++ {
		beforeStale, beforeRows := counts.staleKeys, counts.scheduleValues
		ctx = transitionContext(t, ctx, height, g.Validators)
		updates, err := guarded.ScheduledEndBlock(ctx, eligible)
		require.NoError(t, err)
		require.Empty(t, updates)
		require.Equal(t, 2, counts.scheduleValues-beforeRows)
		if height > int64(g.Params.HistoricalEntries) {
			require.Equal(t, 1, counts.staleKeys-beforeStale)
		} else {
			require.Equal(t, 0, counts.staleKeys-beforeStale)
		}
	}
	_, err = k.GetHistoricalInfo(ctx, 38)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = k.GetHistoricalInfo(ctx, 39)
	require.NoError(t, err)
	full, err := k.ExportGenesis(ctx)
	require.NoError(t, err, "cold export still validates all financial state")
	require.Len(t, full.Delegations, 4097)
	require.EqualValues(t, 71, full.ScheduledValidatorSets[0].EffectiveHeight)
}

func TestHistoryCursorBootstrapsByOneReverseSeekAndPrunesExactRange(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	g.Params.HistoricalEntries = 32
	require.NoError(t, k.InitGenesis(ctx, g))
	for height := int64(1); height <= 32; height++ {
		require.NoError(t, k.SetHistoricalInfo(ctx, history(t, height, g.Validators)))
	}
	// Simulate an existing development store predating the derived marker.
	require.NoError(t, service.OpenKVStore(ctx).Delete([]byte{historyProgressKey}))
	counts := new(consensusReadCounts)
	guarded, err := NewStateKeeper(consensusGuardService{service, counts})
	require.NoError(t, err)
	require.NoError(t, guarded.SetHistoricalInfo(ctx, history(t, 33, g.Validators)))
	require.Equal(t, 1, counts.reverseKeys)
	require.Equal(t, 1, counts.staleKeys)
	require.NoError(t, guarded.SetHistoricalInfo(ctx, history(t, 34, g.Validators)))
	require.Equal(t, 1, counts.reverseKeys, "persisted cursor must survive a fresh keeper instance")
	g.Params.HistoricalEntries = 8
	require.NoError(t, k.SetParams(ctx, g.Params)) // Cold parameter validation.
	before := counts.staleKeys
	require.NoError(t, guarded.SetHistoricalInfo(ctx, history(t, 35, g.Validators)))
	require.Equal(t, 25, counts.staleKeys-before, "retention reduction prunes the entire stale range")
	newest, pruned, err := k.historyProgress(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 35, newest)
	require.EqualValues(t, 27, pruned)
	require.ErrorContains(t, guarded.SetHistoricalInfo(ctx, history(t, 2, g.Validators)), "out of order")
	require.NoError(t, guarded.SetHistoricalInfo(ctx, history(t, 30, g.Validators)), "retained identical row remains idempotent")
	_, err = k.GetHistoricalInfo(ctx, 27)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = k.GetHistoricalInfo(ctx, 28)
	require.NoError(t, err)
}

func TestScheduledConsensusLateWriteAndGasFailuresRestoreEveryRow(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators))
	changed := g.Validators[0]
	changed.Stake = 150
	require.NoError(t, k.SetValidator(ctx, changed))
	ctx = transitionContext(t, ctx, 1, g.Validators)
	eligible := []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress}
	before := nativeStoreSnapshot(t, service, ctx)
	for _, prefix := range []byte{historyProgressKey, schedulePrefix} {
		bad, err := NewStateKeeper(failingService{service: service, prefix: prefix})
		require.NoError(t, err)
		_, err = bad.ScheduledEndBlock(ctx, eligible)
		require.ErrorContains(t, err, "injected write failure")
		require.Equal(t, before, nativeStoreSnapshot(t, service, ctx))
	}
	probe, _ := ctx.CacheContext()
	probe = probe.WithGasMeter(storetypes.NewInfiniteGasMeter())
	_, err := k.ScheduledEndBlock(probe, eligible)
	require.NoError(t, err)
	work := probe.GasMeter().GasConsumed()
	require.Greater(t, work, uint64(1))
	var recovered interface{}
	func() {
		defer func() { recovered = recover() }()
		_, _ = k.ScheduledEndBlock(ctx.WithGasMeter(storetypes.NewGasMeter(work-1)), eligible)
	}()
	require.IsType(t, storetypes.ErrorOutOfGas{}, recovered)
	require.Equal(t, before, nativeStoreSnapshot(t, service, ctx))
}

func TestScheduledConsensusRejectsThirdRowWithoutDecodingIt(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators))
	require.NoError(t, service.OpenKVStore(ctx).Set(scheduledKey(3), []byte("not even a protobuf row")))
	counts := new(consensusReadCounts)
	bounded, err := NewStateKeeper(consensusGuardService{service, counts})
	require.NoError(t, err)
	before := nativeStoreSnapshot(t, service, ctx)
	_, err = bounded.ScheduledEndBlock(transitionContext(t, ctx, 1, g.Validators), []string{g.Validators[0].OperatorAddress})
	require.ErrorContains(t, err, "exactly two")
	require.Equal(t, 2, counts.scheduleValues)
	require.Equal(t, before, nativeStoreSnapshot(t, service, ctx))
}
