package systemcontracts_test

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	ethsecp "github.com/cosmos/evm/crypto/ethsecp256k1"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const (
	nativeLaunchRehearsalEnv             = "MCND_NATIVE_LAUNCH_REHEARSAL"
	nativeLaunchVerifyEnv                = "MCND_NATIVE_LAUNCH_VERIFY"
	nativeLaunchChainID                  = "mintcandy_990011-1"
	nativeLaunchEVMChainID        uint64 = 990011
	nativeLaunchConsensusKeyScope        = "MintCandy isolated launch rehearsal consensus fixture "
)

var (
	nativeLaunchInitialPowers = [3]int64{4, 4, 2}
	nativeLaunchGenesisTime   = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
)

func nativeLaunchAccountKey(index int) *ethsecp.PrivKey {
	return &ethsecp.PrivKey{Key: crypto.Keccak256([]byte("MintCandy isolated launch rehearsal account fixture"), []byte{byte(index)})}
}

func checkNativeLaunchPublicGenesis(t *testing.T, body []byte) {
	t.Helper()
	doc, err := cmttypes.GenesisDocFromJSON(body)
	require.NoError(t, err)
	require.Equal(t, nativeLaunchChainID, doc.ChainID)
	require.Equal(t, nativeLaunchGenesisTime, doc.GenesisTime)
	require.Equal(t, int64(1), doc.InitialHeight)
	require.Empty(t, doc.AppHash)
	require.Empty(t, doc.Validators, "InitChain derives native consensus power from authenticated stake")
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &envelope))
	for name := range envelope {
		switch name {
		case "app_hash", "app_state", "chain_id", "consensus_params", "genesis_time", "initial_height", "validators":
		default:
			t.Fatalf("unexpected public genesis field %q", name)
		}
	}
	text := string(body)
	for _, field := range []string{`"priv_key"`, `"private_key"`, `"mnemonic"`, `"node_key"`, `"raw"`} {
		require.NotContains(t, text, field)
	}
	checkPrivateKey := func(key []byte) {
		t.Helper()
		require.NotContains(t, text, hex.EncodeToString(key))
		require.NotContains(t, text, base64.StdEncoding.EncodeToString(key))
	}
	for i := range 5 {
		checkPrivateKey(nativeLaunchAccountKey(i).Bytes())
	}
	for _, name := range []string{"a", "b", "c"} {
		checkPrivateKey(cmted25519.GenPrivKeyFromSecret([]byte(nativeLaunchConsensusKeyScope + name)).Bytes())
	}
}

func TestOwnedAppNativeLaunchPreview(t *testing.T) {
	if os.Getenv(nativeLaunchVerifyEnv) == "1" {
		verifyNativeLaunchPreview(t, os.Getenv(nativeConsensusDaemonFixtureEnv))
		return
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "private-rehearsal")
	require.NoError(t, os.Mkdir(dir, 0700))
	for _, step := range []struct{ name, filter, verify string }{
		{"seed", "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$", ""},
		{"verify", "^TestOwnedAppNativeLaunchPreview$", "1"},
	} {
		if !t.Run(step.name, func(t *testing.T) {
			child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=3m", "-test.run="+step.filter)
			child.Env = append(os.Environ(), nativeConsensusDaemonFixtureEnv+"="+dir,
				nativeLaunchRehearsalEnv+"=1", nativeLaunchVerifyEnv+"="+step.verify)
			output, err := child.CombinedOutput()
			require.NoError(t, err, "%s", output)
		}) {
			return
		}
	}
}

func verifyNativeLaunchPreview(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "public"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "genesis.json", entries[0].Name())
	require.True(t, entries[0].Type().IsRegular())
	public, err := os.ReadFile(filepath.Join(dir, "public", "genesis.json"))
	require.NoError(t, err)
	private, err := os.ReadFile(filepath.Join(dir, "genesis.json"))
	require.NoError(t, err)
	require.Equal(t, private, public)
	checkNativeLaunchPublicGenesis(t, public)
	doc, err := cmttypes.GenesisDocFromJSON(public)
	require.NoError(t, err)
	var expect nativeHistoricalExpectations
	readNativeHistoricalJSON(t, filepath.Join(dir, "expectations.json"), &expect)
	require.Len(t, expect.Validators, len(nativeLaunchInitialPowers))
	app := ownedapp.NewApp(log.NewNopLogger(), dbm.NewMemDB(), true, appOptions{
		flags.FlagHome: t.TempDir(), flags.FlagChainID: nativeLaunchChainID, srvflags.EVMChainID: nativeLaunchEVMChainID,
	})
	t.Cleanup(func() { require.NoError(t, app.Close()) })
	var state ownedapp.GenesisState
	require.NoError(t, json.Unmarshal(doc.AppState, &state))
	codec := app.AppCodec()
	require.NoError(t, ownedapp.ValidateNativeGenesisAllocation(codec, state))
	var bank banktypes.GenesisState
	var coins cointypes.GenesisState
	var profile ownedapp.WithdrawalProfile
	require.NoError(t, codec.UnmarshalJSON(state[banktypes.ModuleName], &bank))
	require.NoError(t, codec.UnmarshalJSON(state[cointypes.ModuleName], &coins))
	require.NoError(t, json.Unmarshal(state[ownedapp.WithdrawalGenesisKey], &profile))
	require.True(t, profile.SettlementOnly && profile.EnableDELDelegation && profile.EnableDELHold && profile.EnableDELHoldReset &&
		profile.EnableNFTDelegation && profile.EnableNativeConsensus && profile.EnableNativeFeeRewards && profile.EnableNativeIBCV1 &&
		profile.EnableNativeGovernance && profile.EnableNativeUpgradeGovernance && profile.EnableNativeOrdinaryDELDowntime &&
		profile.EnableNativeHeldDELDowntime && profile.EnableNativeNFTDowntime && profile.EnableNativeDuplicateVote)
	require.Len(t, coins.Coins, 1)
	coin := coins.Coins[0]
	params := doc.ConsensusParams.ToProto()
	init, err := app.InitChain(&abci.RequestInitChain{ChainId: doc.ChainID, InitialHeight: doc.InitialHeight, Time: doc.GenesisTime,
		ConsensusParams: &params, AppStateBytes: doc.AppState})
	require.NoError(t, err)
	validators, err := cmttypes.PB2TM.ValidatorUpdates(init.Validators)
	require.NoError(t, err)
	require.Len(t, validators, len(expect.Validators))
	set := cmttypes.NewValidatorSet(validators)
	require.Equal(t, int64(10), set.TotalVotingPower())
	l := nativeHistoricalLifecycle{t: t, app: app, doc: doc, expect: expect, set: set}
	l.commit(l.finalize(nil, nil))
	ctx := l.snapshot()
	for i, expected := range expect.Validators {
		_, validator := set.GetByAddress(common.FromHex(expected.Consensus))
		require.NotNil(t, validator)
		require.Equal(t, expected.PubKey, base64.StdEncoding.EncodeToString(validator.PubKey.Bytes()))
		require.Equal(t, nativeLaunchInitialPowers[i], validator.VotingPower)
		active, err := app.NativeValidatorKeeper.GetActiveDELReserve(ctx, expected.Operator)
		require.NoError(t, err)
		require.Equal(t, sdkmath.NewIntWithDecimal(validator.VotingPower, 18).String(), active.String())
	}
	total := sdkmath.ZeroInt()
	for _, balance := range bank.Balances {
		addr, err := sdk.AccAddressFromBech32(balance.Address)
		require.NoError(t, err)
		for _, amount := range balance.Coins {
			require.Equal(t, identity.BaseDenom, amount.Denom)
			require.Equal(t, amount.String(), app.BankKeeper.GetBalance(ctx, addr, amount.Denom).String())
			total = total.Add(amount.Amount)
		}
	}
	require.Equal(t, total.String(), l.supply().String())
	require.Equal(t, total.String(), bank.Supply.AmountOf(identity.BaseDenom).String())
	require.Equal(t, total.String(), coins.Params.BaseVolume.String())
	require.True(t, app.BankKeeper.GetBalance(ctx, sdk.AccAddress(expect.Actor.Bytes()), identity.BaseDenom).IsPositive())
	backed := func(addr common.Address) sdkmath.Int {
		return app.BankKeeper.GetBalance(ctx, sdk.AccAddress(addr.Bytes()), identity.BaseDenom).Amount
	}
	require.Equal(t, sdkmath.NewIntWithDecimal(set.TotalVotingPower(), 18).String(), backed(profile.WrappedDEL).String())
	require.True(t, backed(profile.PositionVault).IsZero())
	require.Equal(t, sdkmath.NewIntWithDecimal(10, 18).String(), backed(common.HexToAddress(coin.DRC20Contract)).String())
	for _, role := range []common.Address{profile.Registry, profile.Gateway, profile.Library, profile.NFTCenter, profile.PositionVault,
		profile.MasterValidator, profile.WrappedDEL, profile.TokenCenter, profile.CurveConverter, common.HexToAddress(coin.DRC20Contract)} {
		require.NotEmpty(t, app.EVMKeeper.GetCode(ctx, app.EVMKeeper.GetCodeHash(ctx, role)))
	}
}
