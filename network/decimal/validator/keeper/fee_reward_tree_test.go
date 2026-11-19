package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func rewardTreeFixture(t *testing.T, owners int) (sdk.Context, *StateKeeper, *types.GenesisState) {
	t.Helper()
	ctx, _, k := environment(t)
	g := genesis(t)
	g.Delegations = nil
	g.Undelegations = nil
	g.Redelegations = nil
	for i := 0; i < owners; i++ {
		raw := make([]byte, 20)
		binary.BigEndian.PutUint64(raw[12:], uint64(i+101))
		owner, e := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(raw)
		require.NoError(t, e)
		g.Delegations = append(g.Delegations, types.Delegation{Delegator: owner, Validator: g.Validators[0].OperatorAddress, Stake: types.NewStakeCoin(sdk.NewInt64Coin(identity.BaseDenom, 10))})
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	ok, e := k.hasNativeFeeRewardTree(ctx)
	require.NoError(t, e)
	if !ok {
		require.NoError(t, k.BuildNativeFeeRewardTree(ctx))
	}
	return ctx, k, g
}
func rewardTreeBankPay(k *StateKeeper) func(sdk.Context, sdk.AccAddress, sdkmath.Int) error {
	return func(ctx sdk.Context, a sdk.AccAddress, amount sdkmath.Int) error {
		key := append([]byte{0xe1}, a...)
		state := k.service.OpenKVStore(ctx)
		b, e := state.Get(key)
		if e != nil {
			return e
		}
		total := new(big.Int).Add(new(big.Int).SetBytes(b), amount.BigInt())
		return state.Set(key, total.FillBytes(make([]byte, 32)))
	}
}
func rewardTreePaid(t *testing.T, k *StateKeeper, ctx sdk.Context, owner string) sdkmath.Int {
	t.Helper()
	a, e := feeRewardAddress(owner)
	require.NoError(t, e)
	b, e := k.service.OpenKVStore(ctx).Get(append([]byte{0xe1}, a...))
	require.NoError(t, e)
	return sdkmath.NewIntFromBigInt(new(big.Int).SetBytes(b))
}
func rewardTreeDrain(t *testing.T, k *StateKeeper, ctx sdk.Context, height int64, page int, pay func(sdk.Context, sdk.AccAddress, sdkmath.Int) error) sdkmath.Int {
	t.Helper()
	total := sdkmath.ZeroInt()
	for i := 0; i < 2000; i++ {
		stats, e := k.ProcessNativeFeeRewards(ctx, height+int64(i), page, pay)
		require.NoError(t, e)
		require.LessOrEqual(t, stats.Visited, page)
		total = total.Add(stats.Amount)
		if !stats.Active {
			return total
		}
	}
	t.Fatal("reward sweep failed to terminate")
	return total
}

func TestNativeFeeRewardTree1000OwnersConservationAndCompressedState(t *testing.T) {
	ctx, k, g := rewardTreeFixture(t, 1000)
	require.NoError(t, k.AccrueNativeFeeRewards(ctx, sdkmath.NewInt(1_000_003), []NativeFeeRewardVote{{g.Validators[0].OperatorAddress, 1}}))
	liability, e := k.AggregateNativeFeeRewardLiability(ctx)
	require.NoError(t, e)
	require.True(t, liability.Equal(sdkmath.NewInt(1_000_003)))
	nodes := 0
	require.NoError(t, k.scan(ctx, feeTreeNodePrefix, func(_, _ []byte) error { nodes++; return nil }))
	require.Equal(t, 1999, nodes)
	require.NoError(t, k.auditNativeFeeRewardTree(ctx))
	early, e := k.ProcessNativeFeeRewards(ctx, 119, 64, rewardTreeBankPay(k))
	require.NoError(t, e)
	require.Zero(t, early.Visited)
	paid := sdkmath.ZeroInt()
	for h := int64(120); h <= 2400; h += 120 {
		paid = paid.Add(rewardTreeDrain(t, k, ctx, h, 64, rewardTreeBankPay(k)))
		remaining, e := k.AggregateNativeFeeRewardLiability(ctx)
		require.NoError(t, e)
		if remaining.IsZero() {
			break
		}
	}
	require.True(t, paid.Equal(liability))
	liability, e = k.AggregateNativeFeeRewardLiability(ctx)
	require.NoError(t, e)
	require.True(t, liability.IsZero())
	require.NoError(t, k.auditNativeFeeRewardTree(ctx))
}

func TestNativeFeeRewardTreeLateEntryWithdrawalCommissionAndCacheRollback(t *testing.T) {
	ctx, k, g := rewardTreeFixture(t, 2)
	op := g.Validators[0].OperatorAddress
	require.NoError(t, k.AccrueNativeFeeRewards(ctx, sdkmath.NewInt(100), []NativeFeeRewardVote{{op, 1}}))
	late := types.Delegation{Delegator: addr(t, identity.Bech32PrefixAccAddr, 9), Validator: op, Stake: types.NewStakeCoin(sdk.NewInt64Coin(identity.BaseDenom, 1000))}
	// The mutation hook settles old root pending before the new weight exists.
	require.NoError(t, k.SetDelegation(ctx, late))
	require.NoError(t, k.ApplyDelegationBatch(ctx, []DelegationChange{{Expected: &g.Delegations[0]}}))
	require.NoError(t, k.auditNativeFeeRewardTree(ctx))
	branch, _ := ctx.CacheContext()
	require.NoError(t, k.AccrueNativeFeeRewards(branch, sdkmath.NewInt(777), []NativeFeeRewardVote{{op, 1}}))
	paid := rewardTreeDrain(t, k, ctx, 120, 2, rewardTreeBankPay(k))
	require.True(t, paid.Equal(sdkmath.NewInt(100)), "discarded fee accrual must not change payouts")
	require.True(t, rewardTreePaid(t, k, ctx, g.Delegations[0].Delegator).Equal(sdkmath.NewInt(45)), "withdrawn owner retains already earned DEL")
	require.True(t, rewardTreePaid(t, k, ctx, g.Delegations[1].Delegator).Equal(sdkmath.NewInt(45)))
	require.True(t, rewardTreePaid(t, k, ctx, late.Delegator).IsZero(), "late deposit must not receive old fees")
	require.True(t, rewardTreePaid(t, k, ctx, g.Validators[0].RewardAddress).Equal(sdkmath.NewInt(10)))
	// Paying the last liability removes a zero-weight leaf and compresses it.
	first, _ := feeRewardAddress(g.Delegations[0].Delegator)
	opKey, _ := operatorKey(feeTreeRootPrefix, op)
	exists, e := k.service.OpenKVStore(ctx).Has(feeTreeKey(feeTreeNodePrefix, opKey[1:], feeTreeRef(first, 160)))
	require.NoError(t, e)
	require.False(t, exists)
	require.NoError(t, k.auditNativeFeeRewardTree(ctx))
}

func TestNativeFeeRewardTreeFailedRecipientRetriesWithoutStarvationOrPartialPayment(t *testing.T) {
	ctx, k, g := rewardTreeFixture(t, 2)
	op := g.Validators[0].OperatorAddress
	require.NoError(t, k.AccrueNativeFeeRewards(ctx, sdkmath.NewInt(100), []NativeFeeRewardVote{{op, 1}}))
	blocked, _ := feeRewardAddress(g.Delegations[0].Delegator)
	bank := rewardTreeBankPay(k)
	failing := func(ctx sdk.Context, a sdk.AccAddress, n sdkmath.Int) error {
		if e := bank(ctx, a, n); e != nil {
			return e
		}
		if bytes.Equal(a, blocked) {
			return errors.New("recipient blocked after attempted write")
		}
		return nil
	}
	paid := rewardTreeDrain(t, k, ctx, 120, 1, failing)
	require.True(t, paid.Equal(sdkmath.NewInt(55)))
	require.True(t, rewardTreePaid(t, k, ctx, g.Delegations[0].Delegator).IsZero(), "failed callback writes must roll back")
	liability, e := k.AggregateNativeFeeRewardLiability(ctx)
	require.NoError(t, e)
	require.True(t, liability.Equal(sdkmath.NewInt(45)))
	// A fresh keeper represents process restart; persistent cursor and credits survive.
	restarted, e := NewStateKeeper(k.service)
	require.NoError(t, e)
	paid = rewardTreeDrain(t, restarted, ctx, 240, 2, bank)
	require.True(t, paid.Equal(sdkmath.NewInt(45)))
	require.NoError(t, k.auditNativeFeeRewardTree(ctx))
}

func TestNativeFeeRewardTreeFiniteSweepBoundaryAndGasBudget(t *testing.T) {
	ctx, k, g := rewardTreeFixture(t, 2)
	op := g.Validators[0].OperatorAddress
	require.NoError(t, k.AccrueNativeFeeRewards(ctx, sdkmath.NewInt(100), []NativeFeeRewardVote{{op, 1}}))
	limited := ctx.WithGasMeter(storetypes.NewGasMeter(NativeFeeRewardPageBudget - 1))
	stats, e := k.ProcessNativeFeeRewards(limited, 120, 64, rewardTreeBankPay(k))
	require.NoError(t, e)
	require.True(t, stats.OutOfGas)
	require.Zero(t, stats.Visited)
	liability, e := k.AggregateNativeFeeRewardLiability(ctx)
	require.NoError(t, e)
	require.True(t, liability.Equal(sdkmath.NewInt(100)))
	// Callback out-of-gas is discarded and never consumes a pending reward.
	stats, e = k.ProcessNativeFeeRewards(ctx, 121, 1, func(sdk.Context, sdk.AccAddress, sdkmath.Int) error {
		panic(storetypes.ErrorOutOfGas{Descriptor: "reward callback"})
	})
	require.NoError(t, e)
	require.True(t, stats.OutOfGas)
	paid := rewardTreeDrain(t, k, ctx, 122, 64, rewardTreeBankPay(k))
	paid = paid.Add(rewardTreeDrain(t, k, ctx, 240, 64, rewardTreeBankPay(k)))
	require.True(t, paid.Equal(sdkmath.NewInt(100)))
	require.NoError(t, k.auditNativeFeeRewardTree(ctx))
}

func TestNativeFeeRewardTreeEmptyAndJailedOperators(t *testing.T) {
	ctx, k, g := rewardTreeFixture(t, 1)
	op := g.Validators[0].OperatorAddress
	v, e := k.GetValidator(ctx, op)
	require.NoError(t, e)
	v.Jailed = true
	require.NoError(t, k.SetValidator(ctx, v))
	// The authenticated prior commit, rather than a later jail flag, decides
	// who earned prior-block fees. Existing stake remains owned by its user.
	require.NoError(t, k.AccrueNativeFeeRewards(ctx, sdkmath.NewInt(10), []NativeFeeRewardVote{{op, 1}}))
	require.ErrorIs(t, k.AccrueNativeFeeRewards(ctx, sdkmath.NewInt(10), nil), ErrNoNativeFeeRewardRecipients)
	liability, e := k.AggregateNativeFeeRewardLiability(ctx)
	require.NoError(t, e)
	require.True(t, liability.Equal(sdkmath.NewInt(10)))
	require.NoError(t, k.auditNativeFeeRewardTree(ctx))
}

func TestNativeFeeRewardTreeRecipientCapacityDoesNotBlockWithdrawal(t *testing.T) {
	ctx, k, g := rewardTreeFixture(t, 2)
	op := g.Validators[0].OperatorAddress
	p, e := k.GetParams(ctx)
	require.NoError(t, e)
	p.MaxDelegations = 2
	require.NoError(t, k.SetParams(ctx, p))
	require.NoError(t, k.AccrueNativeFeeRewards(ctx, sdkmath.NewInt(100), []NativeFeeRewardVote{{op, 1}}))
	require.NoError(t, k.ApplyDelegationBatch(ctx, []DelegationChange{{Expected: &g.Delegations[0]}}))
	late := types.Delegation{Delegator: addr(t, identity.Bech32PrefixAccAddr, 9), Validator: op, Stake: types.NewStakeCoin(sdk.NewInt64Coin(identity.BaseDenom, 10))}
	require.NoError(t, k.SetDelegation(ctx, late))
	opKey, e := operatorKey(feeTreeRootPrefix, op)
	require.NoError(t, e)
	count, e := k.nativeFeeRewardRetained(ctx, opKey[1:])
	require.NoError(t, e)
	require.EqualValues(t, 4, count)
	// At capacity, removing active weight still succeeds and preserves its
	// pending payout. A new owner cannot replace that unpaid historical claim.
	require.NoError(t, k.ApplyDelegationBatch(ctx, []DelegationChange{{Expected: &g.Delegations[1]}}))
	extra := late
	extra.Delegator = addr(t, identity.Bech32PrefixAccAddr, 10)
	require.ErrorContains(t, k.SetDelegation(ctx, extra), "recipient capacity")
	validator, e := k.GetValidator(ctx, op)
	require.NoError(t, e)
	validator.RewardAddress = addr(t, identity.Bech32PrefixAccAddr, 11)
	require.ErrorContains(t, k.SetValidator(ctx, validator), "recipient capacity", "unpaid old commission must not be overwritten at a full census")
	// An existing owner's top-up consumes no new recipient slot.
	late.Stake.Stake.Amount = late.Stake.Stake.Amount.AddRaw(1)
	require.NoError(t, k.SetDelegation(ctx, late))
	lower := p
	lower.MaxDelegations = 1
	require.ErrorContains(t, k.SetParams(ctx, lower), "unpaid reward recipients", "lowering a parameter cannot invalidate retained earned claims")
	require.NoError(t, k.auditNativeFeeRewardTree(ctx))
	paid := rewardTreeDrain(t, k, ctx, 120, 64, rewardTreeBankPay(k))
	require.True(t, paid.Equal(sdkmath.NewInt(100)))
	require.NoError(t, k.SetDelegation(ctx, extra))
	require.NoError(t, k.auditNativeFeeRewardTree(ctx))
}

func TestNativeFeeRewardTreeMigrationAndEscrowMismatchFailClosed(t *testing.T) {
	ctx, k, g := rewardTreeFixture(t, 2)
	op := g.Validators[0].OperatorAddress
	require.NoError(t, k.MigrateNativeFeeRewardState(ctx))
	require.NoError(t, k.MigrateNativeFeeRewardState(ctx))
	require.NoError(t, k.AccrueNativeFeeRewards(ctx, sdkmath.NewInt(100), []NativeFeeRewardVote{{op, 1}}))
	require.NoError(t, k.MigrateNativeFeeRewardState(ctx), "migration must retain already accrued rewards")
	liability, e := k.AggregateNativeFeeRewardLiability(ctx)
	require.NoError(t, e)
	require.True(t, liability.Equal(sdkmath.NewInt(100)))
	branch, _ := ctx.CacheContext()
	require.NoError(t, k.writeNativeFeeRewardLiability(branch, big.NewInt(101)))
	require.ErrorContains(t, k.auditNativeFeeRewardTree(branch), "do not conserve")
	require.NoError(t, k.auditNativeFeeRewardTree(ctx))
	paid := rewardTreeDrain(t, k, ctx, 120, 64, rewardTreeBankPay(k))
	require.True(t, paid.Equal(sdkmath.NewInt(100)))
}

func TestNativeFeeRewardTreeDeepPendingProgressUnderContinuousNewFees(t *testing.T) {
	ctx, _, k := environment(t)
	g := genesis(t)
	g.Delegations = nil
	g.Undelegations = nil
	g.Redelegations = nil
	base := make([]byte, 20)
	base[19] = 1
	for i := 0; i < 160; i++ {
		raw := bytes.Clone(base)
		if i < 159 {
			raw[i/8] |= 1 << uint(7-i%8)
		}
		owner, e := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(raw)
		require.NoError(t, e)
		g.Delegations = append(g.Delegations, types.Delegation{Delegator: owner, Validator: g.Validators[0].OperatorAddress, Stake: types.NewStakeCoin(sdk.NewInt64Coin(identity.BaseDenom, 10))})
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	op := g.Validators[0].OperatorAddress
	opKey, e := operatorKey(feeTreeRootPrefix, op)
	require.NoError(t, e)
	paidAny := false
	for i := 0; i < 40; i++ {
		require.NoError(t, k.AccrueNativeFeeRewards(ctx, sdkmath.NewInt(100_000), []NativeFeeRewardVote{{op, 1}}))
		paid, amount, oog, e := k.payNativeFeeRewardOne(ctx, feeTreePayKey(opKey[1:], base), rewardTreeBankPay(k))
		require.NoError(t, e)
		require.False(t, oog, "deep valid tree must fit bounded per-recipient work")
		if paid && amount.IsPositive() {
			paidAny = true
		}
		require.NoError(t, k.auditNativeFeeRewardTree(ctx))
	}
	require.True(t, paidAny, "new fees at root must not starve earlier deep credits")
}
