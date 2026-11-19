package keeper

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestNativeFeeRewardIndexAdmits1000OwnersWithoutRescanning(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	operator := g.Validators[0].OperatorAddress
	g.Delegations = nil
	for i := 1; i <= 1000; i++ {
		raw := make([]byte, 20)
		binary.BigEndian.PutUint64(raw[12:], uint64(i+100))
		owner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(raw)
		require.NoError(t, err)
		g.Delegations = append(g.Delegations, types.Delegation{Delegator: owner, Validator: operator, Stake: types.NewStakeCoin(sdk.NewInt64Coin(identity.BaseDenom, 1))})
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.ValidateNativeFeeRewardAdmission(ctx, func(sdk.AccAddress) bool { return true }))
	count, err := k.feeRewardOwnerCount(ctx, operator)
	require.NoError(t, err)
	require.EqualValues(t, 1000, count)
	payouts, err := k.PlanNativeFeeRewards(ctx, sdkmath.NewInt(10001), []NativeFeeRewardVote{{operator, 1}})
	require.NoError(t, err)
	sum := sdkmath.ZeroInt()
	for _, p := range payouts {
		sum = sum.Add(p.Amount)
	}
	require.True(t, sum.Equal(sdkmath.NewInt(10001)))
	// Only dirty prefixes may be iterated on a normal admitted mutation.
	bounded, err := NewStateKeeper(feeRewardHotPathService{service})
	require.NoError(t, err)
	next := copyDelegation(t, g.Delegations[0])
	next.Stake.Stake.Amount = next.Stake.Stake.Amount.AddRaw(2)
	require.NoError(t, bounded.SetDelegation(ctx, next))
	checks := 0
	require.NoError(t, bounded.ValidateNativeFeeRewardChanges(ctx, func(sdk.AccAddress) bool { checks++; return true }))
	require.Equal(t, 2, checks, "one changed owner and one commission recipient")
	checks = 0
	require.NoError(t, bounded.ValidateNativeFeeRewardChanges(ctx, func(sdk.AccAddress) bool { checks++; return true }))
	require.Zero(t, checks)
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
	// A new row, rather than a new position ID for an existing row, exceeds
	// the existing per-validator Params.MaxDelegations=1000 admission limit.
	extra := next
	extra.Delegator = addr(t, identity.Bech32PrefixAccAddr, 99)
	require.ErrorContains(t, bounded.SetDelegation(ctx, extra), "delegation limit")
}

func TestNativeFeeRewardIndexCacheRollbackPolicyAndCorruption(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.ValidateNativeFeeRewardAdmission(ctx, func(sdk.AccAddress) bool { return true }))
	before, err := k.readNativeFeeRewardOperator(ctx, g.Validators[0].OperatorAddress)
	require.NoError(t, err)
	child, _ := ctx.CacheContext()
	next := copyDelegation(t, g.Delegations[0])
	next.Delegator = addr(t, identity.Bech32PrefixAccAddr, 3)
	require.NoError(t, k.ApplyDelegationBatch(child, []DelegationChange{{Expected: &g.Delegations[0], Next: &next}}))
	require.ErrorContains(t, k.ValidateNativeFeeRewardChanges(child, func(a sdk.AccAddress) bool { return !bytes.Equal(a, bytes.Repeat([]byte{3}, 20)) }), "owner address is not admitted")
	checks := 0
	require.NoError(t, k.ValidateNativeFeeRewardChanges(child, func(sdk.AccAddress) bool { checks++; return true }))
	require.Equal(t, 2, checks, "failed admission must not acknowledge dirty recipients")
	after, err := k.readNativeFeeRewardOperator(ctx, g.Validators[0].OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, before, after, "discarded outer cache cannot mutate ownership")
	// Updating a reward address must participate in incremental policy checks.
	v, err := k.GetValidator(ctx, g.Validators[0].OperatorAddress)
	require.NoError(t, err)
	v.RewardAddress = addr(t, identity.Bech32PrefixAccAddr, 4)
	require.NoError(t, k.SetValidator(ctx, v))
	require.ErrorContains(t, k.ValidateNativeFeeRewardChanges(ctx, func(a sdk.AccAddress) bool { return !bytes.Equal(a, bytes.Repeat([]byte{4}, 20)) }), "commission address is not admitted")
	key, err := feeRewardOwnerKey(feeRewardOwnerPrefix, g.Delegations[0])
	require.NoError(t, err)
	require.NoError(t, service.OpenKVStore(ctx).Set(key, []byte{1}))
	next = copyDelegation(t, g.Delegations[0])
	next.Stake.Stake.Amount = next.Stake.Stake.Amount.AddRaw(1)
	require.ErrorContains(t, k.SetDelegation(ctx, next), "invalid native fee reward owner weight")
	actual, err := k.GetDelegation(ctx, next.Delegator, next.Validator, next.Stake.Type, next.Stake.ID)
	require.NoError(t, err)
	require.Equal(t, g.Delegations[0], actual)
}

func TestNativeFeeRewardIndexColdMigrationRequiresExplicitAudit(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	for _, prefix := range []byte{feeRewardOwnerPrefix, feeRewardCountPrefix, feeRewardDirtyOwnerPrefix, feeRewardDirtyCommissionPrefix} {
		var keys [][]byte
		require.NoError(t, k.scan(ctx, prefix, func(key, _ []byte) error { keys = append(keys, key); return nil }))
		for _, key := range keys {
			require.NoError(t, service.OpenKVStore(ctx).Delete(key))
		}
	}
	require.NoError(t, service.OpenKVStore(ctx).Delete([]byte{feeRewardIndexMarker}))
	require.ErrorIs(t, k.ValidateNativeFeeRewardChanges(ctx, func(sdk.AccAddress) bool { return true }), ErrNativeFeeRewardIndexUnavailable)
	require.NoError(t, k.BuildNativeFeeRewardIndex(ctx))
	require.NoError(t, k.ValidateNativeFeeRewardAdmission(ctx, func(sdk.AccAddress) bool { return true }))
	require.ErrorContains(t, k.BuildNativeFeeRewardIndex(ctx), "already initialized")
	_, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
}

type feeRewardHotPathService struct{ store.KVStoreService }

func (s feeRewardHotPathService) OpenKVStore(ctx context.Context) store.KVStore {
	return feeRewardHotPathStore{s.KVStoreService.OpenKVStore(ctx)}
}

type feeRewardHotPathStore struct{ store.KVStore }

func (s feeRewardHotPathStore) Iterator(start, end []byte) (store.Iterator, error) {
	if len(start) != 1 || start[0] != feeRewardDirtyOwnerPrefix && start[0] != feeRewardDirtyCommissionPrefix {
		return nil, errors.New("unexpected owner/delegation scan on fee reward admission hot path")
	}
	return s.KVStore.Iterator(start, end)
}
