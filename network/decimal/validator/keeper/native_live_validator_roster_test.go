package keeper

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestNativeLiveValidatorRosterIsOptInAtGenesis(t *testing.T) {
	ctx, _, keeper := bindingEnvironment(t, false)
	_, err := keeper.NativeLiveValidatorOperators(ctx)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, keeper.SetValidator(ctx, validator(t, 3, 0)))
	_, err = keeper.NativeLiveValidatorOperators(ctx)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorContains(t, keeper.InitializeNativeLiveValidatorRoster(ctx.WithBlockHeight(0)), "requires stake provenance")

	ctx, keeper, _ = delProvenanceGenesis(t)
	require.ErrorContains(t, keeper.InitializeNativeLiveValidatorRoster(ctx), "requires fresh genesis")
	started, _ := ctx.CacheContext()
	require.NoError(t, keeper.RecordNativeEvidenceBlock(started))
	require.ErrorContains(t, keeper.InitializeNativeLiveValidatorRoster(started.WithBlockHeight(0)), "before the first block")
	failed, _ := ctx.CacheContext()
	require.NoError(t, keeper.InitializeNativeLiveValidatorRoster(failed.WithBlockHeight(0)))
	_, err = keeper.NativeLiveValidatorOperators(ctx)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, keeper.InitializeNativeLiveValidatorRoster(ctx.WithBlockHeight(0)))
	require.ErrorContains(t, keeper.InitializeNativeLiveValidatorRoster(ctx.WithBlockHeight(0)), "already initialized")
	roster, err := keeper.NativeLiveValidatorOperators(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{addr(t, identity.Bech32PrefixValAddr, 1), addr(t, identity.Bech32PrefixValAddr, 2)}, roster)
	corrupt, _ := ctx.CacheContext()
	require.NoError(t, keeper.service.OpenKVStore(corrupt).Set([]byte{nativeLiveValidatorRosterKey}, []byte{nativeLiveValidatorRosterVersion, 1}))
	_, err = keeper.NativeLiveValidatorOperators(corrupt)
	require.ErrorContains(t, err, "invalid native live validator roster encoding")
}

func TestNativeLiveValidatorRosterExclusionFreesOneRegistrationSlot(t *testing.T) {
	ctx, keeper, _ := delProvenanceGenesis(t)
	require.NoError(t, keeper.InitializeNativeLiveValidatorRoster(ctx.WithBlockHeight(0)))
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize).WithBlockHeight(10).WithBlockTime(time.Unix(2010, 0).UTC())
	newValidator := func(id byte) types.Validator {
		v := validator(t, id, 0)
		v.Status = types.BondStatus_Unbonded
		return v
	}
	for id := byte(MaxNativeLiveValidators); id > 2; id-- {
		require.NoError(t, keeper.SetValidator(ctx, newValidator(id)))
	}
	want := make([]string, 0, MaxNativeLiveValidators)
	for id := byte(1); id <= MaxNativeLiveValidators; id++ {
		want = append(want, addr(t, identity.Bech32PrefixValAddr, id))
	}
	roster, err := keeper.NativeLiveValidatorOperators(ctx)
	require.NoError(t, err)
	require.Equal(t, want, roster)
	replacement := newValidator(MaxNativeLiveValidators + 1)
	replacementConsensus, err := replacement.GetConsAddr()
	require.NoError(t, err)
	require.ErrorIs(t, keeper.SetValidator(ctx, replacement), ErrNativeLiveValidatorRosterFull)
	_, err = keeper.GetValidatorByConsAddr(ctx, replacementConsensus)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = keeper.GetValidator(ctx, replacement.OperatorAddress)
	require.ErrorIs(t, err, ErrNotFound)

	offender, err := keeper.GetValidator(ctx, want[1])
	require.NoError(t, err)
	oldConsensus, err := offender.GetConsAddr()
	require.NoError(t, err)
	candidate := queuedDuplicateVote(t, 2, 2, 4)
	candidate.ConsensusAddress = oldConsensus
	_, err = keeper.enqueueNativeDuplicateVoteCandidates(ctx, []NativeDuplicateVoteCandidate{candidate})
	require.NoError(t, err)
	jobs, err := keeper.PendingNativeDuplicateVotePenalties(ctx, 1)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	broken, err := NewStateKeeperWithDomain(failingService{service: keeper.service, prefix: nativeLiveValidatorRosterKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.FinalizeNativeDuplicateVotePenalty(ctx, jobs[0]), "injected write failure")
	_, err = keeper.GetNativeDuplicateVoteExclusion(ctx, oldConsensus)
	require.ErrorIs(t, err, ErrNotFound)
	remaining, err := keeper.PendingNativeDuplicateVotePenalties(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, jobs, remaining)
	stillLive, err := keeper.GetValidatorByConsAddr(ctx, oldConsensus)
	require.NoError(t, err)
	require.False(t, stillLive.Jailed)
	roster, err = keeper.NativeLiveValidatorOperators(ctx)
	require.NoError(t, err)
	require.Equal(t, want, roster)

	require.NoError(t, keeper.FinalizeNativeDuplicateVotePenalty(ctx, jobs[0]))
	retired, err := keeper.GetValidatorByConsAddr(ctx, oldConsensus)
	require.NoError(t, err)
	require.Equal(t, offender.OperatorAddress, retired.OperatorAddress)
	require.True(t, retired.Jailed)
	roster, err = keeper.NativeLiveValidatorOperators(ctx)
	require.NoError(t, err)
	require.Len(t, roster, MaxNativeLiveValidators-1)
	require.NotContains(t, roster, offender.OperatorAddress)
	oldKeyAlias := retired
	oldKeyAlias.OperatorAddress = replacement.OperatorAddress
	require.ErrorIs(t, keeper.SetValidator(ctx, oldKeyAlias), ErrNativeConsensusKeyPermanentlyExcluded)
	retired.Jailed = false
	require.ErrorIs(t, keeper.SetValidator(ctx, retired), ErrNativeConsensusKeyPermanentlyExcluded)

	broken, err = NewStateKeeperWithDomain(failingService{service: keeper.service, prefix: validatorPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.SetValidator(ctx, replacement), "injected write failure")
	unchanged, err := keeper.NativeLiveValidatorOperators(ctx)
	require.NoError(t, err)
	require.Equal(t, roster, unchanged)
	_, err = keeper.GetValidatorByConsAddr(ctx, replacementConsensus)
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, keeper.SetValidator(ctx, replacement))
	reopened, err := NewStateKeeperWithDomain(keeper.service, bindingTestChain, 990001)
	require.NoError(t, err)
	actual, err := reopened.NativeLiveValidatorOperators(ctx)
	require.NoError(t, err)
	require.Equal(t, append(roster, replacement.OperatorAddress), actual)
	all, err := reopened.GetValidators(ctx)
	require.NoError(t, err)
	require.Len(t, all, MaxNativeLiveValidators+1, "old validator metadata remains available")
	require.ErrorIs(t, reopened.SetValidator(ctx, newValidator(MaxNativeLiveValidators+2)), ErrNativeLiveValidatorRosterFull)
}
