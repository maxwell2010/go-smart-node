package systemcontracts_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	"github.com/cometbft/cometbft/privval"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/maxwell2010/go-smart-node/network/launchgenesis"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const nativeConsensusDaemonFixtureEnv = "MCND_NATIVE_CONSENSUS_FIXTURE_DIR"
const nativeFoundingLedgerEnv = "MCND_FOUNDING_LEDGER_FILE"
const nativeConsensusFeeRewardsEnv = "MCND_NATIVE_CONSENSUS_FEE_REWARDS"
const nativeConsensusIBCV1Env = "MCND_NATIVE_CONSENSUS_IBC_V1"
const nativeConsensusDowntimeEnv = "MCND_NATIVE_CONSENSUS_DEL_DOWNTIME"
const nativeConsensusJailRecoveryEnv = "MCND_NATIVE_CONSENSUS_DEL_JAIL_RECOVERY"
const nativeConsensusHeldDowntimeEnv = "MCND_NATIVE_CONSENSUS_HELD_DEL_DOWNTIME"
const emptyUserAssetsPhaseEnv = "MCND_EMPTY_USER_ASSETS_PHASE"

type nativeConsensusFixtureValidator struct {
	Name         string `json:"name"`
	Operator     string `json:"operator"`
	EVM          string `json:"evm"`
	Consensus    string `json:"consensusAddress"`
	PubKey       string `json:"publicKey"`
	ReserveData  string `json:"reserveData"`
	Reward       string `json:"reward"`
	RewardNative string `json:"rewardNative"`
	Initial      int64  `json:"initialPower"`
	AfterStake   int64  `json:"afterStakePower"`
}

func exerciseFoundingLedgerRollback(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile,
	sponsor common.Address, ledger launchgenesis.Ledger, withdrawalSeconds int64,
) {
	t.Helper()
	ctx, _ := e.ctx().CacheContext()
	source := sdk.AccAddress(sponsor.Bytes())
	wrapped := sdk.AccAddress(profile.WrappedDEL.Bytes())
	balance := e.app.BankKeeper.GetBalance(ctx, source, identity.BaseDenom).Amount
	budget := sdkmath.NewIntWithDecimal(40_000, 18).SubRaw(1)
	require.True(t, balance.GT(budget))
	recipient := sdk.AccAddress(crypto.PubkeyToAddress(e.keys[0].PublicKey).Bytes())
	require.NoError(t, e.app.BankKeeper.SendCoins(ctx, source, recipient,
		sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, balance.Sub(budget)))))
	require.ErrorContains(t, launchgenesis.ApplyFoundingPositions(e.app, ctx, profile, sponsor, ledger, withdrawalSeconds), "funding call 4: execute delegateDELTo")
	require.True(t, e.app.BankKeeper.GetBalance(ctx, wrapped, identity.BaseDenom).Amount.IsZero())
	require.True(t, e.app.BankKeeper.GetBalance(ctx, source, identity.BaseDenom).Amount.Equal(budget))
	state, err := e.app.NativeValidatorKeeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, state.Validators)
	require.Empty(t, state.Delegations)
	require.Empty(t, state.EVMStakeBindings)
	t.Log("founding ledger rolled back after the fourth Gateway funding call exceeded the remaining balance")
}

func exportNativeConsensusDaemonFixture(t *testing.T, e isolatedBackendEnv, dir string, profile ownedapp.WithdrawalProfile,
	gateway, master, wrapper, vault contracts.Artifact, nft *nativeNFTDowntimeFixture, gov func(contracts.Artifact, common.Address, string, ...interface{})) {
	t.Helper()
	require.True(t, filepath.IsAbs(dir))
	info, err := os.Lstat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Zero(t, info.Mode().Perm()&0077)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, files)

	const gasLimit = 24_000_000
	key := e.keys[3]
	actor := crypto.PubkeyToAddress(key.PublicKey)
	launch, launchRehearsal := nativeLaunchSpecForMode(os.Getenv(nativeLaunchRehearsalEnv))
	var foundingLedger *launchgenesis.Ledger
	if path := os.Getenv(nativeFoundingLedgerEnv); path != "" {
		require.True(t, launch.sponsoredFounder(), "external ledger replay requires the isolated sponsored fixture")
		file, err := os.Open(path)
		require.NoError(t, err)
		ledger, err := launchgenesis.ReadLedger(file)
		require.NoError(t, file.Close())
		require.NoError(t, err)
		foundingLedger = &ledger
	}
	if launch.sponsoredFounder() {
		actor = nativeSponsoredFounderAddress()
		incoming := sdk.AccAddress(actor.Bytes())
		require.Nil(t, e.app.AccountKeeper.GetAccount(e.ctx(), incoming))
		e.app.AccountKeeper.SetAccount(e.ctx(), e.app.AccountKeeper.NewAccountWithAddress(e.ctx(), incoming))
	}
	beneficiary := sdk.AccAddress(actor.Bytes()).String()
	emptyAssets := seedEmptyUserAssets()
	fullUpgrade := launchRehearsal || os.Getenv(nativeConsensusFullUpgradeEnv) == "1"
	coinIndexMigration := os.Getenv("MCND_NATIVE_COIN_INDEX_MIGRATION_CI") == "1"
	fourSignerMigration := os.Getenv("MCND_NATIVE_FOUR_SIGNER_MIGRATION_CI") == "1"
	feeRewards := emptyAssets || fullUpgrade || os.Getenv(nativeConsensusFeeRewardsEnv) == "1"
	ibcV1 := fullUpgrade || os.Getenv(nativeConsensusIBCV1Env) == "1"
	upgradeGovernance := launchRehearsal || os.Getenv(nativeConsensusUpgradeGovernanceEnv) == "1"
	historical := os.Getenv(nativeHistoricalConsensusPhaseEnv) == "seed"
	downtime := os.Getenv(nativeConsensusDowntimeEnv) == "1" || historical
	jailRecovery := os.Getenv(nativeConsensusJailRecoveryEnv) == "1"
	heldDowntime := os.Getenv(nativeConsensusHeldDowntimeEnv) == "1"
	require.False(t, jailRecovery && !downtime, "the jail recovery fixture requires the downtime profile")
	require.False(t, heldDowntime && (!downtime || jailRecovery), "held downtime has its own isolated fixture")
	require.False(t, upgradeGovernance && !fullUpgrade && (feeRewards || ibcV1 || downtime), "the basic upgrade governance fixture has a separate profile")
	require.False(t, nft != nil && !fullUpgrade && (!downtime || jailRecovery || heldDowntime), "NFT downtime has its own isolated fixture")
	if fullUpgrade {
		require.True(t, upgradeGovernance && (nft != nil || launch.emptyAssets()) && !downtime && !jailRecovery && !heldDowntime, "full upgrade requires its own fixture and NFT profile")
	}
	initial, after := []int64{2, 1, 0}, []int64{2, 0, 3}
	keyScope := "mintcandy native consensus process fixture "
	if fullUpgrade {
		keyScope = "mintcandy full profile upgrade process fixture "
	}
	if coinIndexMigration {
		require.True(t, fullUpgrade && upgradeGovernance && !launchRehearsal, "coin index migration requires the full upgrade fixture")
	}
	if fourSignerMigration {
		require.True(t, coinIndexMigration, "four-signer migration requires the coin index migration fixture")
		initial = []int64{2, 2, 2, 2}
		after = initial
		keyScope = "mintcandy four validator module migration process fixture "
	}
	if launchRehearsal {
		initial, after = launch.powers, launch.powers
		keyScope = launch.keyScope
	}
	if downtime {
		require.False(t, feeRewards || ibcV1, "the ordinary DEL downtime fixture has a separate profile")
		initial, after = []int64{3, 3, 2}, []int64{3, 3, 0}
		keyScope = "mintcandy native ordinary del downtime process fixture "
	}
	const jailRecoveryDuration = 60 * time.Second
	if jailRecovery {
		keyScope = "mintcandy native ordinary del jail recovery process fixture "
	}
	if heldDowntime {
		initial, after = []int64{3, 3, 0}, []int64{3, 3, 2}
		keyScope = "mintcandy native held del downtime process fixture "
	}
	if nft != nil && !fullUpgrade {
		initial, after = []int64{3, 3, 0}, []int64{3, 3, 2}
		keyScope = "mintcandy native NFT downtime process fixture "
	}
	if historical {
		require.False(t, feeRewards || ibcV1 || upgradeGovernance || jailRecovery || heldDowntime)
		if nft != nil {
			require.True(t, os.Getenv(nativeHistoricalMaxNFTPhaseEnv) == "seed" || os.Getenv(nativeCustomNFTPhaseEnv) == "seed")
		}
		initial, after = []int64{4, 4, 2}, []int64{4, 4, 0}
		keyScope = nativeHistoricalConsensusKeyScope
	}
	if emptyAssets && !launchRehearsal {
		require.False(t, fullUpgrade || historical || downtime || launchRehearsal || nft != nil)
		initial, after = []int64{10000, 10000, 10000, 10000}, []int64{10000, 10000, 10000, 10000}
		keyScope = "mintcandy empty user assets process fixture "
	}
	commission := sdkmath.LegacyNewDecWithPrec(ownedapp.NativeValidatorMinCommissionPercent, 2)
	if feeRewards {
		commission = sdkmath.LegacyMustNewDecFromStr("0.25")
	}
	if launch.fourValidators() {
		commission = sdkmath.LegacyMustNewDecFromStr("0.05")
	}
	collector := authtypes.NewModuleAddress(authtypes.FeeCollectorName)
	nativeKeeper := e.app.NativeValidatorKeeper
	domain, ok := nativeKeeper.ConfiguredEVMSourceDomain()
	require.True(t, ok)
	if launchRehearsal {
		require.Equal(t, launch.chainID, domain.ChainID)
		require.Equal(t, launch.evmID, domain.EVMChainID)
	}
	var custom *nativeCustomCollateralGenesisFixture
	if emptyAssets || launchRehearsal || coinIndexMigration || os.Getenv(nativeCustomCollateralGenesisPhaseEnv) == "seed" {
		require.True(t, emptyAssets || historical || launchRehearsal || coinIndexMigration)
		var fixtureVolume int64
		if os.Getenv(nativeCustomNFTMaxEnv) == "1" && nft != nil {
			fixtureVolume = 2*int64(nft.units) + 8
		}
		if launch.fourValidators() {
			fixtureVolume = launch.drc20Reserve
		}
		if launch.sponsoredFounder() {
			require.NotEmpty(t, profile.TokenCenter)
			require.NotEmpty(t, profile.CurveConverter)
			custom = &nativeCustomCollateralGenesisFixture{center: profile.TokenCenter, converter: profile.CurveConverter}
		} else {
			custom = seedNativeCustomCollateralGenesis(t, e, profile, gov, fixtureVolume)
		}
		if (launchRehearsal && !launch.fourValidators()) || os.Getenv(nativeCustomNFTPhaseEnv) == "seed" {
			require.NotNil(t, nft)
			nft.mintCustom(t, e, custom, actor)
		}
	}
	config, err := json.Marshal(profile)
	require.NoError(t, err)
	type masterValidator struct {
		Validator common.Address
		Meta      string
	}
	var masterValidators []masterValidator
	var validators []nativeConsensusFixtureValidator
	for i := range initial {
		name := string("abcd"[i])
		// Reproducible keys scoped to the isolated CI chain and private homes.
		signer := cmted25519.GenPrivKeyFromSecret([]byte(keyScope + name))
		pub := signer.PubKey()
		address := common.BytesToAddress(pub.Address())
		if (jailRecovery || historical) && name == "c" {
			address = crypto.PubkeyToAddress(e.keys[2].PublicKey)
			require.NotEqual(t, common.BytesToAddress(pub.Address()), address)
		}
		if os.Getenv("MCND_VALIDATOR_CONTROLS_PHASE") != "" {
			address = crypto.PubkeyToAddress(e.keys[i].PublicKey)
		}
		reward := crypto.PubkeyToAddress(e.keys[i].PublicKey)
		if fourSignerMigration && reward == actor {
			reward = crypto.PubkeyToAddress(e.keys[4].PublicKey)
		}
		if launch.fourValidators() {
			reward = crypto.PubkeyToAddress(nativeFourLaunchStakeKey(e.keys, i).PublicKey)
			address = reward
			require.NotEqual(t, common.BytesToAddress(pub.Address()), address)
		}
		operator, err := nativeKeeper.ValidatorAddressCodec().BytesToString(address.Bytes())
		require.NoError(t, err)
		moniker := "native-consensus-fixture-" + name
		publicKey := base64.StdEncoding.EncodeToString(pub.Bytes())
		rewardNative := sdk.AccAddress(reward.Bytes()).String()
		meta, err := json.Marshal(map[string]interface{}{
			"operator_address": operator, "reward_address": reward.Hex(), "consensus_pubkey": publicKey,
			"description": map[string]string{"moniker": moniker}, "commission": commission.String(),
		})
		require.NoError(t, err)
		masterValidators = append(masterValidators, masterValidator{Validator: address, Meta: string(meta)})
		row, err := validatortypes.NewValidator(operator, rewardNative, &ed25519.PubKey{Key: pub.Bytes()},
			validatortypes.Description{Moniker: moniker}, commission)
		require.NoError(t, err)
		row.Online = true
		if foundingLedger == nil {
			require.NoError(t, nativeKeeper.SetValidator(e.ctx(), row))
		}
		validators = append(validators, nativeConsensusFixtureValidator{
			Name: name, Operator: operator, EVM: address.Hex(), Consensus: pub.Address().String(), PubKey: publicKey,
			Initial: initial[i], AfterStake: after[i],
			Reward: reward.Hex(), RewardNative: rewardNative,
		})
		if !historical {
			pv := privval.NewFilePV(signer, filepath.Join(dir, "priv_validator_key_"+name+".json"), filepath.Join(dir, "unused_sign_state_"+name+".json"))
			pv.Key.Save()
		}
	}
	withdrawalSeconds := int64(20)
	if ibcV1 {
		withdrawalSeconds = int64(validatortypes.DefaultParams().UndelegationTime / time.Second)
	}
	if historical {
		withdrawalSeconds = 2
	}
	if foundingLedger != nil {
		sponsor := crypto.PubkeyToAddress(key.PublicKey)
		exerciseFoundingLedgerRollback(t, e, profile, sponsor, *foundingLedger, withdrawalSeconds)
		require.NoError(t, launchgenesis.ApplyFoundingPositions(e.app, e.ctx(), profile, sponsor, *foundingLedger, withdrawalSeconds))
		require.ErrorContains(t,
			launchgenesis.ApplyFoundingPositions(e.app, e.ctx(), profile, sponsor, *foundingLedger, withdrawalSeconds), "empty native validator roster")
		for i := range validators {
			row, err := nativeKeeper.GetValidator(e.ctx(), validators[i].Operator)
			require.NoError(t, err)
			validators[i].RewardNative = row.RewardAddress
			reward, err := sdk.AccAddressFromBech32(row.RewardAddress)
			require.NoError(t, err)
			validators[i].Reward = common.BytesToAddress(reward).Hex()
		}
	} else {
		for _, validator := range masterValidators {
			gov(master, profile.MasterValidator, "addValidators", []masterValidator{validator})
		}
		gov(gateway, profile.Gateway, "setFreezeTime", big.NewInt(withdrawalSeconds), new(big.Int))
	}
	if !launch.sponsoredFounder() {
		e.ctx().KVStore(e.app.GetKey(validatortypes.StoreKey)).Set([]byte{ownedapp.WithdrawalProfileKey}, config)
		require.NoError(t, e.app.LoadWithdrawalSettlement(e.ctx()))
	}

	amount := func(power int64) sdkmath.Int { return sdkmath.NewIntWithDecimal(power, 18) }
	initialStakeBonus := sdkmath.ZeroInt()
	if fourSignerMigration {
		// A 0.25 DEL withdrawal must leave all four validators at equal power.
		initialStakeBonus = amount(1).QuoRaw(2)
	}
	initialStake := func(validator nativeConsensusFixtureValidator) sdkmath.Int {
		return amount(validator.Initial).Add(initialStakeBonus)
	}
	pack := func(artifact contracts.Artifact, method string, args ...interface{}) []byte {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		return data
	}
	var seeded int
	for i, validator := range validators {
		if validator.Initial == 0 {
			continue
		}
		if foundingLedger != nil {
			seeded++
			continue
		}
		e.nextGasLimit(gasLimit)
		stakeKey := key
		if launch.fourValidators() && !emptyAssets {
			stakeKey = nativeFourLaunchStakeKey(e.keys, i)
		}
		operator := common.HexToAddress(validator.EVM)
		principal := initialStake(validator)
		if launch.sponsoredFounder() {
			// The disposable deployment app funds the Gateway with its own
			// signer. The released app's native InitChain attests this EVM
			// snapshot against the projected founder-owned positions.
			e.send(stakeKey, &profile.Gateway, principal.BigInt(), pack(gateway, "delegateDELTo", actor, operator), true)
			var words [64]byte // Zero token ID and hold timestamp.
			position := validatorkeeper.GatewayDELStake{
				Gateway: profile.Gateway, WrappedDEL: profile.WrappedDEL, Beneficiary: actor, Validator: operator,
				StakeID: crypto.Keccak256Hash(operator[:], actor[:], profile.WrappedDEL[:], words[:]), Amount: principal,
			}
			require.NoError(t, nativeKeeper.ApplyGatewayDELDelegation(e.ctx(), position, principal))
		} else {
			e.send(stakeKey, &profile.Gateway, principal.BigInt(), pack(gateway, "delegateDEL", operator), true)
		}
		seeded++
	}
	ctx, cdc := e.ctx(), e.app.AppCodec()
	for _, validator := range validators {
		active, err := nativeKeeper.GetActiveDELReserve(ctx, validator.Operator)
		require.NoError(t, err)
		require.True(t, active.Equal(initialStake(validator)))
	}
	genesis, err := e.app.ModuleManager.ExportGenesisForModules(ctx, cdc, nil)
	require.NoError(t, err)
	var nativeGenesis validatortypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(genesis[validatortypes.ModuleName], &nativeGenesis))
	require.Len(t, nativeGenesis.Delegations, seeded)
	require.Len(t, nativeGenesis.EVMStakeBindings, seeded)
	require.Empty(t, nativeGenesis.EVMFrozenBindings)
	require.Empty(t, nativeGenesis.Undelegations)
	nativeGenesis.Params.MaxValidators = 2
	if emptyAssets || launchRehearsal || fourSignerMigration {
		nativeGenesis.Params.MaxValidators = uint32(len(validators))
	}
	if launch.fourValidators() {
		nativeGenesis.Params.MaxValidators = 16
		nativeGenesis.Params.SignedBlocksWindow = 1000
		nativeGenesis.Params.MinSignedPerWindow = sdkmath.LegacyMustNewDecFromStr("0.50")
		nativeGenesis.Params.SlashFractionDowntime = sdkmath.LegacyMustNewDecFromStr("0.01")
		nativeGenesis.Params.DowntimeJailDuration = time.Hour
	}
	if downtime {
		nativeGenesis.Params.MaxValidators = 3
		nativeGenesis.Params.SignedBlocksWindow = 3
		nativeGenesis.Params.MinSignedPerWindow = sdkmath.LegacyMustNewDecFromStr("0.25")
		nativeGenesis.Params.SlashFractionDowntime = sdkmath.LegacyMustNewDecFromStr("0.01")
		nativeGenesis.Params.DowntimeJailDuration = time.Hour
	}
	if jailRecovery {
		nativeGenesis.Params.DowntimeJailDuration = jailRecoveryDuration
	}
	if emptyAssets || historical || fullUpgrade {
		nativeGenesis.Params.SlashFractionDoubleSign = sdkmath.LegacyMustNewDecFromStr("0.05")
		for i := range nativeGenesis.EVMStakeBindings {
			row := &nativeGenesis.EVMStakeBindings[i]
			row.CreatedHeight, row.UpdatedHeight = 1, 1
		}
	}
	nativeGenesis.BindingRevision = 0
	genesis[validatortypes.ModuleName] = cdc.MustMarshalJSON(&nativeGenesis)

	// The seed App used an SDK validator only to produce the signed EVM
	// snapshot. The process chain starts with the native validator set alone.
	defaults := e.app.DefaultGenesis()
	for _, module := range []string{stakingtypes.ModuleName, genutiltypes.ModuleName, slashingtypes.ModuleName, distrtypes.ModuleName} {
		genesis[module] = defaults[module]
	}
	var bank banktypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(genesis[banktypes.ModuleName], &bank))
	discard := map[string]bool{
		authtypes.NewModuleAddress(stakingtypes.BondedPoolName).String():    true,
		authtypes.NewModuleAddress(stakingtypes.NotBondedPoolName).String(): true,
		authtypes.NewModuleAddress(distrtypes.ModuleName).String():          true,
	}
	if feeRewards {
		discard[collector.String()] = true
	}
	bank.Supply = sdk.NewCoins()
	balances := bank.Balances[:0]
	for _, row := range bank.Balances {
		if !discard[row.Address] {
			bank.Supply = bank.Supply.Add(row.Coins...)
			balances = append(balances, row)
		}
	}
	bank.Balances = balances
	if launch.emptyAssets() {
		applyNativeEmptyLaunchAllocation(t, &bank, profile.WrappedDEL, actor, launch)
		if foundingLedger != nil {
			bank.Balances, bank.Supply = foundingLedger.Bank.Balances, foundingLedger.Bank.Supply
		}
	} else if launch.fourValidators() {
		require.NotNil(t, custom)
		require.NotNil(t, nft)
		applyNativeFourLaunchAllocation(t, &bank, profile.WrappedDEL, common.HexToAddress(custom.coin.DRC20Contract), nft.address)
	}
	genesis[banktypes.ModuleName] = cdc.MustMarshalJSON(&bank)
	profile.EnableNativeConsensus = true
	profile.EnableNativeFeeRewards = feeRewards
	profile.EnableNativeIBCV1 = ibcV1
	profile.EnableNativeOrdinaryDELDowntime = downtime || fullUpgrade
	profile.EnableNativeDuplicateVote = historical || fullUpgrade
	if historical {
		profile.EnableNativeGovernance = true
		configureNativeRegistrationGovernance(t, e, genesis)
	}
	profile.EnableDELHold = heldDowntime || fullUpgrade
	profile.EnableDELHoldReset = fullUpgrade
	profile.EnableNativeHeldDELDowntime = heldDowntime || fullUpgrade
	var governanceFixture []byte
	if upgradeGovernance {
		profile.EnableNativeGovernance = true
		profile.EnableNativeUpgradeGovernance = true
		if !launchRehearsal {
			governanceFixture = nativeGovernanceUpgradeFixture(t, e, genesis, fullUpgrade)
		}
	}
	if nft != nil {
		profile.EnableNFTDelegation = true
		profile.EnableNativeNFTDowntime = true
		nft.addGenesis(t, e, profile, genesis, beneficiary)
	}
	if emptyAssets {
		profile.EnableNFTDelegation = true
		profile.EnableNativeNFTDowntime = fullUpgrade
		addEmptyNFTGenesis(t, e, profile, genesis)
	}
	if custom != nil {
		var coins cointypes.GenesisState
		require.NoError(t, cdc.UnmarshalJSON(genesis[cointypes.ModuleName], &coins))
		require.Empty(t, coins.Coins)
		if !emptyAssets {
			coins.Coins = []cointypes.Coin{custom.coin}
		}
		if launch.emptyAssets() {
			coins.Params.BaseTitle = "MintCandy"
		}
		genesis[cointypes.ModuleName] = cdc.MustMarshalJSON(&coins)
		profile.TokenCenter, profile.CurveConverter = custom.center, custom.converter
	}
	genesis[ownedapp.WithdrawalGenesisKey], err = json.Marshal(profile)
	require.NoError(t, err)
	require.NoError(t, ownedapp.ReconcileNativeGenesisAllocation(cdc, genesis))
	require.NoError(t, ownedapp.ValidateNativeGenesisAllocation(cdc, genesis))

	genesisTime := time.Now().UTC().Truncate(time.Second).Add(-time.Second)
	if launchRehearsal {
		genesisTime = nativeLaunchGenesisTime
	}
	if downtime {
		// Let all three processes connect before the first three-sample window.
		genesisTime = genesisTime.Add(15 * time.Second)
	}
	doc := cmttypes.GenesisDoc{ChainID: ctx.ChainID(), InitialHeight: ctx.BlockHeight(),
		GenesisTime: genesisTime, ConsensusParams: cmttypes.DefaultConsensusParams()}
	if launch.emptyAssets() {
		doc.ConsensusParams.Block.MaxGas = gasLimit
		doc.ConsensusParams.Evidence.MaxAgeNumBlocks = 100_000
		doc.ConsensusParams.Evidence.MaxAgeDuration = 48 * time.Hour
	}
	if emptyAssets || historical || fullUpgrade {
		doc.InitialHeight = 1
	}
	if historical {
		doc.ConsensusParams.Evidence.MaxAgeNumBlocks = 5
		doc.ConsensusParams.Evidence.MaxAgeDuration = 4 * time.Second
	}
	doc.AppState, err = json.Marshal(genesis)
	require.NoError(t, err)
	body, err := cmtjson.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	if fullUpgrade {
		// Validate the exact serialized envelope through the same SDK and
		// application validation used by mcnd genesis validate.
		saved, err := genutiltypes.AppGenesisFromReader(bytes.NewReader(body))
		require.NoError(t, err)
		require.NoError(t, saved.ValidateAndComplete())
		var serializedState map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(saved.AppState, &serializedState))
		require.JSONEq(t, string(genesis[ownedapp.WithdrawalGenesisKey]), string(serializedState[ownedapp.WithdrawalGenesisKey]))
		require.NoError(t, ownedapp.ValidateNativeGenesisAllocation(cdc, serializedState))
		require.NoError(t, ownedapp.NewBasicModuleManager().ValidateGenesis(cdc, e.app.TxConfig(), serializedState))
	}
	write := func(name string, data []byte) {
		t.Helper()
		file, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, err)
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		require.NoError(t, writeErr)
		require.NoError(t, closeErr)
	}
	write("genesis.json", body)
	if launchRehearsal {
		checkNativeLaunchPublicGenesis(t, body, launch)
		require.NoError(t, os.Mkdir(filepath.Join(dir, "public"), 0700))
		write("public/genesis.json", body)
	}
	if len(governanceFixture) != 0 {
		write("governance.json", governanceFixture)
	}

	target := common.HexToAddress(validators[2].EVM)
	var zero common.Hash
	holdDeadline := new(big.Int)
	if heldDowntime {
		holdDeadline.SetInt64(genesisTime.Add(50 * time.Second).Unix())
	}
	holdWord := common.BigToHash(holdDeadline)
	stakeID := crypto.Keccak256Hash(target.Bytes(), actor.Bytes(), profile.WrappedDEL.Bytes(), zero.Bytes(), holdWord.Bytes())
	hexData := func(value []byte) string { return "0x" + hex.EncodeToString(value) }
	for i := range validators {
		validators[i].ReserveData = hexData(pack(gateway, "getValidatorReserve", common.HexToAddress(validators[i].EVM), profile.WrappedDEL))
	}
	expectations := map[string]interface{}{
		"chainId": ctx.ChainID(), "evmChainId": domain.EVMChainID, "gateway": profile.Gateway.Hex(), "wrapped": profile.WrappedDEL.Hex(),
		"actor": actor.Hex(), "actorNative": beneficiary, "wrappedNative": sdk.AccAddress(profile.WrappedDEL.Bytes()).String(),
		"feeCollector": common.BytesToAddress(collector).Hex(), "feeCollectorNative": collector.String(), "feeRewards": feeRewards,
		"validators": validators, "stakeId": stakeID.Hex(),
		"supplyData": hexData(pack(wrapper, "totalSupply")), "escrowData": hexData(pack(wrapper, "balanceOf", profile.Gateway)),
	}
	if launchRehearsal {
		expect, err := json.Marshal(expectations)
		require.NoError(t, err)
		write("expectations.json", expect)
		return
	}
	if downtime {
		masterState := make(map[string]string, len(validators))
		for _, validator := range validators {
			masterState[validator.Name] = hexData(pack(master, "getValidatorState", common.HexToAddress(validator.EVM)))
		}
		expectations["master"] = profile.MasterValidator.Hex()
		expectations["masterStateData"] = masterState
		expectations["stakePenaltyData"] = hexData(pack(gateway, "getStakePenaltyIndex", stakeID))
		if nft != nil {
			nft.expectations(t, e, profile, gateway, vault, target, actor, key, expectations)
			if historical {
				expectations["delStakeId"] = stakeID.Hex()
			}
		}
		if heldDowntime {
			nonce := e.app.EVMKeeper.GetNonce(ctx, actor)
			price := big.NewInt(1_000_000_000_000)
			signer := ethtypes.NewEIP155Signer(big.NewInt(990001))
			principal := amount(2).BigInt()
			remainder := new(big.Int).Sub(principal, new(big.Int).Div(new(big.Int).Set(principal), big.NewInt(100)))
			calls := []struct {
				name  string
				value *big.Int
				input []byte
			}{
				{"heldDelegate", principal, pack(gateway, "delegateHoldDEL", target, holdDeadline)},
				{"heldWithdraw", new(big.Int), pack(gateway, "withdrawHold", target, profile.WrappedDEL, remainder, holdDeadline)},
			}
			for i, call := range calls {
				tx := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce + uint64(i), To: &profile.Gateway,
					Gas: gasLimit, GasPrice: price, Data: call.input, Value: call.value})
				signed, err := ethtypes.SignTx(tx, signer, key)
				require.NoError(t, err)
				raw, err := signed.MarshalBinary()
				require.NoError(t, err)
				expectations[call.name] = map[string]string{"raw": hexData(raw), "hash": signed.Hash().Hex()}
			}
			expectations["heldNonce"] = nonce
			expectations["holdDeadline"] = holdDeadline.Int64()
			expectations["heldStakeData"] = hexData(pack(gateway, "getHoldStake", target, actor, profile.WrappedDEL, holdDeadline))
			expectations["heldFrozenData"] = hexData(pack(gateway, "getFrozenStake", new(big.Int)))
		}
		if jailRecovery {
			nonce := e.app.EVMKeeper.GetNonce(ctx, target)
			price := big.NewInt(1_000_000_000_000)
			signer := ethtypes.NewEIP155Signer(big.NewInt(990001))
			input := pack(master, "unpauseSelf")
			for i, name := range []string{"earlyUnpause", "unpause"} {
				tx := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce + uint64(i), To: &profile.MasterValidator,
					Gas: gasLimit, GasPrice: price, Data: input})
				signed, err := ethtypes.SignTx(tx, signer, e.keys[2])
				require.NoError(t, err)
				raw, err := signed.MarshalBinary()
				require.NoError(t, err)
				expectations[name] = map[string]string{"raw": hexData(raw), "hash": signed.Hash().Hex()}
			}
			expectations["unjailNonce"] = nonce
			expectations["jailSeconds"] = int64(jailRecoveryDuration / time.Second)

			actorNonce := e.app.EVMKeeper.GetNonce(ctx, actor)
			topUp := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: actorNonce, To: &profile.Gateway,
				Gas: gasLimit, GasPrice: price, Data: pack(gateway, "delegateDEL", target), Value: amount(1).QuoRaw(4).BigInt()})
			signed, err := ethtypes.SignTx(topUp, signer, key)
			require.NoError(t, err)
			raw, err := signed.MarshalBinary()
			require.NoError(t, err)
			expectations["recoveredTopUp"] = map[string]string{"raw": hexData(raw), "hash": signed.Hash().Hex()}
			expectations["recoveredTopUpNonce"] = actorNonce
		}
		body, err := json.Marshal(expectations)
		require.NoError(t, err)
		write("expectations.json", body)
		return
	}
	deposit := amount(validators[2].AfterStake)
	nonce := e.app.EVMKeeper.GetNonce(ctx, actor)
	price := big.NewInt(1_000_000_000_000)
	signer := ethtypes.NewEIP155Signer(big.NewInt(990001))
	input := pack(gateway, "delegateDEL", target)
	sign := func(nonce uint64, value *big.Int) map[string]string {
		t.Helper()
		tx := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce, To: &profile.Gateway, Value: value,
			Gas: gasLimit, GasPrice: price, Data: input})
		signed, err := ethtypes.SignTx(tx, signer, key)
		require.NoError(t, err)
		raw, err := signed.MarshalBinary()
		require.NoError(t, err)
		return map[string]string{"raw": "0x" + hex.EncodeToString(raw), "hash": signed.Hash().Hex()}
	}
	topUp := amount(1).QuoRaw(4)
	expectations["amount"], expectations["topUpAmount"] = deposit.String(), topUp.String()
	expectations["gasPrice"], expectations["gasLimit"], expectations["nonce"] = price.String(), gasLimit, nonce
	expectations["delegate"], expectations["topUp"] = sign(nonce, deposit.BigInt()), sign(nonce+1, topUp.BigInt())
	if fullUpgrade {
		target := validators[0]
		principal := amount(1).QuoRaw(4)
		// The governance scenario commits two proposals and two votes before
		// the same actor submits this EVM withdrawal using the shared nonce.
		withdraw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce + 4, To: &profile.Gateway, Value: new(big.Int),
			Gas: gasLimit, GasPrice: price, Data: pack(gateway, "withdraw", common.HexToAddress(target.EVM), profile.WrappedDEL, principal.BigInt())})
		signed, err := ethtypes.SignTx(withdraw, signer, key)
		require.NoError(t, err)
		raw, err := signed.MarshalBinary()
		require.NoError(t, err)
		expectations["upgradeWithdrawal"] = map[string]interface{}{
			"raw": hexData(raw), "hash": signed.Hash().Hex(), "nonce": nonce + 4,
			"validator": target.EVM, "validatorNative": target.Operator,
			"pendingAtomic": principal.String(), "activeAtomic": initialStake(target).Sub(principal).String(),
			"frozenData": []string{hexData(pack(gateway, "getFrozenStake", new(big.Int)))},
		}
	}
	expect, err := json.Marshal(expectations)
	require.NoError(t, err)
	write("expectations.json", expect)
}
