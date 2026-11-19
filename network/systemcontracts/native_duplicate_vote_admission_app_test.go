package systemcontracts_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

const deferredDuplicateAdmissionEnv = "MCND_NATIVE_DEFERRED_DUPLICATE_ADMISSION"

func TestOwnedAppNativeDuplicateVoteAdmissionWithFailedFinancialAppend(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "native-deferred-duplicate")
	require.NoError(t, os.Mkdir(dir, 0700))
	for _, phase := range []string{"seed", "uncommitted"} {
		filter := "^TestOwnedAppNativeHistoricalConsensusLifecycle$"
		if phase == "seed" {
			filter = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$"
		}
		child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=3m", "-test.run="+filter)
		child.Env = append(os.Environ(), nativeHistoricalConsensusPhaseEnv+"="+phase, nativeConsensusDaemonFixtureEnv+"="+dir,
			deferredDuplicateAdmissionEnv+"=1", nativeConsensusFeeRewardsEnv+"=", nativeConsensusIBCV1Env+"=", nativeConsensusUpgradeGovernanceEnv+"=",
			nativeConsensusJailRecoveryEnv+"=", nativeConsensusHeldDowntimeEnv+"=", nativeConsensusNFTDowntimeEnv+"=",
			signedNFTAdmissionPhaseEnv+"=", delLifecycleFixtureEnv+"=", withdrawalGenesisFixtureEnv+"=")
		output, err := child.CombinedOutput()
		require.NoError(t, err, "phase=%s\n%s", phase, output)
		if phase == "uncommitted" {
			require.Contains(t, string(output), "native admission excluded key despite deferred financial append")
		}
	}
}

func runNativeDeferredDuplicateAdmission(l nativeHistoricalLifecycle) {
	t := l.t
	t.Helper()
	k := l.app.NativeValidatorKeeper
	beforeSupply := l.supply()
	before, err := k.GetEVMStakeBinding(l.snapshot(), validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, l.expect.Gateway[:], l.expect.StakeID[:])
	require.NoError(t, err)
	history, err := k.GetHistoricalInfo(l.snapshot(), 2)
	require.NoError(t, err)
	set, err := validatorkeeper.ConsensusSet(history.Valset)
	require.NoError(t, err)
	consensus := common.FromHex(l.target.Consensus)
	_, offender := set.GetByAddress(consensus)
	require.NotNil(t, offender)
	// A real internal EVM append is rejected before it can commit. Native
	// admission has already consumed authenticated FinalizeBlock evidence.
	l.app.EVMKeeper.SetInternalCommitGuard(func(sdk.Context, core.Message, []*ethtypes.Log) error {
		return errors.New("injected financial append rejection")
	})
	block := l.finalize(nil, []abci.Misbehavior{{Type: abci.MisbehaviorType_DUPLICATE_VOTE, Validator: abci.Validator{Address: consensus, Power: offender.VotingPower}, Height: 2, Time: history.Header.Time, TotalVotingPower: set.TotalVotingPower()}})
	excluded, deferred := false, false
	for _, event := range block.Events {
		if event.Type == "decimal_native_duplicate_vote_excluded" {
			excluded = true
		}
		if event.Type == "decimal_native_duplicate_vote_deferred" {
			deferred = true
		}
		require.NotEqual(t, "decimal_native_duplicate_vote_applied", event.Type)
	}
	require.True(t, excluded)
	require.True(t, deferred)
	l.commit(block)
	v, err := k.GetValidatorByConsAddr(l.snapshot(), consensus)
	require.NoError(t, err)
	require.True(t, v.Jailed)
	exclusion, err := k.GetNativeDuplicateVoteExclusion(l.snapshot(), consensus)
	require.NoError(t, err)
	require.Equal(t, int64(5), exclusion.SettledHeight)
	jobs, err := k.PendingNativeDuplicateVotePenalties(l.snapshot(), 8)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.True(t, beforeSupply.Equal(l.supply()))
	after, err := k.GetEVMStakeBinding(l.snapshot(), before.Kind, before.Gateway, before.StakeID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	l.assertMasterPenaltyState(false)
	changes, err := validatorkeeper.ConsensusSet(history.Valset)
	require.NoError(t, err)
	require.Equal(t, int64(10), changes.TotalVotingPower())
	require.Len(t, block.ValidatorUpdates, 1)
	require.Zero(t, block.ValidatorUpdates[0].Power)
	for h := int64(6); h <= 7; h++ {
		l.commit(l.finalize(nil, nil))
	}
	effective, err := k.GetHistoricalInfo(l.snapshot(), 7)
	require.NoError(t, err)
	remaining, err := validatorkeeper.ConsensusSet(effective.Valset)
	require.NoError(t, err)
	require.False(t, remaining.HasAddress(consensus))
	require.Equal(t, int64(8), remaining.TotalVotingPower(), "remaining voting power is not manufactured to replace the excluded key")
	jobs, err = k.PendingNativeDuplicateVotePenalties(l.snapshot(), 8)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.True(t, beforeSupply.Equal(l.supply()))
	t.Log("native admission excluded key despite deferred financial append")
}
