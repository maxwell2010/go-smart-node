//go:build sdk53

package governance

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"cosmossdk.io/collections"
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
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	nativetypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

var _ govtypes.StakingKeeper = (*Sessions)(nil)

func sessionGovFixture(t *testing.T) (sdk.Context, *nativekeeper.StateKeeper, *nativetypes.GenesisState, *Sessions, *govkeeper.Keeper, *nativeReadCount, *int) {
	t.Helper()
	_, _, _, g := nativeFixture(t)
	key, govKey := storetypes.NewKVStoreKey("native-scoped"), storetypes.NewKVStoreKey("gov-scoped")
	ctx := testutil.DefaultContextWithKeys(map[string]*storetypes.KVStoreKey{"native-scoped": key, "gov-scoped": govKey}, nil, nil).WithBlockHeight(1).WithChainID("mintcandy_300001-1").WithBlockTime(time.Unix(1000, 0).UTC())
	count := &nativeReadCount{}
	native, e := nativekeeper.NewStateKeeper(countingService{runtime.NewKVStoreService(key), count})
	require.NoError(t, e)
	require.NoError(t, native.InitGenesis(ctx, g))
	count.gets = 0
	calls := new(int)
	reader, e := NewSessions(native, sourceFn(func(c context.Context, r Request) (Weights, error) {
		*calls++
		return baseSource().SnapshotWeights(c, r)
	}), sdkmath.OneInt(), func(c sdk.Context) any { return c.MultiStore().GetKVStore(key) })
	require.NoError(t, e)
	auth := tallyAuth{}
	authority, e := auth.AddressCodec().BytesToString(auth.GetModuleAddress(govtypes.ModuleName))
	require.NoError(t, e)
	keeper := govkeeper.NewKeeper(codec.NewProtoCodec(codectypes.NewInterfaceRegistry()), runtime.NewKVStoreService(govKey), auth, nil, reader, nil, nil, govtypes.DefaultConfig(), authority)
	p := v1.DefaultParams()
	p.Quorum = "0.1"
	require.NoError(t, keeper.Params.Set(ctx, p))
	return ctx, native, g, reader, keeper, count, calls
}
func proposalVote(t *testing.T, ctx sdk.Context, k *govkeeper.Keeper, proposal uint64, id byte, option v1.VoteOption) {
	t.Helper()
	raw := sdk.AccAddress(bytes.Repeat([]byte{id}, 20))
	require.NoError(t, k.Votes.Set(ctx, collections.Join(proposal, raw), v1.Vote{ProposalId: proposal, Voter: account(t, identity.Bech32PrefixAccAddr, id), Options: []*v1.WeightedVoteOption{{Option: option, Weight: "1"}}}))
}

func TestSDK53SessionsRefreshForRepeatedTalliesAtSameHeight(t *testing.T) {
	ctx, native, g, reader, k, _, calls := sessionGovFixture(t)
	for _, id := range []uint64{1, 2} {
		proposalVote(t, ctx, k, id, 1, v1.OptionYes)
		proposalVote(t, ctx, k, id, 2, v1.OptionNo)
	}
	require.NoError(t, reader.Run(ctx, func(sc sdk.Context) error {
		_, _, first, e := k.Tally(sc, v1.Proposal{Id: 1})
		if e != nil {
			return e
		}
		require.Equal(t, "60", first.YesCount)
		require.Equal(t, "40", first.NoCount)
		v := g.Validators[0]
		v.Stake = 200
		if e = native.SetValidator(sc, v); e != nil {
			return e
		}
		_, _, second, e := k.Tally(sc, v1.Proposal{Id: 2})
		if e != nil {
			return e
		}
		require.Equal(t, "120", second.YesCount)
		require.Equal(t, "80", second.NoCount)
		return nil
	}))
	require.Equal(t, 2, *calls)
}

func TestSDK53FailedScopedTallyPreservesVotesAndNewSessionRetries(t *testing.T) {
	ctx, _, _, reader, k, _, calls := sessionGovFixture(t)
	proposalVote(t, ctx, k, 1, 1, v1.OptionYes)
	var leaked sdk.Context
	require.ErrorContains(t, reader.Run(ctx, func(sc sdk.Context) error {
		leaked = sc
		_, _, _, e := k.Tally(sc, v1.Proposal{Id: 1})
		if e != nil {
			return e
		}
		return errors.New("discard this operation")
	}), "discard this operation")
	present, e := k.Votes.Has(ctx, collections.Join(uint64(1), sdk.AccAddress(bytes.Repeat([]byte{1}, 20))))
	require.NoError(t, e)
	require.True(t, present)
	_, e = reader.TotalBondedTokens(leaked)
	require.ErrorContains(t, e, "closed")
	require.NoError(t, reader.Run(ctx, func(sc sdk.Context) error {
		_, _, result, e := k.Tally(sc, v1.Proposal{Id: 1})
		require.Equal(t, "100", result.YesCount)
		return e
	}))
	require.Equal(t, 2, *calls)
}

func TestSDK53TallyQueriesAreScopedAndNeverDeleteStoredVotes(t *testing.T) {
	ctx, _, _, reader, k, _, calls := sessionGovFixture(t)
	require.NoError(t, k.Proposals.Set(ctx, 1, v1.Proposal{Id: 1, Status: v1.StatusVotingPeriod}))
	proposalVote(t, ctx, k, 1, 1, v1.OptionYes)
	proposalVote(t, ctx, k, 1, 2, v1.OptionNo)
	query, e := reader.QueryServer(k)
	require.NoError(t, e)
	for i := 0; i < 2; i++ {
		result, e := query.TallyResult(ctx, &v1.QueryTallyResultRequest{ProposalId: 1})
		require.NoError(t, e)
		require.Equal(t, "60", result.Tally.YesCount)
		require.Equal(t, "40", result.Tally.NoCount)
	}
	present, e := k.Votes.Has(ctx, collections.Join(uint64(1), sdk.AccAddress(bytes.Repeat([]byte{2}, 20))))
	require.NoError(t, e)
	require.True(t, present)
	require.Equal(t, 2, *calls)
}

func TestSDK53SessionNativeReadCountDoesNotGrowWithVotingAccounts(t *testing.T) {
	measure := func(extra int) int {
		ctx, _, _, reader, k, count, calls := sessionGovFixture(t)
		proposalVote(t, ctx, k, 1, 1, v1.OptionYes)
		for i := 0; i < extra; i++ {
			proposalVote(t, ctx, k, 1, byte(i+4), v1.OptionNo)
		}
		count.gets = 0
		require.NoError(t, reader.Run(ctx, func(sc sdk.Context) error {
			_, _, result, e := k.Tally(sc, v1.Proposal{Id: 1})
			require.Equal(t, "100", result.YesCount)
			return e
		}))
		require.Equal(t, 1, *calls)
		return count.gets
	}
	baseline, many := measure(0), measure(50)
	require.Positive(t, baseline)
	require.Equal(t, baseline, many, "native fingerprint scans must not run per voter")
	t.Logf("native KV Get calls: 1 voter=%d, 51 voters=%d", baseline, many)
}

type changeNativePowerHook struct {
	govtypes.MultiGovHooks
	native    *nativekeeper.StateKeeper
	validator nativetypes.Validator
}

func (h changeNativePowerHook) AfterProposalVotingPeriodEnded(ctx context.Context, id uint64) error {
	if id == 1 {
		v := h.validator
		v.Stake = 200
		return h.native.SetValidator(sdk.UnwrapSDKContext(ctx), v)
	}
	return nil
}

func TestSDK53CanonicalEndBlockRefreshesAfterPriorProposalNativeChange(t *testing.T) {
	ctx, native, g, reader, k, _, calls := sessionGovFixture(t)
	k.SetHooks(changeNativePowerHook{native: native, validator: g.Validators[0]})
	start, end := ctx.BlockTime().Add(-time.Hour), ctx.BlockTime().Add(-time.Second)
	for _, id := range []uint64{1, 2} {
		require.NoError(t, k.Proposals.Set(ctx, id, v1.Proposal{Id: id, Status: v1.StatusVotingPeriod, VotingStartTime: &start, VotingEndTime: &end}))
		require.NoError(t, k.ActiveProposalsQueue.Set(ctx, collections.Join(end, id), id))
		proposalVote(t, ctx, k, id, 1, v1.OptionYes)
		proposalVote(t, ctx, k, id, 2, v1.OptionNo)
	}
	require.NoError(t, reader.EndBlock(ctx, k))
	first, e := k.Proposals.Get(ctx, 1)
	require.NoError(t, e)
	second, e := k.Proposals.Get(ctx, 2)
	require.NoError(t, e)
	require.Equal(t, v1.StatusPassed, first.Status)
	require.Equal(t, v1.StatusPassed, second.Status)
	require.Equal(t, "60", first.FinalTallyResult.YesCount)
	require.Equal(t, "120", second.FinalTallyResult.YesCount)
	require.Equal(t, 2, *calls)
}

func TestSDK53CanonicalEndBlockLateTallyFailureDiscardsEarlierEffects(t *testing.T) {
	ctx, native, g, reader, k, _, calls := sessionGovFixture(t)
	original := reader.source
	reader.source = sourceFn(func(c context.Context, r Request) (Weights, error) {
		if *calls >= 1 {
			return Weights{}, errors.New("second valuation unavailable")
		}
		return original.SnapshotWeights(c, r)
	})
	k.SetHooks(changeNativePowerHook{native: native, validator: g.Validators[0]})
	start, end := ctx.BlockTime().Add(-time.Hour), ctx.BlockTime().Add(-time.Second)
	for _, id := range []uint64{1, 2} {
		require.NoError(t, k.Proposals.Set(ctx, id, v1.Proposal{Id: id, Status: v1.StatusVotingPeriod, VotingStartTime: &start, VotingEndTime: &end}))
		require.NoError(t, k.ActiveProposalsQueue.Set(ctx, collections.Join(end, id), id))
		proposalVote(t, ctx, k, id, 1, v1.OptionYes)
	}
	require.ErrorContains(t, reader.EndBlock(ctx, k), "second valuation unavailable")
	first, e := k.Proposals.Get(ctx, 1)
	require.NoError(t, e)
	require.Equal(t, v1.StatusVotingPeriod, first.Status)
	stored, e := native.GetValidator(ctx, g.Validators[0].OperatorAddress)
	require.NoError(t, e)
	require.EqualValues(t, 100, stored.Stake)
	present, e := k.Votes.Has(ctx, collections.Join(uint64(1), sdk.AccAddress(bytes.Repeat([]byte{1}, 20))))
	require.NoError(t, e)
	require.True(t, present)
}
