package systemcontracts_test

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/privval"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/gov"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/authority"
	authoritytypes "github.com/maxwell2010/go-smart-node/network/authority/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type appOptions map[string]interface{}

func (o appOptions) Get(key string) interface{} { return o[key] }

type withdrawalFixtureLog struct {
	t         *testing.T
	deferrals *[]string
}

func (w withdrawalFixtureLog) Write(data []byte) (int, error) {
	line := string(data)
	if strings.Contains(line, "withdrawal settlement deferred") {
		*w.deferrals = append(*w.deferrals, line)
		w.t.Log(strings.TrimSpace(line))
	} else if strings.Contains(line, "withdrawal block settlement work") {
		w.t.Log(strings.TrimSpace(line))
	}
	return len(data), nil
}

// A normal application receipt-validation error must discard a completed
// contract operation and its native bank effects before transaction commit.
type nativeBurnReceiptControl struct {
	reject   *bool
	rejected *uint64
	topic    common.Hash
}

func (h nativeBurnReceiptControl) PostTxProcessing(_ sdk.Context, _ common.Address, _ core.Message, receipt *ethtypes.Receipt) error {
	if !*h.reject {
		return nil
	}
	for _, log := range receipt.Logs {
		if log.Address == common.HexToAddress("0x0000000000000000000000000000000000000900") && len(log.Topics) > 0 && log.Topics[0] == h.topic {
			(*h.rejected)++
			return errors.New("ordinary native burn receipt validation failed")
		}
	}
	return nil
}

// This test binary owns exactly one production-tag App. It never resets the
// process-global EVM configuration, patches storage or broadcasts transactions.
func TestOwnedAppSignedRegistryBootstrap(t *testing.T) {
	const chainID = "mintcandy_990001-1"
	nftFixturePhase := os.Getenv(signedNFTAdmissionPhaseEnv)
	var withdrawalDeferrals []string
	a := ownedapp.NewApp(log.NewLogger(withdrawalFixtureLog{t: t, deferrals: &withdrawalDeferrals}), dbm.NewMemDB(), true, appOptions{flags.FlagHome: t.TempDir(), flags.FlagChainID: chainID, srvflags.EVMChainID: uint64(990001)})
	t.Cleanup(func() { require.NoError(t, a.Close()) })
	primitiveABI, err := contracts.CompiledABI("IMintCandyNativeBurn")
	require.NoError(t, err)
	rejectNativeBurn := false
	var rejectedNativeBurns uint64
	require.False(t, a.EVMKeeper.HasHooks(), "the control must not overwrite production hooks")
	a.EVMKeeper.SetHooks(nativeBurnReceiptControl{reject: &rejectNativeBurn, rejected: &rejectedNativeBurns, topic: primitiveABI.Events["NativeBurn"].ID})
	keys := make([]*ecdsa.PrivateKey, 5)
	accounts := make([]authtypes.GenesisAccount, 5)
	balances := make([]banktypes.Balance, 5)
	funds := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, math.NewInt(10000).Mul(math.NewInt(1_000_000_000_000_000_000))))
	for i := range keys {
		key, err := registryFixtureKey(i, nftFixturePhase != "")
		require.NoError(t, err)
		keys[i], err = crypto.ToECDSA(key.Bytes())
		require.NoError(t, err)
		accounts[i] = authtypes.NewBaseAccount(key.PubKey().Address().Bytes(), key.PubKey(), uint64(i), 0)
		require.Equal(t, crypto.PubkeyToAddress(keys[i].PublicKey).Bytes(), accounts[i].GetAddress().Bytes())
		balances[i] = banktypes.Balance{Address: accounts[i].GetAddress().String(), Coins: funds}
	}
	signer := cmted25519.GenPrivKey()
	if nftFixturePhase != "" {
		signer = cmted25519.GenPrivKeyFromSecret([]byte("MintCandy isolated NFT fixture consensus signer"))
	}
	validator := cmttypes.NewValidator(signer.PubKey(), 1)
	vals := cmttypes.NewValidatorSet([]*cmttypes.Validator{validator})
	defaults := a.DefaultGenesis()
	genesis, err := simtestutil.GenesisStateWithValSet(a.AppCodec(), defaults, vals, accounts, balances...)
	require.NoError(t, err)
	// SDK fixture helpers replace the application's denom and drop bank metadata.
	defaults = a.DefaultGenesis()
	var bankDefault, bankFunded banktypes.GenesisState
	var stakeDefault, stakeFunded stakingtypes.GenesisState
	a.AppCodec().MustUnmarshalJSON(defaults[banktypes.ModuleName], &bankDefault)
	a.AppCodec().MustUnmarshalJSON(genesis[banktypes.ModuleName], &bankFunded)
	a.AppCodec().MustUnmarshalJSON(defaults[stakingtypes.ModuleName], &stakeDefault)
	a.AppCodec().MustUnmarshalJSON(genesis[stakingtypes.ModuleName], &stakeFunded)
	stakeFunded.Params = stakeDefault.Params
	bankFunded.DenomMetadata = bankDefault.DenomMetadata
	bankFunded.Supply = sdk.NewCoins()
	pool := authtypes.NewModuleAddress(stakingtypes.BondedPoolName).String()
	for i, balance := range bankFunded.Balances {
		if balance.Address == pool {
			coins := sdk.NewCoins()
			for _, coin := range balance.Coins {
				if coin.Denom == sdk.DefaultBondDenom {
					coin.Denom = identity.BaseDenom
				}
				coins = coins.Add(coin)
			}
			bankFunded.Balances[i].Coins = coins
		}
		bankFunded.Supply = bankFunded.Supply.Add(bankFunded.Balances[i].Coins...)
	}
	genesis[banktypes.ModuleName] = a.AppCodec().MustMarshalJSON(&bankFunded)
	genesis[stakingtypes.ModuleName] = a.AppCodec().MustMarshalJSON(&stakeFunded)
	if os.Getenv(withdrawalGenesisFixtureEnv) != "" || nftFixturePhase == "seed" {
		// The SDK fixture starts with an already bonded validator. Populate
		// the signing state normally created by the staking bond hook.
		var slashingGenesis slashingtypes.GenesisState
		a.AppCodec().MustUnmarshalJSON(genesis[slashingtypes.ModuleName], &slashingGenesis)
		addr := sdk.ConsAddress(validator.Address)
		info := slashingtypes.NewValidatorSigningInfo(addr, 0, 0, time.Unix(0, 0).UTC(), false, 0)
		slashingGenesis.SigningInfos = append(slashingGenesis.SigningInfos, slashingtypes.SigningInfo{
			Address: info.Address, ValidatorSigningInfo: info,
		})
		genesis[slashingtypes.ModuleName] = a.AppCodec().MustMarshalJSON(&slashingGenesis)
	}
	require.NoError(t, ownedapp.ReconcileNativeGenesisAllocation(a.AppCodec(), genesis))
	data, err := json.Marshal(genesis)
	require.NoError(t, err)
	now := time.Unix(1900000000, 0).UTC()
	if os.Getenv(withdrawalGenesisFixtureEnv) != "" || os.Getenv(nativeConsensusDaemonFixtureEnv) != "" {
		now = time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	}
	var nftFixture signedNFTAdmissionGenesis
	genesisTime, initialHeight := now, int64(0)
	if nftFixturePhase == "verify" {
		nftFixture = readSignedNFTAdmissionGenesis(t)
		data, genesisTime, initialHeight = nftFixture.AppState, nftFixture.Time, nftFixture.Height
		now = genesisTime.Add(-time.Duration(initialHeight-1) * time.Second)
	}
	_, err = a.InitChain(&abci.RequestInitChain{ChainId: chainID, Time: genesisTime, InitialHeight: initialHeight, AppStateBytes: data, ConsensusParams: simtestutil.DefaultConsensusParams})
	require.NoError(t, err)
	// Materialize fresh genesis before comparing balances through the committed
	// root store. InitChain alone only populates the pending finalize state.
	_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: max(initialHeight, 1), Time: genesisTime.Add(time.Second), NextValidatorsHash: vals.Hash(), ProposerAddress: vals.Proposer.Address})
	require.NoError(t, err)
	_, err = a.Commit()
	require.NoError(t, err)
	ctx := func() sdk.Context {
		return a.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: a.LastBlockHeight() + 1, Time: now.Add(time.Duration(a.LastBlockHeight()+1) * time.Second), ProposerAddress: vals.Proposer.Address})
	}
	price := big.NewInt(1_000_000_000_000)
	nativeCredit := new(big.Int)
	var lastBlockEvents []abci.Event
	var lastAllEvents []abci.Event
	var nextGasLimit uint64
	send := func(key *ecdsa.PrivateKey, to *common.Address, value *big.Int, input []byte, success bool) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		from := crypto.PubkeyToAddress(key.PublicKey)
		before := a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount
		nonce := a.EVMKeeper.GetNonce(ctx(), from)
		gasLimit := uint64(8_000_000)
		if nextGasLimit != 0 {
			gasLimit, nextGasLimit = nextGasLimit, 0
		}
		raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce, To: to, Value: value, Gas: gasLimit, GasPrice: price, Data: input})
		signer := ethtypes.NewEIP155Signer(big.NewInt(990001))
		signed, err := ethtypes.SignTx(raw, signer, key)
		require.NoError(t, err)
		msg := &evmtypes.MsgEthereumTx{}
		require.NoError(t, msg.FromSignedEthereumTx(signed, signer))
		tx, err := msg.BuildTx(a.TxConfig().NewTxBuilder(), identity.BaseDenom)
		require.NoError(t, err)
		encoded, err := a.TxConfig().TxEncoder()(tx)
		require.NoError(t, err)
		height := a.LastBlockHeight() + 1
		result, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: now.Add(time.Duration(height) * time.Second), NextValidatorsHash: vals.Hash(), ProposerAddress: vals.Proposer.Address, Txs: [][]byte{encoded}})
		require.NoError(t, err)
		lastBlockEvents = result.Events
		require.Len(t, result.TxResults, 1)
		lastAllEvents = append(append([]abci.Event(nil), result.Events...), result.TxResults[0].Events...)
		require.Zero(t, result.TxResults[0].Code, result.TxResults[0].Log)
		var messages sdk.TxMsgData
		require.NoError(t, a.AppCodec().Unmarshal(result.TxResults[0].Data, &messages))
		require.Len(t, messages.MsgResponses, 1)
		response := &evmtypes.MsgEthereumTxResponse{}
		require.NoError(t, response.Unmarshal(messages.MsgResponses[0].Value))
		if success {
			reason, _ := abi.UnpackRevert(response.Ret)
			require.Empty(t, response.VmError, "revert: %s", reason)
		} else {
			require.NotEmpty(t, response.VmError)
			require.Empty(t, response.Logs)
		}
		require.Positive(t, response.GasUsed)
		_, err = a.Commit()
		require.NoError(t, err)
		fee := math.NewIntFromBigInt(new(big.Int).Mul(price, new(big.Int).SetUint64(response.GasUsed)))
		debit := fee
		if success {
			debit = debit.Add(math.NewIntFromBigInt(value)).Sub(math.NewIntFromBigInt(nativeCredit))
		}
		require.Equal(t, debit, before.Sub(a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount), "actual signed transaction must charge exactly receipt gas plus committed value")
		require.Equal(t, a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount.BigInt(), a.EVMKeeper.GetBalance(ctx(), from).ToBig())
		require.Equal(t, nonce+1, a.EVMKeeper.GetNonce(ctx(), from))
		nativeCredit.SetUint64(0)
		return response
	}
	advance := func() {
		height := a.LastBlockHeight() + 1
		result, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: now.Add(time.Duration(height) * time.Second), NextValidatorsHash: vals.Hash(), ProposerAddress: vals.Proposer.Address})
		require.NoError(t, err)
		lastBlockEvents, lastAllEvents = result.Events, result.Events
		_, err = a.Commit()
		require.NoError(t, err)
	}
	load := func(name string) contracts.Artifact {
		t.Helper()
		artifact, err := contracts.Load(name)
		require.NoError(t, err)
		return artifact
	}
	if nftFixturePhase == "verify" {
		runSignedNFTAdmissionLifecycle(t, isolatedBackendEnv{
			app: a, ctx: ctx, advance: advance, nextGasLimit: func(limit uint64) { nextGasLimit = limit },
			send: send, load: load, keys: keys, credit: nativeCredit, events: func() []abci.Event { return lastAllEvents },
			blockEvents: func() []abci.Event { return lastBlockEvents }, deferrals: func() []string { return withdrawalDeferrals },
			govOwner: common.BytesToAddress(authtypes.NewModuleAddress(govtypes.ModuleName)),
		}, nftFixture)
		return
	}
	freshAsset := func(name string) contracts.Artifact {
		if name == "DRC721" {
			return load("NewNetworkDRC721")
		}
		if name == "DRC1155" {
			return load("NewNetworkDRC1155")
		}
		return load(name)
	}
	registry, proxy, bootstrap := load("DecimalContractCenter"), load("DecimalContractCenterProxy"), load("NewNetworkProxyBootstrap")
	deploy := func(artifact contracts.Artifact, args ...interface{}) common.Address {
		t.Helper()
		from := crypto.PubkeyToAddress(keys[0].PublicKey)
		address := crypto.CreateAddress(from, a.EVMKeeper.GetNonce(ctx(), from))
		code, err := artifact.Deployment(args...)
		require.NoError(t, err)
		send(keys[0], nil, new(big.Int), code, true)
		require.Equal(t, crypto.Keccak256Hash(artifact.Runtime), a.EVMKeeper.GetCodeHash(ctx(), address))
		require.Equal(t, artifact.Runtime, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), address)))
		return address
	}
	implementation, setup := deploy(registry), deploy(bootstrap)
	owner := crypto.PubkeyToAddress(keys[1].PublicKey)
	init, err := bootstrap.ABI.Pack("bootstrap", owner, implementation, []byte{})
	require.NoError(t, err)
	address := deploy(proxy, setup, init)
	read := func(method string, args ...interface{}) common.Address {
		t.Helper()
		input, err := registry.ABI.Pack(method, args...)
		require.NoError(t, err)
		response := send(keys[1], &address, new(big.Int), input, true)
		values, err := registry.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		require.Len(t, values, 1)
		return values[0].(common.Address)
	}
	require.Equal(t, owner, read("owner"))
	require.Equal(t, implementation, read("getImpl"))
	input, err := registry.ABI.Pack("setContractAddress", "fixture-contract", implementation)
	require.NoError(t, err)
	send(keys[1], &address, new(big.Int), input, true)
	require.Equal(t, implementation, read("getContractAddress", "fixture-contract"))
	failed := send(keys[2], &address, new(big.Int), input, false)
	require.GreaterOrEqual(t, len(failed.Ret), 4)
	var selector [4]byte
	copy(selector[:], failed.Ret[:4])
	reason, err := registry.ABI.ErrorByID(selector)
	require.NoError(t, err)
	require.Equal(t, "OwnableUnauthorizedAccount", reason.Name)
	arguments, err := reason.Inputs.Unpack(failed.Ret[4:])
	require.NoError(t, err)
	require.Equal(t, []interface{}{crypto.PubkeyToAddress(keys[2].PublicKey)}, arguments)
	require.Equal(t, implementation, read("getContractAddress", "fixture-contract"))
	send(keys[1], &setup, new(big.Int), init, false)
	send(keys[1], &address, new(big.Int), init, false)
	require.Equal(t, owner, read("owner"))
	zeroInit, err := bootstrap.ABI.Pack("bootstrap", common.Address{}, implementation, []byte{})
	require.NoError(t, err)
	invalid, err := proxy.Deployment(setup, zeroInit)
	require.NoError(t, err)
	failedAddress := crypto.CreateAddress(crypto.PubkeyToAddress(keys[0].PublicKey), a.EVMKeeper.GetNonce(ctx(), crypto.PubkeyToAddress(keys[0].PublicKey)))
	send(keys[0], nil, new(big.Int), invalid, false)
	require.Empty(t, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), failedAddress)))
	require.Equal(t, owner, read("owner"))

	t.Run("fresh validator registry initialization", func(t *testing.T) {
		fresh := load("NewNetworkMasterValidator")
		implementation := deploy(fresh)
		initialization, err := fresh.ABI.Pack("initializeFresh", address)
		require.NoError(t, err)
		init, err := bootstrap.ABI.Pack("bootstrap", owner, implementation, initialization)
		require.NoError(t, err)
		validatorAddress := deploy(proxy, setup, init)
		read, err := fresh.ABI.Pack("contractCenter")
		require.NoError(t, err)
		response := send(keys[1], &validatorAddress, new(big.Int), read, true)
		values, err := fresh.ABI.Unpack("contractCenter", response.Ret)
		require.NoError(t, err)
		require.Equal(t, address, values[0].(common.Address))
		send(keys[1], &validatorAddress, new(big.Int), initialization, false)
		send(keys[1], &implementation, new(big.Int), initialization, false)
		var penaltyReadGas uint64
		penaltyReader := func(method string, args ...interface{}) []interface{} {
			data, err := fresh.ABI.Pack(method, args...)
			require.NoError(t, err)
			result := send(keys[1], &validatorAddress, new(big.Int), data, true)
			penaltyReadGas = result.MaxUsedGas
			values, err := fresh.ABI.Unpack(method, result.Ret)
			require.NoError(t, err)
			return values
		}
		member := crypto.PubkeyToAddress(keys[2].PublicKey)
		require.Zero(t, penaltyReader("getPenaltyCount", member)[0].(*big.Int).Sign())
		meta := []struct {
			Validator common.Address
			Meta      string
		}{{Validator: member, Meta: "bounded penalty read fixture"}}
		add, err := fresh.ABI.Pack("addValidators", meta)
		require.NoError(t, err)
		send(keys[1], &validatorAddress, new(big.Int), add, true)
		require.True(t, penaltyReader("isActive", member)[0].(bool))
		activeReadAtZero := penaltyReadGas
		headerAtZero := penaltyReader("getValidatorState", member)
		require.Equal(t, uint8(2), headerAtZero[0])
		require.False(t, headerAtZero[1].(bool))
		require.Zero(t, headerAtZero[2].(*big.Int).Sign())
		headerReadAtZero := penaltyReadGas
		for _, penalty := range []uint16{1, 2} {
			data, err := fresh.ABI.Pack("addPenalty", member, penalty)
			require.NoError(t, err)
			send(keys[1], &validatorAddress, new(big.Int), data, true)
			data, err = fresh.ABI.Pack("unpauseValidator", member)
			require.NoError(t, err)
			send(keys[1], &validatorAddress, new(big.Int), data, true)
		}
		require.Equal(t, int64(2), penaltyReader("getPenaltyCount", member)[0].(*big.Int).Int64())
		require.Equal(t, []uint16{1}, penaltyReader("getPenaltiesRange", member, big.NewInt(0), big.NewInt(1))[0])
		require.Equal(t, []uint16{2}, penaltyReader("getPenaltiesRange", member, big.NewInt(1), big.NewInt(256))[0])
		require.Empty(t, penaltyReader("getPenaltiesRange", member, big.NewInt(2), big.NewInt(256))[0])
		require.Empty(t, penaltyReader("getPenaltiesRange", member, big.NewInt(0), big.NewInt(0))[0])
		require.Equal(t, []uint16{1, 2}, penaltyReader("getValidatorPenalties", member)[0])
		for _, bounds := range [][2]int64{{3, 1}, {0, 257}} {
			invalid, err := fresh.ABI.Pack("getPenaltiesRange", member, big.NewInt(bounds[0]), big.NewInt(bounds[1]))
			require.NoError(t, err)
			failed := send(keys[1], &validatorAddress, new(big.Int), invalid, false)
			var selector [4]byte
			require.GreaterOrEqual(t, len(failed.Ret), 4)
			copy(selector[:], failed.Ret[:4])
			reason, err := fresh.ABI.ErrorByID(selector)
			require.NoError(t, err)
			require.Equal(t, "InvalidPenaltyReadRange", reason.Name)
		}

		// Exercise actual packed-array pagination with a nontrivial bounded history.
		for index := 2; index < 257; index++ {
			data, err := fresh.ABI.Pack("addPenalty", member, uint16(1+index%2))
			require.NoError(t, err)
			send(keys[1], &validatorAddress, new(big.Int), data, true)
			data, err = fresh.ABI.Pack("unpauseValidator", member)
			require.NoError(t, err)
			send(keys[1], &validatorAddress, new(big.Int), data, true)
		}
		require.True(t, penaltyReader("isActive", member)[0].(bool))
		t.Logf("ordinary isActive MaxUsedGas history0=%d history257=%d", activeReadAtZero, penaltyReadGas)
		headerAt257 := penaltyReader("getValidatorState", member)
		require.Equal(t, uint8(2), headerAt257[0])
		require.False(t, headerAt257[1].(bool))
		require.Equal(t, int64(257), headerAt257[2].(*big.Int).Int64())
		require.Equal(t, headerReadAtZero, penaltyReadGas, "fixed header must not copy history")
		t.Logf("actual fixed validator header MaxUsedGas history0=%d history257=%d ABIwords=3", headerReadAtZero, penaltyReadGas)
		require.Equal(t, int64(257), penaltyReader("getPenaltyCount", member)[0].(*big.Int).Int64())
		require.Len(t, penaltyReader("getPenaltiesRange", member, big.NewInt(0), big.NewInt(32))[0], 32)
		t.Logf("actual penalty range32 MaxUsedGas=%d", penaltyReadGas)
		require.Less(t, penaltyReadGas, uint64(100000))
		require.Len(t, penaltyReader("getPenaltiesRange", member, big.NewInt(0), big.NewInt(256))[0], 256)
		t.Logf("actual penalty range256 MaxUsedGas=%d", penaltyReadGas)
		require.Equal(t, []uint16{1}, penaltyReader("getPenaltiesRange", member, big.NewInt(256), big.NewInt(256))[0])

	})

	t.Run("fresh full system proxy set", func(t *testing.T) {
		lib := load("MintCandyDelegationNFTLib")
		from := crypto.PubkeyToAddress(keys[0].PublicKey)
		libAddress := crypto.CreateAddress(from, a.EVMKeeper.GetNonce(ctx(), from))
		code, err := lib.Deployment()
		require.NoError(t, err)
		send(keys[0], nil, new(big.Int), code, true)
		expectedLibrary, err := contracts.ExpectedRuntime("MintCandyDelegationNFTLib", nil, map[string]common.Hash{"owned/DecimalDelegation.sol:DelegationNFTLib.library_deploy_address": common.BytesToHash(libAddress.Bytes())})
		require.NoError(t, err)
		require.Equal(t, expectedLibrary, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), libAddress)))
		delegation, err := contracts.LoadLinked("MintCandyDelegation", map[string]common.Address{"owned/DecimalDelegation.sol:DelegationNFTLib": libAddress})
		require.NoError(t, err)
		roles := map[string]common.Address{}
		artifacts := map[string]contracts.Artifact{}
		for _, role := range []string{"master-validator", "delegation", "token-center", "nft-center"} {
			name := map[string]string{"master-validator": "NewNetworkMasterValidator", "delegation": "DecimalDelegation", "delegation-nft": "DecimalDelegationNFT", "token-center": "NewNetworkTokenCenter", "nft-center": "NewNetworkNFTCenter"}[role]
			artifact := delegation
			if role != "delegation" {
				artifact = load(name)
			}
			impl := deploy(artifact)
			var initialization []byte
			if role == "master-validator" {
				initialization, err = artifact.ABI.Pack("initializeFresh", address)
				require.NoError(t, err)
			}
			if role == "token-center" || role == "nft-center" {
				initialization, err = artifact.ABI.Pack("initializeFresh", address)
				require.NoError(t, err)
			}
			init, err := bootstrap.ABI.Pack("bootstrap", owner, impl, initialization)
			require.NoError(t, err)
			instance := deploy(proxy, setup, init)
			roles[role], artifacts[role] = instance, artifact
			ownerInput, err := artifact.ABI.Pack("owner")
			require.NoError(t, err)
			response := send(keys[1], &instance, new(big.Int), ownerInput, true)
			values, err := artifact.ABI.Unpack("owner", response.Ret)
			require.NoError(t, err)
			require.Equal(t, owner, values[0])
			if role != "master-validator" {
				input, err := artifact.ABI.Pack("setContractCenter", address)
				require.NoError(t, err)
				send(keys[1], &instance, new(big.Int), input, true)
			}
			register, err := registry.ABI.Pack("setContractAddress", role, instance)
			require.NoError(t, err)
			send(keys[1], &address, new(big.Int), register, true)
			require.Equal(t, instance, read("getContractAddress", role))
		}
		roles["delegation-nft"] = roles["delegation"]
		nftAlias, err := registry.ABI.Pack("setContractAddress", "delegation-nft", roles["delegation"])
		require.NoError(t, err)
		send(keys[1], &address, new(big.Int), nftAlias, true)
		wrapped := deploy(load("MintCandyWrappedDEL"))
		register, err := registry.ABI.Pack("setContractAddress", "wdel", wrapped)
		require.NoError(t, err)
		send(keys[1], &address, new(big.Int), register, true)
		configure := func(role, method string, args ...interface{}) {
			target := roles[role]
			input, err := artifacts[role].ABI.Pack(method, args...)
			require.NoError(t, err)
			send(keys[2], &target, new(big.Int), input, false)
			send(keys[1], &target, new(big.Int), input, true)
		}
		configure("token-center", "upgradeToken", deploy(load("DRC20")))
		configure("token-center", "upgradeTokenReserveless", deploy(load("DRC20Reserveless")))
		for _, nft := range []struct {
			name    string
			kind    uint8
			reserve bool
		}{{"DRC721", 0, true}, {"DRC1155", 1, true}, {"DRC721Reserveless", 0, false}, {"DRC1155Reserveless", 1, false}} {
			implementation := deploy(freshAsset(nft.name))
			configure("nft-center", "upgradeNFT", implementation, nft.kind, nft.reserve)
			target := roles["nft-center"]
			input, err := artifacts["nft-center"].ABI.Pack("implementation", nft.kind, nft.reserve)
			require.NoError(t, err)
			result := send(keys[1], &target, new(big.Int), input, true)
			values, err := artifacts["nft-center"].ABI.Unpack("implementation", result.Ret)
			require.NoError(t, err)
			require.Equal(t, implementation, values[0])
		}
		t.Run("bounded penalty pages and ordered same-block records", func(t *testing.T) {
			checkSignedPagedPenalties(t, keys, roles, artifacts, wrapped, func() *big.Int { return a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount.BigInt() }, send,
				func(key *ecdsa.PrivateKey, target common.Address, inputs [][]byte) int64 {
					return appendSignedPenaltiesSameBlock(t, a, ctx, vals, now, price, key, target, inputs...)
				})
		})
		t.Run("lazy tiny unit settlement both withdrawal orders", func(t *testing.T) {
			for _, reverse := range []bool{false, true} {
				checkSignedLazyTinyUnitSettlement(t, reverse, keys, roles, artifacts, wrapped,
					func() *big.Int { return a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount.BigInt() },
					func(owner common.Address) *big.Int {
						return a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(owner.Bytes()), identity.BaseDenom).Amount.BigInt()
					}, send)
			}
		})
		t.Run("pending NFT exit realizes native loss", func(t *testing.T) {
			checkSignedNFTBusinessCase(t, false, "withdraw", keys, roles, artifacts, wrapped, func() *big.Int { return a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount.BigInt() }, send)
		})
		for _, exitMode := range []string{"transfer", "forced"} {
			t.Run("pending NFT "+exitMode+" realizes native loss", func(t *testing.T) {
				checkSignedNFTBusinessCase(t, false, exitMode, keys, roles, artifacts, wrapped, func() *big.Int { return a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount.BigInt() }, send)
			})
		}
		t.Run("ERC1155 reserve units reconcile", func(t *testing.T) {
			checkSignedNFTBusinessCase(t, true, "penalty", keys, roles, artifacts, wrapped, func() *big.Int { return a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount.BigInt() }, send)
		})
		for _, exitMode := range []string{"withdraw", "transfer", "forced", "rounding"} {
			t.Run("ERC1155 "+exitMode+" realizes native reserve loss", func(t *testing.T) {
				checkSignedNFTBusinessCase(t, true, exitMode, keys, roles, artifacts, wrapped, func() *big.Int { return a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount.BigInt() }, send)
			})
		}
		t.Run("mixed coin NFT penalty conservation", func(t *testing.T) {
			checkSignedMixedPenalty(t, 0, false, true, keys, roles, artifacts, wrapped, address, registry, func() *big.Int { return a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount.BigInt() }, send)
			checkSignedMixedPenalty(t, 2, true, true, keys, roles, artifacts, wrapped, address, registry, func() *big.Int { return a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount.BigInt() }, send)
		})

		for _, settleFirst := range []bool{false, true} {
			for _, reverse := range []bool{false, true} {
				name := "shared1155 A-first"
				if reverse {
					name = "shared1155 B-first"
				}
				if settleFirst {
					name += " pre-settled"
				} else {
					name += " lazy"
				}
				t.Run(name, func(t *testing.T) {
					checkSignedShared1155(t, reverse, settleFirst, keys, roles, artifacts, wrapped,
						func() *big.Int { return a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount.BigInt() },
						func(owner common.Address) *big.Int {
							return a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(owner.Bytes()), identity.BaseDenom).Amount.BigInt()
						},
						func(refund *big.Int) { nativeCredit.Set(refund) }, price, send)
				})
			}
		}
		t.Run("canonical NFT penalty negative control", func(t *testing.T) {
			configure("nft-center", "upgradeNFT", deploy(load("DRC721")), uint8(0), true)
			canonicalLibrary := load("DelegationNFTLib")
			from := crypto.PubkeyToAddress(keys[0].PublicKey)
			canonicalLib := crypto.CreateAddress(from, a.EVMKeeper.GetNonce(ctx(), from))
			creation, err := canonicalLibrary.Deployment()
			require.NoError(t, err)
			send(keys[0], nil, new(big.Int), creation, true)
			expectedLibraryRuntime, err := contracts.ExpectedRuntime("DelegationNFTLib", nil, map[string]common.Hash{"sources/DecimalDelegation.sol:DelegationNFTLib.library_deploy_address": common.BytesToHash(canonicalLib.Bytes())})
			require.NoError(t, err)
			require.Equal(t, expectedLibraryRuntime, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), canonicalLib)))
			canonical, err := contracts.LoadLinked("DecimalDelegation", map[string]common.Address{"sources/DecimalDelegation.sol:DelegationNFTLib": canonicalLib})
			require.NoError(t, err)
			master := load("NewNetworkMasterValidator")
			masterImpl, canonicalImpl := deploy(master), deploy(canonical)
			masterArgs, err := master.ABI.Pack("initializeFresh", address)
			require.NoError(t, err)
			masterSetup, err := bootstrap.ABI.Pack("bootstrap", owner, masterImpl, masterArgs)
			require.NoError(t, err)
			canonicalSetup, err := bootstrap.ABI.Pack("bootstrap", owner, canonicalImpl, []byte{})
			require.NoError(t, err)
			masterProxy, canonicalProxy := deploy(proxy, setup, masterSetup), deploy(proxy, setup, canonicalSetup)
			centerArgs, err := canonical.ABI.Pack("setContractCenter", address)
			require.NoError(t, err)
			send(keys[1], &canonicalProxy, new(big.Int), centerArgs, true)
			controlRoles, controlArtifacts := cloneRoleMap(roles), cloneArtifactMap(artifacts)
			controlRoles["master-validator"], controlRoles["delegation"], controlRoles["delegation-nft"] = masterProxy, canonicalProxy, canonicalProxy
			controlArtifacts["master-validator"], controlArtifacts["delegation"] = master, canonical
			for _, role := range []string{"master-validator", "delegation", "delegation-nft"} {
				input, err := registry.ABI.Pack("setContractAddress", role, controlRoles[role])
				require.NoError(t, err)
				send(keys[1], &address, new(big.Int), input, true)
			}
			supply := func() *big.Int { return a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount.BigInt() }
			t.Run("tiny unit global rounding negative control", func(t *testing.T) { checkSignedTinyUnitRounding(t, keys, controlRoles, controlArtifacts, send) })
			checkSignedMixedPenalty(t, 3, false, false, keys, controlRoles, controlArtifacts, wrapped, address, registry, supply, send)
			checkSignedMixedPenalty(t, 4, true, false, keys, controlRoles, controlArtifacts, wrapped, address, registry, supply, send)
		})
	})

	t.Run("atomic constructor handoff to governance", func(t *testing.T) {
		govOwner := common.BytesToAddress(authtypes.NewModuleAddress("gov"))
		require.NotEqual(t, crypto.PubkeyToAddress(keys[0].PublicKey), govOwner)
		from := crypto.PubkeyToAddress(keys[0].PublicKey)
		lib := load("MintCandyDelegationNFTLib")
		libAddress := crypto.CreateAddress(from, a.EVMKeeper.GetNonce(ctx(), from))
		libCode, err := lib.Deployment()
		require.NoError(t, err)
		send(keys[0], nil, new(big.Int), libCode, true)
		expectedLibrary, err := contracts.ExpectedRuntime("MintCandyDelegationNFTLib", nil, map[string]common.Hash{"owned/DecimalDelegation.sol:DelegationNFTLib.library_deploy_address": common.BytesToHash(libAddress.Bytes())})
		require.NoError(t, err)
		require.Equal(t, expectedLibrary, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), libAddress)))
		linked, err := contracts.LoadLinked("MintCandyDelegation", map[string]common.Address{"owned/DecimalDelegation.sol:DelegationNFTLib": libAddress})
		require.NoError(t, err)
		names := []string{"master-validator", "delegation", "token-center", "nft-center"}
		roleABI := map[string]contracts.Artifact{}
		implementations := map[string]common.Address{}
		for _, role := range names {
			name := map[string]string{"master-validator": "NewNetworkMasterValidator", "delegation-nft": "DecimalDelegationNFT", "token-center": "NewNetworkTokenCenter", "nft-center": "NewNetworkNFTCenter"}[role]
			artifact := linked
			if role != "delegation" {
				artifact = load(name)
			}
			roleABI[role] = artifact
			implementations[role] = deploy(artifact)
		}
		tokenImpls := map[string]common.Address{}
		for _, name := range []string{"DRC20", "DRC20Reserveless", "DRC721", "DRC1155", "DRC721Reserveless", "DRC1155Reserveless"} {
			tokenImpls[name] = deploy(freshAsset(name))
		}
		wrapped := deploy(load("MintCandyWrappedDEL"))
		registryImpl := deploy(registry)
		nonce := a.EVMKeeper.GetNonce(ctx(), from)
		govRegistry := crypto.CreateAddress(from, nonce)
		roles := map[string]common.Address{}
		var registryConfig [][]byte
		for i, role := range names {
			roles[role] = crypto.CreateAddress(from, nonce+1+uint64(i))
			call, err := registry.ABI.Pack("setContractAddress", role, roles[role])
			require.NoError(t, err)
			registryConfig = append(registryConfig, call)
		}
		// The updated protocol uses one unified financial ledger for coin and NFT.
		roles["delegation-nft"] = roles["delegation"]
		nftAlias, err := registry.ABI.Pack("setContractAddress", "delegation-nft", roles["delegation"])
		require.NoError(t, err)
		registryConfig = append(registryConfig, nftAlias)
		wrappedRegister, err := registry.ABI.Pack("setContractAddress", "wdel", wrapped)
		require.NoError(t, err)
		registryConfig = append(registryConfig, wrappedRegister)
		registryInit, err := bootstrap.ABI.Pack("bootstrapConfigured", govOwner, registryImpl, []byte{}, registryConfig)
		require.NoError(t, err)

		expectedCreation, err := proxy.Deployment(setup, registryInit)
		require.NoError(t, err)
		configuredCreation, err := contracts.ConfiguredProxyDeployment(setup, registryImpl, govOwner, []byte{}, registryConfig)
		require.NoError(t, err)
		require.Equal(t, expectedCreation, configuredCreation)
		send(keys[0], nil, new(big.Int), configuredCreation, true)
		require.Equal(t, crypto.Keccak256Hash(proxy.Runtime), a.EVMKeeper.GetCodeHash(ctx(), govRegistry))

		for _, role := range names {
			artifact := roleABI[role]
			var initialization []byte
			var config [][]byte
			pack := func(method string, args ...interface{}) []byte {
				call, err := artifact.ABI.Pack(method, args...)
				require.NoError(t, err)
				return call
			}
			if role == "master-validator" {
				initialization = pack("initializeFresh", govRegistry)
			} else if role != "token-center" && role != "nft-center" {
				config = append(config, pack("setContractCenter", govRegistry))
			}
			if role == "token-center" {
				initialization = pack("initializeFresh", govRegistry)
				config = append(config, pack("upgradeToken", tokenImpls["DRC20"]), pack("upgradeTokenReserveless", tokenImpls["DRC20Reserveless"]))
			}
			if role == "nft-center" {
				initialization = pack("initializeFresh", govRegistry)
				for _, nft := range []struct {
					name    string
					kind    uint8
					reserve bool
				}{{"DRC721", 0, true}, {"DRC1155", 1, true}, {"DRC721Reserveless", 0, false}, {"DRC1155Reserveless", 1, false}} {
					config = append(config, pack("upgradeNFT", tokenImpls[nft.name], nft.kind, nft.reserve))
				}
			}
			init, err := bootstrap.ABI.Pack("bootstrapConfigured", govOwner, implementations[role], initialization, config)
			require.NoError(t, err)
			require.Equal(t, roles[role], deploy(proxy, setup, init))
		}
		query := func(artifact contracts.Artifact, address common.Address, method string, args ...interface{}) []interface{} {
			call, err := artifact.ABI.Pack(method, args...)
			require.NoError(t, err)
			response := send(keys[0], &address, new(big.Int), call, true)
			values, err := artifact.ABI.Unpack(method, response.Ret)
			require.NoError(t, err)
			return values
		}
		require.Equal(t, govOwner, query(registry, govRegistry, "owner")[0])
		require.Equal(t, roles["delegation"], query(registry, govRegistry, "getContractAddress", "delegation-nft")[0])
		require.Equal(t, query(registry, govRegistry, "getContractAddress", "delegation")[0], query(registry, govRegistry, "getContractAddress", "delegation-nft")[0])
		send(keys[0], &govRegistry, new(big.Int), registryInit, false)
		send(keys[0], &setup, new(big.Int), registryInit, false)
		for _, role := range names {
			artifact, target := roleABI[role], roles[role]
			require.Equal(t, govOwner, query(artifact, target, "owner")[0])
			require.Equal(t, target, query(registry, govRegistry, "getContractAddress", role)[0])
			getter := "getContractCenter"
			if role == "master-validator" {
				getter = "contractCenter"
			}
			require.Equal(t, govRegistry, query(artifact, target, getter)[0])
			forbidden, err := artifact.ABI.Pack("upgrade", implementations[role], []byte{})
			require.NoError(t, err)
			send(keys[0], &target, new(big.Int), forbidden, false)
			send(keys[1], &target, new(big.Int), forbidden, false)
		}
		require.Equal(t, tokenImpls["DRC20"], query(roleABI["token-center"], roles["token-center"], "implementation")[0])
		require.Equal(t, tokenImpls["DRC20Reserveless"], query(roleABI["token-center"], roles["token-center"], "implementationReserveless")[0])
		for _, nft := range []struct {
			name    string
			kind    uint8
			reserve bool
		}{{"DRC721", 0, true}, {"DRC1155", 1, true}, {"DRC721Reserveless", 0, false}, {"DRC1155Reserveless", 1, false}} {
			require.Equal(t, tokenImpls[nft.name], query(roleABI["nft-center"], roles["nft-center"], "implementation", nft.kind, nft.reserve)[0])
		}

		t.Run("canonical governance authority execution", func(t *testing.T) {
			role := roles["master-validator"]
			artifact := roleABI["master-validator"]
			proxyHash := crypto.Keccak256Hash(proxy.Runtime)
			manifest := authority.Manifest{Registry: authority.Role{Address: govRegistry, ProxyCodeHash: proxyHash, ApprovedImplementations: []common.Hash{crypto.Keccak256Hash(registry.Runtime)}}, Roles: map[string]authority.Role{
				"master-validator": {Address: role, ProxyCodeHash: proxyHash, ApprovedImplementations: []common.Hash{crypto.Keccak256Hash(artifact.Runtime)}},
			}}

			childHash := func(name string) common.Hash { return crypto.Keccak256Hash(freshAsset(name).Runtime) }
			manifest.Roles["token-center"] = authority.Role{Address: roles["token-center"], ProxyCodeHash: proxyHash, ApprovedImplementations: []common.Hash{crypto.Keccak256Hash(roleABI["token-center"].Runtime)}, ApprovedChildren: map[string][]common.Hash{"upgradeToken": {childHash("DRC20")}, "upgradeTokenReserveless": {childHash("DRC20Reserveless")}}}
			manifest.Roles["nft-center"] = authority.Role{Address: roles["nft-center"], ProxyCodeHash: proxyHash, ApprovedImplementations: []common.Hash{crypto.Keccak256Hash(roleABI["nft-center"].Runtime)}, ApprovedChildren: map[string][]common.Hash{"upgradeNFT:0:true": {childHash("DRC721")}, "upgradeNFT:1:true": {childHash("DRC1155")}, "upgradeNFT:0:false": {childHash("DRC721Reserveless")}, "upgradeNFT:1:false": {childHash("DRC1155Reserveless")}}}
			markerAddress := accounts[2].GetAddress()
			nativeFailure := false
			hook := authorityReceiptHook(func(c sdk.Context, sender common.Address, message core.Message, receipt *ethtypes.Receipt) error {
				require.Equal(t, govOwner, sender)
				require.Zero(t, message.Value.Sign())
				require.Equal(t, uint64(1), receipt.Status)
				marker := a.AccountKeeper.GetAccount(c, markerAddress)
				require.NoError(t, marker.SetSequence(marker.GetSequence()+1))
				a.AccountKeeper.SetAccount(c, marker)
				c.EventManager().EmitEvent(sdk.NewEvent("native_authority_probe"))
				if nativeFailure {
					return errors.New("native receipt rejected")
				}
				return nil
			})
			bridge, err := authority.NewKeeper(a.EVMKeeper, manifest, hook)
			require.NoError(t, err)
			authoritytypes.RegisterInterfaces(a.InterfaceRegistry())
			authoritytypes.RegisterMsgServer(a.MsgServiceRouter(), bridge)
			original, ok := a.ModuleManager.Modules[govtypes.ModuleName].(gov.AppModule)
			require.True(t, ok)
			a.ModuleManager.Modules[govtypes.ModuleName] = authorityGovModule{AppModule: original}
			defer func() { a.ModuleManager.Modules[govtypes.ModuleName] = original }()
			government := authtypes.NewModuleAddress(govtypes.ModuleName).String()
			makeMessage := func(reward int64) *authoritytypes.MsgExecuteSystemContract {
				data, err := artifact.ABI.Pack("setRewardPerBlock", big.NewInt(reward))
				require.NoError(t, err)
				return &authoritytypes.MsgExecuteSystemContract{Authority: government, Calls: []*authoritytypes.SystemCall{{Role: "master-validator", Target: role.Bytes(), TargetCodeHash: proxyHash.Bytes(), Implementation: implementations["master-validator"].Bytes(), ImplementationCodeHash: crypto.Keccak256Hash(artifact.Runtime).Bytes(), Data: data, GasLimit: 200000}}}
			}
			readReward := func() *big.Int { return query(artifact, role, "getRewardPerBlock")[0].(*big.Int) }
			marker := func() uint64 { return a.AccountKeeper.GetAccount(ctx(), markerAddress).GetSequence() }
			t.Run("internal work charges pre-refund EVM execution", func(t *testing.T) {
				// Both controls begin at exactly the same real seeded contract
				// state. Subtract actual SDK state-commit work measured in a
				// disposable committed preview: clearing the second slot itself
				// incurs another metered SDK store write, as well as its EVM work.
				seed, _ := ctx().CacheContext()
				require.NoError(t, authority.RunGovernance(seed, func(c sdk.Context) error {
					_, err := bridge.ExecuteSystemContract(c, makeMessage(123))
					return err
				}))
				var overhead, previousRefund int64
				for i, method := range []string{"setRewardPerBlock", "clearRewardPerBlock"} {
					request := makeMessage(0)
					if method == "clearRewardPerBlock" {
						request.Calls[0].Data, err = artifact.ABI.Pack(method)
						require.NoError(t, err)
					}
					preview, _ := seed.CacheContext()
					preview = preview.WithGasMeter(storetypes.NewInfiniteGasMeter())
					message := core.Message{From: govOwner, To: &role, Value: new(big.Int), GasLimit: request.Calls[0].GasLimit, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: request.Calls[0].Data, SkipNonceChecks: true, SkipFromEOACheck: true}
					raw, err := a.EVMKeeper.ApplyMessage(preview, statedb.New(preview, a.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, true, false, true)
					require.NoError(t, err)
					require.Empty(t, raw.VmError)
					require.Greater(t, raw.MaxUsedGas, raw.GasUsed, "control must earn a real EVM storage refund")
					rawSDK := int64(preview.GasMeter().GasConsumed())
					measured, _ := seed.CacheContext()
					measured = measured.WithGasMeter(storetypes.NewInfiniteGasMeter())
					require.NoError(t, authority.RunGovernance(measured, func(c sdk.Context) error {
						_, err := bridge.ExecuteSystemContract(c, request)
						return err
					}))
					charged := int64(measured.GasMeter().GasConsumed())
					refund := int64(raw.MaxUsedGas - raw.GasUsed)
					t.Logf("%s: EVM work=%d after-refund=%d commit SDK=%d total SDK=%d", method, raw.MaxUsedGas, raw.GasUsed, rawSDK, charged)
					if i == 0 {
						overhead, previousRefund = charged-int64(raw.MaxUsedGas)-rawSDK, refund
					} else {
						require.NotEqual(t, previousRefund, refund, "controls must exercise different refunds")
						require.Equal(t, overhead, charged-int64(raw.MaxUsedGas)-rawSDK, "remaining native work cannot reclaim EVM storage refunds")
					}
				}
			})
			// A rejected native hook rolls back both real EVM state and native account writes.
			beforeReward, beforeMarker := readReward(), marker()
			nativeFailure = true
			require.ErrorContains(t, authority.RunGovernance(ctx(), func(c sdk.Context) error { _, err := bridge.ExecuteSystemContract(c, makeMessage(123)); return err }), "native receipt rejected")
			require.Equal(t, beforeReward, readReward())
			require.Equal(t, beforeMarker, marker())
			nativeFailure = false
			unauthorized := makeMessage(123)
			unauthorized.Authority = accounts[0].GetAddress().String()
			_, err = bridge.ExecuteSystemContract(ctx(), unauthorized)
			require.ErrorContains(t, err, "only governance")
			_, err = bridge.ExecuteSystemContract(ctx(), makeMessage(123))
			require.ErrorContains(t, err, "active same-block")

			for _, mode := range []string{"noncanonical", "ownership", "unknown selector", "unapproved upgrade", "migration", "intrinsic gas"} {
				invalid := makeMessage(123)
				switch mode {
				case "noncanonical":
					invalid.Calls[0].Data = append(invalid.Calls[0].Data, 0)
				case "ownership":
					invalid.Calls[0].Data, err = artifact.ABI.Pack("transferOwnership", crypto.PubkeyToAddress(keys[0].PublicKey))
					require.NoError(t, err)
				case "unknown selector":
					invalid.Calls[0].Data = []byte{0xde, 0xad, 0xbe, 0xef}
				case "unapproved upgrade":
					invalid.Calls[0].Data, err = artifact.ABI.Pack("upgrade", registryImpl, []byte{})
					require.NoError(t, err)
					invalid.Calls[0].ReplacementCodeHash = crypto.Keccak256Hash(registry.Runtime).Bytes()
				case "migration":
					invalid.Calls[0].Data, err = artifact.ABI.Pack("upgrade", implementations["master-validator"], []byte{1})
					require.NoError(t, err)
					invalid.Calls[0].ReplacementCodeHash = crypto.Keccak256Hash(artifact.Runtime).Bytes()
				case "intrinsic gas":
					invalid.Calls[0].GasLimit = 1
				}
				require.Error(t, authority.RunGovernance(ctx(), func(c sdk.Context) error { _, err := bridge.ExecuteSystemContract(c, invalid); return err }), mode)
				require.Equal(t, beforeMarker, marker(), mode)
			}
			badRegistry := manifest
			badRegistry.Registry.Address = address // Explicitly EOA-owned earlier registry.
			wrongOwnerBridge, err := authority.NewKeeper(a.EVMKeeper, badRegistry, hook)
			require.NoError(t, err)
			require.ErrorContains(t, authority.RunGovernance(ctx(), func(c sdk.Context) error {
				_, err := wrongOwnerBridge.ExecuteSystemContract(c, makeMessage(123))
				return err
			}), "owner is not governance")
			badRegistry = manifest
			badRegistry.Registry.ProxyCodeHash = common.HexToHash("0x01")
			wrongCodeBridge, err := authority.NewKeeper(a.EVMKeeper, badRegistry, hook)
			require.NoError(t, err)
			require.ErrorContains(t, authority.RunGovernance(ctx(), func(c sdk.Context) error {
				_, err := wrongCodeBridge.ExecuteSystemContract(c, makeMessage(123))
				return err
			}), "proxy code differs")

			factoryMessage := func(role, method, child string, kind uint8, reserve bool) *authoritytypes.MsgExecuteSystemContract {
				artifact := roleABI[role]
				args := []interface{}{tokenImpls[child]}
				if method == "upgradeNFT" {
					args = append(args, kind, reserve)
				}
				data, err := artifact.ABI.Pack(method, args...)
				require.NoError(t, err)
				return &authoritytypes.MsgExecuteSystemContract{Authority: government, Calls: []*authoritytypes.SystemCall{{Role: role, Target: roles[role].Bytes(), TargetCodeHash: proxyHash.Bytes(), Implementation: implementations[role].Bytes(), ImplementationCodeHash: crypto.Keccak256Hash(artifact.Runtime).Bytes(), Data: data, GasLimit: 200000, ReplacementCodeHash: childHash(child).Bytes()}}}
			}
			// Mutating the caller's nested map/slice cannot widen the keeper's policy.
			tokenChildren := manifest.Roles["token-center"].ApprovedChildren
			tokenChildren["upgradeToken"][0] = childHash("DRC20Reserveless")
			for _, slot := range []struct {
				role, method, child string
				kind                uint8
				reserve             bool
			}{
				{"token-center", "upgradeToken", "DRC20", 0, false}, {"token-center", "upgradeTokenReserveless", "DRC20Reserveless", 0, false},
				{"nft-center", "upgradeNFT", "DRC721", 0, true}, {"nft-center", "upgradeNFT", "DRC1155", 1, true},
				{"nft-center", "upgradeNFT", "DRC721Reserveless", 0, false}, {"nft-center", "upgradeNFT", "DRC1155Reserveless", 1, false},
			} {
				message := factoryMessage(slot.role, slot.method, slot.child, slot.kind, slot.reserve)
				require.NoError(t, authority.RunGovernance(ctx(), func(c sdk.Context) error { _, err := bridge.ExecuteSystemContract(c, message); return err }), slot.child)
			}
			baseline := marker()
			factoryState := func() []common.Address {
				values := []common.Address{query(roleABI["token-center"], roles["token-center"], "implementation")[0].(common.Address), query(roleABI["token-center"], roles["token-center"], "implementationReserveless")[0].(common.Address)}
				for _, slot := range []struct {
					kind    uint8
					reserve bool
				}{{0, true}, {1, true}, {0, false}, {1, false}} {
					values = append(values, query(roleABI["nft-center"], roles["nft-center"], "implementation", slot.kind, slot.reserve)[0].(common.Address))
				}
				return values
			}
			originalState := factoryState()
			for _, wrong := range []struct {
				role, method, child string
				kind                uint8
				reserve             bool
			}{
				{"token-center", "upgradeToken", "DRC20Reserveless", 0, false}, {"token-center", "upgradeTokenReserveless", "DRC20", 0, false},
				{"nft-center", "upgradeNFT", "DRC1155", 0, true}, {"nft-center", "upgradeNFT", "DRC721", 1, true},
				{"nft-center", "upgradeNFT", "DRC721Reserveless", 0, true}, {"nft-center", "upgradeNFT", "DRC1155Reserveless", 1, true},
				{"nft-center", "upgradeNFT", "DRC721", 0, false}, {"nft-center", "upgradeNFT", "DRC1155", 1, false},
			} {
				message := factoryMessage(wrong.role, wrong.method, wrong.child, wrong.kind, wrong.reserve)
				require.ErrorContains(t, authority.RunGovernance(ctx(), func(c sdk.Context) error { _, err := bridge.ExecuteSystemContract(c, message); return err }), "not approved", wrong.child)
				require.Equal(t, baseline, marker())
				require.Equal(t, originalState, factoryState())
			}
			tokenChildren["upgradeToken"][0] = childHash("DRC20")
			beforeMarker = marker()
			// All protocol signers and routes are validated by the unmodified SDK keeper.
			params, err := a.GovKeeper.Params.Get(ctx())
			require.NoError(t, err)
			period := time.Second
			expedited := 500 * time.Millisecond
			params.VotingPeriod = &period
			params.ExpeditedVotingPeriod = &expedited
			params.MinDeposit = sdk.NewCoins(sdk.NewInt64Coin(identity.BaseDenom, 1))
			require.NoError(t, a.GovKeeper.Params.Set(ctx(), params))
			submit := func(messages []sdk.Msg) uint64 {
				current := ctx()
				proposal, err := a.GovKeeper.SubmitProposal(current, messages, "", "System administration", "Bounded authority execution", accounts[0].GetAddress(), false)
				require.NoError(t, err)
				active, err := a.GovKeeper.AddDeposit(current, proposal.Id, accounts[0].GetAddress(), params.MinDeposit)
				require.NoError(t, err)
				require.True(t, active)
				require.NoError(t, a.GovKeeper.AddVote(current, proposal.Id, sdk.AccAddress(vals.Proposer.Address), govv1.NewNonSplitVoteOption(govv1.OptionYes), ""))
				// Two genuine blocks advance to the end of the voting period. The second
				// returns the proposal deposit; account debit still reconciles exactly.
				ownerCall, err := registry.ABI.Pack("owner")
				require.NoError(t, err)
				send(keys[0], &govRegistry, new(big.Int), ownerCall, true)
				nativeCredit.Set(sdk.Coins(params.MinDeposit).AmountOf(identity.BaseDenom).BigInt())
				send(keys[0], &govRegistry, new(big.Int), ownerCall, true)
				return proposal.Id
			}
			passed := submit([]sdk.Msg{makeMessage(321)})
			proposal, err := a.GovKeeper.Proposals.Get(ctx(), passed)
			require.NoError(t, err)
			require.Equal(t, govv1.StatusPassed, proposal.Status)
			require.Zero(t, big.NewInt(321).Cmp(readReward()))
			require.Equal(t, beforeMarker+1, marker())
			stale := makeMessage(999)
			stale.Calls[0].ImplementationCodeHash = make([]byte, 32)
			failed := submit([]sdk.Msg{makeMessage(777), stale})
			proposal, err = a.GovKeeper.Proposals.Get(ctx(), failed)
			require.NoError(t, err)
			require.Equal(t, govv1.StatusFailed, proposal.Status)
			for _, event := range lastBlockEvents {
				require.NotEqual(t, "native_authority_probe", event.Type, "failed proposal must not leak native hook events")
				require.NotEqual(t, "mintcandy.authority.v1.EventSystemContractExecuted", event.Type)
			}
			require.Zero(t, big.NewInt(321).Cmp(readReward()))
			require.Equal(t, beforeMarker+1, marker(), "later failed proposal message must discard earlier native hook writes")
		})

		t.Run("fresh factory creation and mint", func(t *testing.T) {
			proxyHash := crypto.Keccak256Hash(proxy.Runtime)
			creator := crypto.PubkeyToAddress(keys[0].PublicKey)
			type nftMeta struct {
				Creator     common.Address
				Symbol      string
				Name        string
				ContractURI string
				Refundable  bool
				Burnable    bool
			}
			meta := nftMeta{Creator: creator, Symbol: "FRESH", Name: "Fresh collection", ContractURI: "ipfs://collection", Refundable: true, Burnable: true}
			center := roles["nft-center"]
			factory := roleABI["nft-center"]
			receiptAuth := &signedNFTAuthenticator{App: a, Registry: govRegistry, RegistryImplementation: registryImpl, Factory: center, FactoryImplementation: implementations["nft-center"], Owner: govOwner, Proxy: proxy, RegistryABI: registry, FactoryABI: factory, Collections: make(map[common.Address]signedNFTCollection)}
			productionView := receiptAuth.productionView(t, roles["delegation"], libAddress, creator)
			// Both independent fresh profiles now create collections through the canonical ABI.
			originalCenter := read("getContractAddress", "nft-center")
			originalABI := load("DecimalNFTCenter")
			before, err := originalABI.ABI.Pack("createDRC721", meta)
			require.NoError(t, err)
			send(keys[0], &originalCenter, new(big.Int), before, true)
			type pausedAsset struct {
				address  common.Address
				artifact contracts.Artifact
				family   uint8
				id       *big.Int
			}
			var assets []pausedAsset
			for _, kind := range []struct {
				method, artifact string
				typ              uint8
				reserve          bool
			}{{"createDRC721", "DRC721", 0, true}, {"createDRC1155", "DRC1155", 1, true}, {"createDRC721Reserveless", "DRC721Reserveless", 0, false}, {"createDRC1155Reserveless", "DRC1155Reserveless", 1, false}} {
				beacon := query(factory, center, "beacon", kind.typ, kind.reserve)[0].(common.Address)
				require.NotEqual(t, common.Address{}, beacon)
				require.NotEmpty(t, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), beacon)))
				create, err := factory.ABI.Pack(kind.method, meta)
				require.NoError(t, err)
				created := send(keys[0], &center, new(big.Int), create, true)
				result, err := factory.ABI.Unpack(kind.method, created.Ret)
				require.NoError(t, err)
				collection := result[0].(common.Address)
				require.NotEqual(t, common.Address{}, collection)
				asset := freshAsset(kind.artifact)
				family := uint8(2) + kind.typ
				if !kind.reserve {
					family = uint8(4) + kind.typ
				}
				expectedBeacon, err := contracts.NFTBeaconRuntime(center, kind.typ, kind.reserve)
				require.NoError(t, err)
				require.Equal(t, expectedBeacon, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), beacon)))
				expectedProxy, err := contracts.PauseAwareProxyRuntime(beacon, center, family)
				require.NoError(t, err)
				require.Equal(t, expectedProxy, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), collection)), "NFT proxy code must derive from known constructor values")
				receiptAuth.Collections[collection] = signedNFTCollection{Beacon: beacon, Implementation: tokenImpls[kind.artifact], Artifact: asset, Family: family}

				huge := new(big.Int).Lsh(big.NewInt(1), 200)
				reserve := big.NewInt(2_000_000_000_000_000_000)
				var mint []byte
				var value *big.Int
				if kind.typ == 0 {
					if kind.reserve {
						mint, err = asset.ABI.Pack("mintByDEL", creator, "ipfs://fresh-item")
						value = reserve
					} else {
						mint, err = asset.ABI.Pack("mint", creator, "ipfs://fresh-item", new(big.Int), common.Address{})
						value = new(big.Int)
					}
				} else {
					if kind.reserve {
						mint, err = asset.ABI.Pack("mintByDEL", creator, huge, big.NewInt(2), "ipfs://shared")
						value = new(big.Int).Mul(reserve, big.NewInt(2))
					} else {
						mint, err = asset.ABI.Pack("mint", creator, huge, big.NewInt(2), "ipfs://shared", new(big.Int), common.Address{})
						value = new(big.Int)
					}
				}
				require.NoError(t, err)
				mintedReceipt := send(keys[0], &collection, value, mint, true)
				receiptAuth.checkMintAdmissionClosed(t, ctx(), mintedReceipt)
				if kind.typ == 0 {
					require.Equal(t, creator, query(asset, collection, "ownerOf", big.NewInt(1))[0])
				} else {
					require.Equal(t, int64(2), query(asset, collection, "balanceOf", creator, huge)[0].(*big.Int).Int64())
				}
				assets = append(assets, pausedAsset{address: collection, artifact: asset, family: family, id: huge})
				require.Zero(t, value.Cmp(a.EVMKeeper.GetBalance(ctx(), collection).ToBig()))
				require.Zero(t, value.Cmp(a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(collection.Bytes()), identity.BaseDenom).Amount.BigInt()))
			}
			tokenCenter := roles["token-center"]
			tokenFactory := roleABI["token-center"]
			originalTokens := read("getContractAddress", "token-center")
			freeData, err := tokenFactory.ABI.Pack("createTokenReserveless", "Fresh free token", "freshfree", true, true, big.NewInt(100), big.NewInt(1000), "fixture")
			require.NoError(t, err)
			send(keys[0], &originalTokens, new(big.Int), freeData, true)
			created := send(keys[0], &tokenCenter, new(big.Int), freeData, true)
			freeResult, err := tokenFactory.ABI.Unpack("createTokenReserveless", created.Ret)
			require.NoError(t, err)
			freeToken := freeResult[0].(common.Address)
			require.Equal(t, int64(100), query(load("DRC20Reserveless"), freeToken, "balanceOf", creator)[0].(*big.Int).Int64())
			require.Zero(t, a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(freeToken.Bytes()), identity.BaseDenom).Amount.Sign())

			// A failed later configuration removes the constructor-created beacons too.
			for _, family := range []struct {
				artifact, method string
				count            uint64
			}{{"NewNetworkNFTCenter", "upgradeNFT", 4}, {"NewNetworkTokenCenter", "upgradeTokenReserveless", 1}} {
				implementation := deploy(load(family.artifact))
				artifact := load(family.artifact)
				initialization, err := artifact.ABI.Pack("initializeFresh", govRegistry)
				require.NoError(t, err)
				var bad []byte
				if family.method == "upgradeNFT" {
					bad, err = artifact.ABI.Pack(family.method, common.Address{}, uint8(0), true)
				} else {
					bad, err = artifact.ABI.Pack(family.method, common.Address{})
				}
				require.NoError(t, err)
				creation, err := contracts.ConfiguredProxyDeployment(setup, implementation, govOwner, initialization, [][]byte{bad})
				require.NoError(t, err)
				failedProxy := crypto.CreateAddress(creator, a.EVMKeeper.GetNonce(ctx(), creator))
				send(keys[0], nil, new(big.Int), creation, false)
				require.Empty(t, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), failedProxy)))
				for nonce := uint64(1); nonce <= family.count; nonce++ {
					beacon := crypto.CreateAddress(failedProxy, nonce)
					require.Empty(t, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), beacon)), "failed constructor must not leave a beacon")
				}
			}
			// Reserve-backed creation remains functional and keeps exactly the net reserve.
			unit := big.NewInt(1_000_000_000_000_000_000)
			type coinMeta struct {
				InitialMint    *big.Int
				MinTotalSupply *big.Int
				MaxTotalSupply *big.Int
				Creator        common.Address
				Crr            uint8
				Identity       string
				Symbol         string
				Name           string
			}
			coin := coinMeta{InitialMint: new(big.Int).Mul(unit, big.NewInt(10)), MinTotalSupply: new(big.Int).Set(unit), MaxTotalSupply: new(big.Int).Mul(unit, big.NewInt(100)), Creator: creator, Crr: 50, Identity: "fixture", Symbol: "freshcoinx", Name: "Fresh reserve coin"}
			create, err := tokenFactory.ABI.Pack("createToken", coin)
			require.NoError(t, err)
			commission := query(tokenFactory, tokenCenter, "getCommissionSymbol", coin.Symbol)[0].(*big.Int)
			reserve := new(big.Int).Mul(unit, big.NewInt(10))
			funding := new(big.Int).Add(reserve, commission)
			send(keys[0], &tokenCenter, funding, create, true)
			coinAddress := query(tokenFactory, tokenCenter, "tokens", coin.Symbol)[0].(common.Address)
			require.NotEqual(t, common.Address{}, coinAddress)
			require.Zero(t, reserve.Cmp(a.EVMKeeper.GetBalance(ctx(), coinAddress).ToBig()))
			require.Equal(t, coin.InitialMint, query(load("DRC20"), coinAddress, "totalSupply")[0])
			freeBeacon := query(tokenFactory, tokenCenter, "reservelessBeacon")[0].(common.Address)
			expectedFreeBeacon, err := contracts.ReservelessTokenBeaconRuntime(tokenCenter)
			require.NoError(t, err)
			require.Equal(t, expectedFreeBeacon, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), freeBeacon)))
			for _, token := range []struct {
				address, beacon common.Address
				family          uint8
				artifact        string
			}{{coinAddress, tokenCenter, 0, "DRC20"}, {freeToken, freeBeacon, 1, "DRC20Reserveless"}} {
				expected, err := contracts.PauseAwareProxyRuntime(token.beacon, tokenCenter, token.family)
				require.NoError(t, err)
				require.Equal(t, expected, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), token.address)))
				wrong, err := contracts.PauseAwareProxyRuntime(token.beacon, govRegistry, token.family)
				require.NoError(t, err)
				require.NotEqual(t, wrong, expected, "wrong factory constructor cannot approve observed proxy code")
				assets = append(assets, pausedAsset{address: token.address, artifact: load(token.artifact), family: token.family})
			}
			// Only the explicit fresh profile keeps reads live during factory pauses.
			pauseManifest := authority.Manifest{Registry: authority.Role{Address: govRegistry, ProxyCodeHash: proxyHash, ApprovedImplementations: []common.Hash{crypto.Keccak256Hash(registry.Runtime)}}, Roles: map[string]authority.Role{}}
			for _, role := range []string{"token-center", "nft-center"} {
				pauseManifest.Roles[role] = authority.Role{Address: roles[role], ProxyCodeHash: proxyHash, ApprovedImplementations: []common.Hash{crypto.Keccak256Hash(roleABI[role].Runtime)}}
			}
			noOpHook := authorityReceiptHook(func(sdk.Context, common.Address, core.Message, *ethtypes.Receipt) error { return nil })
			pauseBridge, err := authority.NewKeeper(a.EVMKeeper, pauseManifest, noOpHook)
			require.NoError(t, err)
			govern := func(role, method string) {
				data, err := roleABI[role].ABI.Pack(method)
				require.NoError(t, err)
				message := &authoritytypes.MsgExecuteSystemContract{Authority: authtypes.NewModuleAddress("gov").String(), Calls: []*authoritytypes.SystemCall{{Role: role, Target: roles[role].Bytes(), TargetCodeHash: proxyHash.Bytes(), Implementation: implementations[role].Bytes(), ImplementationCodeHash: crypto.Keccak256Hash(roleABI[role].Runtime).Bytes(), Data: data, GasLimit: 200000}}}
				require.NoError(t, authority.RunGovernance(ctx(), func(c sdk.Context) error { _, err := pauseBridge.ExecuteSystemContract(c, message); return err }))
			}
			govern("token-center", "pause")
			govern("nft-center", "pause")
			require.Equal(t, common.Address{}, query(tokenFactory, tokenCenter, "implementation")[0])
			require.Equal(t, common.Address{}, query(factory, center, "implementation", uint8(0), true)[0])
			recipient := crypto.PubkeyToAddress(keys[1].PublicKey)
			for _, asset := range assets {
				if asset.family < 2 {
					require.NotZero(t, query(asset.artifact, asset.address, "totalSupply")[0].(*big.Int).Sign())
					require.NotZero(t, query(asset.artifact, asset.address, "balanceOf", creator)[0].(*big.Int).Sign())
					transfer, err := asset.artifact.ABI.Pack("transfer", recipient, big.NewInt(1))
					require.NoError(t, err)
					send(keys[0], &asset.address, new(big.Int), transfer, false)
				} else if asset.family == 2 || asset.family == 4 {
					require.Equal(t, creator, query(asset.artifact, asset.address, "ownerOf", big.NewInt(1))[0])
					require.Equal(t, "ipfs://fresh-item", query(asset.artifact, asset.address, "tokenURI", big.NewInt(1))[0])
					transfer, err := asset.artifact.ABI.Pack("transferFrom", creator, recipient, big.NewInt(1))
					require.NoError(t, err)
					send(keys[0], &asset.address, new(big.Int), transfer, false)
					send(keys[0], &asset.address, big.NewInt(1), nil, false)
				} else {
					require.Equal(t, int64(2), query(asset.artifact, asset.address, "balanceOf", creator, asset.id)[0].(*big.Int).Int64())
					transfer, err := asset.artifact.ABI.Pack("safeTransferFrom", creator, recipient, asset.id, big.NewInt(1), []byte{})
					require.NoError(t, err)
					send(keys[0], &asset.address, new(big.Int), transfer, false)
				}
				if asset.family >= 2 {
					tokenID := big.NewInt(1)
					quantity := uint32(1)
					if asset.family == 3 || asset.family == 5 {
						tokenID = asset.id
						quantity = 2
					}
					productionView.checkRead(t, ctx(), asset.address, creator, recipient, tokenID, quantity, 0)
				}
				// Raw storage resolution is explicit, same-context and never cached supply.
				target, abiFactory := tokenCenter, tokenFactory
				if asset.family >= 2 {
					target, abiFactory = center, factory
				}
				require.NotEqual(t, common.Address{}, query(abiFactory, target, "readImplementation", asset.family)[0])
			}
			govern("token-center", "unpause")
			govern("nft-center", "unpause")
			for _, asset := range assets {
				if asset.family < 2 {
					transfer, err := asset.artifact.ABI.Pack("transfer", recipient, big.NewInt(1))
					require.NoError(t, err)
					send(keys[0], &asset.address, new(big.Int), transfer, true)
					require.Equal(t, int64(1), query(asset.artifact, asset.address, "balanceOf", recipient)[0].(*big.Int).Int64())
				} else if asset.family == 2 || asset.family == 4 {
					transfer, err := asset.artifact.ABI.Pack("transferFrom", creator, recipient, big.NewInt(1))
					require.NoError(t, err)
					transferred := send(keys[0], &asset.address, new(big.Int), transfer, true)
					receiptAuth.checkTransfer(t, ctx(), transferred, asset.address, creator, recipient, big.NewInt(1))
					productionView.checkTransfer(t, ctx(), transferred, asset.address, creator, recipient, big.NewInt(1))
					require.Equal(t, recipient, query(asset.artifact, asset.address, "ownerOf", big.NewInt(1))[0])
				} else {
					transfer, err := asset.artifact.ABI.Pack("safeTransferFrom", creator, recipient, asset.id, big.NewInt(1), []byte{})
					require.NoError(t, err)
					transferred := send(keys[0], &asset.address, new(big.Int), transfer, true)
					receiptAuth.checkTransfer(t, ctx(), transferred, asset.address, creator, recipient, asset.id)
					productionView.checkTransfer(t, ctx(), transferred, asset.address, creator, recipient, asset.id)
					require.Equal(t, int64(1), query(asset.artifact, asset.address, "balanceOf", recipient, asset.id)[0].(*big.Int).Int64())
					// Two entries force an actual TransferBatch in OpenZeppelin; a
					// one-entry safeBatchTransferFrom emits TransferSingle instead.
					batch, err := asset.artifact.ABI.Pack("safeBatchTransferFrom", creator, recipient, []*big.Int{asset.id, asset.id}, []*big.Int{new(big.Int), big.NewInt(1)}, []byte{})
					require.NoError(t, err)
					batchReceipt := send(keys[0], &asset.address, new(big.Int), batch, true)
					receiptAuth.checkTransfer(t, ctx(), batchReceipt, asset.address, creator, recipient, asset.id)
					productionView.checkTransfer(t, ctx(), batchReceipt, asset.address, creator, recipient, asset.id)
					require.Equal(t, int64(2), query(asset.artifact, asset.address, "balanceOf", recipient, asset.id)[0].(*big.Int).Int64())
				}
			}

			// Missing nativeBurn must fail closed and restore every reserve/owner write.
			for _, kind := range []struct {
				method, artifact string
				is1155           bool
			}{{"createDRC721", "NewNetworkDRC721", false}, {"createDRC1155", "NewNetworkDRC1155", true}} {
				nonrefundable := meta
				nonrefundable.Refundable = false
				data, err := factory.ABI.Pack(kind.method, nonrefundable)
				require.NoError(t, err)
				response := send(keys[0], &center, new(big.Int), data, true)
				result, err := factory.ABI.Unpack(kind.method, response.Ret)
				require.NoError(t, err)
				target := result[0].(common.Address)
				asset := load(kind.artifact)
				value := big.NewInt(2_000_000_000_000_000_000)
				var mint, burn []byte
				if kind.is1155 {
					mint, err = asset.ABI.Pack("mintByDEL", creator, big.NewInt(1), big.NewInt(1), "ipfs://burn-fixture")
					require.NoError(t, err)
					burn, err = asset.ABI.Pack("burn", big.NewInt(1), big.NewInt(1))
				} else {
					mint, err = asset.ABI.Pack("mintByDEL", creator, "ipfs://burn-fixture")
					require.NoError(t, err)
					burn, err = asset.ABI.Pack("burn", big.NewInt(1))
				}
				require.NoError(t, err)
				send(keys[0], &target, value, mint, true)
				reserveBefore, err := asset.ABI.Methods["getReserve"].Outputs.Pack(query(asset, target, "getReserve", big.NewInt(1))...)
				require.NoError(t, err)

				primitiveAddress := common.HexToAddress("0x0000000000000000000000000000000000000900")
				_, active, err := a.EVMKeeper.GetPrecompileInstance(ctx(), primitiveAddress)
				require.NoError(t, err)
				require.True(t, active, "this application profile must exercise the active native supply primitive")
				if active {
					supplyBeforeFailure := a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount
					rejectedBefore := rejectedNativeBurns
					rejectNativeBurn = true
					failed := send(keys[0], &target, new(big.Int), burn, false)
					rejectNativeBurn = false
					require.Contains(t, failed.VmError, "ordinary native burn receipt validation failed")
					require.Equal(t, rejectedBefore+1, rejectedNativeBurns, "the real native burn receipt must reach the rejecting validator")
					reserveAfterFailure, err := asset.ABI.Methods["getReserve"].Outputs.Pack(query(asset, target, "getReserve", big.NewInt(1))...)
					require.NoError(t, err)
					require.Equal(t, reserveBefore, reserveAfterFailure)
					if kind.is1155 {
						require.Equal(t, int64(1), query(asset, target, "balanceOf", creator, big.NewInt(1))[0].(*big.Int).Int64())
					} else {
						require.Equal(t, creator, query(asset, target, "ownerOf", big.NewInt(1))[0])
					}
					require.Zero(t, value.Cmp(a.EVMKeeper.GetBalance(ctx(), target).ToBig()))
					require.Zero(t, value.Cmp(a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(target.Bytes()), identity.BaseDenom).Amount.BigInt()))
					minted, burned := math.ZeroInt(), math.ZeroInt()
					for _, event := range lastAllEvents {
						require.NotEqual(t, "mintcandy_native_burn", event.Type, "rejected receipt must not publish a native burn event")
						if event.Type != banktypes.EventTypeCoinMint && event.Type != banktypes.EventTypeCoinBurn {
							continue
						}
						for _, attribute := range event.Attributes {
							if attribute.Key != sdk.AttributeKeyAmount {
								continue
							}
							coins, err := sdk.ParseCoinsNormalized(attribute.Value)
							require.NoError(t, err)
							if event.Type == banktypes.EventTypeCoinMint {
								minted = minted.Add(coins.AmountOf(identity.BaseDenom))
							} else {
								burned = burned.Add(coins.AmountOf(identity.BaseDenom))
							}
						}
					}
					require.Equal(t, supplyBeforeFailure.Add(minted).Sub(burned), a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount, "rejected NFT burn restores real native supply")
					t.Logf("ordinary %s receipt rejection restored NFT ownership, reserve, native balances and bank supply", kind.artifact)
				}
				supplyBefore := a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount
				outcome := send(keys[0], &target, new(big.Int), burn, active)
				if !active {
					t.Log("ordinary native reserve control: missing backend rejects and restores state")
					var selector [4]byte
					require.GreaterOrEqual(t, len(outcome.Ret), 4)
					copy(selector[:], outcome.Ret[:4])
					reason, err := asset.ABI.ErrorByID(selector)
					require.NoError(t, err)
					require.Equal(t, "NativeReserveBurnFailed", reason.Name)
					reserveAfter, err := asset.ABI.Methods["getReserve"].Outputs.Pack(query(asset, target, "getReserve", big.NewInt(1))...)
					require.NoError(t, err)
					require.Equal(t, reserveBefore, reserveAfter)
					if kind.is1155 {
						require.Equal(t, int64(1), query(asset, target, "balanceOf", creator, big.NewInt(1))[0].(*big.Int).Int64())
					} else {
						require.Equal(t, creator, query(asset, target, "ownerOf", big.NewInt(1))[0])
					}
					require.Zero(t, value.Cmp(a.EVMKeeper.GetBalance(ctx(), target).ToBig()))
					require.Zero(t, value.Cmp(a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(target.Bytes()), identity.BaseDenom).Amount.BigInt()))
				} else {
					t.Log("ordinary native reserve control: active backend burns real bank supply")
					// Reconcile independent bank supply with every mint/burn event in this
					// block, including ordinary mint-module or fee activity if configured.
					minted, burned := math.ZeroInt(), math.ZeroInt()
					for _, event := range lastAllEvents {
						if event.Type != banktypes.EventTypeCoinMint && event.Type != banktypes.EventTypeCoinBurn {
							continue
						}
						for _, attribute := range event.Attributes {
							if attribute.Key != sdk.AttributeKeyAmount {
								continue
							}
							coins, err := sdk.ParseCoinsNormalized(attribute.Value)
							require.NoError(t, err)
							amount := coins.AmountOf(identity.BaseDenom)
							if event.Type == banktypes.EventTypeCoinMint {
								minted = minted.Add(amount)
							} else {
								burned = burned.Add(amount)
							}
						}
					}
					require.True(t, burned.GTE(math.NewIntFromBigInt(value)), "native NFT destruction must burn at least its reserve")
					require.Equal(t, supplyBefore.Add(minted).Sub(burned), a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount)
					primitiveABI, err := contracts.CompiledABI("IMintCandyNativeBurn")
					require.NoError(t, err)
					found := false
					for _, log := range outcome.Logs {
						if common.HexToAddress(log.Address) != primitiveAddress || len(log.Topics) != 2 || common.HexToHash(log.Topics[0]) != primitiveABI.Events["NativeBurn"].ID {
							continue
						}
						require.Equal(t, target, common.BytesToAddress(common.HexToHash(log.Topics[1]).Bytes()[12:]))
						values, err := primitiveABI.Events["NativeBurn"].Inputs.NonIndexed().Unpack(log.Data)
						require.NoError(t, err)
						require.Zero(t, value.Cmp(values[0].(*big.Int)))
						found = true
					}
					require.True(t, found, "native reserve destruction must carry its acknowledged event")
					require.True(t, a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(target.Bytes()), identity.BaseDenom).Amount.IsZero())
					require.True(t, a.EVMKeeper.GetBalance(ctx(), target).IsZero())
				}
			}

		})
		// A previous external registry write must also disappear on a later failure.
		fixture := load("ConfiguredBootstrapTestTarget")
		fixtureImpl := deploy(fixture)
		registryImpl = deploy(registry)
		currentNonce := a.EVMKeeper.GetNonce(ctx(), from)
		externalRegistry := crypto.CreateAddress(from, currentNonce)
		failedProxy := crypto.CreateAddress(from, currentNonce+1)
		externalInit, err := bootstrap.ABI.Pack("bootstrap", failedProxy, registryImpl, []byte{})
		require.NoError(t, err)
		require.Equal(t, externalRegistry, deploy(proxy, setup, externalInit))
		earlier, err := fixture.ABI.Pack("configureExternal", externalRegistry, "rollback-proof", wrapped)
		require.NoError(t, err)
		later, err := fixture.ABI.Pack("failConfiguration")
		require.NoError(t, err)
		failing, err := bootstrap.ABI.Pack("bootstrapConfigured", govOwner, fixtureImpl, []byte{}, [][]byte{earlier, later})
		require.NoError(t, err)
		creation, err := proxy.Deployment(setup, failing)
		require.NoError(t, err)
		response := send(keys[0], nil, new(big.Int), creation, false)
		require.Empty(t, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), failedProxy)))
		var selector [4]byte
		require.GreaterOrEqual(t, len(response.Ret), 4)
		copy(selector[:], response.Ret[:4])
		custom, err := fixture.ABI.ErrorByID(selector)
		require.NoError(t, err)
		require.Equal(t, "RejectedConfiguration", custom.Name)
		require.Equal(t, common.Address{}, query(registry, externalRegistry, "getContractAddress", "rollback-proof")[0])
		require.Equal(t, failedProxy, query(registry, externalRegistry, "owner")[0])
		// Configurations may not take ownership or redirect implementation mid-setup.
		changed, err := fixture.ABI.Pack("transferOwnership", govOwner)
		require.NoError(t, err)
		invalid, err := bootstrap.ABI.Pack("bootstrapConfigured", govOwner, fixtureImpl, []byte{}, [][]byte{changed})
		require.NoError(t, err)
		creation, err = proxy.Deployment(setup, invalid)
		require.NoError(t, err)
		send(keys[0], nil, new(big.Int), creation, false)
		invalid, err = bootstrap.ABI.Pack("bootstrapConfigured", common.Address{}, fixtureImpl, []byte{}, [][]byte{})
		require.NoError(t, err)
		creation, err = proxy.Deployment(setup, invalid)
		require.NoError(t, err)
		send(keys[0], nil, new(big.Int), creation, false)
	})
	t.Run("fresh native-backed wrapper", func(t *testing.T) {
		wrapper := load("MintCandyWrappedDEL")
		address := deploy(wrapper)
		amount := big.NewInt(1_000_000_000_000_000_000)
		call := func(key *ecdsa.PrivateKey, method string, success bool, args ...interface{}) *evmtypes.MsgEthereumTxResponse {
			if method == "withdraw" && success {
				nativeCredit.Set(args[0].(*big.Int))
			}
			input, err := wrapper.ABI.Pack(method, args...)
			require.NoError(t, err)
			return send(key, &address, new(big.Int), input, success)
		}
		supply := func() *big.Int {
			response := call(keys[0], "totalSupply", true)
			values, err := wrapper.ABI.Unpack("totalSupply", response.Ret)
			require.NoError(t, err)
			return values[0].(*big.Int)
		}
		balanced := func(expected *big.Int) {
			require.Zero(t, expected.Cmp(supply()))
			require.Zero(t, expected.Cmp(a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(address.Bytes()), identity.BaseDenom).Amount.BigInt()))
			require.Zero(t, expected.Cmp(a.EVMKeeper.GetBalance(ctx(), address).ToBig()))
		}
		deposit, err := wrapper.ABI.Pack("deposit")
		require.NoError(t, err)
		send(keys[0], &address, amount, deposit, true)
		balanced(amount)
		// receive() follows the same backing rule for a plain native transfer.
		send(keys[0], &address, amount, nil, true)
		twice := new(big.Int).Mul(amount, big.NewInt(2))
		balanced(twice)
		recipient := crypto.PubkeyToAddress(keys[1].PublicKey)
		call(keys[0], "transfer", true, recipient, amount)
		call(keys[1], "withdraw", true, amount)
		balanced(amount)
		call(keys[1], "withdraw", false, big.NewInt(1))
		balanced(amount)
		call(keys[0], "approve", true, recipient, amount)
		call(keys[1], "transferFrom", true, crypto.PubkeyToAddress(keys[0].PublicKey), recipient, amount)
		call(keys[1], "withdraw", true, amount)
		balanced(new(big.Int))

		probe := load("WrappedDELTestReceiver")
		owner := crypto.PubkeyToAddress(keys[0].PublicKey)
		probeAddress := crypto.CreateAddress(owner, a.EVMKeeper.GetNonce(ctx(), owner))
		code, err := probe.Deployment(address)
		require.NoError(t, err)
		send(keys[0], nil, new(big.Int), code, true) // Immutable wrapper address changes runtime bytes.
		fund, err := probe.ABI.Pack("fund")
		require.NoError(t, err)
		send(keys[0], &probeAddress, amount, fund, true)
		balanced(amount)
		redeem, err := probe.ABI.Pack("redeem", amount)
		require.NoError(t, err)
		send(keys[0], &probeAddress, new(big.Int), redeem, false)
		balanced(amount)
		balance := call(keys[0], "balanceOf", true, probeAddress)
		values, err := wrapper.ABI.Unpack("balanceOf", balance.Ret)
		require.NoError(t, err)
		require.Equal(t, amount, values[0].(*big.Int), "rejected native withdrawal must restore burned wrapped units")
	})
	t.Run("isolated NFT vault backend", func(t *testing.T) {
		runIsolatedVaultBackend(t, isolatedBackendEnv{app: a, ctx: ctx, send: send, load: load, deploy: deploy, keys: keys, credit: nativeCredit, events: func() []abci.Event { return lastAllEvents }, setup: setup, govOwner: common.BytesToAddress(authtypes.NewModuleAddress(govtypes.ModuleName))})
	})
	t.Run("canonical isolated NFT Gateway", func(t *testing.T) {
		runCanonicalIsolatedGateway(t, isolatedBackendEnv{app: a, ctx: ctx, send: send, load: load, deploy: deploy, keys: keys, credit: nativeCredit, events: func() []abci.Event { return lastAllEvents }, setup: setup}, func(reject bool) { rejectNativeBurn = reject })
	})
	t.Run("authenticated isolated position reader", func(t *testing.T) {
		runIsolatedPositionReader(t, isolatedBackendEnv{
			app: a, ctx: ctx, advance: advance, nextGasLimit: func(limit uint64) { nextGasLimit = limit },
			genesisClock: func(next time.Time) {
				require.True(t, next.After(ctx().BlockTime()))
				now = next.Add(-time.Duration(a.LastBlockHeight()+1) * time.Second)
			},
			saveGenesisKey: func(dir string) {
				pv := privval.NewFilePV(signer, filepath.Join(dir, "priv_validator_key.json"), filepath.Join(dir, "unused_sign_state.json"))
				pv.Key.Save()
			},
			send: send, load: load, deploy: deploy, keys: keys, credit: nativeCredit, events: func() []abci.Event { return lastAllEvents },
			blockEvents: func() []abci.Event { return lastBlockEvents }, deferrals: func() []string { return withdrawalDeferrals },
			setup: setup, govOwner: common.BytesToAddress(authtypes.NewModuleAddress(govtypes.ModuleName)),
		})
	})
	t.Run("curve converter ordinary financial controls", func(t *testing.T) {
		runCurveConverterFinancial(t, isolatedBackendEnv{app: a, ctx: ctx, send: send, load: load, deploy: deploy, keys: keys, credit: nativeCredit, events: func() []abci.Event { return lastAllEvents }, blockEvents: func() []abci.Event { return lastBlockEvents }, setup: setup, govOwner: common.BytesToAddress(authtypes.NewModuleAddress(govtypes.ModuleName))})
	})
	t.Log("actual owned-App signed registry/proxy constructor, explicit owner, authorization, reinitialization rejection and exact native/EVM fees passed")
}
