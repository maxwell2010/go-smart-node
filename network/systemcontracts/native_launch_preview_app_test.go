package systemcontracts_test

import (
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/keys/multisig"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	ethsecp "github.com/cosmos/evm/crypto/ethsecp256k1"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const (
	nativeLaunchRehearsalEnv             = "MCND_NATIVE_LAUNCH_REHEARSAL"
	nativeLaunchVerifyEnv                = "MCND_NATIVE_LAUNCH_VERIFY"
	nativeLaunchChainID                  = "mintcandy_990011-1"
	nativeLaunchEVMChainID        uint64 = 990011
	nativeLaunchConsensusKeyScope        = "MintCandy isolated launch rehearsal consensus fixture "
	nativeFourLaunchChainID              = "mintcandy_990012-1"
	nativeFourLaunchEVMChainID    uint64 = 990012
	nativeFourLaunchKeyScope             = "MintCandy isolated four-validator launch fixture "
)

var (
	nativeLaunchInitialPowers      = [3]int64{4, 4, 2}
	nativeLaunchGenesisTime        = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	nativeFourLaunchAccountIndices = [4]int{0, 1, 2, 4}
)

type nativeLaunchSpec struct {
	chainID      string
	evmID        uint64
	powers       []int64
	keyScope     string
	accountCount int
	drc20Reserve int64
}

func nativeLaunchSpecForMode(mode string) (nativeLaunchSpec, bool) {
	switch mode {
	case "1":
		return nativeLaunchSpec{nativeLaunchChainID, nativeLaunchEVMChainID, nativeLaunchInitialPowers[:], nativeLaunchConsensusKeyScope, 5, 10}, true
	case "4":
		return nativeLaunchSpec{nativeFourLaunchChainID, nativeFourLaunchEVMChainID,
			[]int64{10_000, 10_000, 10_000, 10_000}, nativeFourLaunchKeyScope, 7, 300}, true
	default:
		return nativeLaunchSpec{}, false
	}
}

func (s nativeLaunchSpec) fourValidators() bool { return len(s.powers) == 4 }

func nativeLaunchAccountKey(index int, four bool) *ethsecp.PrivKey {
	if four {
		return &ethsecp.PrivKey{Key: crypto.Keccak256([]byte(nativeFourLaunchKeyScope+"account"), []byte{byte(index)})}
	}
	return &ethsecp.PrivKey{Key: crypto.Keccak256([]byte("MintCandy isolated launch rehearsal account fixture"), []byte{byte(index)})}
}

func nativeFourLaunchTreasuryKey(index int) *secp256k1.PrivKey {
	return &secp256k1.PrivKey{Key: crypto.Keccak256([]byte(nativeFourLaunchKeyScope+"treasury"), []byte{byte(index)})}
}

func nativeFourLaunchTreasury() *multisig.LegacyAminoPubKey {
	signers := make([]cryptotypes.PubKey, 5)
	for i := range signers {
		signers[i] = nativeFourLaunchTreasuryKey(i).PubKey()
	}
	return multisig.NewLegacyAminoPubKey(3, signers)
}

func nativeFourLaunchBankAllocation(wrapped, drc20, nft common.Address) map[string]int64 {
	balances := map[string]int64{
		sdk.AccAddress(wrapped.Bytes()).String():                                    40_000,
		sdk.AccAddress(drc20.Bytes()).String():                                      300,
		sdk.AccAddress(nft.Bytes()).String():                                        100,
		sdk.AccAddress(nativeLaunchAccountKey(3, true).PubKey().Address()).String(): 45_000,
		sdk.AccAddress(nativeFourLaunchTreasury().Address()).String():               14_000,
	}
	for _, i := range []int{0, 1, 2, 4, 5, 6} {
		balances[sdk.AccAddress(nativeLaunchAccountKey(i, true).PubKey().Address()).String()] = 100
	}
	return balances
}

func applyNativeFourLaunchAllocation(t *testing.T, bank *banktypes.GenesisState, wrapped, drc20, nft common.Address) {
	t.Helper()
	amount := func(n int64) sdkmath.Int { return sdkmath.NewIntWithDecimal(n, 18) }
	// Check the snapshot already holds the real collateral before normalizing
	// test-account funding and removing setup-only gas balances.
	backing := map[string]int64{
		sdk.AccAddress(wrapped.Bytes()).String(): 40_000,
		sdk.AccAddress(drc20.Bytes()).String():   300,
		sdk.AccAddress(nft.Bytes()).String():     100,
	}
	for _, row := range bank.Balances {
		if want, ok := backing[row.Address]; ok {
			require.Equal(t, amount(want).String(), row.Coins.AmountOf(identity.BaseDenom).String())
			delete(backing, row.Address)
		}
	}
	require.Empty(t, backing)
	bank.Balances = nil
	bank.Supply = sdk.NewCoins()
	for address, value := range nativeFourLaunchBankAllocation(wrapped, drc20, nft) {
		coins := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, amount(value)))
		bank.Balances = append(bank.Balances, banktypes.Balance{Address: address, Coins: coins})
		bank.Supply = bank.Supply.Add(coins...)
	}
	sort.Slice(bank.Balances, func(i, j int) bool { return bank.Balances[i].Address < bank.Balances[j].Address })
	require.Len(t, bank.Balances, 11)
	require.Equal(t, amount(100_000).String(), bank.Supply.AmountOf(identity.BaseDenom).String())
}

func nativeFourLaunchStakeKey(keys []*ecdsa.PrivateKey, index int) *ecdsa.PrivateKey {
	return keys[nativeFourLaunchAccountIndices[index]]
}

func checkNativeLaunchPublicGenesis(t *testing.T, body []byte, spec nativeLaunchSpec) {
	t.Helper()
	doc, err := cmttypes.GenesisDocFromJSON(body)
	require.NoError(t, err)
	require.Equal(t, spec.chainID, doc.ChainID)
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
	for i := range spec.accountCount {
		checkPrivateKey(nativeLaunchAccountKey(i, spec.fourValidators()).Bytes())
	}
	for i := range spec.powers {
		checkPrivateKey(cmted25519.GenPrivKeyFromSecret([]byte(spec.keyScope + string("abcd"[i]))).Bytes())
	}
	if spec.fourValidators() {
		for i := range 5 {
			checkPrivateKey(nativeFourLaunchTreasuryKey(i).Bytes())
		}
	}
}

func TestOwnedAppNativeLaunchPreview(t *testing.T) {
	if os.Getenv(nativeLaunchVerifyEnv) == "1" {
		spec, ok := nativeLaunchSpecForMode(os.Getenv(nativeLaunchRehearsalEnv))
		require.True(t, ok)
		verifyNativeLaunchPreview(t, os.Getenv(nativeConsensusDaemonFixtureEnv), spec)
		return
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	for _, mode := range []string{"1", "4"} {
		t.Run("validator_fixture_"+mode, func(t *testing.T) {
			runNativeLaunchPreview(t, binary, mode)
		})
	}
}

func runNativeLaunchPreview(t *testing.T, binary, mode string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private-rehearsal")
	require.NoError(t, os.Mkdir(dir, 0700))
	for _, step := range []struct{ name, filter, verify string }{
		{"seed", "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$", ""},
		{"verify", "^TestOwnedAppNativeLaunchPreview$", "1"},
	} {
		if !t.Run(step.name, func(t *testing.T) {
			child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=3m", "-test.run="+step.filter)
			child.Env = append(os.Environ(), nativeConsensusDaemonFixtureEnv+"="+dir,
				nativeLaunchRehearsalEnv+"="+mode, nativeLaunchVerifyEnv+"="+step.verify)
			output, err := child.CombinedOutput()
			require.NoError(t, err, "%s", output)
		}) {
			return
		}
	}
}

func verifyNativeLaunchPreview(t *testing.T, dir string, spec nativeLaunchSpec) {
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
	checkNativeLaunchPublicGenesis(t, public, spec)
	doc, err := cmttypes.GenesisDocFromJSON(public)
	require.NoError(t, err)
	var expect nativeHistoricalExpectations
	readNativeHistoricalJSON(t, filepath.Join(dir, "expectations.json"), &expect)
	require.Len(t, expect.Validators, len(spec.powers))
	app := ownedapp.NewApp(log.NewNopLogger(), dbm.NewMemDB(), true, appOptions{
		flags.FlagHome: t.TempDir(), flags.FlagChainID: spec.chainID, srvflags.EVMChainID: spec.evmID,
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
	var totalPower int64
	for _, power := range spec.powers {
		totalPower += power
	}
	require.Equal(t, totalPower, set.TotalVotingPower())
	l := nativeHistoricalLifecycle{t: t, app: app, doc: doc, expect: expect, set: set}
	l.commit(l.finalize(nil, nil))
	ctx := l.snapshot()
	for i, expected := range expect.Validators {
		_, validator := set.GetByAddress(common.FromHex(expected.Consensus))
		require.NotNil(t, validator)
		require.Equal(t, expected.PubKey, base64.StdEncoding.EncodeToString(validator.PubKey.Bytes()))
		require.Equal(t, spec.powers[i], validator.VotingPower)
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
	require.Equal(t, sdkmath.NewIntWithDecimal(spec.drc20Reserve, 18).String(), backed(common.HexToAddress(coin.DRC20Contract)).String())
	for _, role := range []common.Address{profile.Registry, profile.Gateway, profile.Library, profile.NFTCenter, profile.PositionVault,
		profile.MasterValidator, profile.WrappedDEL, profile.TokenCenter, profile.CurveConverter, common.HexToAddress(coin.DRC20Contract)} {
		require.NotEmpty(t, app.EVMKeeper.GetCode(ctx, app.EVMKeeper.GetCodeHash(ctx, role)))
	}
	if spec.fourValidators() {
		var nft nfttypes.GenesisState
		require.NoError(t, codec.UnmarshalJSON(state[nfttypes.ModuleName], &nft))
		require.Len(t, nft.ExternalBindings, 1)
		origin := common.BytesToAddress(nft.ExternalBindings[0].Contract)
		require.Equal(t, sdkmath.NewIntWithDecimal(100, 18).String(), backed(origin).String())
		var native validatortypes.GenesisState
		require.NoError(t, codec.UnmarshalJSON(state[validatortypes.ModuleName], &native))
		require.Len(t, native.Delegations, 4)
		for _, d := range native.Delegations {
			operator, err := app.NativeValidatorKeeper.ValidatorAddressCodec().StringToBytes(d.Validator)
			require.NoError(t, err)
			require.Equal(t, sdk.AccAddress(operator).String(), d.Delegator)
		}
		require.Equal(t, sdkmath.NewIntWithDecimal(100_000, 18).String(), total.String())
		require.Len(t, bank.Balances, 11)
		for address, value := range nativeFourLaunchBankAllocation(profile.WrappedDEL, common.HexToAddress(coin.DRC20Contract), origin) {
			decoded, err := sdk.AccAddressFromBech32(address)
			require.NoError(t, err)
			require.Equal(t, sdkmath.NewIntWithDecimal(value, 18).String(), app.BankKeeper.GetBalance(ctx, decoded, identity.BaseDenom).Amount.String())
		}
		treasury := nativeFourLaunchTreasury()
		account := app.AccountKeeper.GetAccount(ctx, sdk.AccAddress(treasury.Address()))
		require.NotNil(t, account)
		require.IsType(t, &multisig.LegacyAminoPubKey{}, account.GetPubKey())
		require.True(t, treasury.Equals(account.GetPubKey()))
		require.True(t, app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(authtypes.FeeCollectorName), identity.BaseDenom).IsZero())
	}
}
