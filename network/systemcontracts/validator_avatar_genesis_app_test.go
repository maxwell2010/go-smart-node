//go:build appstate

package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const validatorAvatarGenesisPhaseEnv = "MCND_VALIDATOR_AVATAR_GENESIS_PHASE"

// This proves the native module's SDK-codec export/import and fresh financial
// genesis avatar admission. Whole-app financial export remains unsupported.
func TestOwnedAppNativeValidatorAvatarGenesisLifecycle(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	for _, phase := range []string{"seed", "source", "source-restart", "import", "import-restart"} {
		if !t.Run(phase, func(t *testing.T) {
			filter, historical, controls, home := "^TestOwnedAppNativeValidatorSelfControls$", "", "run", dir
			if phase == "seed" {
				filter, historical, controls = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$", "seed", "seed"
			}
			if phase == "source-restart" || phase == "import-restart" {
				controls = "restart"
			}
			if phase == "import" || phase == "import-restart" {
				home = filepath.Join(dir, "avatar-import")
			}
			child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=3m", "-test.run="+filter)
			child.Env = append(os.Environ(), validatorAvatarGenesisPhaseEnv+"="+phase,
				validatorControlsPhaseEnv+"="+controls, nativeConsensusDaemonFixtureEnv+"="+home,
				nativeHistoricalConsensusPhaseEnv+"="+historical, nativeCustomCollateralGenesisPhaseEnv+"=",
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

func exportNativeAvatarGenesisFixture(t *testing.T, l nativeHistoricalLifecycle, dir string) {
	t.Helper()
	_, err := l.app.ExportAppStateAndValidators(false, nil, nil)
	require.ErrorContains(t, err, "active withdrawal profile", "whole-app export must remain fail-closed")
	g, err := l.app.NativeValidatorKeeper.ExportGenesis(l.snapshot())
	require.NoError(t, err)
	require.Len(t, g.ValidatorAvatars, 1)
	data, err := l.app.AppCodec().MarshalJSON(g)
	require.NoError(t, err)
	var decoded validatortypes.GenesisState
	require.NoError(t, l.app.AppCodec().UnmarshalJSON(data, &decoded))
	require.Equal(t, g.ValidatorAvatars, decoded.ValidatorAvatars)
	binary, err := l.app.AppCodec().Marshal(g)
	require.NoError(t, err)
	var wire validatortypes.GenesisState
	require.NoError(t, l.app.AppCodec().Unmarshal(binary, &wire))
	require.Equal(t, g.ValidatorAvatars, wire.ValidatorAvatars, "generated protobuf field survives binary transport too")
	// Import the complete native module genesis into an independent fresh SDK
	// store. This is not a claim to restore the bank/EVM or runtime evidence state.
	key := storetypes.NewKVStoreKey("avatar-import-validator")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("avatar-import-transient")).
		WithChainID(l.doc.ChainID).WithBlockHeight(l.app.LastBlockHeight()).WithBlockTime(l.snapshot().BlockTime())
	fresh, err := validatorkeeper.NewStateKeeperWithDomain(runtime.NewKVStoreService(key), l.doc.ChainID, 990001)
	require.NoError(t, err)
	require.NoError(t, fresh.InitGenesis(ctx, &decoded))
	again, err := fresh.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, g.ValidatorAvatars, again.ValidatorAvatars)
	for _, row := range g.ValidatorAvatars {
		uri, err := fresh.GetNativeValidatorAvatar(ctx, row.OperatorAddress)
		require.NoError(t, err)
		require.Equal(t, row.AvatarURI, uri)
	}
	// Only avatar metadata is transplanted to the original independently
	// authenticated fresh financial snapshot. Every financial module stays as
	// seeded, rather than pretending that the module artifact is full genesis.
	var doc map[string]json.RawMessage
	raw, err := os.ReadFile(filepath.Join(dir, "genesis.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &doc))
	var state ownedapp.GenesisState
	require.NoError(t, json.Unmarshal(doc["app_state"], &state))
	var initial validatortypes.GenesisState
	require.NoError(t, l.app.AppCodec().UnmarshalJSON(state[validatortypes.ModuleName], &initial))
	initial.ValidatorAvatars = append([]validatortypes.ValidatorAvatar(nil), decoded.ValidatorAvatars...)
	state[validatortypes.ModuleName], err = l.app.AppCodec().MarshalJSON(&initial)
	require.NoError(t, err)
	doc["app_state"], err = json.Marshal(state)
	require.NoError(t, err)
	raw, err = json.Marshal(doc)
	require.NoError(t, err)
	out := filepath.Join(dir, "avatar-import")
	require.NoError(t, os.Mkdir(out, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(out, "genesis.json"), raw, 0600))
	expected, err := os.ReadFile(filepath.Join(dir, "expectations.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(out, "expectations.json"), expected, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(out, "exported-native-validator.json"), data, 0600))
}

func assertNativeAvatarGenesisImported(t *testing.T, l nativeHistoricalLifecycle, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "exported-native-validator.json"))
	require.NoError(t, err)
	var exported validatortypes.GenesisState
	require.NoError(t, l.app.AppCodec().UnmarshalJSON(raw, &exported))
	require.Len(t, exported.ValidatorAvatars, 1)
	for _, row := range exported.ValidatorAvatars {
		uri, err := l.app.NativeValidatorKeeper.GetNativeValidatorAvatar(l.snapshot(), row.OperatorAddress)
		require.NoError(t, err)
		require.Equal(t, row.AvatarURI, uri, "fresh actual InitChain imports exported avatar metadata")
	}
}

// Protected identity, rewards, key and stake fields must remain independent of
// avatar updates; gas charges alone explain the signer's bank balance change.
func nativeAvatarProtected(t *testing.T, l nativeHistoricalLifecycle, operator string) string {
	t.Helper()
	v, err := l.app.NativeValidatorKeeper.GetValidator(l.snapshot(), operator)
	require.NoError(t, err)
	raw, err := json.Marshal([]interface{}{v.OperatorAddress, v.RewardAddress, v.ConsensusPubkey.TypeUrl,
		v.ConsensusPubkey.Value, v.Rewards.String(), v.TotalRewards.String(), v.Stake, v.Jailed, v.Commission.String()})
	require.NoError(t, err)
	return string(raw)
}

type nativeAvatarImportCheckpoint struct {
	Protected string `json:"protected"`
	Balance   string `json:"balance"`
	Supply    string `json:"supply"`
	Height    int64  `json:"height"`
}

func exerciseNativeAvatarAfterImport(t *testing.T, l nativeHistoricalLifecycle, profile ownedapp.WithdrawalProfile, dir string) {
	t.Helper()
	master, err := contracts.CompiledABI("NewNetworkMasterValidator")
	require.NoError(t, err)
	op := common.HexToAddress(l.expect.Validators[0].EVM)
	operator := l.expect.Validators[0].Operator
	protected := nativeAvatarProtected(t, l, operator)
	supply := l.supply().String()
	send := func(keyIndex int, target common.Address, metadata string, success bool) *ethtypes.Transaction {
		key, err := registryFixtureKey(keyIndex, true)
		require.NoError(t, err)
		private, err := crypto.ToECDSA(key.Bytes())
		require.NoError(t, err)
		from := crypto.PubkeyToAddress(private.PublicKey)
		data, err := master.Pack("updateValidatorMeta", target, metadata)
		require.NoError(t, err)
		price := big.NewInt(1_000_000_000_000)
		tx, err := ethtypes.SignTx(ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: l.app.EVMKeeper.GetNonce(l.snapshot(), from),
			To: &profile.MasterValidator, Value: new(big.Int), Gas: 24_000_000, GasPrice: price, Data: data}),
			ethtypes.NewEIP155Signer(big.NewInt(990001)), private)
		require.NoError(t, err)
		before := l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount
		block, response := l.finalizeSignedEthereum(tx)
		if success {
			require.Empty(t, response.VmError)
		} else {
			require.NotEmpty(t, response.VmError)
			require.Empty(t, response.Logs)
		}
		l.commit(block)
		after := l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount
		want := new(big.Int).Mul(price, new(big.Int).SetUint64(response.GasUsed))
		require.Equal(t, want.String(), before.Sub(after).String(), "only ordinary signed gas is charged")
		require.Equal(t, protected, nativeAvatarProtected(t, l, operator))
		require.Equal(t, supply, l.supply().String())
		return tx
	}
	get := func() string {
		avatar, err := l.app.NativeValidatorKeeper.GetNativeValidatorAvatar(l.snapshot(), operator)
		require.NoError(t, err)
		return avatar
	}
	// The inherited controls suite already exercised foreign access, malformed
	// URI rollback and nonce replay after this fresh genesis was imported.
	before := get()
	send(1, op, `{"avatar_uri":"ipfs://bafyforged/foreign.png"}`, false)
	require.Equal(t, before, get())
	send(0, op, `{"avatar_uri":"javascript:bad"}`, false)
	require.Equal(t, before, get())
	canonical, err := ownedapp.CanonicalValidatorControlMetadata([]byte(`{"avatar_uri":"ipfs://bafyimported/new.png"}`))
	require.NoError(t, err)
	send(0, op, string(canonical), true)
	require.Equal(t, "ipfs://bafyimported/new.png", get())
	cleared := send(0, op, `{"avatar_uri":""}`, true)
	require.Empty(t, get())
	g, err := l.app.NativeValidatorKeeper.ExportGenesis(l.snapshot())
	require.NoError(t, err)
	require.Empty(t, g.ValidatorAvatars, "clear removes the genesis row too")
	// Exact replay fails ante before application state, fees or nonce can move.
	balance := l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(op.Bytes()), identity.BaseDenom).Amount.String()
	msg := &evmtypes.MsgEthereumTx{}
	require.NoError(t, msg.FromSignedEthereumTx(cleared, ethtypes.NewEIP155Signer(big.NewInt(990001))))
	encodedTx, err := msg.BuildTx(l.app.TxConfig().NewTxBuilder(), identity.BaseDenom)
	require.NoError(t, err)
	encoded, err := l.app.TxConfig().TxEncoder()(encodedTx)
	require.NoError(t, err)
	result := l.finalize([][]byte{encoded}, nil)
	require.NotZero(t, result.TxResults[0].Code)
	l.commit(result)
	require.Empty(t, get())
	require.Equal(t, balance, l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(op.Bytes()), identity.BaseDenom).Amount.String())
	require.Equal(t, protected, nativeAvatarProtected(t, l, operator))
	writeNativeHistoricalJSON(t, filepath.Join(dir, "avatar-import-checkpoint.json"), nativeAvatarImportCheckpoint{
		Protected: protected, Balance: balance, Supply: supply, Height: l.app.LastBlockHeight()})
}

func assertNativeAvatarImportedRestart(t *testing.T, l nativeHistoricalLifecycle, dir string) {
	t.Helper()
	var checkpoint nativeAvatarImportCheckpoint
	readNativeHistoricalJSON(t, filepath.Join(dir, "avatar-import-checkpoint.json"), &checkpoint)
	require.Equal(t, checkpoint.Height, l.app.LastBlockHeight())
	operator := l.expect.Validators[0].Operator
	require.Equal(t, checkpoint.Protected, nativeAvatarProtected(t, l, operator))
	op := common.HexToAddress(l.expect.Validators[0].EVM)
	require.Equal(t, checkpoint.Balance, l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(op.Bytes()), identity.BaseDenom).Amount.String())
	require.Equal(t, checkpoint.Supply, l.supply().String())
	uri, err := l.app.NativeValidatorKeeper.GetNativeValidatorAvatar(l.snapshot(), operator)
	require.NoError(t, err)
	require.Empty(t, uri, "signed clear survives reopening the actual application database")
	g, err := l.app.NativeValidatorKeeper.ExportGenesis(l.snapshot())
	require.NoError(t, err)
	require.Empty(t, g.ValidatorAvatars)
}
