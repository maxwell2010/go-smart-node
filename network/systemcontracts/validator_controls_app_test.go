//go:build appstate

package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
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
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const validatorControlsPhaseEnv = "MCND_VALIDATOR_CONTROLS_PHASE"

// Fresh isolated database and deterministic CI-only Ethereum operators. No RPC,
// server service or external signer is contacted by any phase of this test.
func TestOwnedAppNativeValidatorSelfControls(t *testing.T) {
	if phase := os.Getenv(validatorControlsPhaseEnv); phase != "" && phase != "seed" {
		runNativeValidatorSelfControls(t, os.Getenv(nativeConsensusDaemonFixtureEnv), phase)
		return
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	for _, phase := range []string{"seed", "run", "restart"} {
		if !t.Run(phase, func(t *testing.T) {
			filter, historical := "^TestOwnedAppNativeValidatorSelfControls$", ""
			if phase == "seed" {
				filter, historical = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$", "seed"
			}
			child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=3m", "-test.run="+filter)
			child.Env = append(os.Environ(), validatorControlsPhaseEnv+"="+phase, nativeConsensusDaemonFixtureEnv+"="+dir,
				nativeHistoricalConsensusPhaseEnv+"="+historical, nativeCustomCollateralGenesisPhaseEnv+"=",
				nativeHistoricalMaxNFTPhaseEnv+"=", nativeConsensusFeeRewardsEnv+"=", nativeConsensusIBCV1Env+"=",
				nativeConsensusUpgradeGovernanceEnv+"=", nativeConsensusJailRecoveryEnv+"=", nativeConsensusHeldDowntimeEnv+"=",
				nativeConsensusNFTDowntimeEnv+"=", nativeConsensusFullUpgradeEnv+"=", signedNFTAdmissionPhaseEnv+"=",
				delLifecycleFixtureEnv+"=", withdrawalGenesisFixtureEnv+"=")
			output, err := child.CombinedOutput()
			require.NoError(t, err, "%s", output)
			for _, line := range strings.Split(string(output), "\n") {
				if strings.Contains(line, "signed self-control method=") {
					t.Log(strings.TrimSpace(line))
				}
			}
		}) {
			return
		}
	}
}

func runNativeValidatorSelfControls(t *testing.T, dir, phase string) {
	doc, err := cmttypes.GenesisDocFromFile(filepath.Join(dir, "genesis.json"))
	require.NoError(t, err)
	var expected nativeHistoricalExpectations
	readNativeHistoricalJSON(t, filepath.Join(dir, "expectations.json"), &expected)
	var state ownedapp.GenesisState
	require.NoError(t, json.Unmarshal(doc.AppState, &state))
	var profile ownedapp.WithdrawalProfile
	require.NoError(t, json.Unmarshal(state[ownedapp.WithdrawalGenesisKey], &profile))
	var bank banktypes.GenesisState
	require.NoError(t, json.Unmarshal(state[banktypes.ModuleName], &bank))
	funding := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntWithDecimal(300, 18)))
	for keyIndex := 0; keyIndex < 2; keyIndex++ {
		key, err := registryFixtureKey(keyIndex, true)
		require.NoError(t, err)
		op := common.BytesToAddress(key.PubKey().Address())
		require.Equal(t, common.HexToAddress(expected.Validators[keyIndex].EVM), op)
		found, credited := false, false
		for i := range bank.Balances {
			if bank.Balances[i].Address == sdk.AccAddress(expected.Actor.Bytes()).String() {
				bank.Balances[i].Coins = bank.Balances[i].Coins.Sub(funding...)
				found = true
			}
			if bank.Balances[i].Address == sdk.AccAddress(op.Bytes()).String() {
				bank.Balances[i].Coins = bank.Balances[i].Coins.Add(funding...)
				credited = true
			}
		}
		require.True(t, found)
		if !credited {
			bank.Balances = append(bank.Balances, banktypes.Balance{Address: sdk.AccAddress(op.Bytes()).String(), Coins: funding})
		}
	}
	state[banktypes.ModuleName], err = json.Marshal(bank)
	require.NoError(t, err)
	doc.AppState, err = json.Marshal(state)
	require.NoError(t, err)
	database, err := dbm.NewDB("validator-controls", dbm.GoLevelDBBackend, dir)
	require.NoError(t, err)
	app := ownedapp.NewApp(log.NewNopLogger(), database, true, appOptions{flags.FlagHome: filepath.Join(dir, "controls-home"), flags.FlagChainID: doc.ChainID, srvflags.EVMChainID: uint64(990001)})
	t.Cleanup(func() { require.NoError(t, app.Close()) })
	l := nativeHistoricalLifecycle{t: t, app: app, doc: doc, expect: expected, target: expected.Validators[0]}
	first := expected.Validators[0]
	second := expected.Validators[1]
	if phase == "restart" && os.Getenv(validatorAvatarGenesisPhaseEnv) == "import-restart" {
		assertNativeAvatarImportedRestart(t, l, dir)
		return
	}
	if phase == "restart" {
		validator, err := app.NativeValidatorKeeper.GetValidator(l.snapshot(), first.Operator)
		require.NoError(t, err)
		require.True(t, validator.Online)
		require.Equal(t, "Self operator A", validator.Description.Moniker)
		require.Equal(t, "0.050000000000000000", validator.Commission.String())
		avatar, err := app.NativeValidatorKeeper.GetNativeValidatorAvatar(l.snapshot(), first.Operator)
		require.NoError(t, err)
		require.Equal(t, "ipfs://bafyfixture/avatar.png", avatar)
		return
	}
	params := doc.ConsensusParams.ToProto()
	init, err := app.InitChain(&abci.RequestInitChain{ChainId: doc.ChainID, InitialHeight: 1, Time: doc.GenesisTime, ConsensusParams: &params, AppStateBytes: doc.AppState})
	require.NoError(t, err)
	validators, err := cmttypes.PB2TM.ValidatorUpdates(init.Validators)
	require.NoError(t, err)
	l.set = cmttypes.NewValidatorSet(validators)
	l.commit(l.finalize(nil, nil))
	if os.Getenv(validatorAvatarGenesisPhaseEnv) == "import" {
		assertNativeAvatarGenesisImported(t, l, dir)
	}
	master, err := contracts.CompiledABI("NewNetworkMasterValidator")
	require.NoError(t, err)
	signedCall := func(index int, method string, args ...interface{}) *ethtypes.Transaction {
		data, err := master.Pack(method, args...)
		require.NoError(t, err)
		key, err := registryFixtureKey(index, true)
		require.NoError(t, err)
		private, err := crypto.ToECDSA(key.Bytes())
		require.NoError(t, err)
		from := crypto.PubkeyToAddress(private.PublicKey)
		raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: app.EVMKeeper.GetNonce(l.snapshot(), from), To: &profile.MasterValidator, Value: new(big.Int), Gas: 24_000_000, GasPrice: big.NewInt(1_000_000_000_000), Data: data})
		signed, err := ethtypes.SignTx(raw, ethtypes.NewEIP155Signer(big.NewInt(990001)), private)
		require.NoError(t, err)
		return signed
	}
	submit := func(index int, success bool, method string, args ...interface{}) *ethtypes.Transaction {
		tx := signedCall(index, method, args...)
		block, response := l.finalizeSignedEthereum(tx)
		if success {
			require.Empty(t, response.VmError)
			t.Logf("signed self-control method=%s operator=%d actual_gas=%d cap=%d", method, index, response.GasUsed, tx.Gas())
		} else {
			require.NotEmpty(t, response.VmError)
			require.Empty(t, response.Logs)
		}
		l.commit(block)
		return tx
	}
	read := func(operator string) string {
		validator, err := app.NativeValidatorKeeper.GetValidator(l.snapshot(), operator)
		require.NoError(t, err)
		avatar, err := app.NativeValidatorKeeper.GetNativeValidatorAvatar(l.snapshot(), operator)
		require.NoError(t, err)
		// Fees and H+2 consensus status can legitimately change at BeginBlock.
		// Compare the fields these controls are authorized to mutate/protect.
		data, err := json.Marshal([]interface{}{validator.OperatorAddress, validator.RewardAddress, validator.ConsensusPubkey.TypeUrl, validator.ConsensusPubkey.Value, validator.Description, validator.Commission.String(), validator.Online, validator.Jailed, avatar})
		require.NoError(t, err)
		return string(data)
	}
	firstBefore := read(first.Operator)
	secondBefore := read(second.Operator)
	// Operator B and a non-member cannot change operator A, even if Master has
	// broader owner authority. Native authorization always binds msg.From to A.
	submit(1, false, "pauseValidator", common.HexToAddress(first.EVM))
	submit(3, false, "updateValidatorMeta", common.HexToAddress(first.EVM), `{"name":"forged"}`)
	require.Equal(t, firstBefore, read(first.Operator))
	require.Equal(t, secondBefore, read(second.Operator))
	submit(0, true, "pauseSelf")
	validator, err := app.NativeValidatorKeeper.GetValidator(l.snapshot(), first.Operator)
	require.NoError(t, err)
	require.False(t, validator.Online)
	_, paused, _ := l.masterValidatorState(common.HexToAddress(first.EVM))
	require.True(t, paused)
	// A valid EVM log with invalid native metadata rolls back the whole call.
	offline := read(first.Operator)
	submit(0, false, "updateValidatorMeta", common.HexToAddress(first.EVM), `{"avatar_uri":"javascript:bad"}`)
	require.Equal(t, offline, read(first.Operator))
	metadata, err := ownedapp.CanonicalValidatorControlMetadata([]byte(`{"name":"Self operator A","description":"Verified signed self control","avatar_uri":"ipfs://bafyfixture/avatar.png","commission":"0.125"}`))
	require.NoError(t, err)
	before, err := app.NativeValidatorKeeper.GetValidator(l.snapshot(), first.Operator)
	require.NoError(t, err)
	replay := submit(0, true, "updateValidatorMeta", common.HexToAddress(first.EVM), string(metadata))
	after, err := app.NativeValidatorKeeper.GetValidator(l.snapshot(), first.Operator)
	require.NoError(t, err)
	require.Equal(t, before.RewardAddress, after.RewardAddress)
	require.Equal(t, before.ConsensusPubkey, after.ConsensusPubkey)
	require.Equal(t, "0.125000000000000000", after.Commission.String())
	aboveFloor := read(first.Operator)
	submit(0, false, "updateValidatorMeta", common.HexToAddress(first.EVM),
		`{"name":"Rejected commission update","commission":"0.049999999999999999"}`)
	require.Equal(t, aboveFloor, read(first.Operator))
	// Reusing the exact signed transaction must fail ante nonce validation and
	// leave both metadata and Master state unchanged.
	prior := read(first.Operator)
	msg := &evmtypes.MsgEthereumTx{}
	require.NoError(t, msg.FromSignedEthereumTx(replay, ethtypes.NewEIP155Signer(big.NewInt(990001))))
	encodedTx, err := msg.BuildTx(app.TxConfig().NewTxBuilder(), identity.BaseDenom)
	require.NoError(t, err)
	encoded, err := app.TxConfig().TxEncoder()(encodedTx)
	require.NoError(t, err)
	result := l.finalize([][]byte{encoded}, nil)
	require.NotZero(t, result.TxResults[0].Code)
	l.commit(result)
	require.Equal(t, prior, read(first.Operator))
	floor, err := ownedapp.CanonicalValidatorControlMetadata([]byte(`{"name":"Self operator A","description":"Verified signed self control","avatar_uri":"ipfs://bafyfixture/avatar.png","commission":"0.05"}`))
	require.NoError(t, err)
	submit(0, true, "updateValidatorMeta", common.HexToAddress(first.EVM), string(floor))
	submit(0, true, "unpauseSelf")
	after, err = app.NativeValidatorKeeper.GetValidator(l.snapshot(), first.Operator)
	require.NoError(t, err)
	require.True(t, after.Online)
	require.Equal(t, "0.050000000000000000", after.Commission.String())
	_, paused, _ = l.masterValidatorState(common.HexToAddress(first.EVM))
	require.False(t, paused)
	require.Equal(t, secondBefore, read(second.Operator))
	switch os.Getenv(validatorAvatarGenesisPhaseEnv) {
	case "source":
		exportNativeAvatarGenesisFixture(t, l, dir)
	case "import":
		exerciseNativeAvatarAfterImport(t, l, profile, dir)
	}
}
