package systemcontracts_test

import (
	"encoding/json"
	"maps"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const nativeCustomCollateralGenesisPhaseEnv = "MCND_NATIVE_DRC20_GENESIS_PHASE"

type nativeCustomCollateralGenesisFixture struct {
	center, converter common.Address
	coin              cointypes.Coin
}

func seedNativeCustomCollateralGenesis(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile,
	gov func(contracts.Artifact, common.Address, string, ...interface{}), fixtureVolume int64,
) *nativeCustomCollateralGenesisFixture {
	t.Helper()
	registry, proxy, bootstrap := e.load("DecimalContractCenter"), e.load("DecimalContractCenterProxy"), e.load("NewNetworkProxyBootstrap")
	center, coin, converter := e.load("NewNetworkTokenCenter"), e.load("DRC20"), e.load("MintCandyCurveConverter")
	centerImpl, coinImpl := e.deploy(center), e.deploy(coin)
	centerInit, err := center.ABI.Pack("initializeFresh", profile.Registry)
	require.NoError(t, err)
	tokenLogic, err := center.ABI.Pack("upgradeToken", coinImpl)
	require.NoError(t, err)
	bootstrapData, err := bootstrap.ABI.Pack("bootstrapConfigured", e.govOwner, centerImpl, centerInit, [][]byte{tokenLogic})
	require.NoError(t, err)
	centerAddress := e.deploy(proxy, e.setup, bootstrapData)
	converter.Runtime, err = contracts.ExpectedRuntime("MintCandyCurveConverter", nil, map[string]common.Hash{
		"MintCandyCurveConverter.sol:MintCandyCurveConverter.REGISTRY": common.BytesToHash(profile.Registry.Bytes()),
	})
	require.NoError(t, err)
	converterAddress := e.deploy(converter, profile.Registry)
	gov(registry, profile.Registry, "setContractAddress", "token-center", centerAddress)
	gov(registry, profile.Registry, "setContractAddress", "curve-converter", converterAddress)

	creator := crypto.PubkeyToAddress(e.keys[3].PublicKey)
	unit := func(amount int64) sdkmath.Int { return sdkmath.NewIntWithDecimal(amount, 18) }
	volume := max(int64(10), fixtureVolume)
	want := cointypes.Coin{Denom: "genesis20", Title: "Fresh isolated DRC20 collateral asset",
		Creator: sdk.AccAddress(creator.Bytes()).String(), CRR: 100, Identity: "fixture://approved-collateral/long-identity",
		Volume: unit(volume), Reserve: unit(volume), MinVolume: unit(5), LimitVolume: unit(max(int64(100), volume+1))}
	metadata := struct {
		InitialMint, MinTotalSupply, MaxTotalSupply *big.Int
		Creator                                     common.Address
		Crr                                         uint8
		Identity, Symbol, Name                      string
	}{want.Volume.BigInt(), want.MinVolume.BigInt(), want.LimitVolume.BigInt(), creator, uint8(want.CRR),
		want.Identity, want.Denom, want.Title}
	data, err := center.ABI.Pack("createToken", metadata)
	require.NoError(t, err)
	// 7-10 character symbols in this pinned factory use a 0.25 DEL fee.
	funding := want.Reserve.Add(sdkmath.NewIntWithDecimal(25, 16))
	e.send(e.keys[3], &centerAddress, funding.BigInt(), data, true)
	lookup, err := center.ABI.Pack("tokens", want.Denom)
	require.NoError(t, err)
	response := e.send(e.keys[3], &centerAddress, new(big.Int), lookup, true)
	require.Len(t, response.Ret, 32)
	token := common.BytesToAddress(response.Ret)
	require.NotEqual(t, common.Address{}, token)
	require.True(t, want.Reserve.Equal(e.app.BankKeeper.GetBalance(e.ctx(), sdk.AccAddress(token.Bytes()), identity.BaseDenom).Amount))
	want.DRC20Contract = token.Hex()
	require.NoError(t, want.Validate())
	return &nativeCustomCollateralGenesisFixture{centerAddress, converterAddress, want}
}

func TestOwnedAppNativeDRC20GenesisAndRestore(t *testing.T) {
	if phase := os.Getenv(nativeCustomCollateralGenesisPhaseEnv); phase != "" {
		checkNativeDRC20Genesis(t, os.Getenv(nativeConsensusDaemonFixtureEnv), phase)
		return
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "native-drc20")
	require.NoError(t, os.Mkdir(dir, 0700))
	for _, phase := range []string{"seed", "reject", "accept", "restart"} {
		if !t.Run(phase, func(t *testing.T) {
			filter, historical := "^TestOwnedAppNativeDRC20GenesisAndRestore$", ""
			if phase == "seed" {
				filter, historical = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$", "seed"
			}
			child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=3m", "-test.run="+filter)
			child.Env = append(os.Environ(), nativeCustomCollateralGenesisPhaseEnv+"="+phase, nativeConsensusDaemonFixtureEnv+"="+dir,
				nativeHistoricalConsensusPhaseEnv+"="+historical, nativeHistoricalMaxNFTPhaseEnv+"=",
				nativeConsensusFeeRewardsEnv+"=", nativeConsensusIBCV1Env+"=", nativeConsensusUpgradeGovernanceEnv+"=",
				nativeConsensusJailRecoveryEnv+"=", nativeConsensusHeldDowntimeEnv+"=", nativeConsensusNFTDowntimeEnv+"=",
				nativeConsensusFullUpgradeEnv+"=", signedNFTAdmissionPhaseEnv+"=", delLifecycleFixtureEnv+"=", withdrawalGenesisFixtureEnv+"=")
			output, err := child.CombinedOutput()
			require.NoError(t, err, "%s", output)
		}) {
			return
		}
	}
}

func checkNativeDRC20Genesis(t *testing.T, dir, phase string) {
	t.Helper()
	doc, err := cmttypes.GenesisDocFromFile(filepath.Join(dir, "genesis.json"))
	require.NoError(t, err)
	var expected nativeHistoricalExpectations
	readNativeHistoricalJSON(t, filepath.Join(dir, "expectations.json"), &expected)
	dbName := "native-drc20-lifecycle"
	if phase == "reject" {
		dbName += "-reject"
	}
	database, err := dbm.NewDB(dbName, dbm.GoLevelDBBackend, dir)
	require.NoError(t, err)
	app := ownedapp.NewApp(log.NewNopLogger(), database, true, appOptions{
		flags.FlagHome: filepath.Join(dir, "home"), flags.FlagChainID: doc.ChainID, srvflags.EVMChainID: uint64(990001),
	})
	t.Cleanup(func() { require.NoError(t, app.Close()) })
	var state ownedapp.GenesisState
	require.NoError(t, json.Unmarshal(doc.AppState, &state))
	var coins cointypes.GenesisState
	require.NoError(t, app.AppCodec().UnmarshalJSON(state[cointypes.ModuleName], &coins))
	require.Len(t, coins.Coins, 1)
	want := coins.Coins[0]
	if phase == "reject" {
		rewardGenesis := maps.Clone(state)
		var profile ownedapp.WithdrawalProfile
		require.NoError(t, json.Unmarshal(rewardGenesis[ownedapp.WithdrawalGenesisKey], &profile))
		profile.EnableNativeFeeRewards = true
		rewardGenesis[ownedapp.WithdrawalGenesisKey], err = json.Marshal(profile)
		require.NoError(t, err)
		var validators validatortypes.GenesisState
		require.NoError(t, app.AppCodec().UnmarshalJSON(rewardGenesis[validatortypes.ModuleName], &validators))
		validators.Validators[0].RewardAddress = sdk.AccAddress(common.HexToAddress(want.DRC20Contract).Bytes()).String()
		rewardGenesis[validatortypes.ModuleName] = app.AppCodec().MustMarshalJSON(&validators)
		require.ErrorContains(t, ownedapp.ValidateNativeGenesisAllocation(app.AppCodec(), rewardGenesis), "fee reward recipient cannot be an approved DRC20 reserve")

		coins.Coins[0].Reserve = want.Reserve.AddRaw(1)
		state[cointypes.ModuleName] = app.AppCodec().MustMarshalJSON(&coins)
		doc.AppState, err = json.Marshal(state)
		require.NoError(t, err)
	}
	l := nativeHistoricalLifecycle{t: t, app: app, doc: doc, expect: expected, target: expected.Validators[2]}
	if phase != "restart" {
		require.Zero(t, app.LastBlockHeight())
		params := doc.ConsensusParams.ToProto()
		initial, err := app.InitChain(&abci.RequestInitChain{ChainId: doc.ChainID, InitialHeight: 1, Time: doc.GenesisTime,
			ConsensusParams: &params, AppStateBytes: doc.AppState})
		if phase == "reject" {
			require.ErrorContains(t, err, "approved DRC20 supply or bank DEL reserve differs")
			return
		}
		require.Equal(t, "accept", phase)
		require.NoError(t, err)
		validators, err := cmttypes.PB2TM.ValidatorUpdates(initial.Validators)
		require.NoError(t, err)
		l.set = cmttypes.NewValidatorSet(validators)
		l.commit(l.finalize(nil, nil))
	}
	if phase == "restart" {
		require.Equal(t, int64(5), app.LastBlockHeight())
		sold := sdkmath.NewIntWithDecimal(1, 18)
		want.Volume, want.Reserve = want.Volume.Sub(sold), want.Reserve.Sub(sold)
	} else {
		require.Equal(t, int64(1), app.LastBlockHeight())
	}
	require.True(t, coins.Params.BaseVolume.Equal(l.supply()))
	coin, err := app.NativeCoinKeeper.GetCoinByDRC(l.snapshot(), want.DRC20Contract)
	require.NoError(t, err)
	require.Equal(t, want.Denom, coin.Denom)
	require.True(t, want.Volume.Equal(coin.Volume))
	require.True(t, want.Reserve.Equal(coin.Reserve))
	target := sdk.AccAddress(common.HexToAddress(want.DRC20Contract).Bytes())
	child, _ := l.snapshot().CacheContext()
	require.ErrorContains(t, app.BankKeeper.SendCoins(child, sdk.AccAddress(expected.Actor.Bytes()), target,
		sdk.NewCoins(sdk.NewInt64Coin(identity.BaseDenom, 1))), "direct DEL credits")
	require.True(t, want.Reserve.Equal(app.BankKeeper.GetBalance(l.snapshot(), target, identity.BaseDenom).Amount))
	if phase == "restart" {
		altered, _ := l.snapshot().CacheContext()
		require.NoError(t, app.NativeCoinKeeper.UpdateCoinVR(altered, want.Denom, want.Volume, want.Reserve.AddRaw(1)))
		require.ErrorContains(t, app.LoadWithdrawalSettlement(altered), "approved DRC20 supply or bank DEL reserve differs")
	} else {
		var profile ownedapp.WithdrawalProfile
		require.NoError(t, json.Unmarshal(state[ownedapp.WithdrawalGenesisKey], &profile))
		l.checkSignedDRC20Delegation(profile, want)
	}
}

func (l nativeHistoricalLifecycle) checkSignedDRC20Delegation(profile ownedapp.WithdrawalProfile, coin cointypes.Coin) {
	t := l.t
	t.Helper()
	coinABI, err := contracts.CompiledABI("DRC20")
	require.NoError(t, err)
	converterABI, err := contracts.CompiledABI("MintCandyCurveConverter")
	require.NoError(t, err)
	token := common.HexToAddress(coin.DRC20Contract)
	validator := common.HexToAddress(l.target.EVM)
	sold := sdkmath.NewIntWithDecimal(1, 18)
	keeper := l.app.NativeValidatorKeeper
	const coinKind = validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN
	before, err := keeper.GetEVMStakeBinding(l.snapshot(), coinKind, l.expect.Gateway.Bytes(), l.expect.StakeID.Bytes())
	require.NoError(t, err)
	wdel := sdk.AccAddress(l.expect.Wrapped.Bytes())
	backing := l.app.BankKeeper.GetBalance(l.snapshot(), wdel, identity.BaseDenom).Amount
	assertState := func(delta sdkmath.Int) {
		t.Helper()
		ctx := l.snapshot()
		updated, err := l.app.NativeCoinKeeper.GetCoinByDRC(ctx, token.Hex())
		require.NoError(t, err)
		require.Equal(t, coin.Volume.Sub(delta).String(), updated.Volume.String())
		require.Equal(t, coin.Reserve.Sub(delta).String(), updated.Reserve.String())
		require.Equal(t, updated.Reserve.String(), l.app.BankKeeper.GetBalance(ctx, sdk.AccAddress(token.Bytes()), identity.BaseDenom).Amount.String())
		stake, err := keeper.GetEVMStakeBinding(ctx, coinKind, l.expect.Gateway.Bytes(), l.expect.StakeID.Bytes())
		require.NoError(t, err)
		require.Equal(t, before.RawAmount.Add(delta).String(), stake.RawAmount.String())
		require.Equal(t, backing.Add(delta).String(), l.app.BankKeeper.GetBalance(ctx, wdel, identity.BaseDenom).Amount.String())
	}
	approve := func(spender common.Address) *ethtypes.Transaction {
		data, err := coinABI.Pack("approve", spender, sold.BigInt())
		require.NoError(t, err)
		return l.signedEthereumCall(token, sdkmath.ZeroInt(), data)
	}
	convert := func(minimum sdkmath.Int) *ethtypes.Transaction {
		deadline := big.NewInt(l.snapshot().BlockTime().Add(time.Minute).Unix())
		data, err := converterABI.Pack("convertAndDelegate", validator, token, sold.BigInt(), minimum.BigInt(), deadline)
		require.NoError(t, err)
		return l.signedEthereumCall(profile.CurveConverter, sdkmath.ZeroInt(), data)
	}
	block, result := l.finalizeSignedEthereum(approve(l.expect.Gateway))
	require.NotEmpty(t, result.VmError, "unapproved spender must be rejected by the native admission hook")
	l.commit(block)
	l.submitSignedEthereum(approve(profile.CurveConverter))
	block, result = l.finalizeSignedEthereum(convert(sold.MulRaw(2)))
	require.NotEmpty(t, result.VmError, "minOut above the quote must revert the entire sale")
	l.commit(block)
	assertState(sdkmath.ZeroInt())
	l.submitSignedEthereum(convert(sold))
	assertState(sold)
}
