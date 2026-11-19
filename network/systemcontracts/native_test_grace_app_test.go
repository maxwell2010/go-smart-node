//go:build appstate

package systemcontracts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/stretchr/testify/require"
)

const nativeTestGracePhaseEnv = "MCND_NATIVE_TEST_GRACE_PHASE"

// Signed EVM transactions use the real application and private disk database.
// Misbehavior is the authenticated Comet ABCI summary, not a forged raw proof.
func TestOwnedAppNativeTestGraceLifecycle(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	for _, phase := range []string{"seed", "run", "restart"} {
		if !t.Run(phase, func(t *testing.T) {
			filter, historical, custom := "^TestOwnedAppNativeReservelessTokenLifecycle$", "", ""
			if phase == "seed" {
				filter, historical, custom = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$", "seed", "seed"
			}
			child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=3m", "-test.run="+filter)
			child.Env = append(os.Environ(), nativeTestGracePhaseEnv+"="+phase, reservelessNativePhaseEnv+"="+phase,
				nativeConsensusDaemonFixtureEnv+"="+dir, nativeHistoricalConsensusPhaseEnv+"="+historical,
				nativeCustomCollateralGenesisPhaseEnv+"="+custom, userNFTNativePhaseEnv+"=",
				nativeHistoricalMaxNFTPhaseEnv+"=", nativeConsensusFeeRewardsEnv+"=", nativeConsensusIBCV1Env+"=",
				nativeConsensusUpgradeGovernanceEnv+"=", nativeConsensusJailRecoveryEnv+"=", nativeConsensusHeldDowntimeEnv+"=",
				nativeConsensusNFTDowntimeEnv+"=", nativeConsensusFullUpgradeEnv+"=", signedNFTAdmissionPhaseEnv+"=",
				delLifecycleFixtureEnv+"=", withdrawalGenesisFixtureEnv+"=")
			output, err := child.CombinedOutput()
			require.NoError(t, err, "%s", output)
		}) {
			return
		}
	}
}

func runNativeTestGraceLifecycle(t *testing.T, l nativeHistoricalLifecycle, dir, phase string) {
	t.Helper()
	proof := func(height int64) abci.Misbehavior {
		history, err := l.app.NativeValidatorKeeper.GetHistoricalInfo(l.snapshot(), height)
		require.NoError(t, err)
		set, err := validatorkeeper.ConsensusSet(history.Valset)
		require.NoError(t, err)
		_, target := set.GetByAddress(common.FromHex(l.target.Consensus))
		require.NotNil(t, target)
		return abci.Misbehavior{Type: abci.MisbehaviorType_DUPLICATE_VOTE,
			Height: height, Time: history.Header.Time,
			Validator: abci.Validator{Address: target.Address, Power: target.VotingPower}, TotalVotingPower: set.TotalVotingPower()}
	}
	assertUnpunished := func() {
		v, err := l.app.NativeValidatorKeeper.GetValidatorByConsAddr(l.snapshot(), sdk.ConsAddress(common.FromHex(l.target.Consensus)))
		require.NoError(t, err)
		require.False(t, v.Jailed)
		_, err = l.app.NativeValidatorKeeper.GetNativeDuplicateVoteExclusion(l.snapshot(), sdk.ConsAddress(common.FromHex(l.target.Consensus)))
		require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
		jobs, err := l.app.NativeValidatorKeeper.PendingNativeDuplicateVotePenalties(l.snapshot(), 8)
		require.NoError(t, err)
		require.Empty(t, jobs)
	}
	checkpoint := filepath.Join(dir, "native-test-grace-supply.json")
	if phase == "run" {
		// A signed deposit remains available during grace; fee charging is intact.
		l.signedGateway("delegateDEL", sdkmath.NewIntWithDecimal(1, 18), common.HexToAddress(l.target.EVM))
		supply := l.supply().String()
		l.commit(l.finalize(nil, []abci.Misbehavior{proof(1)}))
		assertUnpunished()
		l.commit(l.finalize(nil, nil))
		require.Equal(t, int64(4), l.app.LastBlockHeight())
		require.Equal(t, supply, l.supply().String())
		_, err := l.app.NativeValidatorKeeper.GetSigningWindow(l.snapshot(), l.target.Operator)
		require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
		writeNativeHistoricalJSON(t, checkpoint, supply)
		return
	}
	require.Equal(t, "restart", phase)
	var supply string
	readNativeHistoricalJSON(t, checkpoint, &supply)
	assertUnpunished()
	l.commit(l.finalize(nil, nil))                          // height 5, last grace block
	l.commit(l.finalize(nil, []abci.Misbehavior{proof(3)})) // old proof arrives after grace
	assertUnpunished()
	require.Equal(t, supply, l.supply().String(), "grace evidence never burns MSP retroactively")
	_, err := l.app.NativeValidatorKeeper.GetSigningWindow(l.snapshot(), l.target.Operator)
	require.ErrorIs(t, err, validatorkeeper.ErrNotFound, "the prior height 5 observation is exempt")
	l.commit(l.finalize(nil, []abci.Misbehavior{proof(6)}))
	v, err := l.app.NativeValidatorKeeper.GetValidatorByConsAddr(l.snapshot(), sdk.ConsAddress(common.FromHex(l.target.Consensus)))
	require.NoError(t, err)
	require.True(t, v.Jailed, "new evidence resumes key exclusion without changing genesis")
	exclusion, err := l.app.NativeValidatorKeeper.GetNativeDuplicateVoteExclusion(l.snapshot(), sdk.ConsAddress(common.FromHex(l.target.Consensus)))
	require.NoError(t, err)
	require.Equal(t, int64(6), exclusion.InfractionHeight)
	before, ok := sdkmath.NewIntFromString(supply)
	require.True(t, ok)
	require.True(t, l.supply().LT(before), "post-grace financial penalties resume")
}
