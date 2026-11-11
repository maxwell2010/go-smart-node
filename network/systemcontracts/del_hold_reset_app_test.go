package systemcontracts_test

import (
	"math/big"
	"strconv"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const delHoldResetPassed = "signed DEL hold reset preserved backing, merged the whole bucket, and settled its withdrawal once"

func TestOwnedAppSignedDELHoldResetLifecycle(t *testing.T) {
	testSignedDELFixture(t, "reset", delHoldResetPassed)
}

func runSignedDELHoldResetLifecycle(t *testing.T, f signedDELFixture) {
	t.Helper()
	e := f.env
	k := e.app.NativeValidatorKeeper
	zero := sdkmath.ZeroInt()
	one := sdkmath.NewIntWithDecimal(1, 18)
	two := one.Add(one)
	three := two.Add(one)
	five := three.Add(two)
	seven := five.Add(two)
	total := seven.Add(two)
	kind := validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN
	supply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom)
	now := func() int64 { return e.ctx().BlockTime().Unix() }
	f.declareValidator()
	f.activeReserve(zero)

	advanceTo := func(at int64) {
		t.Helper()
		for now() < at {
			e.advance()
		}
	}
	stakeID := func(deadline int64) common.Hash {
		t.Helper()
		var id [32]byte
		if deadline == 0 {
			id = f.read(f.gateway, f.target, "getStakeId", f.validator, f.user, f.wrapped).([32]byte)
		} else {
			id = f.read(f.gateway, f.target, "getHoldStakeId", f.validator, f.user, f.wrapped, big.NewInt(deadline)).([32]byte)
		}
		require.NotEqual(t, [32]byte{}, id)
		return common.Hash(id)
	}
	assertBucket := func(deadline int64, amount sdkmath.Int) {
		t.Helper()
		method := "getStake"
		args := []interface{}{f.validator, f.user, f.wrapped}
		if deadline != 0 {
			method = "getHoldStake"
			args = append(args, big.NewInt(deadline))
		}
		row := abi.ConvertType(f.read(f.gateway, f.target, method, args...), new(gatewayDELStake)).(*gatewayDELStake)
		require.Zero(t, amount.BigInt().Cmp(row.Amount))
		binding, err := k.GetEVMStakeBinding(e.ctx(), kind, f.target.Bytes(), stakeID(deadline).Bytes())
		if amount.IsZero() {
			require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
			return
		}
		require.NoError(t, err)
		require.True(t, binding.Active)
		require.True(t, amount.Equal(binding.RawAmount))
	}
	assertNative := func(kind validatortypes.StakeType, amount sdkmath.Int, deadlines ...int64) {
		t.Helper()
		row, err := k.GetDelegation(e.ctx(), f.beneficiary, f.operator, kind, identity.BaseDenom)
		if amount.IsZero() {
			require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
			return
		}
		require.NoError(t, err)
		require.True(t, amount.Equal(row.Stake.Stake.Amount))
		require.Len(t, row.Stake.Holds, len(deadlines))
		for i, deadline := range deadlines {
			require.Equal(t, deadline, row.Stake.Holds[i].HoldEndTime)
		}
	}

	baseTime := now()
	selected, siblingA, siblingB := baseTime+14, baseTime+5, baseTime+7
	heldID, unheldID := stakeID(selected), stakeID(0)
	require.NotEqual(t, heldID, unheldID)
	f.signed("delegateDEL", two, true, f.validator)
	f.signed("delegateHoldDEL", three, true, f.validator, big.NewInt(selected))
	f.signed("delegateHoldDEL", two, true, f.validator, big.NewInt(selected))
	f.signed("delegateHoldDEL", one, true, f.validator, big.NewInt(siblingA))
	f.signed("delegateHoldDEL", one, true, f.validator, big.NewInt(siblingB))
	assertBucket(selected, five)
	assertBucket(0, two)
	assertNative(validatortypes.StakeType_CoinHold, seven, siblingA, siblingB, selected, selected)
	assertNative(validatortypes.StakeType_Coin, two)
	f.activeReserve(total)
	f.backing(total)

	reject := func(key int, method string, deadlineArgument interface{}) {
		t.Helper()
		before := f.native()
		f.signedBy(key, method, zero, false, f.validator, f.user, f.wrapped, deadlineArgument)
		require.Equal(t, before, f.native())
		f.activeReserve(total)
		f.backing(total)
	}
	reject(0, "resetHolds", []*big.Int{big.NewInt(selected)})
	advanceTo(siblingB)
	// Each sibling is already resettable in Solidity. These calls exercise
	// the App's signer, selector and batch-size gates on live buckets.
	reject(1, "resetHolds", []*big.Int{big.NewInt(siblingA)})
	reject(0, "resetHold", big.NewInt(siblingA))
	reject(0, "resetHolds", []*big.Int{big.NewInt(siblingA), big.NewInt(siblingB)})
	assertBucket(selected, five)
	assertBucket(siblingA, one)
	assertBucket(siblingB, one)
	assertBucket(0, two)

	advanceTo(selected)
	require.Equal(t, selected, now())
	beforeHeld, err := k.GetDelegation(e.ctx(), f.beneficiary, f.operator, validatortypes.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	beforeUnheld, err := k.GetEVMStakeBinding(e.ctx(), kind, f.target.Bytes(), unheldID.Bytes())
	require.NoError(t, err)
	f.signed("resetHolds", zero, true, f.validator, f.user, f.wrapped, []*big.Int{big.NewInt(selected)})
	f.eventOnce("decimal_del_hold_reset", map[string]string{
		"stake_id": heldID.Hex(), "unheld_stake_id": unheldID.Hex(),
		"hold_deadline": strconv.FormatInt(selected, 10), "amount": five.String(),
	})
	assertBucket(selected, zero)
	assertBucket(siblingA, one)
	assertBucket(siblingB, one)
	assertBucket(0, seven)
	assertNative(validatortypes.StakeType_CoinHold, two, siblingA, siblingB)
	assertNative(validatortypes.StakeType_Coin, seven)
	afterHeld, err := k.GetDelegation(e.ctx(), f.beneficiary, f.operator, validatortypes.StakeType_CoinHold, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, beforeHeld.Stake.Holds[:2], afterHeld.Stake.Holds, "reset must preserve sibling amounts and original hold start times")
	afterUnheld, err := k.GetEVMStakeBinding(e.ctx(), kind, f.target.Bytes(), unheldID.Bytes())
	require.NoError(t, err)
	require.Equal(t, beforeUnheld.CreatedHeight, afterUnheld.CreatedHeight)
	require.Empty(t, f.native().Undelegations)
	require.Empty(t, f.native().EVMFrozenBindings)
	require.Equal(t, supply, e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom))
	f.activeReserve(total)
	f.backing(total)
	reject(0, "resetHolds", []*big.Int{big.NewInt(selected)})

	maturity := now() + signedDELFreezeSeconds
	withdraw := f.signed("withdraw", zero, true, f.validator, f.wrapped, seven.BigInt())
	index := f.withdrawalIndex(withdraw, unheldID)
	assertBucket(0, zero)
	assertNative(validatortypes.StakeType_Coin, zero)
	queue := f.native().Undelegations
	require.Len(t, queue, 1)
	require.Len(t, queue[0].Entries, 1)
	require.True(t, seven.Equal(queue[0].Entries[0].Stake.Stake.Amount))
	f.activeReserve(two)
	f.backing(total)

	advanceTo(maturity)
	e.credit.Set(seven.BigInt())
	f.signed("complete", zero, true, []*big.Int{index})
	f.eventOnce("decimal_withdrawal_completed", map[string]string{"frozen_index": common.BigToHash(index).Hex()})
	f.signed("complete", zero, false, []*big.Int{index})
	require.Empty(t, f.native().Undelegations)
	assertBucket(siblingA, one)
	assertBucket(siblingB, one)
	assertNative(validatortypes.StakeType_CoinHold, two, siblingA, siblingB)
	require.Equal(t, supply, e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom))
	f.activeReserve(two)
	f.backing(two)
	t.Log(delHoldResetPassed)
}
