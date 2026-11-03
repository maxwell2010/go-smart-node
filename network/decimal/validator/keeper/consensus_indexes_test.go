package keeper

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

type pointRegistryService struct{ store.KVStoreService }

func (s pointRegistryService) OpenKVStore(ctx context.Context) store.KVStore {
	return pointRegistryStore{s.KVStoreService.OpenKVStore(ctx)}
}

type pointRegistryStore struct{ store.KVStore }

func (s pointRegistryStore) Iterator(start, end []byte) (store.Iterator, error) {
	if len(start) == 0 || start[0] == validatorPrefix || start[0] == powerPrefix {
		return nil, errors.New("hot operation attempted complete candidate or raw power registry scan")
	}
	return s.KVStore.Iterator(start, end)
}

func inactiveValidator(t *testing.T, id uint64) types.Validator {
	t.Helper()
	raw := make([]byte, 20)
	binary.BigEndian.PutUint64(raw[12:], id+1000)
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(raw)
	require.NoError(t, err)
	reward, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(raw)
	require.NoError(t, err)
	v, err := types.NewValidator(operator, reward, ed25519.GenPrivKeyFromSecret(raw).PubKey(), types.Description{Moniker: "inactive fixture"}, sdkmath.LegacyZeroDec())
	require.NoError(t, err)
	return v
}

func TestConsensusHotPathUsesOnlyEligiblePreviousAndBondedUnion(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	for i := uint64(1); i <= 2048; i++ {
		v := inactiveValidator(t, i)
		g.Validators = append(g.Validators, v)
		g.LastValidatorPowers = append(g.LastValidatorPowers, types.LastValidatorPower{Address: v.OperatorAddress, Power: 0})
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	cold, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, cold.LastValidatorPowers, 2, "cold initialization explicitly normalizes zero power rows")
	require.EqualValues(t, g.LastTotalPower, cold.LastTotalPower)
	require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators[:2]))
	counts := new(consensusReadCounts)
	hot, err := NewStateKeeper(pointRegistryService{consensusGuardService{service, counts}})
	require.NoError(t, err)
	inactive := g.Validators[len(g.Validators)-1]
	inactive.Description.Moniker = "point update"
	require.NoError(t, hot.SetValidator(ctx, inactive))
	first := g.Validators[0]
	first.Stake, first.Status = 150, types.BondStatus_Unbonded
	require.NoError(t, hot.SetValidator(ctx, first))
	// Previously emitted power stays100 despite current RS150 and Unbonded.
	previous, err := hot.previousPositivePowers(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 100, previous[first.OperatorAddress])
	g.Params.MaxValidators = 1
	require.NoError(t, k.SetParams(ctx, g.Params)) // Cold parameter validation.
	ctx = transitionContext(t, ctx, 1, g.Validators[:2])
	updates, err := hot.ScheduledEndBlock(ctx, []string{first.OperatorAddress, g.Validators[1].OperatorAddress})
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.Zero(t, updates[0].Power)
	final, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, final.LastValidatorPowers, 1)
	require.Equal(t, g.Validators[1].OperatorAddress, final.LastValidatorPowers[0].Address)
	require.EqualValues(t, 2, final.ScheduledValidatorSets[0].EffectiveHeight)
	require.Len(t, final.ScheduledValidatorSets[0].Validators, 2, "H+1 preserves the previous larger active set")
	require.Len(t, final.ScheduledValidatorSets[1].Validators, 1, "new maximum only affects H+2")
}

func clearTestPrefix(t *testing.T, ctx sdk.Context, k *StateKeeper, prefix byte) {
	t.Helper()
	var keys [][]byte
	require.NoError(t, k.scan(ctx, prefix, func(key, _ []byte) error { keys = append(keys, key); return nil }))
	for _, key := range keys {
		require.NoError(t, k.service.OpenKVStore(ctx).Delete(key))
	}
}

func TestColdConsensusNormalizationRebuildAndAuditAreExplicit(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	zero := inactiveValidator(t, 5000)
	g.Validators = append(g.Validators, zero)
	require.NoError(t, k.InitGenesis(ctx, g))
	// Recreate the old development layout with literal zero power and no
	// derived membership indexes. No hot path may silently repair it.
	key, _ := operatorKey(powerPrefix, zero.OperatorAddress)
	require.NoError(t, k.put(ctx, key, &types.LastValidatorPower{Address: zero.OperatorAddress, Power: 0}))
	require.NoError(t, service.OpenKVStore(ctx).Delete([]byte{bondedTotalsKey}))
	clearTestPrefix(t, ctx, k, bondedIndexPrefix)
	clearTestPrefix(t, ctx, k, positivePowerPrefix)
	_, err := k.ExportGenesis(ctx)
	require.Error(t, err)
	before := nativeStoreSnapshot(t, service, ctx)
	bad, err := NewStateKeeper(failingService{service: service, prefix: positivePowerPrefix})
	require.NoError(t, err)
	require.ErrorContains(t, bad.RebuildConsensusIndexes(ctx), "injected write failure")
	require.Equal(t, before, nativeStoreSnapshot(t, service, ctx))
	require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators[:2]))
	canonical, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, canonical.LastValidatorPowers, 2)
	require.EqualValues(t, 300, canonical.LastTotalPower)
	fresh, _, imported := environment(t)
	require.NoError(t, imported.InitGenesis(fresh, canonical))
	again, err := imported.ExportGenesis(fresh)
	require.NoError(t, err)
	assertGenesisBytes(t, canonical, again)
	// The full cold export detects a missing derived member and an explicit
	// rebuild repairs only indexes after validating the complete source ledger.
	key, _ = operatorKey(bondedIndexPrefix, g.Validators[0].OperatorAddress)
	require.NoError(t, service.OpenKVStore(ctx).Delete(key))
	_, err = k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "incomplete")
	require.NoError(t, k.RebuildConsensusIndexes(ctx))
	again, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, canonical, again)
}

func TestConsensusMembershipWritesAndPreviousPowersRollBackTogether(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators))
	before := nativeStoreSnapshot(t, service, ctx)
	bad, err := NewStateKeeper(failingService{service: service, prefix: bondedTotalsKey})
	require.NoError(t, err)
	v := g.Validators[0]
	v.Stake = 150
	require.ErrorContains(t, bad.SetValidator(ctx, v), "injected write failure")
	require.Equal(t, before, nativeStoreSnapshot(t, service, ctx))
	bad, err = NewStateKeeper(failingService{service: service, prefix: positivePowerPrefix})
	require.NoError(t, err)
	require.ErrorContains(t, bad.SetLastValidatorPowers(ctx, g.LastValidatorPowers), "injected write failure")
	require.Equal(t, before, nativeStoreSnapshot(t, service, ctx))
	_, err = bad.ScheduledEndBlock(transitionContext(t, ctx, 1, g.Validators), []string{g.Validators[0].OperatorAddress})
	require.ErrorContains(t, err, "injected write failure")
	require.Equal(t, before, nativeStoreSnapshot(t, service, ctx))
	// Duplicate zero rows are rejected before normalization, not silently merged.
	require.ErrorContains(t, k.SetLastValidatorPowers(ctx, []types.LastValidatorPower{{Address: v.OperatorAddress, Power: 0}, {Address: v.OperatorAddress, Power: 0}}), "duplicate")
	require.Equal(t, before, nativeStoreSnapshot(t, service, ctx))
}

func TestConsensusIndexesPreserveAcceptedOperatorText(t *testing.T) {
	ctx, _, k := environment(t)
	v := validator(t, 1, 100)
	v.OperatorAddress = strings.ToUpper(v.OperatorAddress)
	g := types.DefaultGenesisState()
	g.Validators = []types.Validator{v}
	g.LastValidatorPowers = []types.LastValidatorPower{{Address: v.OperatorAddress, Power: 100}}
	g.LastTotalPower = 100
	require.NoError(t, g.Validate(), "existing SDK address rules accept uppercase Bech32")
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.BootstrapConsensusSchedule(ctx, 1, g.Validators))
	v.Stake = 150
	require.NoError(t, k.SetValidator(ctx, v))
	updates, err := k.ScheduledEndBlock(transitionContext(t, ctx, 1, g.Validators), []string{v.OperatorAddress})
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.EqualValues(t, 150, updates[0].Power)
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, v.OperatorAddress, exported.Validators[0].OperatorAddress)
	require.Equal(t, v.OperatorAddress, exported.LastValidatorPowers[0].Address)
}
