package governance

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	nativetypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func account(t *testing.T, prefix string, id byte) string {
	t.Helper()
	s, e := address.NewBech32Codec(prefix).BytesToString(bytes.Repeat([]byte{id}, 20))
	require.NoError(t, e)
	return s
}
func nativeFixture(t *testing.T) (sdk.Context, store.KVStoreService, *nativekeeper.StateKeeper, *nativetypes.GenesisState) {
	t.Helper()
	key := storetypes.NewKVStoreKey("native-governance")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient")).WithBlockHeight(1).WithChainID("mintcandy_300001-1")
	service := runtime.NewKVStoreService(key)
	native, e := nativekeeper.NewStateKeeper(service)
	require.NoError(t, e)
	v, e := nativetypes.NewValidator(account(t, identity.Bech32PrefixValAddr, 1), account(t, identity.Bech32PrefixAccAddr, 1), ed25519.GenPrivKeyFromSecret([]byte{1}).PubKey(), nativetypes.Description{Moniker: "native"}, sdkmath.LegacyMustNewDecFromStr("0.1"))
	require.NoError(t, e)
	v.Status = nativetypes.BondStatus_Bonded
	v.Stake = 100
	g := nativetypes.DefaultGenesisState()
	g.Validators = []nativetypes.Validator{v}
	unit := sdkmath.NewIntWithDecimal(1, 18)
	self := nativetypes.NewStakeCoin(sdk.NewCoin("del", unit.MulRaw(20)))
	self.Type = nativetypes.StakeType_CoinHold
	self.Holds = []*nativetypes.StakeHold{{Amount: unit.MulRaw(5), HoldStartTime: 100, HoldEndTime: 200}}
	nft := nativetypes.NewStakeNFT("actual-token", []uint32{1, 2}, sdk.NewCoin("del", unit.MulRaw(20)))
	nft.Type = nativetypes.StakeType_NFTHold
	nft.Holds = []*nativetypes.StakeHold{{Amount: unit.MulRaw(10), SubTokenIDs: []uint32{1}, HoldStartTime: 100, HoldEndTime: 200}}
	g.Delegations = []nativetypes.Delegation{
		{Delegator: v.RewardAddress, Validator: v.OperatorAddress, Stake: self},
		{Delegator: account(t, identity.Bech32PrefixAccAddr, 2), Validator: v.OperatorAddress, Stake: nativetypes.NewStakeCoin(sdk.NewCoin("del", unit.MulRaw(20)))},
		{Delegator: account(t, identity.Bech32PrefixAccAddr, 2), Validator: v.OperatorAddress, Stake: nft},
		{Delegator: account(t, identity.Bech32PrefixAccAddr, 3), Validator: v.OperatorAddress, Stake: nativetypes.NewStakeCoin(sdk.NewCoin("del", unit.MulRaw(40)))},
	}
	require.NoError(t, native.InitGenesis(ctx, g))
	return ctx, service, native, g
}
func baseSource() BaseReserveSource { return BaseReserveSource{} }

type sourceFn func(context.Context, Request) (Weights, error)

func (f sourceFn) SnapshotWeights(ctx context.Context, r Request) (Weights, error) { return f(ctx, r) }

func TestNativeGovernanceSnapshotConservesAndGroupsAllPositions(t *testing.T) {
	ctx, _, native, g := nativeFixture(t)
	a, e := Build(ctx, native, baseSource(), sdkmath.OneInt())
	require.NoError(t, e)
	total, e := a.TotalBondedTokens(ctx)
	require.NoError(t, e)
	require.True(t, total.Equal(sdkmath.NewInt(100)))
	// 20 coin + 20 NFT reserve is 40; held amounts are subsets, not 5/10 bonuses.
	raw := sdk.AccAddress(bytes.Repeat([]byte{2}, 20))
	count := 0
	require.NoError(t, a.IterateDelegations(ctx, raw, func(i int64, d stakingtypes.DelegationI) bool {
		count++
		require.EqualValues(t, 0, i)
		require.Equal(t, g.Validators[0].OperatorAddress, d.GetValidatorAddr())
		require.True(t, d.GetShares().Equal(sdkmath.LegacyNewDecFromInt(sdkmath.NewIntWithDecimal(40, 18))))
		return false
	}))
	require.Equal(t, 1, count)
	sum := sdkmath.LegacyZeroDec()
	for _, id := range []byte{1, 2, 3} {
		require.NoError(t, a.IterateDelegations(ctx, sdk.AccAddress(bytes.Repeat([]byte{id}, 20)), func(_ int64, d stakingtypes.DelegationI) bool { sum = sum.Add(d.GetShares()); return false }))
	}
	require.True(t, sum.Equal(sdkmath.LegacyNewDecFromInt(sdkmath.NewIntWithDecimal(100, 18))))
	count = 0
	require.NoError(t, a.IterateBondedValidatorsByPower(ctx, func(_ int64, v stakingtypes.ValidatorI) bool {
		count++
		require.True(t, v.(stakingtypes.Validator).Tokens.Equal(total))
		require.True(t, v.GetDelegatorShares().Equal(sum))
		return false
	}))
	require.Equal(t, 1, count)
}

func TestNativeGovernanceRejectsMissingDuplicateStaleAndInconsistentWeights(t *testing.T) {
	ctx, _, native, _ := nativeFixture(t)
	_, e := Build(ctx, native, nil, sdkmath.OneInt())
	require.Error(t, e)
	cases := []struct {
		name   string
		change func(*Weights)
	}{
		{"missing", func(w *Weights) { w.Positions = w.Positions[:1] }},
		{"duplicate", func(w *Weights) { w.Positions[1] = w.Positions[0] }},
		{"unknown", func(w *Weights) { w.Positions[0].PositionHash = [32]byte{9} }},
		{"negative", func(w *Weights) { w.Positions[0].Ownership = sdkmath.NewInt(-1) }},
		{"nonconserving", func(w *Weights) { w.Positions[0].Ownership = w.Positions[0].Ownership.AddRaw(1) }},
		{"wrong height", func(w *Weights) { w.Height++ }},
		{"wrong state", func(w *Weights) { w.StateHash[0] ^= 1 }},
		{"nil ownership", func(w *Weights) { w.Positions[0].Ownership = sdkmath.Int{} }},
		{"missing totals", func(w *Weights) { w.Totals = nil }},
		{"wrong total", func(w *Weights) { w.Totals[0].Ownership = w.Totals[0].Ownership.AddRaw(1) }},
		{"unknown validator", func(w *Weights) { w.Totals[0].Validator = "unknown" }},
		{"missing valuation policy", func(w *Weights) { w.ValuationID = "" }},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			_, e := Build(ctx, native, sourceFn(func(c context.Context, r Request) (Weights, error) {
				w, e := baseSource().SnapshotWeights(c, r)
				if e != nil {
					return w, e
				}
				item.change(&w)
				return w, nil
			}), sdkmath.OneInt())
			require.Error(t, e)
		})
	}
	_, e = Build(ctx, native, sourceFn(func(context.Context, Request) (Weights, error) { return Weights{}, errors.New("valuation unavailable") }), sdkmath.OneInt())
	require.ErrorContains(t, e, "valuation unavailable")
}

func TestNativeGovernanceRejectsUnsafeArithmeticAndUndefinedOwnership(t *testing.T) {
	ctx, _, native, g := nativeFixture(t)
	var missing sourceFn
	_, e := Build(ctx, native, missing, sdkmath.OneInt())
	require.Error(t, e)
	_, e = Build(ctx, native, baseSource(), sdkmath.Int{})
	require.Error(t, e)
	_, e = Build(ctx, native, baseSource(), sdkmath.NewIntWithDecimal(1, 60))
	require.ErrorContains(t, e, "arithmetic bounds")
	maximum := sdkmath.NewIntFromBigInt(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)))
	_, e = Build(ctx, native, sourceFn(func(c context.Context, r Request) (Weights, error) {
		w, e := baseSource().SnapshotWeights(c, r)
		if e != nil {
			return w, e
		}
		w.Positions[0].Ownership = maximum
		w.Positions[1].Ownership = maximum
		return w, nil
	}), sdkmath.OneInt())
	require.ErrorContains(t, e, "overflowing")
	_, e = Build(ctx, native, sourceFn(func(c context.Context, r Request) (Weights, error) {
		w, e := baseSource().SnapshotWeights(c, r)
		if e != nil {
			return w, e
		}
		for i := range w.Positions {
			w.Positions[i].Ownership = sdkmath.ZeroInt()
		}
		w.Totals[0].Ownership = sdkmath.ZeroInt()
		return w, nil
	}), sdkmath.OneInt())
	require.ErrorContains(t, e, "no voting ownership")
	// A zero-power validator's valid stake ownership does not become SDK vote
	// weight merely because coin balances or NFT counts are nonzero.
	v := g.Validators[0]
	v.Stake = 0
	require.NoError(t, native.SetValidator(ctx, v))
	a, e := Build(ctx, native, baseSource(), sdkmath.OneInt())
	require.NoError(t, e)
	total, e := a.TotalBondedTokens(ctx)
	require.NoError(t, e)
	require.True(t, total.IsZero())
}

func TestNativeGovernanceProviderCannotCommitWritesAndSnapshotCannotGoStale(t *testing.T) {
	ctx, _, native, g := nativeFixture(t)
	_, e := Build(ctx, native, sourceFn(func(c context.Context, r Request) (Weights, error) {
		v := g.Validators[0]
		v.Stake++
		require.NoError(t, native.SetValidator(sdk.UnwrapSDKContext(c), v))
		return baseSource().SnapshotWeights(c, r)
	}), sdkmath.OneInt())
	require.ErrorContains(t, e, "modified native")
	v, e := native.GetValidator(ctx, g.Validators[0].OperatorAddress)
	require.NoError(t, e)
	require.EqualValues(t, 100, v.Stake)
	a, e := Build(ctx, native, baseSource(), sdkmath.OneInt())
	require.NoError(t, e)
	child, _ := ctx.CacheContext()
	v.Stake++
	require.NoError(t, native.SetValidator(child, v))
	_, e = a.TotalBondedTokens(child)
	require.ErrorContains(t, e, "changed after")
	total, e := a.TotalBondedTokens(ctx)
	require.NoError(t, e)
	require.True(t, total.Equal(sdkmath.NewInt(100)))
	_, e = a.TotalBondedTokens(ctx.WithBlockHeight(2))
	require.ErrorContains(t, e, "outside")
	_, e = a.TotalBondedTokens(ctx.WithChainID("other"))
	require.ErrorContains(t, e, "outside")
}

func TestNativeGovernanceCustomReservesNeedExplicitValuation(t *testing.T) {
	ctx, _, native, g := nativeFixture(t)
	d := g.Delegations[1]
	d.Stake = nativetypes.NewStakeCoin(sdk.NewCoin("custom", d.Stake.Stake.Amount))
	require.NoError(t, native.SetDelegation(ctx, d))
	_, e := Build(ctx, native, baseSource(), sdkmath.OneInt())
	require.ErrorContains(t, e, "authoritative valuation")
}
