package systemcontracts_test

import (
	"crypto/ecdsa"
	"encoding/json"
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
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	ethsecp "github.com/cosmos/evm/crypto/ethsecp256k1"
	srvflags "github.com/cosmos/evm/server/flags"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
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
		return a.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: a.LastBlockHeight() + 1, Time: now.Add(time.Duration(a.LastBlockHeight()+1) * time.Second)})
	}
	price := big.NewInt(1_000_000_000_000)
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
			debit = debit.Add(math.NewIntFromBigInt(value))
		}
		require.Equal(t, debit, before.Sub(a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount), "actual signed transaction must charge exactly receipt gas plus committed value")
		require.Equal(t, a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount.BigInt(), a.EVMKeeper.GetBalance(ctx(), from).ToBig())
		require.Equal(t, nonce+1, a.EVMKeeper.GetNonce(ctx(), from))
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
	t.Log("actual owned-App signed registry/proxy constructor, explicit owner, authorization, reinitialization rejection and exact native/EVM fees passed")
}
