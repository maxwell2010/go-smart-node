package app

import (
	"encoding/json"
	"fmt"
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
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

type options map[string]interface{}

func (o options) Get(key string) interface{} { return o[key] }

func testApp(t *testing.T, chainID string, evmID uint64) *App {
	t.Helper()
	resetEVMConfigurationForTest()
	a := NewApp(log.NewNopLogger(), dbm.NewMemDB(), true, options{
		flags.FlagHome: t.TempDir(), flags.FlagChainID: chainID, srvflags.EVMChainID: evmID,
	})
	t.Cleanup(func() { require.NoError(t, a.Close()) })
	return a
}

func fundedTestGenesis(t *testing.T, a *App, vals *cmttypes.ValidatorSet, accounts []authtypes.GenesisAccount, balances ...banktypes.Balance) map[string]json.RawMessage {
	t.Helper()
	defaults := a.DefaultGenesis()
	genesis, err := simtestutil.GenesisStateWithValSet(a.AppCodec(), defaults, vals, accounts, balances...)
	require.NoError(t, err)
	defaults = a.DefaultGenesis() // the SDK helper mutates the supplied map
	// The SDK fixture replaces module defaults with its sample staking denom and
	// drops bank metadata. Keep the real application configuration in the fixture.
	var bankDefault, bankFunded banktypes.GenesisState
	var stakingDefault, stakingFunded stakingtypes.GenesisState
	a.AppCodec().MustUnmarshalJSON(defaults[banktypes.ModuleName], &bankDefault)
	a.AppCodec().MustUnmarshalJSON(genesis[banktypes.ModuleName], &bankFunded)
	a.AppCodec().MustUnmarshalJSON(defaults[stakingtypes.ModuleName], &stakingDefault)
	a.AppCodec().MustUnmarshalJSON(genesis[stakingtypes.ModuleName], &stakingFunded)
	stakingFunded.Params = stakingDefault.Params
	bankFunded.DenomMetadata = bankDefault.DenomMetadata
	pool := authtypes.NewModuleAddress(stakingtypes.BondedPoolName).String()
	bankFunded.Supply = sdk.NewCoins()
	for i, balance := range bankFunded.Balances {
		if balance.Address == pool {
			coins := sdk.NewCoins()
			for _, coin := range balance.Coins {
				if coin.Denom == sdk.DefaultBondDenom {
					coin.Denom = stakingDefault.Params.BondDenom
				}
				coins = coins.Add(coin)
			}
			bankFunded.Balances[i].Coins = coins
		}
		bankFunded.Supply = bankFunded.Supply.Add(bankFunded.Balances[i].Coins...)
	}
	genesis[banktypes.ModuleName] = a.AppCodec().MustMarshalJSON(&bankFunded)
	genesis[stakingtypes.ModuleName] = a.AppCodec().MustMarshalJSON(&stakingFunded)
	require.NoError(t, ReconcileNativeGenesisAllocation(a.AppCodec(), genesis))
	return genesis
}

func assertOwnApplicationConstructorAndGenesis(t *testing.T, a *App) {
	basics := NewBasicModuleManager()
	require.Len(t, basics, len(a.ModuleManager.Modules))
	for name := range a.ModuleManager.Modules {
		require.Contains(t, basics, name)
	}
	require.NotNil(t, a.EVMKeeper)
	require.NotNil(t, a.IBCKeeper)
	require.NotNil(t, a.TransferKeeper)
	require.Equal(t, identity.Bech32PrefixAccAddr, sdk.GetConfig().GetBech32AccountAddrPrefix())
	_, err := a.InitChainer(sdk.Context{}, &abci.RequestInitChain{ChainId: "decimal_75-1"})
	require.Error(t, err)
	_, err = a.InitChainer(sdk.Context{}, &abci.RequestInitChain{ChainId: "mintcandy_990001-2"})
	require.Error(t, err, "the same EVM domain with a different genesis revision must also be rejected")
	g := a.DefaultGenesis()
	var bank banktypes.GenesisState
	a.AppCodec().MustUnmarshalJSON(g[banktypes.ModuleName], &bank)
	require.Empty(t, bank.Balances, "operator must supply distribution; do not clone mainnet balances")
	require.True(t, bank.Supply.Empty())
	var evm evmtypes.GenesisState
	a.AppCodec().MustUnmarshalJSON(g[evmtypes.ModuleName], &evm)
	require.Equal(t, identity.BaseDenom, evm.Params.EvmDenom)
}

func TestStaticEncodingAndGenesisWithoutApplication(t *testing.T) {
	before := evmtypes.GetChainConfig()
	for _, evmID := range []uint64{990001, 990002} {
		config := MakeEncodingConfig(evmID)
		for _, url := range []string{"/cosmos.evm.vm.v1.MsgEthereumTx", "/ibc.applications.transfer.v1.MsgTransfer", "/cosmos.bank.v1beta1.MsgSend"} {
			_, err := config.InterfaceRegistry.Resolve(url)
			require.NoError(t, err)
		}
		genesis := DefaultGenesis(config.Codec)
		require.NoError(t, NewBasicModuleManager().ValidateGenesis(config.Codec, config.TxConfig, genesis))
	}
	require.True(t, before == evmtypes.GetChainConfig(), "stateless CLI configuration must never initialize or replace the EVM keeper chain singleton")
}

func TestOwnApplicationRejectsLegacyAndMismatchedDomains(t *testing.T) {
	for _, tc := range []struct {
		chain string
		evm   uint64
	}{{"decimal_75-1", 75}, {"mintcandy_75-1", 75}, {"mintcandy_990001-1", 990002}} {
		t.Run(tc.chain, func(t *testing.T) {
			require.Panics(t, func() {
				NewApp(log.NewNopLogger(), dbm.NewMemDB(), true, options{flags.FlagHome: t.TempDir(), flags.FlagChainID: tc.chain, srvflags.EVMChainID: tc.evm})
			})
		})
	}
}

func TestFreshOwnApplicationSignedNativeEVMTransfer(t *testing.T) {
	const chainID = "mintcandy_990001-1"
	a := testApp(t, chainID, 990001)
	assertOwnApplicationConstructorAndGenesis(t, a)
	assertMountedNativeAssetsBeforeInit(t, a)
	key, err := ethsecp.GenerateKey()
	require.NoError(t, err)
	account := authtypes.NewBaseAccount(key.PubKey().Address().Bytes(), key.PubKey(), 0, 0)
	_, pv := cmttypes.RandValidator(false, 1)
	pub, err := pv.GetPubKey()
	require.NoError(t, err)
	valSet := cmttypes.NewValidatorSet([]*cmttypes.Validator{cmttypes.NewValidator(pub, 1)})
	genesis := fundedTestGenesis(t, a, valSet, []authtypes.GenesisAccount{account}, banktypes.Balance{Address: account.GetAddress().String(), Coins: sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, math.NewInt(1_000_000).Mul(math.NewInt(1_000_000_000_000_000_000))))})
	data, err := json.Marshal(genesis)
	require.NoError(t, err)
	now := time.Unix(1900000000, 0).UTC()
	_, err = a.InitChain(&abci.RequestInitChain{ChainId: chainID, Time: now, AppStateBytes: data, ConsensusParams: simtestutil.DefaultConsensusParams})
	require.NoError(t, err)
	ethKey, err := crypto.ToECDSA(key.Bytes())
	require.NoError(t, err)
	from := crypto.PubkeyToAddress(ethKey.PublicKey)
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	price := big.NewInt(1_000_000_000_000)
	tx := ethtypes.NewTransaction(0, to, big.NewInt(123), 21000, price, nil)
	signed, err := ethtypes.SignTx(tx, ethtypes.NewEIP155Signer(big.NewInt(990001)), ethKey)
	require.NoError(t, err)
	msg := &evmtypes.MsgEthereumTx{}
	require.NoError(t, msg.FromSignedEthereumTx(signed, ethtypes.NewEIP155Signer(big.NewInt(990001))))
	canonical, err := msg.BuildTx(a.TxConfig().NewTxBuilder(), identity.BaseDenom)
	require.NoError(t, err)
	txBytes, err := a.TxConfig().TxEncoder()(canonical)
	require.NoError(t, err)
	var rejected [][]byte
	var rejectedNames []string
	appendForeign := func(raw *ethtypes.Transaction, signer ethtypes.Signer, name string) {
		signed, signErr := ethtypes.SignTx(raw, signer, ethKey)
		require.NoError(t, signErr)
		msg := &evmtypes.MsgEthereumTx{}
		require.NoError(t, msg.FromSignedEthereumTx(signed, signer))
		built, buildErr := msg.BuildTx(a.TxConfig().NewTxBuilder(), identity.BaseDenom)
		require.NoError(t, buildErr)
		encoded, encodeErr := a.TxConfig().TxEncoder()(built)
		require.NoError(t, encodeErr)
		rejected = append(rejected, encoded)
		rejectedNames = append(rejectedNames, name)
	}
	appendForeign(tx, ethtypes.NewEIP155Signer(big.NewInt(75)), "Decimal chain75")
	appendForeign(tx, ethtypes.NewEIP155Signer(big.NewInt(990002)), "different EVM domain990002")
	seen := map[uint64]bool{}
	for value := int64(1000); len(seen) < 2 && value < 1128; value++ {
		unprotected := ethtypes.NewTransaction(0, to, big.NewInt(value), 21000, price, nil)
		signed, signErr := ethtypes.SignTx(unprotected, ethtypes.HomesteadSigner{}, ethKey)
		require.NoError(t, signErr)
		v, _, _ := signed.RawSignatureValues()
		if !seen[v.Uint64()] {
			appendForeign(unprotected, ethtypes.HomesteadSigner{}, fmt.Sprintf("unprotected legacy v%d", v.Uint64()))
			seen[v.Uint64()] = true
		}
	}
	require.Len(t, seen, 2)
	allTransactions := append(rejected, txBytes)
	result, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: now.Add(time.Second), Txs: allTransactions, NextValidatorsHash: valSet.Hash(), ProposerAddress: pub.Address()})
	require.NoError(t, err)
	require.Len(t, result.TxResults, len(allTransactions))
	for i, name := range rejectedNames {
		require.NotZero(t, result.TxResults[i].Code, "actual ante must reject %s", name)
	}
	accepted := result.TxResults[len(rejected)]
	require.Zero(t, accepted.Code, accepted.Log)
	var txData sdk.TxMsgData
	require.NoError(t, a.AppCodec().Unmarshal(accepted.Data, &txData))
	require.Len(t, txData.MsgResponses, 1)
	var evmResponse evmtypes.MsgEthereumTxResponse
	require.NoError(t, evmResponse.Unmarshal(txData.MsgResponses[0].Value))
	require.Empty(t, evmResponse.VmError)
	require.Positive(t, evmResponse.GasUsed)
	ctx := a.NewContextLegacy(false, cmtproto.Header{ChainID: chainID, Height: 1, Time: now.Add(time.Second)})
	require.Equal(t, math.NewInt(123), a.BankKeeper.GetBalance(ctx, sdk.AccAddress(to.Bytes()), identity.BaseDenom).Amount)
	expectedFee := new(big.Int).Mul(price, new(big.Int).SetUint64(evmResponse.GasUsed))
	expectedBalance := math.NewInt(1_000_000).Mul(math.NewInt(1_000_000_000_000_000_000)).Sub(math.NewInt(123)).Sub(math.NewIntFromBigInt(expectedFee))
	require.Equal(t, expectedBalance, a.BankKeeper.GetBalance(ctx, sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount, "real sender debit must equal transferred value plus exact charged legacy gas fee")
	require.Equal(t, uint64(1), a.EVMKeeper.GetNonce(ctx, from), "rejected signing domains must not consume nonce")
	t.Run("bounded_precompile_financial_rollback", func(t *testing.T) {
		checkBoundedFinancialRollback(t, a, ethKey, valSet, now)
	})
	t.Run("proposer_coinbase_address", func(t *testing.T) {
		checkCoinbaseOpcode(t, a, ethKey, valSet, now)
	})
	assertNativeAllocationExport(t, a)
}
