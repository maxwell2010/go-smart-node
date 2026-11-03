package evmview_test

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
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
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/evmview"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type appOptions map[string]interface{}

func (o appOptions) Get(key string) interface{} { return o[key] }

// This test binary owns exactly one production-tag App. It never resets the
// process-global EVM configuration, patches storage or broadcasts transactions.
func TestAuthoritativeCoinViewWithSignedOwnEVM(t *testing.T) {
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
	require.NoError(t, ownedapp.ReconcileNativeGenesisAllocation(a.AppCodec(), genesis))
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
		return a.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, ProposerAddress: vals.Proposer.Address, Height: a.LastBlockHeight() + 1, Time: now.Add(time.Duration(a.LastBlockHeight()+1) * time.Second)})
	}
	price := big.NewInt(1_000_000_000_000)
	nativeCredit := new(big.Int)
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

	deployCode := func(code []byte, value *big.Int) common.Address {
		t.Helper()
		from := crypto.PubkeyToAddress(keys[0].PublicKey)
		address := crypto.CreateAddress(from, a.EVMKeeper.GetNonce(ctx(), from))
		send(keys[0], nil, value, code, true)
		return address
	}
	artifact := load("DRC20")
	proxy := load("DecimalContractCenterProxy")
	implementation := deployCode(artifact.Creation, new(big.Int))
	unit := big.NewInt(1_000_000_000_000_000_000)
	owner := crypto.PubkeyToAddress(keys[0].PublicKey)
	initialization, err := artifact.ABI.Pack("initialize", "Coin view fixture", "viewcoin", owner, uint8(50), new(big.Int).Mul(unit, big.NewInt(10)), unit, new(big.Int).Mul(unit, big.NewInt(100)), "")
	require.NoError(t, err)
	creation, err := proxy.Deployment(implementation, []byte{})
	require.NoError(t, err)
	token := deployCode(creation, new(big.Int))
	send(keys[0], &token, new(big.Int).Mul(unit, big.NewInt(5)), initialization, true)
	metadata := &fixtureMetadata{coin: cointypes.Coin{Denom: "viewcoin", Title: "Coin view fixture", Creator: accounts[0].GetAddress().String(), CRR: 50, Volume: math.NewIntFromBigInt(new(big.Int).Mul(unit, big.NewInt(9))), Reserve: math.NewIntFromBigInt(unit), MinVolume: math.NewIntFromBigInt(unit), LimitVolume: math.NewIntFromBigInt(new(big.Int).Mul(unit, big.NewInt(100))), DRC20Contract: token.Hex()}}
	binding := &fixtureBinding{binding: evmview.Binding{Address: token, RuntimeHash: a.EVMKeeper.GetCodeHash(ctx(), token), Implementation: implementation, ImplementationHash: a.EVMKeeper.GetCodeHash(ctx(), implementation)}}
	compiled, err := contracts.CompiledABI("DRC20")
	require.NoError(t, err)
	reader, err := evmview.New(metadata, a.BankKeeper, a.EVMKeeper, binding, compiled, 200_000, 2_000_000)
	require.NoError(t, err)
	metered := ctx().WithGasMeter(storetypes.NewGasMeter(3_000_000))
	beforeNonce := a.EVMKeeper.GetNonce(ctx(), common.Address{})
	hydrated, err := reader.GetCoin(metered, "viewcoin")
	require.NoError(t, err)
	require.Positive(t, metered.GasMeter().GasConsumed())
	require.Equal(t, math.NewIntFromBigInt(new(big.Int).Mul(unit, big.NewInt(10))), hydrated.Volume)
	require.Equal(t, math.NewIntFromBigInt(new(big.Int).Mul(unit, big.NewInt(5))), hydrated.Reserve)
	require.Equal(t, metadata.coin.Reserve, math.NewIntFromBigInt(unit), "metadata fixture intentionally remains stale")
	require.Equal(t, beforeNonce, a.EVMKeeper.GetNonce(ctx(), common.Address{}))
	// Actual same-context bank credit does not execute the token's receive hook.
	branch, _ := ctx().CacheContext()
	require.NoError(t, a.BankKeeper.SendCoins(branch, accounts[0].GetAddress(), sdk.AccAddress(token.Bytes()), sdk.NewCoins(sdk.NewCoin("del", math.NewInt(123)))))
	credited, err := reader.GetCoin(branch, "viewcoin")
	require.NoError(t, err)
	require.Equal(t, hydrated.Reserve.AddRaw(123), credited.Reserve)
	parent, err := reader.GetCoin(ctx(), "viewcoin")
	require.NoError(t, err)
	require.Equal(t, hydrated.Reserve, parent.Reserve)
	// An explicitly supplied wrong runtime/implementation policy fails closed.
	savedBinding := binding.binding
	binding.binding.Implementation = common.Address{}
	binding.binding.ImplementationHash = common.Hash{}
	_, err = reader.GetCoin(ctx(), "viewcoin")
	require.ErrorContains(t, err, "implementation")
	binding.binding = savedBinding
	binding.binding.ImplementationHash = common.HexToHash("0x01")
	_, err = reader.GetCoin(ctx(), "viewcoin")
	require.ErrorContains(t, err, "implementation")
	binding.binding.ImplementationHash = a.EVMKeeper.GetCodeHash(ctx(), implementation)
	binding.binding.RuntimeHash = common.HexToHash("0x01")
	_, err = reader.GetCoin(ctx(), "viewcoin")
	require.ErrorContains(t, err, "runtime")
	_, err = reader.GetCoin(ctx(), "del")
	require.ErrorContains(t, err, "base DEL")
	// Deliberately approved adversarial fixture code proves execution bounds and
	// discard semantics independently from the genuine DRC20 happy path.
	for _, test := range []struct {
		name, runtime string
		success       bool
	}{
		{"state-write", "6001600055678ac7230489e8000060005260206000f3", true},
		{"revert", "60006000fd", false},
		{"empty-return", "60006000f3", false},
		{"gas-loop", "5b600056", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := hex.DecodeString(test.runtime)
			require.NoError(t, err)
			prefix, err := hex.DecodeString(fmt.Sprintf("60%02x600c60003960%02x6000f3", len(runtime), len(runtime)))
			require.NoError(t, err)
			target := deployCode(append(prefix, runtime...), new(big.Int))
			metadata.coin.DRC20Contract = target.Hex()
			binding.binding = evmview.Binding{Address: target, RuntimeHash: a.EVMKeeper.GetCodeHash(ctx(), target)}
			before := a.EVMKeeper.GetState(ctx(), target, common.Hash{})
			viewContext := ctx().WithGasMeter(storetypes.NewGasMeter(3_000_000))
			result, err := reader.GetCoin(viewContext, "viewcoin")
			if test.name == "gas-loop" {
				require.GreaterOrEqual(t, viewContext.GasMeter().GasConsumed(), uint64(200_000))
				require.LessOrEqual(t, viewContext.GasMeter().GasConsumed(), uint64(2_200_000))
				t.Logf("bounded loop EVM cap200000, total charged SDKgas=%d", viewContext.GasMeter().GasConsumed())
			}
			if test.success {
				require.NoError(t, err)
				require.Equal(t, math.NewIntFromBigInt(new(big.Int).Mul(unit, big.NewInt(10))), result.Volume)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, before, a.EVMKeeper.GetState(ctx(), target, common.Hash{}), "view writes must never reach native EVM store")
		})
	}
	metadata.coin.DRC20Contract = token.Hex()
	binding.binding = savedBinding
	bounded, err := evmview.New(metadata, a.BankKeeper, a.EVMKeeper, binding, compiled, 200_000, 1)
	require.NoError(t, err)
	_, err = bounded.GetCoin(ctx(), "viewcoin")
	require.ErrorContains(t, err, "SDK gas")
	var absent *fixtureMetadata
	_, err = evmview.New(absent, a.BankKeeper, a.EVMKeeper, binding, compiled, 200_000, 2_000_000)
	require.Error(t, err)

}

// Only metadata and admission policy are fixtures. The deployed compiler-pinned
// token, proxy, signed FinalizeBlock/Commit, EVM and SDK bank state are genuine.
type fixtureMetadata struct{ coin cointypes.Coin }

func (m *fixtureMetadata) GetCoin(_ sdk.Context, denom string) (cointypes.Coin, error) {
	return m.coin, nil
}
func (m *fixtureMetadata) GetParams(sdk.Context) (cointypes.Params, error) {
	return cointypes.DefaultParams(), nil
}

type fixtureBinding struct{ binding evmview.Binding }

func (b *fixtureBinding) CoinBinding(sdk.Context, string) (evmview.Binding, error) {
	return b.binding, nil
}
