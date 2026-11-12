package systemcontracts_test

import (
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const nativeHistoricalConsensusPhaseEnv = "MCND_NATIVE_HISTORICAL_CONSENSUS_PHASE"
const historicalLifecyclePassed = "fresh native ABCI historical penalty and payout lifecycle passed"

type nativeHistoricalExpectations struct {
	Actor, Gateway, Wrapped, Master common.Address
	StakeID                         common.Hash
	Validators                      []nativeConsensusFixtureValidator
}

type nativeHistoricalCheckpoint struct {
	BeforeHash, AfterHash []byte
	BeforeSupply          string
}

// The seed process exports actual signed contract custody. All subsequent
// processes use one private database and already-decoded ABCI metadata only.
func TestOwnedAppNativeHistoricalConsensusLifecycle(t *testing.T) {
	if phase := os.Getenv(nativeHistoricalConsensusPhaseEnv); phase != "" {
		runNativeHistoricalConsensusLifecycle(t, os.Getenv(nativeConsensusDaemonFixtureEnv), phase)
		return
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "native-historical")
	require.NoError(t, os.Mkdir(dir, 0700))
	for _, phase := range []string{"seed", "uncommitted", "commit", "restart"} {
		if !t.Run(phase, func(t *testing.T) {
			filter := "^TestOwnedAppNativeHistoricalConsensusLifecycle$"
			if phase == "seed" {
				filter = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$"
			}
			child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=3m", "-test.run="+filter)
			child.Env = append(os.Environ(), nativeHistoricalConsensusPhaseEnv+"="+phase, nativeConsensusDaemonFixtureEnv+"="+dir,
				nativeConsensusFeeRewardsEnv+"=", nativeConsensusIBCV1Env+"=", nativeConsensusUpgradeGovernanceEnv+"=",
				nativeConsensusJailRecoveryEnv+"=", nativeConsensusHeldDowntimeEnv+"=", nativeConsensusNFTDowntimeEnv+"=",
				signedNFTAdmissionPhaseEnv+"=", delLifecycleFixtureEnv+"=", withdrawalGenesisFixtureEnv+"=")
			output, err := child.CombinedOutput()
			require.NoError(t, err, "%s", output)
			if phase == "restart" {
				require.Contains(t, string(output), historicalLifecyclePassed)
			}
		}) {
			return
		}
	}
}

type nativeHistoricalLifecycle struct {
	t      *testing.T
	app    *ownedapp.App
	doc    *cmttypes.GenesisDoc
	expect nativeHistoricalExpectations
	target nativeConsensusFixtureValidator
	set    *cmttypes.ValidatorSet
}

func runNativeHistoricalConsensusLifecycle(t *testing.T, dir, phase string) {
	t.Helper()
	doc, err := cmttypes.GenesisDocFromFile(filepath.Join(dir, "genesis.json"))
	require.NoError(t, err)
	require.Equal(t, int64(1), doc.InitialHeight)
	require.Equal(t, int64(5), doc.ConsensusParams.Evidence.MaxAgeNumBlocks)
	require.Equal(t, 4*time.Second, doc.ConsensusParams.Evidence.MaxAgeDuration)
	var expected nativeHistoricalExpectations
	readNativeHistoricalJSON(t, filepath.Join(dir, "expectations.json"), &expected)
	require.Len(t, expected.Validators, 3)
	database, err := dbm.NewDB("native-historical-lifecycle", dbm.GoLevelDBBackend, dir)
	require.NoError(t, err)
	app := ownedapp.NewApp(log.NewNopLogger(), database, true, appOptions{
		flags.FlagHome: filepath.Join(dir, "home"), flags.FlagChainID: doc.ChainID, srvflags.EVMChainID: uint64(990001),
	})
	t.Cleanup(func() { require.NoError(t, app.Close()) })
	l := nativeHistoricalLifecycle{t: t, app: app, doc: doc, expect: expected, target: expected.Validators[2]}
	checkpointPath := filepath.Join(dir, "historical-checkpoint.json")
	var checkpoint nativeHistoricalCheckpoint
	if phase == "uncommitted" {
		require.Zero(t, app.LastBlockHeight())
		consensus := doc.ConsensusParams.ToProto()
		init, err := app.InitChain(&abci.RequestInitChain{ChainId: doc.ChainID, InitialHeight: 1, Time: doc.GenesisTime,
			ConsensusParams: &consensus, AppStateBytes: doc.AppState})
		require.NoError(t, err)
		validators, err := cmttypes.PB2TM.ValidatorUpdates(init.Validators)
		require.NoError(t, err)
		require.Len(t, validators, 3)
		l.set = cmttypes.NewValidatorSet(validators)
		require.Equal(t, int64(10), l.set.TotalVotingPower())
		l.commit(l.finalize(nil, nil))
		clock, err := app.NativeValidatorKeeper.NativeEvidenceBlockAtSnapshot(l.context(1))
		require.NoError(t, err)
		require.Equal(t, validatorkeeper.CommittedEvidenceBlock{Height: 1, Time: doc.GenesisTime}, clock)
		l.signedGateway("delegateDEL", sdkmath.NewIntWithDecimal(1, 18), common.HexToAddress(l.target.EVM))
		l.signedGateway("withdraw", sdkmath.ZeroInt(), common.HexToAddress(l.target.EVM), expected.Wrapped, sdkmath.NewIntWithDecimal(1, 18).BigInt())
		l.commit(l.finalize(nil, nil))
		require.Equal(t, int64(4), app.LastBlockHeight())
		checkpoint.BeforeHash = app.LastCommitID().Hash
		checkpoint.BeforeSupply = l.supply().String()
		writeNativeHistoricalJSON(t, checkpointPath, checkpoint)
		l.assertNativePenaltyState(false)
		l.assertMasterPenaltyState(false)
		l.finalizePenalty()
		require.Equal(t, int64(4), app.LastBlockHeight(), "FinalizeBlock alone cannot persist historical settlement")
		require.Equal(t, checkpoint.BeforeHash, app.LastCommitID().Hash)
		return
	}

	readNativeHistoricalJSON(t, checkpointPath, &checkpoint)
	beforeSupply, ok := sdkmath.NewIntFromString(checkpoint.BeforeSupply)
	require.True(t, ok)
	burned := sdkmath.NewIntWithDecimal(1, 17)
	if phase == "commit" {
		require.Equal(t, int64(4), app.LastBlockHeight())
		require.Equal(t, checkpoint.BeforeHash, app.LastCommitID().Hash)
		require.True(t, beforeSupply.Equal(l.supply()))
		// The committed app hash authenticates EVM storage before the
		// first reopened block initializes the EVM denomination globals.
		l.assertNativePenaltyState(false)
		l.commit(l.finalizePenalty())
		l.assertNativePenaltyState(true)
		l.assertMasterPenaltyState(true)
		require.True(t, beforeSupply.Sub(burned).Equal(l.supply()))
		checkpoint.AfterHash = app.LastCommitID().Hash
		writeNativeHistoricalJSON(t, checkpointPath, checkpoint)
		return
	}
	require.Equal(t, "restart", phase)
	require.Equal(t, int64(5), app.LastBlockHeight())
	require.Equal(t, checkpoint.AfterHash, app.LastCommitID().Hash)
	l.assertNativePenaltyState(true)
	clock, err := app.NativeValidatorKeeper.NativeEvidenceBlockAtSnapshot(l.context(5))
	require.NoError(t, err)
	require.Equal(t, int64(5), clock.Height)
	beneficiary := sdk.AccAddress(expected.Actor[:])
	beforePayout := app.BankKeeper.GetBalance(l.snapshot(), beneficiary, identity.BaseDenom).Amount
	for height := int64(6); height <= 11; height++ {
		l.commit(l.finalize(nil, nil))
		if height == 6 {
			l.assertMasterPenaltyState(true)
		}
		row, err := app.NativeValidatorKeeper.GetEVMFrozenBinding(l.snapshot(), expected.Gateway[:], make([]byte, 32))
		require.NoError(t, err)
		wantBalance := beforePayout
		if height >= 10 {
			wantBalance = wantBalance.Add(sdkmath.NewIntWithDecimal(95, 16))
		}
		require.Equal(t, height < 10, row.Active, "payout waits until both evidence limits have strictly elapsed")
		require.True(t, wantBalance.Equal(app.BankKeeper.GetBalance(l.snapshot(), beneficiary, identity.BaseDenom).Amount))
		require.True(t, beforeSupply.Sub(burned).Equal(l.supply()))
		if height == 6 || height == 7 {
			history, err := app.NativeValidatorKeeper.GetHistoricalInfo(l.snapshot(), height)
			require.NoError(t, err)
			set, err := validatorkeeper.ConsensusSet(history.Valset)
			require.NoError(t, err)
			require.Equal(t, height == 6, set.HasAddress(common.FromHex(l.target.Consensus)), "the removal takes effect at H+2")
		}
	}
	_, err = app.NativeValidatorKeeper.GetUndelegation(l.snapshot(), beneficiary.String(), l.target.Operator)
	require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
	exclusion, err := app.NativeValidatorKeeper.GetNativeDuplicateVoteExclusion(l.snapshot(), sdk.ConsAddress(common.FromHex(l.target.Consensus)))
	require.NoError(t, err)
	require.Equal(t, validatorkeeper.NativeDuplicateVoteExclusion{InfractionHeight: 2, SettledHeight: 5}, exclusion)
	t.Log(historicalLifecyclePassed)
}

func (l nativeHistoricalLifecycle) context(height int64) sdk.Context {
	return l.app.NewUncachedContext(false, cmtproto.Header{ChainID: l.doc.ChainID, Height: height,
		Time: l.doc.GenesisTime.Add(time.Duration(height-1) * time.Second), ProposerAddress: common.FromHex(l.expect.Validators[0].Consensus)})
}

func (l nativeHistoricalLifecycle) snapshot() sdk.Context { return l.context(l.app.LastBlockHeight()) }

func (l nativeHistoricalLifecycle) supply() sdkmath.Int {
	return l.app.BankKeeper.GetSupply(l.snapshot(), identity.BaseDenom).Amount
}

func (l nativeHistoricalLifecycle) finalize(txs [][]byte, misbehavior []abci.Misbehavior) *abci.ResponseFinalizeBlock {
	t := l.t
	t.Helper()
	height := l.app.LastBlockHeight() + 1
	current, next := l.set, l.set
	var last abci.CommitInfo
	if height > 1 {
		keeper := l.app.NativeValidatorKeeper
		g, err := keeper.ExportGenesis(l.snapshot())
		require.NoError(t, err)
		require.Len(t, g.ScheduledValidatorSets, 2)
		require.Equal(t, height, g.ScheduledValidatorSets[0].EffectiveHeight)
		require.Equal(t, height+1, g.ScheduledValidatorSets[1].EffectiveHeight)
		current, err = validatorkeeper.ConsensusSet(g.ScheduledValidatorSets[0].Validators)
		require.NoError(t, err)
		next, err = validatorkeeper.ConsensusSet(g.ScheduledValidatorSets[1].Validators)
		require.NoError(t, err)
		history, err := keeper.GetHistoricalInfo(l.snapshot(), height-1)
		require.NoError(t, err)
		previous, err := validatorkeeper.ConsensusSet(history.Valset)
		require.NoError(t, err)
		for _, validator := range previous.Validators {
			last.Votes = append(last.Votes, abci.VoteInfo{Validator: abci.Validator{Address: validator.Address, Power: validator.VotingPower},
				BlockIdFlag: cmtproto.BlockIDFlagCommit})
		}
	}
	hash := sha256.Sum256(sdk.Uint64ToBigEndian(uint64(height)))
	result, err := l.app.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: l.context(height).BlockTime(), Hash: hash[:],
		NextValidatorsHash: next.Hash(), ProposerAddress: current.Validators[0].Address, DecidedLastCommit: last, Txs: txs, Misbehavior: misbehavior})
	require.NoError(t, err)
	return result
}

func (l nativeHistoricalLifecycle) commit(result *abci.ResponseFinalizeBlock) {
	l.t.Helper()
	_, err := l.app.Commit()
	require.NoError(l.t, err)
	require.Equal(l.t, result.AppHash, l.app.LastCommitID().Hash)
}

func (l nativeHistoricalLifecycle) signedGateway(method string, value sdkmath.Int, args ...interface{}) {
	t := l.t
	t.Helper()
	gateway, err := contracts.CompiledABI("MintCandyIsolatedDelegation")
	require.NoError(t, err)
	data, err := gateway.Pack(method, args...)
	require.NoError(t, err)
	key, err := registryFixtureKey(3, true)
	require.NoError(t, err)
	privateKey, err := crypto.ToECDSA(key.Bytes())
	require.NoError(t, err)
	require.Equal(t, l.expect.Actor, crypto.PubkeyToAddress(privateKey.PublicKey))
	signer := ethtypes.NewEIP155Signer(big.NewInt(990001))
	raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: l.app.EVMKeeper.GetNonce(l.snapshot(), l.expect.Actor), To: &l.expect.Gateway,
		Value: value.BigInt(), Gas: 24_000_000, GasPrice: big.NewInt(1_000_000_000_000), Data: data})
	signed, err := ethtypes.SignTx(raw, signer, privateKey)
	require.NoError(t, err)
	msg := &evmtypes.MsgEthereumTx{}
	require.NoError(t, msg.FromSignedEthereumTx(signed, signer))
	tx, err := msg.BuildTx(l.app.TxConfig().NewTxBuilder(), identity.BaseDenom)
	require.NoError(t, err)
	encoded, err := l.app.TxConfig().TxEncoder()(tx)
	require.NoError(t, err)
	block := l.finalize([][]byte{encoded}, nil)
	require.Len(t, block.TxResults, 1)
	require.Zero(t, block.TxResults[0].Code, block.TxResults[0].Log)
	var messages sdk.TxMsgData
	require.NoError(t, l.app.AppCodec().Unmarshal(block.TxResults[0].Data, &messages))
	require.Len(t, messages.MsgResponses, 1)
	var response evmtypes.MsgEthereumTxResponse
	require.NoError(t, response.Unmarshal(messages.MsgResponses[0].Value))
	require.Empty(t, response.VmError)
	l.commit(block)
}

func (l nativeHistoricalLifecycle) finalizePenalty() *abci.ResponseFinalizeBlock {
	t := l.t
	t.Helper()
	require.Equal(t, int64(4), l.app.LastBlockHeight())
	history, err := l.app.NativeValidatorKeeper.GetHistoricalInfo(l.snapshot(), 2)
	require.NoError(t, err)
	set, err := validatorkeeper.ConsensusSet(history.Valset)
	require.NoError(t, err)
	_, target := set.GetByAddress(common.FromHex(l.target.Consensus))
	require.NotNil(t, target)
	require.Equal(t, int64(2), target.VotingPower)
	// Only the summary delivered by the already-validated ABCI boundary is
	// supplied. The test has no consensus signers or raw evidence objects.
	block := l.finalize(nil, []abci.Misbehavior{{Type: abci.MisbehaviorType_DUPLICATE_VOTE,
		Validator: abci.Validator{Address: target.Address, Power: target.VotingPower}, Height: 2,
		Time: history.Header.Time, TotalVotingPower: set.TotalVotingPower()}})
	var applied []abci.Event
	for _, event := range block.Events {
		require.NotEqual(t, "decimal_native_duplicate_vote_deferred", event.Type)
		if event.Type == "decimal_native_duplicate_vote_applied" {
			applied = append(applied, event)
		}
	}
	require.Len(t, applied, 1)
	attributes := make(map[string]string)
	for _, attribute := range applied[0].Attributes {
		attributes[attribute.Key] = attribute.Value
	}
	require.Equal(t, l.target.Operator, attributes["operator"])
	require.Equal(t, "2", attributes["infraction_height"])
	require.Equal(t, sdkmath.NewIntWithDecimal(1, 17).String(), attributes["burned"])
	changes, err := cmttypes.PB2TM.ValidatorUpdates(block.ValidatorUpdates)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, target.Address, changes[0].Address)
	require.Zero(t, changes[0].VotingPower)
	return block
}

func (l nativeHistoricalLifecycle) assertNativePenaltyState(applied bool) {
	t := l.t
	t.Helper()
	keeper, ctx := l.app.NativeValidatorKeeper, l.snapshot()
	consensus := sdk.ConsAddress(common.FromHex(l.target.Consensus))
	require.NotEqual(t, common.HexToAddress(l.target.EVM).Bytes(), []byte(consensus))
	validator, err := keeper.GetValidatorByConsAddr(ctx, consensus)
	require.NoError(t, err)
	require.Equal(t, l.target.Operator, validator.OperatorAddress)
	require.Equal(t, applied, validator.Jailed)
	exclusion, err := keeper.GetNativeDuplicateVoteExclusion(ctx, consensus)
	principal, cursor := sdkmath.NewIntWithDecimal(1, 18), int64(0)
	if applied {
		require.NoError(t, err)
		require.Equal(t, validatorkeeper.NativeDuplicateVoteExclusion{InfractionHeight: 2, SettledHeight: 5}, exclusion)
		principal, cursor = sdkmath.NewIntWithDecimal(95, 16), 1
	} else {
		require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
	}
	jobs, err := keeper.PendingNativeDuplicateVotePenalties(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, jobs)
	active, err := keeper.GetEVMStakeBinding(ctx, validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, l.expect.Gateway[:], l.expect.StakeID[:])
	require.NoError(t, err)
	require.True(t, principal.Add(sdkmath.NewIntWithDecimal(1, 18)).Equal(active.RawAmount))
	require.Equal(t, cursor, common.BytesToHash(active.PenaltyCursor).Big().Int64())
	lots, err := keeper.GetActiveDELProvenance(ctx, active)
	require.NoError(t, err)
	require.Len(t, lots, 2)
	require.Zero(t, lots[0].FundedHeight)
	require.True(t, principal.Equal(lots[0].Amount))
	require.Equal(t, int64(2), lots[1].FundedHeight)
	require.True(t, sdkmath.NewIntWithDecimal(1, 18).Equal(lots[1].Amount))
	frozen, err := keeper.GetEVMFrozenBinding(ctx, l.expect.Gateway[:], make([]byte, 32))
	require.NoError(t, err)
	require.True(t, frozen.Active)
	require.True(t, principal.Equal(frozen.RawAmount))
	require.Equal(t, l.context(5).BlockTime().Unix(), common.BytesToHash(frozen.CompletionTimestamp).Big().Int64())
}

func (l nativeHistoricalLifecycle) assertMasterPenaltyState(applied bool) {
	t := l.t
	t.Helper()
	cursor := int64(0)
	if applied {
		cursor = 1
	}
	master, err := contracts.CompiledABI("NewNetworkMasterValidator")
	require.NoError(t, err)
	data, err := master.Pack("getValidatorState", common.HexToAddress(l.target.EVM))
	require.NoError(t, err)
	child, _ := l.snapshot().CacheContext()
	child = child.WithGasMeter(storetypes.NewGasMeter(3_000_000))
	msg := core.Message{From: l.expect.Actor, To: &l.expect.Master, Value: new(big.Int), Data: data, GasLimit: 500_000,
		GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), SkipNonceChecks: true, SkipFromEOACheck: true}
	response, err := l.app.EVMKeeper.ApplyMessage(child, statedb.New(child, l.app.EVMKeeper, statedb.NewEmptyTxConfig()), msg, nil, false, false, true)
	require.NoError(t, err)
	require.Empty(t, response.VmError)
	state, err := master.Unpack("getValidatorState", response.Ret)
	require.NoError(t, err)
	require.Len(t, state, 3)
	require.Equal(t, applied, state[1].(bool))
	require.Equal(t, cursor, state[2].(*big.Int).Int64())
}

func readNativeHistoricalJSON(t *testing.T, path string, value interface{}) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, value))
}

func writeNativeHistoricalJSON(t *testing.T, path string, value interface{}) {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
}
