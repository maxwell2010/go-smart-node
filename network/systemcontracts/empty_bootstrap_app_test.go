//go:build appstate

package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/ethereum/go-ethereum/common"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// A real empty user-asset InitChain, followed by public signed asset creation,
// disk restart and persisted admission checks. No public network is accessed.
func TestOwnedAppEmptyUserAssetsLifecycle(t *testing.T) {
	if phase := os.Getenv(emptyUserAssetsPhaseEnv); phase != "" {
		runEmptyUserAssetsLifecycle(t, os.Getenv(nativeConsensusDaemonFixtureEnv), phase)
		return
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	for _, phase := range []string{"seed", "run", "restart"} {
		if !t.Run(phase, func(t *testing.T) {
			filter := "^TestOwnedAppEmptyUserAssetsLifecycle$"
			if phase == "seed" {
				filter = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$"
			}
			child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=5m", "-test.run="+filter)
			child.Env = append(os.Environ(), emptyUserAssetsPhaseEnv+"="+phase, nativeConsensusDaemonFixtureEnv+"="+dir,
				nativeHistoricalConsensusPhaseEnv+"=", nativeCustomCollateralGenesisPhaseEnv+"=", nativeLaunchRehearsalEnv+"=",
				nativeHistoricalMaxNFTPhaseEnv+"=", nativeCustomNFTPhaseEnv+"=", nativeConsensusFeeRewardsEnv+"=",
				nativeConsensusIBCV1Env+"=", nativeConsensusDowntimeEnv+"=", nativeConsensusUpgradeGovernanceEnv+"=",
				nativeConsensusJailRecoveryEnv+"=", nativeConsensusHeldDowntimeEnv+"=", nativeConsensusNFTDowntimeEnv+"=",
				nativeConsensusFullUpgradeEnv+"=", signedNFTAdmissionPhaseEnv+"=", reservelessNativePhaseEnv+"=",
				userNFTNativePhaseEnv+"=", delLifecycleFixtureEnv+"=", withdrawalGenesisFixtureEnv+"=")
			output, err := child.CombinedOutput()
			require.NoError(t, err, "%s", output)
			t.Logf("%s: %s", phase, output)
		}) {
			return
		}
	}
}

func runEmptyUserAssetsLifecycle(t *testing.T, dir, phase string) {
	doc, err := cmttypes.GenesisDocFromFile(filepath.Join(dir, "genesis.json"))
	require.NoError(t, err)
	require.Equal(t, int64(1), doc.InitialHeight)
	doc.ConsensusParams.Block.MaxGas = 24_000_000
	var state ownedapp.GenesisState
	require.NoError(t, json.Unmarshal(doc.AppState, &state))
	var profile ownedapp.WithdrawalProfile
	require.NoError(t, json.Unmarshal(state[ownedapp.WithdrawalGenesisKey], &profile))
	require.True(t, profile.EnableNFTDelegation)
	require.True(t, profile.EnableNativeFeeRewards)
	var expected nativeHistoricalExpectations
	readNativeHistoricalJSON(t, filepath.Join(dir, "expectations.json"), &expected)
	require.Len(t, expected.Validators, 4)
	for _, validator := range expected.Validators {
		require.Equal(t, int64(10000), validator.Initial)
	}
	database, err := dbm.NewDB("empty-user-assets", dbm.GoLevelDBBackend, dir)
	require.NoError(t, err)
	app := ownedapp.NewApp(log.NewNopLogger(), database, true, appOptions{flags.FlagHome: filepath.Join(dir, "home"),
		flags.FlagChainID: doc.ChainID, srvflags.EVMChainID: uint64(990001)})
	t.Cleanup(func() { require.NoError(t, app.Close()) })
	var coins cointypes.GenesisState
	require.NoError(t, app.AppCodec().UnmarshalJSON(state[cointypes.ModuleName], &coins))
	require.Empty(t, coins.Coins)
	var nft nfttypes.GenesisState
	require.NoError(t, app.AppCodec().UnmarshalJSON(state[nfttypes.ModuleName], &nft))
	require.Len(t, nft.Collections, 1)
	require.Equal(t, profile.PositionVault.Hex(), nft.Collections[0].AddressDRC)
	require.Empty(t, nft.Collections[0].Tokens)
	require.Empty(t, nft.ExternalBindings)
	require.Empty(t, nft.IsolatedPositions)
	require.Len(t, nft.IsolatedVaults, 1)
	l := nativeHistoricalLifecycle{t: t, app: app, doc: doc, expect: expected, target: expected.Validators[2]}
	if phase == "run" {
		params := doc.ConsensusParams.ToProto()
		init, err := app.InitChain(&abci.RequestInitChain{ChainId: doc.ChainID, InitialHeight: 1, Time: doc.GenesisTime,
			ConsensusParams: &params, AppStateBytes: doc.AppState})
		require.NoError(t, err)
		validators, err := cmttypes.PB2TM.ValidatorUpdates(init.Validators)
		require.NoError(t, err)
		require.Len(t, validators, 4)
		l.set = cmttypes.NewValidatorSet(validators)
		l.commit(l.finalize(nil, nil))
	} else {
		require.Equal(t, "restart", phase)
	}
	if os.Getenv(userNFTNativePhaseEnv) != "" {
		runNativeUserNFTLifecycle(t, l, dir, phase)
		return
	}
	if phase == "run" {
		assertNativeBackedTokenLifecycle(t, l, profile, dir, "")
	} else {
		assertNativeBackedTokenRestart(t, l, dir)
	}
	assertEmptySeedReservelessLifecycle(t, l, profile, dir, phase)
	runNativeUserNFTLifecycle(t, l, dir, phase)
}

func assertEmptySeedReservelessLifecycle(t *testing.T, l nativeHistoricalLifecycle, profile ownedapp.WithdrawalProfile, dir, phase string) {
	t.Helper()
	center, err := contracts.CompiledABI("NewNetworkTokenCenter")
	require.NoError(t, err)
	free, err := contracts.CompiledABI("DRC20Reserveless")
	require.NoError(t, err)
	var checkpoint struct {
		Address common.Address
		Supply  string
	}
	send := func(to common.Address, data []byte) ([]byte, uint64) {
		t.Helper()
		tx := l.signedEthereumCall(to, sdkmath.ZeroInt(), data)
		block, response := l.finalizeSignedEthereum(tx)
		require.Empty(t, response.VmError)
		l.commit(block)
		return response.Ret, response.GasUsed
	}
	if phase == "run" {
		checkpoint.Supply = l.supply().String()
		data, err := center.Pack("createTokenReserveless", "EMPTY LAUNCH RESERVE-FREE ASSET", "emptyfree", true, true,
			big.NewInt(100), big.NewInt(200), nativeAvatarMetadata)
		require.NoError(t, err)
		balance := func(address common.Address) sdkmath.Int {
			return l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(address.Bytes()), identity.BaseDenom).Amount
		}
		zeroBefore := balance(common.Address{})
		ret, gas := send(profile.TokenCenter, data)
		values, err := center.Unpack("createTokenReserveless", ret)
		require.NoError(t, err)
		checkpoint.Address = values[0].(common.Address)
		require.True(t, balance(checkpoint.Address).IsZero())
		require.True(t, balance(common.Address{}).Equal(zeroBefore))
		t.Logf("reserveless ticker creation value_atoms=0 reserve_atoms=0 gas_used=%d", gas)
		_, err = l.app.NativeCoinKeeper.GetCoinByDRC(l.snapshot(), checkpoint.Address.Hex())
		require.Error(t, err, "reserve-free assets cannot enter backed collateral admission")
		data, err = free.Pack("mint", l.expect.Actor, big.NewInt(25))
		require.NoError(t, err)
		send(checkpoint.Address, data)
		encoded, err := json.Marshal(checkpoint)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "empty-free.json"), encoded, 0600))
	} else {
		readNativeHistoricalJSON(t, filepath.Join(dir, "empty-free.json"), &checkpoint)
		data, err := free.Pack("approve", l.expect.Actor, big.NewInt(1))
		require.NoError(t, err)
		send(checkpoint.Address, data)
	}
	require.Equal(t, checkpoint.Supply, l.supply().String(), "token issuance and restart must preserve native supply")
	slot := common.HexToHash("0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace02")
	require.Equal(t, int64(125), l.app.EVMKeeper.GetState(l.snapshot(), checkpoint.Address, slot).Big().Int64())
}
