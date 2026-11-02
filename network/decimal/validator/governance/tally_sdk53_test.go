//go:build sdk53

package governance

import (
	"bytes"
	"context"
	"testing"
	"time"

	"cosmossdk.io/collections"
	coreaddress "cosmossdk.io/core/address"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govkeeper "github.com/cosmos/cosmos-sdk/x/gov/keeper"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	nativetypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

var _ govtypes.StakingKeeper = (*Adapter)(nil)

// Only auth address decoding is exercised by the real SDK tally. Bank,
// distribution and message execution are deliberately absent from this fixture.
type tallyAuth struct{}

func (tallyAuth) AddressCodec() coreaddress.Codec {
	return address.NewBech32Codec(identity.Bech32PrefixAccAddr)
}
func (tallyAuth) GetModuleAddress(name string) sdk.AccAddress {
	return authtypes.NewModuleAddress(name)
}
func (tallyAuth) GetModuleAccount(context.Context, string) sdk.ModuleAccountI {
	panic("unexpected module account query during tally")
}
func (tallyAuth) GetAccount(context.Context, sdk.AccAddress) sdk.AccountI {
	panic("unexpected account query during tally")
}
func (tallyAuth) SetModuleAccount(context.Context, sdk.ModuleAccountI) {
	panic("unexpected account write during tally")
}

func tallyFixture(t *testing.T, g *nativetypes.GenesisState, scale sdkmath.Int) (sdk.Context, *govkeeper.Keeper) {
	t.Helper()
	nativeKey, govKey := storetypes.NewKVStoreKey("native-tally"), storetypes.NewKVStoreKey("gov-tally")
	ctx := testutil.DefaultContextWithKeys(map[string]*storetypes.KVStoreKey{"native-tally": nativeKey, "gov-tally": govKey}, nil, nil).WithBlockHeight(1).WithChainID("mintcandy_300001-1")
	native, e := nativekeeper.NewStateKeeper(runtime.NewKVStoreService(nativeKey))
	require.NoError(t, e)
	require.NoError(t, native.InitGenesis(ctx, g))
	snapshot, e := Build(ctx, native, baseSource(), scale)
	require.NoError(t, e)
	auth := tallyAuth{}
	authority, e := auth.AddressCodec().BytesToString(auth.GetModuleAddress(govtypes.ModuleName))
	require.NoError(t, e)
	keeper := govkeeper.NewKeeper(codec.NewProtoCodec(codectypes.NewInterfaceRegistry()), runtime.NewKVStoreService(govKey), auth, nil, snapshot, nil, nil, govtypes.DefaultConfig(), authority)
	p := v1.DefaultParams()
	p.Quorum = "0.1"
	require.NoError(t, keeper.Params.Set(ctx, p))
	return ctx, keeper
}
func vote(t *testing.T, ctx sdk.Context, k *govkeeper.Keeper, id byte, options ...*v1.WeightedVoteOption) {
	t.Helper()
	raw := sdk.AccAddress(bytes.Repeat([]byte{id}, 20))
	require.NoError(t, k.Votes.Set(ctx, collections.Join(uint64(1), raw), v1.Vote{ProposalId: 1, Voter: account(t, identity.Bech32PrefixAccAddr, id), Options: options}))
}
func option(value v1.VoteOption) *v1.WeightedVoteOption {
	return &v1.WeightedVoteOption{Option: value, Weight: "1"}
}

func TestSDK53ActualTallyPreservesValidatorDelegatorOverridesAndNFTs(t *testing.T) {
	_, _, _, g := nativeFixture(t)
	ctx, k := tallyFixture(t, g, sdkmath.OneInt())
	vote(t, ctx, k, 1, option(v1.OptionYes))
	vote(t, ctx, k, 2, option(v1.OptionNo))
	passed, burn, result, e := k.Tally(ctx, v1.Proposal{Id: 1})
	require.NoError(t, e)
	require.True(t, passed)
	require.False(t, burn)
	require.Equal(t, "60", result.YesCount)
	require.Equal(t, "40", result.NoCount)
	require.Equal(t, "0", result.AbstainCount)
	// Validator's 20 self-owned units are deducted then counted once with its
	// own vote. The delegator's combined coin+NFT 40 overrides the validator.
}

func TestSDK53ActualTallyWithoutValidatorVoteOnlyCountsActiveDelegator(t *testing.T) {
	_, _, _, g := nativeFixture(t)
	ctx, k := tallyFixture(t, g, sdkmath.OneInt())
	vote(t, ctx, k, 2, option(v1.OptionYes))
	passed, _, result, e := k.Tally(ctx, v1.Proposal{Id: 1})
	require.NoError(t, e)
	require.True(t, passed)
	require.Equal(t, "40", result.YesCount)
	require.Equal(t, "0", result.NoCount)
}

func TestSDK53ActualTallyKeepsFractionalAndPartiallyUnbondedOwnership(t *testing.T) {
	_, _, _, g := nativeFixture(t)
	g.Validators[0].Stake = 1
	unit := sdkmath.NewIntWithDecimal(1, 18)
	g.Delegations = []nativetypes.Delegation{
		{Delegator: g.Validators[0].RewardAddress, Validator: g.Validators[0].OperatorAddress, Stake: nativetypes.NewStakeCoin(sdk.NewCoin("del", unit.MulRaw(3).QuoRaw(10)))},
		{Delegator: account(t, identity.Bech32PrefixAccAddr, 2), Validator: g.Validators[0].OperatorAddress, Stake: nativetypes.NewStakeNFT("fractional-nft", []uint32{1}, sdk.NewCoin("del", unit.MulRaw(7).QuoRaw(10)))},
	}
	g.Undelegations = []nativetypes.Undelegation{{Delegator: g.Validators[0].RewardAddress, Validator: g.Validators[0].OperatorAddress, Entries: []nativetypes.UndelegationEntry{{CreationHeight: 1, CompletionTime: time.Unix(100000, 0).UTC(), Stake: nativetypes.NewStakeCoin(sdk.NewCoin("del", unit.QuoRaw(5)))}}}}
	// Both positions floor to zero consensus units individually, while the
	// validator has one authoritative unit. Ownership remains 30/70, not zero.
	ctx, k := tallyFixture(t, g, sdkmath.NewInt(100))
	vote(t, ctx, k, 1, option(v1.OptionYes))
	vote(t, ctx, k, 2, option(v1.OptionNo))
	passed, _, result, e := k.Tally(ctx, v1.Proposal{Id: 1})
	require.NoError(t, e)
	require.False(t, passed)
	require.Equal(t, "30", result.YesCount)
	require.Equal(t, "70", result.NoCount)
}

func TestSDK53ActualTallyRetainsWeightedDelegatorOverrides(t *testing.T) {
	_, _, _, g := nativeFixture(t)
	ctx, k := tallyFixture(t, g, sdkmath.OneInt())
	vote(t, ctx, k, 1, option(v1.OptionYes))
	vote(t, ctx, k, 2, &v1.WeightedVoteOption{Option: v1.OptionYes, Weight: "0.25"}, &v1.WeightedVoteOption{Option: v1.OptionNo, Weight: "0.75"})
	passed, _, result, e := k.Tally(ctx, v1.Proposal{Id: 1})
	require.NoError(t, e)
	require.True(t, passed)
	require.Equal(t, "70", result.YesCount)
	require.Equal(t, "30", result.NoCount)
}

func TestSDK53ActualTallyDoesNotCountZeroReserveNFTByQuantity(t *testing.T) {
	_, _, _, g := nativeFixture(t)
	g.Delegations[2].Stake.Stake.Amount = sdkmath.ZeroInt()
	g.Delegations[2].Stake.Holds = nil
	g.Validators[0].Stake = 80
	ctx, k := tallyFixture(t, g, sdkmath.OneInt())
	vote(t, ctx, k, 1, option(v1.OptionYes))
	vote(t, ctx, k, 2, option(v1.OptionNo))
	passed, _, result, e := k.Tally(ctx, v1.Proposal{Id: 1})
	require.NoError(t, e)
	require.True(t, passed)
	require.Equal(t, "60", result.YesCount)
	require.Equal(t, "20", result.NoCount)
}
