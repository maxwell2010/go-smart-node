//go:build appstate

package systemcontracts_test

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/stretchr/testify/require"
)

const nativeTestGracePhaseEnv = "MCND_NATIVE_TEST_GRACE_PHASE"
const nativeTestGraceDowntimeEnv = "MCND_NATIVE_TEST_GRACE_DOWNTIME"

func TestOwnedAppNativeTestGraceDowntimeLifecycle(t *testing.T) {
	t.Setenv(nativeTestGraceDowntimeEnv, "1")
	TestOwnedAppNativeTestGraceLifecycle(t)
}

// Signed EVM transactions use the real application and private disk database.
// Misbehavior is the authenticated Comet ABCI summary, not a forged raw proof.
func TestOwnedAppNativeTestGraceLifecycle(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
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
		depositTarget := l.target.EVM
		if os.Getenv(nativeTestGraceDowntimeEnv) == "1" {
			depositTarget = l.expect.Validators[0].EVM
		}
		l.signedGateway("delegateDEL", sdkmath.NewIntWithDecimal(1, 18), common.HexToAddress(depositTarget))
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
	if os.Getenv(nativeTestGraceDowntimeEnv) == "1" {
		runNativeGraceDowntimeResumption(t, l, supply)
		return
	}
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

// Feed valid identity/power metadata with one absent validator and retain more
// than two thirds of total power. Only post-grace samples may form a violation.
func runNativeGraceDowntimeResumption(t *testing.T, l nativeHistoricalLifecycle, beforeSupply string) {
	t.Helper()
	params, err := l.app.NativeValidatorKeeper.GetParams(l.snapshot())
	require.NoError(t, err)
	consensus := sdk.ConsAddress(common.FromHex(l.target.Consensus))
	for sample := int64(1); sample <= params.SignedBlocksWindow+2; sample++ {
		height := l.app.LastBlockHeight() + 1
		g, err := l.app.NativeValidatorKeeper.ExportGenesis(l.snapshot())
		require.NoError(t, err)
		current, err := validatorkeeper.ConsensusSet(g.ScheduledValidatorSets[0].Validators)
		require.NoError(t, err)
		next, err := validatorkeeper.ConsensusSet(g.ScheduledValidatorSets[1].Validators)
		require.NoError(t, err)
		history, err := l.app.NativeValidatorKeeper.GetHistoricalInfo(l.snapshot(), height-1)
		require.NoError(t, err)
		previous, err := validatorkeeper.ConsensusSet(history.Valset)
		require.NoError(t, err)
		var last abci.CommitInfo
		var signed int64
		for _, validator := range previous.Validators {
			flag := cmtproto.BlockIDFlagCommit
			if bytes.Equal(validator.Address, consensus) {
				flag = cmtproto.BlockIDFlagAbsent
			} else {
				signed += validator.VotingPower
			}
			last.Votes = append(last.Votes, abci.VoteInfo{Validator: abci.Validator{
				Address: validator.Address, Power: validator.VotingPower}, BlockIdFlag: flag})
		}
		require.Greater(t, signed*3, previous.TotalVotingPower()*2, "the fixture never disables the consensus quorum rule")
		hash := sha256.Sum256(sdk.Uint64ToBigEndian(uint64(height)))
		result, err := l.app.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: l.context(height).BlockTime(), Hash: hash[:],
			NextValidatorsHash: next.Hash(), ProposerAddress: current.Validators[0].Address, DecidedLastCommit: last})
		require.NoError(t, err)
		l.commit(result)
		validator, err := l.app.NativeValidatorKeeper.GetValidatorByConsAddr(l.snapshot(), consensus)
		require.NoError(t, err)
		if validator.Jailed {
			require.GreaterOrEqual(t, sample, params.SignedBlocksWindow, "grace observations never count toward jail")
			_, err := l.app.NativeValidatorKeeper.GetNativeDuplicateVoteExclusion(l.snapshot(), consensus)
			require.ErrorIs(t, err, validatorkeeper.ErrNotFound, "downtime never permanently excludes the key")
			before, ok := sdkmath.NewIntFromString(beforeSupply)
			require.True(t, ok)
			require.True(t, l.supply().LT(before), "downtime resumes its financial penalty")
			return
		}
		require.Equal(t, beforeSupply, l.supply().String(), "no burn before a full post-grace violation")
	}
	t.Fatal("post-grace downtime never resumed automatic jail")
}
