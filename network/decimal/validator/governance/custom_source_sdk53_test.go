//go:build sdk53

package governance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govkeeper "github.com/cosmos/cosmos-sdk/x/gov/keeper"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/gogoproto/proto"
	coinkeeper "github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	nativetypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func customFixture(t *testing.T) (sdk.Context, *nativekeeper.StateKeeper, *coinkeeper.StateKeeper, cointypes.Coin) {
	_, _, _, g := nativeFixture(t)
	for _, i := range []int{1, 2} {
		g.Delegations[i].Stake.Stake.Denom = "custom"
		if i == 1 {
			g.Delegations[i].Stake.ID = "custom"
		}
	}
	nativeKey, coinKey := storetypes.NewKVStoreKey("native-custom"), storetypes.NewKVStoreKey("coin-custom")
	ctx := testutil.DefaultContextWithKeys(map[string]*storetypes.KVStoreKey{"native-custom": nativeKey, "coin-custom": coinKey}, nil, nil).WithBlockHeight(1).WithChainID("mintcandy_300001-1")
	native, err := nativekeeper.NewStateKeeper(runtime.NewKVStoreService(nativeKey))
	require.NoError(t, err)
	require.NoError(t, native.InitGenesis(ctx, g))
	coins, err := coinkeeper.NewStateKeeper(runtime.NewKVStoreService(coinKey))
	require.NoError(t, err)
	require.NoError(t, coins.SetParams(ctx, cointypes.DefaultParams()))
	unit := sdkmath.NewIntWithDecimal(1, 18)
	coin := cointypes.Coin{Denom: "custom", Title: "Custom", Creator: g.Validators[0].RewardAddress, CRR: 100, Volume: unit.MulRaw(100), Reserve: unit.MulRaw(50), MinVolume: sdkmath.ZeroInt(), LimitVolume: unit.MulRaw(1000)}
	require.NoError(t, coins.SetCoin(ctx, coin))
	return ctx, native, coins, coin
}

func TestCustomOwnershipFractionalAllocationConservesAndIgnoresInputOrder(t *testing.T) {
	ctx, native, coins, coin := customFixture(t)
	g, err := native.ExportGenesis(ctx)
	require.NoError(t, err)
	for i, n := range []int64{1, 2, 3, 5} {
		stake := nativetypes.NewStakeCoin(sdk.NewCoin("custom", sdkmath.NewInt(n)))
		if i == 2 {
			stake = nativetypes.NewStakeNFT("actual-token", []uint32{1, 2}, sdk.NewCoin("custom", sdkmath.NewInt(n)))
		}
		g.Delegations[i].Stake = stake
		g.Delegations[i].Delegator = account(t, "d0", byte(10+i))
	}
	coin.Volume = sdkmath.NewInt(11)
	coin.Reserve = sdkmath.NewInt(3)
	require.NoError(t, coins.SetCoin(ctx, coin))
	source := AggregateReserveSource{coins}
	evaluate := func() map[[32]byte]string {
		data, err := proto.Marshal(g)
		require.NoError(t, err)
		weights, err := source.SnapshotWeights(ctx, Request{ChainID: ctx.ChainID(), Height: ctx.BlockHeight(), StateHash: sha256.Sum256(data), NativeGenesis: data})
		require.NoError(t, err)
		sum := new(big.Int)
		byID := map[[32]byte]string{}
		for _, p := range weights.Positions {
			sum.Add(sum, p.Ownership.BigInt())
			byID[p.PositionHash] = p.Ownership.String()
		}
		require.Equal(t, "3000000000000000000", sum.String())
		require.Equal(t, "3000000000000000000", weights.Totals[0].Ownership.String())
		return byID
	}
	first := evaluate()
	for i, j := 0, len(g.Delegations)-1; i < j; i, j = i+1, j-1 {
		g.Delegations[i], g.Delegations[j] = g.Delegations[j], g.Delegations[i]
	}
	require.Equal(t, first, evaluate())
}

func TestCustomValuationMissingAndMalformedCoinFailsClosed(t *testing.T) {
	ctx, native, coins, _ := customFixture(t)
	g, err := native.ExportGenesis(ctx)
	require.NoError(t, err)
	for i := range g.Delegations {
		if g.Delegations[i].Stake.Stake.Denom == "custom" {
			g.Delegations[i].Stake.Stake.Denom = "missing"
		}
	}
	data, err := proto.Marshal(g)
	require.NoError(t, err)
	_, err = (AggregateReserveSource{coins}).SnapshotWeights(ctx, Request{ChainID: ctx.ChainID(), Height: ctx.BlockHeight(), NativeGenesis: data})
	require.Error(t, err)
	_, err = Build(ctx, native, AggregateReserveSource{}, sdkmath.OneInt())
	require.ErrorContains(t, err, "coin state")
}

func TestCustomOwnershipIncludesNFTAndKeepsAuthoritativeValidatorPower(t *testing.T) {
	ctx, native, coins, coin := customFixture(t)
	a, err := Build(ctx, native, AggregateReserveSource{coins}, sdkmath.OneInt())
	require.NoError(t, err)
	total, err := a.TotalBondedTokens(ctx)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(100), total)
	// 40 custom units (half from NFT reserve) liquidate to20 DEL. Together
	// with60 DEL direct ownership this yields80 DEL ownership, not100 power.
	require.Equal(t, new(big.Int).Mul(sdkmath.NewIntWithDecimal(80, 18).BigInt(), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)).String(), a.validators[0].ownership.String())
	count := 0
	require.NoError(t, a.IterateDelegations(ctx, sdk.AccAddress(bytes.Repeat([]byte{2}, 20)), func(_ int64, d stakingtypes.DelegationI) bool {
		count++
		expected := sdkmath.NewIntFromBigInt(new(big.Int).Mul(sdkmath.NewIntWithDecimal(20, 18).BigInt(), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)))
		require.Equal(t, sdkmath.LegacyNewDecFromInt(expected), d.GetShares())
		return false
	}))
	require.Equal(t, 1, count)
	coin.Reserve = coin.Reserve.AddRaw(1)
	require.NoError(t, coins.SetCoin(ctx, coin))
	_, err = a.TotalBondedTokens(ctx)
	require.ErrorContains(t, err, "valuation dependency changed")
}

func TestCustomSnapshotRejectsUnbackedAggregate(t *testing.T) {
	ctx, native, coins, coin := customFixture(t)
	coin.Volume = sdkmath.NewIntWithDecimal(39, 18)
	require.NoError(t, coins.SetCoin(ctx, coin))
	_, err := Build(ctx, native, AggregateReserveSource{coins}, sdkmath.OneInt())
	require.ErrorContains(t, err, "invalid aggregate")
}

func TestCustomSaleReturnArithmeticAndCurve(t *testing.T) {
	// With50% CRR, selling2 of10 units from1000 reserve returns360,
	// subject to the pinned floating-point implementation's final-unit rounding.
	result, err := saleReturn(sdkmath.NewInt(10), sdkmath.NewInt(1000), sdkmath.NewInt(2), 50)
	require.NoError(t, err)
	require.True(t, result.GTE(sdkmath.NewInt(359)) && result.LTE(sdkmath.NewInt(360)))
	huge := sdkmath.NewIntFromBigInt(new(big.Int).Lsh(big.NewInt(1), 200))
	half := sdkmath.NewIntFromBigInt(new(big.Int).Lsh(big.NewInt(1), 199))
	result, err = saleReturn(huge, huge, half, 100)
	require.NoError(t, err)
	require.Equal(t, half, result)
	_, err = saleReturn(sdkmath.ZeroInt(), sdkmath.OneInt(), sdkmath.OneInt(), 50)
	require.Error(t, err)
}

func TestActualSDK53TallyCustomAndNFTDelegatorOverride(t *testing.T) {
	ctx, native, _, coin := customFixture(t)
	// A separate real gov store is supplied by a new multistore, carrying the
	// same validated native genesis and coin state.
	g, err := native.ExportGenesis(ctx)
	require.NoError(t, err)
	nk, ck, gk := storetypes.NewKVStoreKey("n"), storetypes.NewKVStoreKey("c"), storetypes.NewKVStoreKey("g")
	ctx = testutil.DefaultContextWithKeys(map[string]*storetypes.KVStoreKey{"n": nk, "c": ck, "g": gk}, nil, nil).WithBlockHeight(1).WithChainID(ctx.ChainID())
	native, err = nativekeeper.NewStateKeeper(runtime.NewKVStoreService(nk))
	require.NoError(t, err)
	require.NoError(t, native.InitGenesis(ctx, g))
	fresh, err := coinkeeper.NewStateKeeper(runtime.NewKVStoreService(ck))
	require.NoError(t, err)
	require.NoError(t, fresh.SetParams(ctx, cointypes.DefaultParams()))
	require.NoError(t, fresh.SetCoin(ctx, coin))
	snapshot, err := Build(ctx, native, AggregateReserveSource{fresh}, sdkmath.OneInt())
	require.NoError(t, err)
	auth := tallyAuth{}
	authority, err := auth.AddressCodec().BytesToString(auth.GetModuleAddress(govtypes.ModuleName))
	require.NoError(t, err)
	keeper := govkeeper.NewKeeper(codec.NewProtoCodec(codectypes.NewInterfaceRegistry()), runtime.NewKVStoreService(gk), auth, nil, snapshot, nil, nil, govtypes.DefaultConfig(), authority)
	p := v1.DefaultParams()
	p.Quorum = "0.1"
	require.NoError(t, keeper.Params.Set(ctx, p))
	vote(t, ctx, keeper, 1, option(v1.OptionYes))
	vote(t, ctx, keeper, 2, option(v1.OptionNo))
	passed, _, result, err := keeper.Tally(context.Context(ctx), v1.Proposal{Id: 1})
	require.NoError(t, err)
	require.True(t, passed)
	require.Equal(t, "75", result.YesCount)
	require.Equal(t, "25", result.NoCount)
}
