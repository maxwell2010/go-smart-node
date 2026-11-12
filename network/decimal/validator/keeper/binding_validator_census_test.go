package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestActiveStakePenaltyPageIncludesHeldNFTAndOrdinarySources(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	direct, nft := seedGatewayDELNFTCompanion(t, ctx, k)
	direct.Amount = sdkmath.NewInt(12)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, direct, direct.Amount))
	held := gatewayDELHoldFixture(2000, 2020, 3)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, held, held.Amount))
	nft.PenaltyCursor = stateWord(0)
	nft.PenaltyCursor[5] = 1
	plan, err := k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &nft}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(ctx, plan))

	control := &penaltySeekControl{}
	bounded, err := NewStateKeeperWithDomain(penaltySeekService{service, direct.Validator[:], control}, bindingTestChain, 990001)
	require.NoError(t, err)
	first, next, err := bounded.GetActiveStakePenaltyPage(ctx, direct.Validator[:], nil, 1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.NotEmpty(t, next)
	require.Equal(t, 1, control.iteratorCalls)
	require.Equal(t, 1, control.nextCalls)
	require.Equal(t, 1, control.closeCalls)
	cached, _ := ctx.CacheContext()
	rest, end, err := k.GetActiveStakePenaltyPage(cached, direct.Validator[:], next, 2)
	require.NoError(t, err)
	require.Len(t, rest, 2)
	require.Nil(t, end)
	require.Equal(t, nft.StakeID, rest[1].StakeID)
	require.Equal(t, nft.PenaltyCursor, rest[1].PenaltyCursor)
	require.ElementsMatch(t, [][]byte{direct.StakeID.Bytes(), held.StakeID.Bytes()}, [][]byte{first[0].StakeID, rest[0].StakeID})
	require.ElementsMatch(t, []types.StakeType{types.StakeType_Coin, types.StakeType_CoinHold},
		[]types.StakeType{first[0].NativeTarget.StakeType, rest[0].NativeTarget.StakeType})
	other, end, err := k.GetActiveStakePenaltyPage(cached, stateAddress(2), nil, 1)
	require.NoError(t, err)
	require.Empty(t, other)
	require.Nil(t, end)
	_, _, err = k.GetActiveStakePenaltyPage(ctx, stateAddress(2), next, 1)
	require.ErrorContains(t, err, "invalid native validator binding continuation")
}

func TestValidatorFrozenIndexTracksBothRedelegationEndpointsAtGenesis(t *testing.T) {
	ctx, service, _ := environment(t)
	ctx = ctx.WithChainID(bindingTestChain).WithBlockHeight(10)
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	g := bindingGenesisState(t, false)
	owner, source := addr(t, identity.Bech32PrefixAccAddr, 2), addr(t, identity.Bech32PrefixValAddr, 1)
	destination := addr(t, identity.Bech32PrefixValAddr, 2)
	entry := types.RedelegationEntry{CreationHeight: 7, CompletionTime: time.Unix(2000, 0).UTC(), Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, sdkmath.OneInt()))}
	hash, err := types.NativeQueueEntryHash(2, owner, source, destination, &entry)
	require.NoError(t, err)
	g.Redelegations = []types.Redelegation{{Delegator: owner, ValidatorSrc: source, ValidatorDst: destination, Entries: []types.RedelegationEntry{entry}}}
	coin := penaltyCoinFixture()
	frozen := types.EVMFrozenBinding{
		Gateway: coin.Gateway, FrozenIndex: stateWord(0), SourceKind: coin.Kind, SourceStakeID: coin.StakeID,
		SourceValidator: coin.Validator, DestinationValidator: stateAddress(2), Beneficiary: coin.Beneficiary,
		FreezeType: 2, CompletionTimestamp: stateWord(2000), RawAmount: coin.RawAmount, ReportedReserveAmount: sdkmath.ZeroInt(),
		ReserveAsset: coin.ReserveAsset, NativeTarget: coin.NativeTarget, NativeEntryHash: hash, Active: true,
	}
	g.EVMFrozenBindings = []types.EVMFrozenBinding{frozen}
	require.NoError(t, k.InitGenesis(ctx, g))
	for _, validator := range [][]byte{frozen.SourceValidator, frozen.DestinationValidator} {
		rows, next, err := k.GetValidatorFrozenBindingsPage(ctx, validator, nil, 1)
		require.NoError(t, err)
		require.Equal(t, g.EVMFrozenBindings, rows)
		require.Nil(t, next)
	}
	record, err := recordFromFrozen(&frozen)
	require.NoError(t, err)
	require.NoError(t, service.OpenKVStore(ctx).Delete(validatorFrozenBindingKey(frozen.DestinationValidator, record.key)))
	_, _, err = k.GetValidatorFrozenBindingsPage(ctx, frozen.SourceValidator, nil, 1)
	require.ErrorContains(t, err, "reverse index mismatch")
	_, err = k.ExportGenesis(ctx)
	require.ErrorContains(t, err, "reverse index mismatch")
}

func TestValidatorFrozenIndexUpdatesWithWithdrawalAndRollsBack(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	position := gatewayDELFixture()
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, position.Amount))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: validatorFrozenBindingPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	amount, deadline := position.Amount, time.Unix(2010, 0)
	position.Amount = sdkmath.ZeroInt()
	require.ErrorContains(t, broken.ApplyGatewayDELWithdrawal(ctx, position, common.Hash{}, amount, deadline), "injected write failure")
	unchanged, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, unchanged)
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, common.Hash{}, amount, deadline))
	cached, _ := ctx.CacheContext()
	pending, end, err := k.GetValidatorFrozenBindingsPage(cached, position.Validator[:], nil, 1)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Nil(t, end)
	ctx = ctx.WithBlockHeight(11).WithBlockTime(deadline)
	require.NoError(t, k.ApplyWithdrawalCompletion(ctx, pending[0]))
	cached, _ = ctx.CacheContext()
	rows, end, err := k.GetValidatorFrozenBindingsPage(cached, position.Validator[:], nil, 1)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Nil(t, end)
	closed, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, closed.EVMFrozenBindings, 1)
	require.False(t, closed.EVMFrozenBindings[0].Active)
	require.Empty(t, closed.Undelegations)
}

func TestLegacyValidatorFrozenCensusIsUnavailableButClaimsWork(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	require.NoError(t, service.OpenKVStore(ctx).Delete([]byte{validatorFrozenIndexMarker}))
	position := gatewayDELFixture()
	amount, deadline := position.Amount, time.Unix(2010, 0)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, amount))
	position.Amount = sdkmath.ZeroInt()
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, common.Hash{}, amount, deadline))
	_, _, err := k.GetValidatorFrozenBindingsPage(ctx, position.Validator[:], nil, 1)
	require.ErrorIs(t, err, ErrValidatorFrozenIndexUnavailable)
	pending, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, pending.EVMFrozenBindings, 1)
	require.NoError(t, k.ApplyWithdrawalCompletion(ctx.WithBlockHeight(11).WithBlockTime(deadline), pending.EVMFrozenBindings[0]))
	_, _, err = k.GetValidatorFrozenBindingsPage(ctx, position.Validator[:], nil, 1)
	require.ErrorIs(t, err, ErrValidatorFrozenIndexUnavailable)
}
