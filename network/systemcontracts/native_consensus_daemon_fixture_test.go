package systemcontracts_test

import (
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
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const nativeConsensusDaemonFixtureEnv = "MCND_NATIVE_CONSENSUS_FIXTURE_DIR"
const nativeConsensusFeeRewardsEnv = "MCND_NATIVE_CONSENSUS_FEE_REWARDS"

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

func exportNativeConsensusDaemonFixture(t *testing.T, e isolatedBackendEnv, dir string, profile ownedapp.WithdrawalProfile,
	gateway, master, wrapper contracts.Artifact, gov func(contracts.Artifact, common.Address, string, ...interface{})) {
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
	beneficiary := sdk.AccAddress(actor.Bytes()).String()
	feeRewards := os.Getenv(nativeConsensusFeeRewardsEnv) == "1"
	commission := sdkmath.LegacyZeroDec()
	if feeRewards {
		commission = sdkmath.LegacyMustNewDecFromStr("0.25")
	}
	collector := authtypes.NewModuleAddress(authtypes.FeeCollectorName)
	nativeKeeper := e.app.NativeValidatorKeeper
	config, err := json.Marshal(profile)
	require.NoError(t, err)
	type masterValidator struct {
		Validator common.Address
		Meta      string
	}
	var masterValidators []masterValidator
	var validators []nativeConsensusFixtureValidator
	for i, name := range []string{"a", "b", "c"} {
		// Reproducible keys scoped to the isolated CI chain and private homes.
		signer := cmted25519.GenPrivKeyFromSecret([]byte("mintcandy native consensus process fixture " + name))
		pub := signer.PubKey()
		address := common.BytesToAddress(pub.Address())
		operator, err := nativeKeeper.ValidatorAddressCodec().BytesToString(address.Bytes())
		require.NoError(t, err)
		moniker := "native-consensus-fixture-" + name
		publicKey := base64.StdEncoding.EncodeToString(pub.Bytes())
		reward := crypto.PubkeyToAddress(e.keys[i].PublicKey)
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
		require.NoError(t, nativeKeeper.SetValidator(e.ctx(), row))
		validators = append(validators, nativeConsensusFixtureValidator{
			Name: name, Operator: operator, EVM: address.Hex(), Consensus: pub.Address().String(), PubKey: publicKey,
			Initial: []int64{2, 1, 0}[i], AfterStake: []int64{2, 0, 3}[i],
			Reward: reward.Hex(), RewardNative: rewardNative,
		})
		pv := privval.NewFilePV(signer, filepath.Join(dir, "priv_validator_key_"+name+".json"), filepath.Join(dir, "unused_sign_state_"+name+".json"))
		pv.Key.Save()
	}
	for _, validator := range masterValidators {
		gov(master, profile.MasterValidator, "addValidators", []masterValidator{validator})
	}
	gov(gateway, profile.Gateway, "setFreezeTime", big.NewInt(20), new(big.Int))
	e.ctx().KVStore(e.app.GetKey(validatortypes.StoreKey)).Set([]byte{ownedapp.WithdrawalProfileKey}, config)
	require.NoError(t, e.app.LoadWithdrawalSettlement(e.ctx()))

	amount := func(power int64) sdkmath.Int { return sdkmath.NewIntWithDecimal(power, 18) }
	pack := func(artifact contracts.Artifact, method string, args ...interface{}) []byte {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		return data
	}
	for _, validator := range validators[:2] {
		e.nextGasLimit(gasLimit)
		e.send(key, &profile.Gateway, amount(validator.Initial).BigInt(),
			pack(gateway, "delegateDEL", common.HexToAddress(validator.EVM)), true)
	}
	ctx, cdc := e.ctx(), e.app.AppCodec()
	for _, validator := range validators {
		active, err := nativeKeeper.GetActiveDELReserve(ctx, validator.Operator)
		require.NoError(t, err)
		require.True(t, active.Equal(amount(validator.Initial)))
	}
	genesis, err := e.app.ModuleManager.ExportGenesisForModules(ctx, cdc, nil)
	require.NoError(t, err)
	var nativeGenesis validatortypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(genesis[validatortypes.ModuleName], &nativeGenesis))
	require.Len(t, nativeGenesis.Delegations, 2)
	require.Len(t, nativeGenesis.EVMStakeBindings, 2)
	require.Empty(t, nativeGenesis.EVMFrozenBindings)
	require.Empty(t, nativeGenesis.Undelegations)
	nativeGenesis.Params.MaxValidators = 2
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
	genesis[banktypes.ModuleName] = cdc.MustMarshalJSON(&bank)
	profile.EnableNativeConsensus = true
	profile.EnableNativeFeeRewards = feeRewards
	genesis[ownedapp.WithdrawalGenesisKey], err = json.Marshal(profile)
	require.NoError(t, err)
	require.NoError(t, ownedapp.ReconcileNativeGenesisAllocation(cdc, genesis))
	require.NoError(t, ownedapp.ValidateNativeGenesisAllocation(cdc, genesis))

	doc := cmttypes.GenesisDoc{ChainID: ctx.ChainID(), InitialHeight: ctx.BlockHeight(),
		GenesisTime: time.Now().UTC().Truncate(time.Second).Add(-time.Second), ConsensusParams: cmttypes.DefaultConsensusParams()}
	doc.AppState, err = json.Marshal(genesis)
	require.NoError(t, err)
	body, err := cmtjson.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
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

	target := common.HexToAddress(validators[2].EVM)
	var zero common.Hash
	stakeID := crypto.Keccak256Hash(target.Bytes(), actor.Bytes(), profile.WrappedDEL.Bytes(), zero.Bytes(), zero.Bytes())
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
	hexData := func(value []byte) string { return "0x" + hex.EncodeToString(value) }
	for i := range validators {
		validators[i].ReserveData = hexData(pack(gateway, "getValidatorReserve", common.HexToAddress(validators[i].EVM), profile.WrappedDEL))
	}
	expect, err := json.Marshal(map[string]interface{}{
		"chainId": ctx.ChainID(), "evmChainId": 990001, "gateway": profile.Gateway.Hex(), "wrapped": profile.WrappedDEL.Hex(),
		"actor": actor.Hex(), "actorNative": beneficiary, "wrappedNative": sdk.AccAddress(profile.WrappedDEL.Bytes()).String(),
		"amount": deposit.String(), "topUpAmount": topUp.String(), "gasPrice": price.String(), "gasLimit": gasLimit, "nonce": nonce,
		"feeCollector": common.BytesToAddress(collector).Hex(), "feeCollectorNative": collector.String(), "feeRewards": feeRewards,
		"validators": validators, "stakeId": stakeID.Hex(),
		"supplyData": hexData(pack(wrapper, "totalSupply")), "escrowData": hexData(pack(wrapper, "balanceOf", profile.Gateway)),
		"delegate": sign(nonce, deposit.BigInt()), "topUp": sign(nonce+1, topUp.BigInt()),
	})
	require.NoError(t, err)
	write("expectations.json", expect)
}
