package systemcontracts_test

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
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
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	ethsecp "github.com/cosmos/evm/crypto/ethsecp256k1"
	srvflags "github.com/cosmos/evm/server/flags"
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

// This test binary owns exactly one production-tag App. It never resets the
// process-global EVM configuration, patches storage or broadcasts transactions.
func TestOwnedAppSignedRegistryBootstrap(t *testing.T) {
	const chainID = "mintcandy_990001-1"
	a := ownedapp.NewApp(log.NewNopLogger(), dbm.NewMemDB(), true, appOptions{flags.FlagHome: t.TempDir(), flags.FlagChainID: chainID, srvflags.EVMChainID: uint64(990001)})
	t.Cleanup(func() { require.NoError(t, a.Close()) })
	keys := make([]*ecdsa.PrivateKey, 3)
	accounts := make([]authtypes.GenesisAccount, 3)
	balances := make([]banktypes.Balance, 3)
	funds := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, math.NewInt(10000).Mul(math.NewInt(1_000_000_000_000_000_000))))
	for i := range keys {
		key, err := ethsecp.GenerateKey()
		require.NoError(t, err)
		keys[i], err = crypto.ToECDSA(key.Bytes())
		require.NoError(t, err)
		accounts[i] = authtypes.NewBaseAccount(key.PubKey().Address().Bytes(), key.PubKey(), uint64(i), 0)
		require.Equal(t, crypto.PubkeyToAddress(keys[i].PublicKey).Bytes(), accounts[i].GetAddress().Bytes())
		balances[i] = banktypes.Balance{Address: accounts[i].GetAddress().String(), Coins: funds}
	}
	validator, _ := cmttypes.RandValidator(false, 1)
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
	data, err := json.Marshal(genesis)
	require.NoError(t, err)
	now := time.Unix(1900000000, 0).UTC()
	_, err = a.InitChain(&abci.RequestInitChain{ChainId: chainID, Time: now, AppStateBytes: data, ConsensusParams: simtestutil.DefaultConsensusParams})
	require.NoError(t, err)
	// Materialize fresh genesis before comparing balances through the committed
	// root store. InitChain alone only populates the pending finalize state.
	_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: now.Add(time.Second), NextValidatorsHash: vals.Hash(), ProposerAddress: vals.Proposer.Address})
	require.NoError(t, err)
	_, err = a.Commit()
	require.NoError(t, err)
	ctx := func() sdk.Context {
		return a.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: a.LastBlockHeight() + 1, Time: now.Add(time.Duration(a.LastBlockHeight()+1) * time.Second), ProposerAddress: vals.Proposer.Address})
	}
	price := big.NewInt(1_000_000_000_000)
	nativeCredit := new(big.Int)
	var lastBlockEvents []abci.Event
	send := func(key *ecdsa.PrivateKey, to *common.Address, value *big.Int, input []byte, success bool) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		from := crypto.PubkeyToAddress(key.PublicKey)
		before := a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount
		nonce := a.EVMKeeper.GetNonce(ctx(), from)
		raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce, To: to, Value: value, Gas: 8_000_000, GasPrice: price, Data: input})
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
	load := func(name string) contracts.Artifact {
		t.Helper()
		artifact, err := contracts.Load(name)
		require.NoError(t, err)
		return artifact
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
		require.Equal(t, int64(257), penaltyReader("getPenaltyCount", member)[0].(*big.Int).Int64())
		require.Len(t, penaltyReader("getPenaltiesRange", member, big.NewInt(0), big.NewInt(32))[0], 32)
		t.Logf("actual penalty range32 MaxUsedGas=%d", penaltyReadGas)
		require.Less(t, penaltyReadGas, uint64(100000))
		require.Len(t, penaltyReader("getPenaltiesRange", member, big.NewInt(0), big.NewInt(256))[0], 256)
		t.Logf("actual penalty range256 MaxUsedGas=%d", penaltyReadGas)
		require.Equal(t, []uint16{1}, penaltyReader("getPenaltiesRange", member, big.NewInt(256), big.NewInt(256))[0])

	})

	t.Run("fresh full system proxy set", func(t *testing.T) {
		lib := load("DelegationNFTLib")
		from := crypto.PubkeyToAddress(keys[0].PublicKey)
		libAddress := crypto.CreateAddress(from, a.EVMKeeper.GetNonce(ctx(), from))
		code, err := lib.Deployment()
		require.NoError(t, err)
		send(keys[0], nil, new(big.Int), code, true)
		require.NotEmpty(t, a.EVMKeeper.GetCode(ctx(), a.EVMKeeper.GetCodeHash(ctx(), libAddress)))
		delegation, err := contracts.LoadLinked("DecimalDelegation", map[string]common.Address{"sources/DecimalDelegation.sol:DelegationNFTLib": libAddress})
		require.NoError(t, err)
		roles := map[string]common.Address{}
		artifacts := map[string]contracts.Artifact{}
		for _, role := range []string{"master-validator", "delegation", "delegation-nft", "token-center", "nft-center"} {
			name := map[string]string{"master-validator": "NewNetworkMasterValidator", "delegation": "DecimalDelegation", "delegation-nft": "DecimalDelegationNFT", "token-center": "DecimalTokenCenter", "nft-center": "DecimalNFTCenter"}[role]
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
				initialization, err = artifact.ABI.Pack("initialize")
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
			implementation := deploy(load(nft.name))
			configure("nft-center", "upgradeNFT", implementation, nft.kind, nft.reserve)
			target := roles["nft-center"]
			input, err := artifacts["nft-center"].ABI.Pack("implementation", nft.kind, nft.reserve)
			require.NoError(t, err)
			result := send(keys[1], &target, new(big.Int), input, true)
			values, err := artifacts["nft-center"].ABI.Unpack("implementation", result.Ret)
			require.NoError(t, err)
			require.Equal(t, implementation, values[0])
		}
	})

	t.Run("atomic constructor handoff to governance", func(t *testing.T) {
		govOwner := common.BytesToAddress(authtypes.NewModuleAddress("gov"))
		require.NotEqual(t, crypto.PubkeyToAddress(keys[0].PublicKey), govOwner)
		from := crypto.PubkeyToAddress(keys[0].PublicKey)
		lib := load("DelegationNFTLib")
		libAddress := crypto.CreateAddress(from, a.EVMKeeper.GetNonce(ctx(), from))
		libCode, err := lib.Deployment()
		require.NoError(t, err)
		send(keys[0], nil, new(big.Int), libCode, true)
		linked, err := contracts.LoadLinked("DecimalDelegation", map[string]common.Address{"sources/DecimalDelegation.sol:DelegationNFTLib": libAddress})
		require.NoError(t, err)
		names := []string{"master-validator", "delegation", "delegation-nft", "token-center", "nft-center"}
		roleABI := map[string]contracts.Artifact{}
		implementations := map[string]common.Address{}
		for _, role := range names {
			name := map[string]string{"master-validator": "NewNetworkMasterValidator", "delegation-nft": "DecimalDelegationNFT", "token-center": "DecimalTokenCenter", "nft-center": "DecimalNFTCenter"}[role]
			artifact := linked
			if role != "delegation" {
				artifact = load(name)
			}
			roleABI[role] = artifact
			implementations[role] = deploy(artifact)
		}
		tokenImpls := map[string]common.Address{}
		for _, name := range []string{"DRC20", "DRC20Reserveless", "DRC721", "DRC1155", "DRC721Reserveless", "DRC1155Reserveless"} {
			tokenImpls[name] = deploy(load(name))
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
			} else {
				config = append(config, pack("setContractCenter", govRegistry))
			}
			if role == "token-center" {
				initialization = pack("initialize")
				config = append(config, pack("upgradeToken", tokenImpls["DRC20"]), pack("upgradeTokenReserveless", tokenImpls["DRC20Reserveless"]))
			}
			if role == "nft-center" {
				initialization = pack("initialize")
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

			childHash := func(name string) common.Hash { return crypto.Keccak256Hash(load(name).Runtime) }
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
	t.Log("actual owned-App signed registry/proxy constructor, explicit owner, authorization, reinitialization rejection and exact native/EVM fees passed")
}
