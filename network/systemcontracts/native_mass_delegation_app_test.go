package systemcontracts_test

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const nativeMassPhaseEnv = "MCND_NATIVE_MASS_PHASE"
const nativeMassCountEnv = "MCND_NATIVE_MASS_COUNT"

type nativeMassCheckpoint struct {
	Count                    int
	Infraction, Announcement int64
	BeforeSupply, Burned     string
	CommittedHash, RetryHash []byte
}

// This runs the real EVM and native ledger through ABCI. The commit/evidence
// metadata has the same shape as authenticated CometBFT input, but this is not
// a four-process consensus-signature test. The expensive censuses are explicit
// CI opt-ins: MCND_MASS_DELEGATION_COUNTS=10,100,1000.
func TestOwnedAppNativeMassDelegationLifecycle(t *testing.T) {
	if phase := os.Getenv(nativeMassPhaseEnv); phase != "" {
		n, err := strconv.Atoi(os.Getenv(nativeMassCountEnv))
		require.NoError(t, err)
		runNativeMassDelegationLifecycle(t, os.Getenv(nativeConsensusDaemonFixtureEnv), phase, n)
		return
	}
	counts := os.Getenv("MCND_MASS_DELEGATION_COUNTS")
	if counts == "" {
		counts = "10"
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	for _, value := range strings.Split(counts, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(value))
		require.NoError(t, err)
		require.Contains(t, []int{10, 100, 1000}, n)
		if !t.Run(strconv.Itoa(n), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "mass")
			require.NoError(t, os.Mkdir(dir, 0700))
			for _, phase := range []string{"seed", "run", "restart"} {
				filter := "^TestOwnedAppNativeMassDelegationLifecycle$"
				if phase == "seed" {
					filter = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$"
				}
				child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=20m", "-test.run="+filter)
				child.Env = append(os.Environ(), nativeMassPhaseEnv+"="+phase, nativeMassCountEnv+"="+strconv.Itoa(n),
					nativeConsensusDaemonFixtureEnv+"="+dir, nativeHistoricalConsensusPhaseEnv+"=",
					nativeConsensusFeeRewardsEnv+"=", nativeConsensusIBCV1Env+"=", nativeConsensusUpgradeGovernanceEnv+"=",
					nativeConsensusJailRecoveryEnv+"=", nativeConsensusHeldDowntimeEnv+"=", nativeConsensusNFTDowntimeEnv+"=",
					signedNFTAdmissionPhaseEnv+"=", delLifecycleFixtureEnv+"=", withdrawalGenesisFixtureEnv+"=")
				if phase == "seed" {
					child.Env = append(child.Env, nativeHistoricalConsensusPhaseEnv+"=seed")
				}
				output, err := child.CombinedOutput()
				require.NoError(t, err, "count=%d phase=%s\n%s", n, phase, output)
				if phase == "restart" {
					require.Contains(t, string(output), "actual EVM mass penalty lifecycle passed")
					t.Log(strings.TrimSpace(string(output)))
				}
			}
		}) {
			return
		}
	}
}

func runNativeMassDelegationLifecycle(t *testing.T, dir, phase string, n int) {
	t.Helper()
	require.Contains(t, []int{10, 100, 1000}, n)
	doc, err := cmttypes.GenesisDocFromFile(filepath.Join(dir, "genesis.json"))
	require.NoError(t, err)
	// A finite consensus ceiling is part of the test, not an increase to make
	// the stress pass. Each EVM operation occupies its own bounded block.
	doc.ConsensusParams.Block.MaxGas = 24_000_000
	var expected nativeHistoricalExpectations
	readNativeHistoricalJSON(t, filepath.Join(dir, "expectations.json"), &expected)
	require.Len(t, expected.Validators, 3)
	database, err := dbm.NewDB("native-mass-lifecycle", dbm.GoLevelDBBackend, dir)
	require.NoError(t, err)
	app := ownedapp.NewApp(log.NewNopLogger(), database, true, appOptions{
		flags.FlagHome: filepath.Join(dir, "home"), flags.FlagChainID: doc.ChainID, srvflags.EVMChainID: uint64(990001),
	})
	t.Cleanup(func() { require.NoError(t, app.Close()) })
	l := nativeHistoricalLifecycle{t: t, app: app, doc: doc, expect: expected, target: expected.Validators[2]}
	checkpointPath := filepath.Join(dir, "mass-checkpoint.json")
	var checkpoint nativeMassCheckpoint
	operator := common.HexToAddress(l.target.EVM)
	one := sdkmath.NewIntWithDecimal(1, 18)
	newOwners := n - 1 // The exported target already has one real genesis position.
	if phase == "run" {
		require.Zero(t, app.LastBlockHeight())
		params := doc.ConsensusParams.ToProto()
		init, err := app.InitChain(&abci.RequestInitChain{ChainId: doc.ChainID, InitialHeight: 1, Time: doc.GenesisTime,
			ConsensusParams: &params, AppStateBytes: doc.AppState})
		require.NoError(t, err)
		updates, err := cmttypes.PB2TM.ValidatorUpdates(init.Validators)
		require.NoError(t, err)
		l.set = cmttypes.NewValidatorSet(updates)
		l.commit(l.finalize(nil, nil))
		nativeMassAssertCensus(t, l, operator, 1)
		before := l.supply()
		actorKey, err := registryFixtureKey(3, true)
		require.NoError(t, err)
		actor, err := crypto.ToECDSA(actorKey.Bytes())
		require.NoError(t, err)
		gateway, err := contracts.CompiledABI("MintCandyIsolatedDelegation")
		require.NoError(t, err)
		data, err := gateway.Pack("delegateDEL", operator)
		require.NoError(t, err)
		for i := 0; i < newOwners; i++ {
			ownerKey := nativeMassOwnerKey(t, i)
			owner := crypto.PubkeyToAddress(ownerKey.PublicKey)
			// Existing actor funds finance both principal and gas. No genesis
			// balance rewrites, synthetic native mint, or mocked stake rows.
			// The isolated native profile admits SDK MsgSend, while an arbitrary
			// EVM value CALL is intentionally outside its Gateway allowlist.
			l.signedGovernance(actorKey, banktypes.NewMsgSend(sdk.AccAddress(expected.Actor[:]), sdk.AccAddress(owner[:]),
				sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, one.MulRaw(2)))))
			l.submitSignedEthereum(nativeMassSignedCall(t, l, ownerKey, expected.Gateway, one, data))
			if (i+1)%100 == 0 {
				t.Logf("actual signed new gateway positions: %d/%d (target total=%d)", i+1, newOwners, n)
			}
		}
		nativeMassAssertCensus(t, l, operator, n)
		require.True(t, before.Equal(l.supply()), "funding and wrapping must not issue native DEL")
		// H is after every real deposit, while a later deposit must escape
		// this historical liability even when it merges into an existing row.
		l.commit(l.finalize(nil, nil))
		l.commit(l.finalize(nil, nil))
		checkpoint.Infraction = app.LastBlockHeight()
		l.submitSignedEthereum(nativeMassSignedCall(t, l, actor, expected.Gateway, one, data))
		history, err := app.NativeValidatorKeeper.GetHistoricalInfo(l.snapshot(), checkpoint.Infraction)
		require.NoError(t, err)
		set, err := validatorkeeper.ConsensusSet(history.Valset)
		require.NoError(t, err)
		_, target := set.GetByAddress(common.FromHex(l.target.Consensus))
		require.NotNil(t, target)
		block := l.finalize(nil, []abci.Misbehavior{{Type: abci.MisbehaviorType_DUPLICATE_VOTE,
			Validator: abci.Validator{Address: target.Address, Power: target.VotingPower}, Height: checkpoint.Infraction,
			Time: history.Header.Time, TotalVotingPower: set.TotalVotingPower()}})
		l.commit(block)
		checkpoint.Announcement = app.LastBlockHeight()
		checkpoint.Count = n
		checkpoint.BeforeSupply = before.String()
		// The genesis target has 2 DEL. All new positions are exactly
		// 1 DEL; the actor's additional 1 DEL was funded after H.
		checkpoint.Burned = one.MulRaw(int64(newOwners + 2)).QuoRaw(20).String()
		require.True(t, before.Equal(l.supply()), "announcement must jail before paged financial work")
		progress, err := app.NativeValidatorKeeper.GetNativePenaltyProgress(l.snapshot(), operator[:])
		require.NoError(t, err)
		require.Equal(t, validatorkeeper.NativePenaltyDuplicateVote, progress.Reason)
		require.Equal(t, checkpoint.Infraction, progress.InfractionHeight)
		validator, err := app.NativeValidatorKeeper.GetValidatorByConsAddr(l.snapshot(), sdk.ConsAddress(target.Address))
		require.NoError(t, err)
		require.True(t, validator.Jailed)
		_, paused, count := l.masterValidatorState(operator)
		require.True(t, paused)
		require.Equal(t, int64(1), count, "one Master announcement for the entire census")
		// The same barrier is shared by signed custody changes, manual
		// completion and the automatic executor (separately route-tested).
		require.ErrorIs(t, app.NativeValidatorKeeper.RequireNoPendingNativePenalty(l.snapshot(), operator[:]), validatorkeeper.ErrNativePenaltyPending)
		for i := 0; i < n/2+3; i++ {
			nativeMassAdvance(t, l, checkpoint)
		}
		_, err = app.NativeValidatorKeeper.GetNativePenaltyProgress(l.snapshot(), operator[:])
		require.NoError(t, err, "the restart must happen during an unfinished job")
		checkpoint.CommittedHash = append([]byte(nil), app.LastCommitID().Hash...)
		// Simulate process loss between FinalizeBlock and Commit. Restart
		// must discard this page, then reproduce its AppHash exactly once.
		discarded := l.finalize(nil, nil)
		checkpoint.RetryHash = append([]byte(nil), discarded.AppHash...)
		require.Equal(t, checkpoint.CommittedHash, app.LastCommitID().Hash)
		writeNativeHistoricalJSON(t, checkpointPath, checkpoint)
		return
	}
	require.Equal(t, "restart", phase)
	readNativeHistoricalJSON(t, checkpointPath, &checkpoint)
	require.Equal(t, n, checkpoint.Count)
	require.Equal(t, checkpoint.CommittedHash, app.LastCommitID().Hash)
	replayed := l.finalize(nil, nil)
	require.Equal(t, checkpoint.RetryHash, replayed.AppHash, "crash replay must reproduce the discarded financial page")
	l.commit(replayed)
	finished := false
	for i := 0; i < 2*n+20; i++ {
		pending, err := app.NativeValidatorKeeper.PendingNativePenalty(l.snapshot(), operator[:])
		require.NoError(t, err)
		if !pending {
			finished = true
			break
		}
		nativeMassAdvance(t, l, checkpoint)
		if (i+1)%100 == 0 {
			t.Logf("bounded financial blocks after restart: %d, height=%d", i+1, app.LastBlockHeight())
		}
	}
	require.True(t, finished, "bounded penalty pages must make progress")
	before, ok := sdkmath.NewIntFromString(checkpoint.BeforeSupply)
	require.True(t, ok)
	burned, ok := sdkmath.NewIntFromString(checkpoint.Burned)
	require.True(t, ok)
	require.True(t, before.Sub(burned).Equal(l.supply()), "5%% excludes the deposit funded after the infraction")
	reserve, err := app.NativeValidatorKeeper.GetActiveDELReserve(l.snapshot(), l.target.Operator)
	require.NoError(t, err)
	require.True(t, one.MulRaw(int64(newOwners+3)).Sub(burned).Equal(reserve))
	require.NoError(t, app.NativeValidatorKeeper.RequireNoPendingNativePenalty(l.snapshot(), operator[:]))
	exclusion, err := app.NativeValidatorKeeper.GetNativeDuplicateVoteExclusion(l.snapshot(), sdk.ConsAddress(common.FromHex(l.target.Consensus)))
	require.NoError(t, err)
	require.Equal(t, checkpoint.Infraction, exclusion.InfractionHeight)
	_, paused, count := l.masterValidatorState(operator)
	require.True(t, paused)
	require.Equal(t, int64(1), count)
	settled := l.supply()
	for i := 0; i < 3; i++ {
		l.commit(l.finalize(nil, nil))
		require.True(t, settled.Equal(l.supply()), "completed job cannot burn again")
	}
	t.Logf("actual EVM mass penalty lifecycle passed: positions=%d, burned=%s, height=%d", n, burned, app.LastBlockHeight())
}

func nativeMassAssertCensus(t *testing.T, l nativeHistoricalLifecycle, operator common.Address, count int) {
	t.Helper()
	genesis, err := l.app.NativeValidatorKeeper.ExportGenesis(l.snapshot())
	require.NoError(t, err)
	actual := 0
	for _, row := range genesis.EVMStakeBindings {
		if common.BytesToAddress(row.Validator) == operator && row.Active {
			actual++
		}
	}
	require.Equal(t, count, actual, "count includes the existing genesis position and cannot exceed MaxDelegations")
}

func nativeMassOwnerKey(t *testing.T, index int) *ecdsa.PrivateKey {
	t.Helper()
	// A uint64/index string prevents the 256-key wraparound of old byte-index
	// fixture helpers. These keys belong only to this disposable test network.
	key, err := crypto.ToECDSA(crypto.Keccak256([]byte(fmt.Sprintf("mintcandy mass EVM owner %d", index))))
	require.NoError(t, err)
	return key
}

func nativeMassSignedCall(t *testing.T, l nativeHistoricalLifecycle, key *ecdsa.PrivateKey, target common.Address, value sdkmath.Int, data []byte) *ethtypes.Transaction {
	t.Helper()
	payer := crypto.PubkeyToAddress(key.PublicKey)
	raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: l.app.EVMKeeper.GetNonce(l.snapshot(), payer), To: &target,
		Value: value.BigInt(), Gas: 24_000_000, GasPrice: big.NewInt(1_000_000_000), Data: data})
	signed, err := ethtypes.SignTx(raw, ethtypes.NewEIP155Signer(big.NewInt(990001)), key)
	require.NoError(t, err)
	return signed
}

func nativeMassAdvance(t *testing.T, l nativeHistoricalLifecycle, checkpoint nativeMassCheckpoint) {
	t.Helper()
	before := l.supply()
	block := l.finalize(nil, nil)
	l.commit(block)
	loss := before.Sub(l.supply())
	require.False(t, loss.IsNegative())
	// A row holds at most the target's original 2 DEL plus a protected later
	// 1 DEL. One financial page can burn at most 0.1 DEL, not the whole census.
	require.True(t, loss.LTE(sdkmath.NewIntWithDecimal(1, 17)), "one block must realize at most one stake row")
	if l.app.LastBlockHeight() == checkpoint.Announcement+1 || l.app.LastBlockHeight() == checkpoint.Announcement+2 {
		history, err := l.app.NativeValidatorKeeper.GetHistoricalInfo(l.snapshot(), l.app.LastBlockHeight())
		require.NoError(t, err)
		set, err := validatorkeeper.ConsensusSet(history.Valset)
		require.NoError(t, err)
		require.Equal(t, l.app.LastBlockHeight() == checkpoint.Announcement+1, set.HasAddress(common.FromHex(l.target.Consensus)), "jail removes consensus power at H+2 before all pages finish")
	}
}
