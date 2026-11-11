package systemcontracts_test

import (
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const delHoldLifecyclePassed = "signed DEL hold buckets, FIFO partial withdrawals, and exactly-once payouts passed"

func TestOwnedAppSignedDELHoldLifecycle(t *testing.T) {
	testSignedDELFixture(t, "hold", delHoldLifecyclePassed)
}

func runSignedDELHoldLifecycle(t *testing.T, f signedDELFixture) {
	t.Helper()
	e := f.env
	k := e.app.NativeValidatorKeeper
	zero := sdkmath.ZeroInt()
	one := sdkmath.NewIntWithDecimal(1, 18)
	two := one.Add(one)
	three := two.Add(one)
	four := two.Add(two)
	five := three.Add(two)
	seven := five.Add(two)
	kind := validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN
	supply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom)
	f.declareValidator()

	type bucket struct {
		id  common.Hash
		end int64
	}
	now := func() int64 { return e.ctx().BlockTime().Unix() }
	bucketAt := func(end int64) bucket {
		t.Helper()
		id := f.read(f.gateway, f.target, "getHoldStakeId", f.validator, f.user, f.wrapped, big.NewInt(end)).([32]byte)
		require.NotEqual(t, [32]byte{}, id)
		return bucket{id: common.Hash(id), end: end}
	}
	firstBucket, laterBucket := bucketAt(now()+5), bucketAt(now()+50)
	require.NotEqual(t, firstBucket.id, laterBucket.id)

	type lot struct {
		start, end int64
		amount     sdkmath.Int
	}
	assertHeld := func(stake validatortypes.Stake, expected ...lot) {
		t.Helper()
		require.Equal(t, validatortypes.StakeType_CoinHold, stake.Type)
		require.Equal(t, identity.BaseDenom, stake.ID)
		require.Equal(t, identity.BaseDenom, stake.Stake.Denom)
		require.Len(t, stake.Holds, len(expected))
		sum := zero
		for i, want := range expected {
			got := stake.Holds[i]
			require.NotNil(t, got)
			require.Equal(t, want.start, got.HoldStartTime)
			require.Equal(t, want.end, got.HoldEndTime)
			require.True(t, want.amount.Equal(got.Amount))
			sum = sum.Add(want.amount)
		}
		require.True(t, sum.Equal(stake.Stake.Amount))
	}
	assertActive := func(expected ...lot) {
		t.Helper()
		held, err := k.GetDelegation(e.ctx(), f.beneficiary, f.operator, validatortypes.StakeType_CoinHold, identity.BaseDenom)
		if len(expected) == 0 {
			require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
			require.Empty(t, f.native().Delegations)
			f.activeReserve(zero)
			return
		}
		require.NoError(t, err)
		assertHeld(held.Stake, expected...)
		f.activeReserve(held.Stake.Stake.Amount)
		require.Len(t, f.native().Delegations, 1, "held deposits must not also enter the unheld DEL row")
	}
	assertBucket := func(b bucket, start int64, amount sdkmath.Int) {
		t.Helper()
		raw := f.read(f.gateway, f.target, "getHoldStake", f.validator, f.user, f.wrapped, big.NewInt(b.end))
		row := abi.ConvertType(raw, new(gatewayDELStake)).(*gatewayDELStake)
		require.Zero(t, amount.BigInt().Cmp(row.Amount))
		binding, err := k.GetEVMStakeBinding(e.ctx(), kind, f.target.Bytes(), b.id.Bytes())
		if amount.IsZero() {
			require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
			return
		}
		require.NoError(t, err)
		require.True(t, binding.Active)
		require.True(t, amount.Equal(binding.RawAmount))
		require.Equal(t, f.validator, row.Validator)
		require.Equal(t, f.user, row.Delegator)
		require.Equal(t, f.wrapped, row.Token)
		require.Equal(t, uint8(4), row.TokenType)
		require.Equal(t, b.end, row.HoldTimestamp.Int64())
		require.Equal(t, start, row.HoldStartTime.Int64())
		require.Equal(t, common.BigToHash(big.NewInt(b.end)).Bytes(), binding.HoldTimestamp)
		require.Equal(t, common.BigToHash(big.NewInt(start)).Bytes(), binding.RawHoldStartTime)
	}
	deposit := func(b bucket, amount sdkmath.Int) lot {
		t.Helper()
		item := lot{start: now(), end: b.end, amount: amount}
		f.signed("delegateHoldDEL", amount, true, f.validator, big.NewInt(b.end))
		f.eventOnce("decimal_del_delegated", map[string]string{"stake_id": b.id.Hex(), "amount": amount.String()})
		return item
	}

	assertActive()
	f.backing(zero)
	firstLot := deposit(firstBucket, three)
	assertBucket(firstBucket, firstLot.start, three)
	assertActive(firstLot)
	f.backing(three)

	topUp := deposit(firstBucket, two)
	require.Greater(t, topUp.start, firstLot.start)
	assertBucket(firstBucket, firstLot.start, five)
	assertActive(firstLot, topUp)
	f.backing(five)

	other := deposit(laterBucket, two)
	assertBucket(laterBucket, other.start, two)
	assertActive(firstLot, topUp, other)
	f.backing(seven)

	advanceTo := func(target int64) {
		t.Helper()
		for now() < target {
			e.advance()
		}
	}
	rejectWithdrawal := func(b bucket, amount sdkmath.Int) {
		t.Helper()
		before := f.native()
		f.signed("withdrawHold", zero, false, f.validator, f.wrapped, amount.BigInt(), big.NewInt(b.end))
		require.Equal(t, before, f.native())
	}
	rejectWithdrawal(firstBucket, one)
	advanceTo(firstBucket.end)
	require.Equal(t, firstBucket.end, now())
	rejectWithdrawal(firstBucket, one)
	beforeReset := f.native()
	f.signed("resetHolds", zero, false, f.validator, f.user, f.wrapped, []*big.Int{big.NewInt(firstBucket.end)})
	require.Equal(t, beforeReset, f.native(), "a mature hold can reset only when the profile opts in")
	assertActive(firstLot, topUp, other)
	f.backing(seven)

	type claim struct {
		source   bucket
		index    *big.Int
		maturity int64
		amount   sdkmath.Int
		lots     []lot
	}
	assertClaim := func(item claim, pending bool) {
		t.Helper()
		frozen, err := k.GetEVMFrozenBinding(e.ctx(), f.target.Bytes(), common.BigToHash(item.index).Bytes())
		require.NoError(t, err)
		require.Equal(t, pending, frozen.Active)
		require.Equal(t, item.source.id.Bytes(), frozen.SourceStakeID)
		require.True(t, item.amount.Equal(frozen.RawAmount))
		require.Equal(t, common.BigToHash(big.NewInt(item.maturity)).Bytes(), frozen.CompletionTimestamp)
		raw := f.read(f.gateway, f.target, "getFrozenStake", item.index)
		row := abi.ConvertType(raw, new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal)
		var status uint8
		if pending {
			status = 1
		}
		require.Equal(t, status, row.FreezeStatus)
		require.Equal(t, item.source.end, row.Stake.HoldTimestamp.Int64())
		require.Equal(t, item.maturity, row.UnfreezeTimestamp.Int64())
		require.Zero(t, item.amount.BigInt().Cmp(row.Stake.Amount))
	}
	assertQueue := func(claims ...claim) {
		t.Helper()
		queue := f.native().Undelegations
		if len(claims) == 0 {
			require.Empty(t, queue)
			return
		}
		require.Len(t, queue, 1)
		require.Equal(t, f.beneficiary, queue[0].Delegator)
		require.Equal(t, f.operator, queue[0].Validator)
		require.Len(t, queue[0].Entries, len(claims))
		for i, item := range claims {
			entry := queue[0].Entries[i]
			require.Equal(t, item.maturity, entry.CompletionTime.Unix())
			assertHeld(entry.Stake, item.lots...)
			assertClaim(item, true)
		}
	}
	request := func(b bucket, amount sdkmath.Int, lots ...lot) claim {
		t.Helper()
		item := claim{source: b, maturity: now() + signedDELFreezeSeconds, amount: amount, lots: lots}
		response := f.signed("withdrawHold", zero, true, f.validator, f.wrapped, amount.BigInt(), big.NewInt(b.end))
		item.index = f.withdrawalIndex(response, b.id)
		f.eventOnce("decimal_del_withdrawal_requested", map[string]string{
			"stake_id": b.id.Hex(), "frozen_index": common.BigToHash(item.index).Hex(), "amount": amount.String(),
		})
		assertClaim(item, true)
		return item
	}
	rejectComplete := func(item claim) {
		t.Helper()
		before := f.native()
		f.signed("complete", zero, false, []*big.Int{item.index})
		require.Equal(t, before, f.native())
	}
	settle := func(item claim, backing sdkmath.Int) {
		t.Helper()
		advanceTo(item.maturity)
		e.credit.Set(item.amount.BigInt())
		f.signed("complete", zero, true, []*big.Int{item.index})
		f.eventOnce("decimal_withdrawal_completed", map[string]string{"frozen_index": common.BigToHash(item.index).Hex()})
		assertClaim(item, false)
		f.backing(backing)
		rejectComplete(item)
		f.backing(backing)
	}

	remainder := lot{start: topUp.start, end: topUp.end, amount: one}
	partial := request(firstBucket, four, firstLot, remainder)
	assertBucket(firstBucket, firstLot.start, one)
	assertBucket(laterBucket, other.start, two)
	assertActive(remainder, other)
	assertQueue(partial)
	f.backing(seven)
	rejectComplete(partial)
	e.advance()
	e.advance()

	final := request(firstBucket, one, remainder)
	require.NotEqual(t, common.BigToHash(partial.index), common.BigToHash(final.index))
	assertBucket(firstBucket, firstLot.start, zero)
	assertBucket(laterBucket, other.start, two)
	assertActive(other)
	assertQueue(partial, final)
	f.backing(seven)

	settle(partial, three)
	assertQueue(final)
	rejectComplete(final)
	settle(final, two)
	assertQueue()
	require.Less(t, now(), laterBucket.end, "settling the first bucket must not release the other deadline")
	assertBucket(laterBucket, other.start, two)
	assertActive(other)

	advanceTo(laterBucket.end + 1)
	last := request(laterBucket, two, other)
	assertActive()
	assertBucket(laterBucket, other.start, zero)
	assertQueue(last)
	settle(last, zero)
	assertQueue()
	require.Equal(t, supply, e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom))
	t.Log(delHoldLifecyclePassed)
}
