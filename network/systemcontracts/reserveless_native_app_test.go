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
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const reservelessNativePhaseEnv = "MCND_RESERVELESS_NATIVE_PHASE"

// A metadata payload is stored as untrusted text. This test never fetches or
// renders its image; safe fetching, durable pinning and display are separate gates.
const nativeAvatarMetadata = `{"name":"Avatar fixture","image":"ipfs://bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku/avatar.png"}`

func TestOwnedAppNativeReservelessTokenLifecycle(t *testing.T) {
	if phase := os.Getenv(reservelessNativePhaseEnv); phase != "" {
		runNativeReservelessTokens(t, os.Getenv(nativeConsensusDaemonFixtureEnv), phase)
		return
	}
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
			child.Env = append(os.Environ(), reservelessNativePhaseEnv+"="+phase, nativeConsensusDaemonFixtureEnv+"="+dir,
				nativeHistoricalConsensusPhaseEnv+"="+historical, nativeCustomCollateralGenesisPhaseEnv+"="+custom,
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

func runNativeReservelessTokens(t *testing.T, dir, phase string) {
	doc, err := cmttypes.GenesisDocFromFile(filepath.Join(dir, "genesis.json"))
	require.NoError(t, err)
	// DRC20 fee envelopes require a finite combined block budget. The generic
	// registry seed intentionally uses Comet defaults (unlimited block gas).
	doc.ConsensusParams.Block.MaxGas = 24_000_000
	var expected nativeHistoricalExpectations
	readNativeHistoricalJSON(t, filepath.Join(dir, "expectations.json"), &expected)
	var state ownedapp.GenesisState
	require.NoError(t, json.Unmarshal(doc.AppState, &state))
	var profile ownedapp.WithdrawalProfile
	require.NoError(t, json.Unmarshal(state[ownedapp.WithdrawalGenesisKey], &profile))
	seedDRC20FeePayer(t, state, expected.Actor)
	otherKey, err := registryFixtureKey(4, true)
	require.NoError(t, err)
	other := common.BytesToAddress(otherKey.PubKey().Address())
	// Move fixture funds, without issuing DEL, to an independent spender. Both
	// callers then pay ordinary signed transaction fees through real ABCI.
	var bank banktypes.GenesisState
	require.NoError(t, json.Unmarshal(state[banktypes.ModuleName], &bank))
	// Each signed call reserves up to 24 DEL (24m gas at 1e12 atoms/gas).
	// Fund the whole positive/negative lifecycle without minting new DEL.
	funding := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntWithDecimal(500, 18)))
	found := false
	credited := false
	for i := range bank.Balances {
		if bank.Balances[i].Address == sdk.AccAddress(expected.Actor.Bytes()).String() {
			bank.Balances[i].Coins = bank.Balances[i].Coins.Sub(funding...)
			found = true
		}
		if bank.Balances[i].Address == sdk.AccAddress(other.Bytes()).String() {
			bank.Balances[i].Coins = bank.Balances[i].Coins.Add(funding...)
			credited = true
		}
	}
	require.True(t, found)
	if !credited {
		bank.Balances = append(bank.Balances, banktypes.Balance{Address: sdk.AccAddress(other.Bytes()).String(), Coins: funding})
	}
	state[banktypes.ModuleName], err = json.Marshal(bank)
	require.NoError(t, err)
	doc.AppState, err = json.Marshal(state)
	require.NoError(t, err)
	database, err := dbm.NewDB("reserveless-native", dbm.GoLevelDBBackend, dir)
	require.NoError(t, err)
	app := ownedapp.NewApp(log.NewNopLogger(), database, true, appOptions{flags.FlagHome: filepath.Join(dir, "home"),
		flags.FlagChainID: doc.ChainID, srvflags.EVMChainID: uint64(990001)})
	t.Cleanup(func() { require.NoError(t, app.Close()) })
	l := nativeHistoricalLifecycle{t: t, app: app, doc: doc, expect: expected, target: expected.Validators[2]}
	if phase == "run" {
		params := doc.ConsensusParams.ToProto()
		init, err := app.InitChain(&abci.RequestInitChain{ChainId: doc.ChainID, InitialHeight: 1, Time: doc.GenesisTime,
			ConsensusParams: &params, AppStateBytes: doc.AppState})
		require.NoError(t, err)
		validators, err := cmttypes.PB2TM.ValidatorUpdates(init.Validators)
		require.NoError(t, err)
		l.set = cmttypes.NewValidatorSet(validators)
		l.commit(l.finalize(nil, nil))
	}
	center, err := contracts.CompiledABI("NewNetworkTokenCenter")
	require.NoError(t, err)
	free, err := contracts.CompiledABI("DRC20Reserveless")
	require.NoError(t, err)
	sendData := func(keyIndex int, target common.Address, data []byte, success bool) []byte {
		t.Helper()
		key, err := registryFixtureKey(keyIndex, true)
		require.NoError(t, err)
		private, err := crypto.ToECDSA(key.Bytes())
		require.NoError(t, err)
		from := crypto.PubkeyToAddress(private.PublicKey)
		raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: app.EVMKeeper.GetNonce(l.snapshot(), from), To: &target,
			Value: new(big.Int), Gas: 24_000_000, GasPrice: big.NewInt(1_000_000_000_000), Data: data})
		signed, err := ethtypes.SignTx(raw, ethtypes.NewEIP155Signer(big.NewInt(990001)), private)
		require.NoError(t, err)
		before := app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount
		block, response := l.finalizeSignedEthereum(signed)
		if success {
			require.Empty(t, response.VmError)
		} else {
			require.NotEmpty(t, response.VmError)
			require.Empty(t, response.Logs)
		}
		l.commit(block)
		fee := sdkmath.NewIntFromBigInt(new(big.Int).Mul(big.NewInt(1_000_000_000_000), new(big.Int).SetUint64(response.GasUsed)))
		require.Equal(t, fee.String(), before.Sub(app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount).String())
		return response.Ret
	}
	call := func(key int, target common.Address, method string, success bool, args ...interface{}) []byte {
		t.Helper()
		data, err := free.Pack(method, args...)
		require.NoError(t, err)
		return sendData(key, target, data, success)
	}
	create := func(name, symbol string, mintable, burnable bool, initial, cap int64, success bool, metadata ...string) common.Address {
		t.Helper()
		text := nativeAvatarMetadata
		if len(metadata) != 0 {
			text = metadata[0]
		}
		data, err := center.Pack("createTokenReserveless", name, symbol, mintable, burnable, big.NewInt(initial), big.NewInt(cap), text)
		require.NoError(t, err)
		ret := sendData(3, profile.TokenCenter, data, success)
		if !success {
			return common.BytesToAddress(ret)
		}
		values, err := center.Unpack("createTokenReserveless", ret)
		require.NoError(t, err)
		return values[0].(common.Address)
	}
	readIdentity := func(token common.Address) string {
		t.Helper()
		data, err := free.Pack("identity")
		require.NoError(t, err)
		child, _ := l.snapshot().CacheContext()
		child = child.WithGasMeter(storetypes.NewGasMeter(3_000_000))
		msg := core.Message{From: expected.Actor, To: &token, Value: new(big.Int), Data: data,
			GasLimit: 500_000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int),
			SkipNonceChecks: true, SkipFromEOACheck: true}
		result, err := app.EVMKeeper.ApplyMessage(child, statedb.New(child, app.EVMKeeper, statedb.NewEmptyTxConfig()), msg, nil, false, false, true)
		require.NoError(t, err)
		require.Empty(t, result.VmError)
		values, err := free.Unpack("identity", result.Ret)
		require.NoError(t, err)
		return values[0].(string)
	}
	supplySlot := common.HexToHash("0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace02")
	balanceSlot := common.HexToHash("0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace00")
	balance := func(token, owner common.Address) int64 {
		return app.EVMKeeper.GetState(l.snapshot(), token, crypto.Keccak256Hash(common.BytesToHash(owner.Bytes()).Bytes(), balanceSlot[:])).Big().Int64()
	}
	var checkpoint struct {
		Token  common.Address
		Supply string
	}
	if phase == "restart" {
		assertDRC20FeeRestart(t, l, dir)
		readNativeHistoricalJSON(t, filepath.Join(dir, "reserveless.json"), &checkpoint)
		require.Equal(t, nativeAvatarMetadata, readIdentity(checkpoint.Token), "avatar metadata text must survive disk restart")
		require.Equal(t, int64(125), app.EVMKeeper.GetState(l.snapshot(), checkpoint.Token, supplySlot).Big().Int64())
		require.Equal(t, int64(112), balance(checkpoint.Token, expected.Actor))
		require.Equal(t, int64(13), balance(checkpoint.Token, other))
		require.Equal(t, checkpoint.Supply, l.supply().String())
		call(3, checkpoint.Token, "mint", true, other, big.NewInt(1))
		require.Equal(t, int64(14), balance(checkpoint.Token, other), "persisted admission must survive restart")
		return
	}
	baseSupply := l.supply().String()
	token := create("Independent reserve-free asset", "freex", true, true, 100, 200, true)
	require.Equal(t, nativeAvatarMetadata, readIdentity(token), "signed creation must preserve avatar metadata bytes")
	assertNativeReserveFeeQuotes(t, l, profile, token)
	assertNativeDRC20FeePayments(t, l, token, dir)
	require.Zero(t, app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(token.Bytes()), identity.BaseDenom).Amount.Sign())
	_, err = app.NativeCoinKeeper.GetCoinByDRC(l.snapshot(), token.Hex())
	require.Error(t, err, "reserveless asset must never become native backed collateral")
	call(3, token, "mint", true, expected.Actor, big.NewInt(50))
	call(4, token, "mint", false, other, big.NewInt(1))
	require.Equal(t, int64(150), app.EVMKeeper.GetState(l.snapshot(), token, supplySlot).Big().Int64())
	call(3, token, "mint", false, expected.Actor, big.NewInt(51))
	call(3, token, "burn", true, big.NewInt(20))
	call(3, token, "transfer", true, other, big.NewInt(10))
	call(3, token, "approve", true, other, big.NewInt(8))
	call(4, token, "transferFrom", true, expected.Actor, other, big.NewInt(3))
	call(4, token, "burnFrom", true, expected.Actor, big.NewInt(5))
	call(4, token, "burnFrom", false, expected.Actor, big.NewInt(1))
	data, err := free.Pack("approve", other, big.NewInt(99))
	require.NoError(t, err)
	sendData(3, token, append(data, make([]byte, 32)...), false)
	call(4, token, "transferFrom", false, expected.Actor, other, big.NewInt(1))
	call(3, token, "transferOwnership", false, other)
	call(4, token, "mint", false, other, big.NewInt(1))
	require.Equal(t, int64(125), app.EVMKeeper.GetState(l.snapshot(), token, supplySlot).Big().Int64())
	require.Equal(t, int64(112), balance(token, expected.Actor))
	require.Equal(t, int64(13), balance(token, other))
	fixed := create("Fixed nonburnable asset", "fixedx", false, false, 10, 0, true)
	call(3, fixed, "mint", false, expected.Actor, big.NewInt(1))
	call(3, fixed, "burn", false, big.NewInt(1))
	require.Equal(t, int64(10), balance(fixed, expected.Actor))
	bad := create(strings.Repeat("x", 97), "longx", true, true, 10, 100, false)
	require.Empty(t, app.EVMKeeper.GetCode(l.snapshot(), app.EVMKeeper.GetCodeHash(l.snapshot(), bad)), "late admission failure must remove created code")
	for index, metadata := range []string{strings.Repeat("x", 257), "metadata\x00tail", string([]byte{0xff})} {
		bad := create("Rejected metadata fixture", []string{"badmetaone", "badmetatwo", "badmetathr"}[index], true, true, 10, 100, false, metadata)
		require.Empty(t, app.EVMKeeper.GetCode(l.snapshot(), app.EVMKeeper.GetCodeHash(l.snapshot(), bad)), "rejected metadata must not leave deployed code")
	}
	call(3, token, "approve", true, profile.CurveConverter, big.NewInt(1))
	converter, err := contracts.CompiledABI("MintCandyCurveConverter")
	require.NoError(t, err)
	data, err = converter.Pack("convertAndDelegate", common.HexToAddress(l.target.EVM), token, big.NewInt(1), big.NewInt(1), big.NewInt(l.snapshot().BlockTime().Unix()+100))
	require.NoError(t, err)
	sendData(3, profile.CurveConverter, data, false)
	require.Equal(t, int64(125), app.EVMKeeper.GetState(l.snapshot(), token, supplySlot).Big().Int64())
	require.Equal(t, baseSupply, l.supply().String(), "ordinary token issuance and destruction must not issue or burn DEL")
	checkpoint.Token, checkpoint.Supply = token, baseSupply
	encoded, err := json.Marshal(checkpoint)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "reserveless.json"), encoded, 0600))
}
