package nativeburn_test

import (
	"crypto/ecdsa"
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
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/maxwell2010/go-smart-node/network/nativeburn"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type appOptions map[string]interface{}

func (o appOptions) Get(key string) interface{} { return o[key] }

// This test binary owns exactly one production-tag App. It never resets the
// process-global EVM configuration, patches storage or broadcasts transactions.
func TestSignedNativeBurnAndWrappedBacking(t *testing.T) {
	const chainID = "mintcandy_990001-1"
	a := ownedapp.NewApp(log.NewNopLogger(), dbm.NewMemDB(), false, appOptions{flags.FlagHome: t.TempDir(), flags.FlagChainID: chainID, srvflags.EVMChainID: uint64(990001)})
	var bankFault func(sdk.Context) error
	a.SetBeginBlocker(func(blockCtx sdk.Context) (sdk.BeginBlock, error) {
		result, err := a.BeginBlocker(blockCtx)
		if err != nil || bankFault == nil {
			return result, err
		}
		return result, bankFault(blockCtx)
	})
	require.NoError(t, a.LoadLatestVersion())
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
	nativeDebit := new(big.Int)
	gasLimit := uint64(8_000_000)
	rejectHook := false
	var lastEvents []abci.Event
	a.EVMKeeper.SetHooks(&burnHook{enabled: &rejectHook})
	send := func(key *ecdsa.PrivateKey, to *common.Address, value *big.Int, input []byte, success bool) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		from := crypto.PubkeyToAddress(key.PublicKey)
		before := a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount
		nonce := a.EVMKeeper.GetNonce(ctx(), from)
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
		require.Len(t, result.TxResults, 1)
		lastEvents = result.TxResults[0].Events
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
			for _, event := range lastEvents {
				require.NotEqual(t, "mintcandy_native_burn", event.Type, "failed burn must not retain a success event")
			}
		}
		require.Positive(t, response.GasUsed)
		_, err = a.Commit()
		require.NoError(t, err)
		fee := math.NewIntFromBigInt(new(big.Int).Mul(price, new(big.Int).SetUint64(response.GasUsed)))
		debit := fee
		if success {
			debit = debit.Add(math.NewIntFromBigInt(value)).Sub(math.NewIntFromBigInt(nativeCredit)).Add(math.NewIntFromBigInt(nativeDebit))
		}
		require.Equal(t, debit, before.Sub(a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount), "actual signed transaction must charge exactly receipt gas plus committed value")
		require.Equal(t, a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount.BigInt(), a.EVMKeeper.GetBalance(ctx(), from).ToBig())
		require.Equal(t, nonce+1, a.EVMKeeper.GetNonce(ctx(), from))
		nativeCredit.SetUint64(0)
		nativeDebit.SetUint64(0)
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

	wrapper := load("MintCandyWrappedDEL")
	wrapperAddress := deployCode(wrapper.Creation, new(big.Int))
	owner := crypto.PubkeyToAddress(keys[0].PublicKey)
	unit := big.NewInt(1_000_000_000_000_000_000)
	amount := func(n int64) *big.Int { return new(big.Int).Mul(unit, big.NewInt(n)) }
	input := func(method string, args ...interface{}) []byte {
		t.Helper()
		data, err := wrapper.ABI.Pack(method, args...)
		require.NoError(t, err)
		return data
	}
	read := func(method string, args ...interface{}) *big.Int {
		t.Helper()
		child, _ := ctx().CacheContext()
		message := core.Message{From: common.Address{}, To: &wrapperAddress, GasLimit: 200_000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Value: new(big.Int), Data: input(method, args...), SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := a.EVMKeeper.ApplyMessage(child, statedb.New(child, a.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, false, false, false)
		require.NoError(t, err)
		require.Empty(t, response.VmError)
		require.Len(t, response.Ret, 32)
		return new(big.Int).SetBytes(response.Ret)
	}
	reserve := func() *big.Int {
		balance := a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(wrapperAddress.Bytes()), identity.BaseDenom).Amount.BigInt()
		require.Zero(t, balance.Cmp(a.EVMKeeper.GetBalance(ctx(), wrapperAddress).ToBig()), "committed WDEL native backing must agree in SDK bank and EVM")
		return balance
	}
	supply := func() math.Int { return a.BankKeeper.GetSupply(ctx(), identity.BaseDenom).Amount }
	// Deposit changes native ownership, not native total supply.
	beforeSupply := supply()
	send(keys[0], &wrapperAddress, amount(10), input("deposit"), true)
	require.Equal(t, beforeSupply, supply())
	require.Equal(t, amount(10), reserve())
	require.Equal(t, amount(10), read("totalSupply"))
	require.Equal(t, amount(10), read("balanceOf", owner))
	// Both WDEL units and actual SDK bank supply fall by the same amount.
	beforeSupply = supply()
	receipt := send(keys[0], &wrapperAddress, new(big.Int), input("burn", amount(2)), true)
	require.Equal(t, math.NewIntFromBigInt(amount(2)), beforeSupply.Sub(supply()))
	require.Equal(t, amount(8), reserve())
	require.Equal(t, amount(8), read("totalSupply"))
	require.Equal(t, amount(8), read("balanceOf", owner))
	found := false
	for _, log := range receipt.Logs {
		if common.HexToAddress(log.Address) == common.HexToAddress(nativeburn.AddressString) {
			require.Equal(t, crypto.Keccak256Hash([]byte("NativeBurn(address,uint256)")), common.HexToHash(log.Topics[0]))
			require.Equal(t, amount(2), new(big.Int).SetBytes(log.Data))
			found = true
		}
	}
	require.True(t, found, "actual native burn receipt event required")
	found = false
	for _, event := range lastEvents {
		if event.Type != "mintcandy_native_burn" {
			continue
		}
		attributes := make(map[string]string)
		for _, attribute := range event.Attributes {
			attributes[attribute.Key] = attribute.Value
		}
		require.Equal(t, sdk.AccAddress(wrapperAddress.Bytes()).String(), attributes["account"])
		require.Equal(t, identity.BaseDenom, attributes["denom"])
		require.Equal(t, amount(2).String(), attributes["amount"])
		found = true
	}
	require.True(t, found, "actual ABCI native burn event required")
	// Withdraw redeems collateral rather than burning native supply.
	beforeSupply = supply()
	nativeCredit.Set(amount(1))
	send(keys[0], &wrapperAddress, new(big.Int), input("withdraw", amount(1)), true)
	require.Equal(t, beforeSupply, supply())
	require.Equal(t, amount(7), reserve())
	require.Equal(t, amount(7), read("totalSupply"))
	// An ordinary signed EOA can burn only its own native amount, with no address arg.
	nativeABI, err := contracts.CompiledABI("IMintCandyNativeBurn")
	require.NoError(t, err)
	direct, err := nativeABI.Pack("nativeBurn", amount(1))
	require.NoError(t, err)
	_, err = nativeburn.NewPrecompile(nil, nativeABI)
	require.Error(t, err)
	_, err = nativeburn.NewPrecompile(a.BankKeeper, abi.ABI{})
	require.Error(t, err)
	burnAddress := common.HexToAddress(nativeburn.AddressString)
	beforeSupply = supply()
	nativeDebit.Set(amount(1))
	directReceipt := send(keys[0], &burnAddress, new(big.Int), direct, true)
	require.Len(t, directReceipt.Ret, 32)
	require.Equal(t, int64(1), new(big.Int).SetBytes(directReceipt.Ret).Int64())
	require.Equal(t, math.NewIntFromBigInt(amount(1)), beforeSupply.Sub(supply()))
	zeroBurn, err := nativeABI.Pack("nativeBurn", new(big.Int))
	require.NoError(t, err)
	beforeSupply = supply()
	send(keys[0], &burnAddress, new(big.Int), zeroBurn, true)
	require.Equal(t, beforeSupply, supply())
	tooMuch, err := nativeABI.Pack("nativeBurn", amount(1_000_000))
	require.NoError(t, err)
	send(keys[0], &burnAddress, new(big.Int), tooMuch, false)
	require.Equal(t, beforeSupply, supply())
	// A different holder cannot destroy the owner's WDEL balance.
	send(keys[1], &wrapperAddress, new(big.Int), input("burn", amount(1)), false)
	require.Equal(t, beforeSupply, supply())
	require.Equal(t, amount(7), read("balanceOf", owner))
	// Any post-transaction hook failure restores ERC20 and native burn together.
	beforeSupply = supply()
	rejectHook = true
	send(keys[0], &wrapperAddress, new(big.Int), input("burn", amount(1)), false)
	rejectHook = false
	require.Equal(t, beforeSupply, supply())
	require.Equal(t, amount(7), reserve())
	require.Equal(t, amount(7), read("totalSupply"))
	require.Equal(t, amount(7), read("balanceOf", owner))
	// EVM gas failure restores storage/bank changes while charging exact gas only.
	beforeSupply = supply()
	gasLimit = 40_000
	send(keys[0], &wrapperAddress, new(big.Int), input("burn", amount(1)), false)
	gasLimit = 8_000_000
	require.Equal(t, beforeSupply, supply())
	require.Equal(t, amount(7), reserve())
	require.Equal(t, amount(7), read("totalSupply"))
	// Value-bearing calls and malformed/foreign selector requests cannot burn.
	beforeSupply = supply()
	send(keys[0], &burnAddress, amount(1), direct, false)
	send(keys[0], &burnAddress, new(big.Int), append(append([]byte(nil), direct...), 0), false)
	foreign := append(crypto.Keccak256([]byte("nativeBurn(address,uint256)"))[:4], make([]byte, 64)...)
	send(keys[0], &burnAddress, new(big.Int), foreign, false)
	require.Equal(t, beforeSupply, supply())
	// This genuine deployed receiver tries to spend the wrapper's remaining
	// backing by DELEGATECALL during withdraw. The fork marks that frame readonly.
	target := load("NativeBurnTestTarget")
	creation, err := target.Deployment(wrapperAddress)
	require.NoError(t, err)
	targetAddress := deployCode(creation, new(big.Int))
	targetInput := func(method string, args ...interface{}) []byte {
		t.Helper()
		data, err := target.ABI.Pack(method, args...)
		require.NoError(t, err)
		return data
	}
	// A successful inner burn followed by an outer revert must restore native
	// supply, wrapper backing, ERC20 storage and the receipt logs together.
	send(keys[0], &targetAddress, amount(2), targetInput("fund"), true)
	beforeSupply = supply()
	send(keys[0], &targetAddress, new(big.Int), targetInput("burnThenRevert", amount(1)), false)
	require.Equal(t, beforeSupply, supply())
	require.Equal(t, amount(9), reserve())
	require.Equal(t, amount(9), read("totalSupply"))
	require.Equal(t, amount(2), read("balanceOf", targetAddress))
	// Restore this fixture's collateral through the ordinary public burn path.
	send(keys[0], &targetAddress, new(big.Int), targetInput("burnWrapped", amount(2)), true)
	require.Equal(t, amount(7), reserve())
	require.Equal(t, amount(7), read("totalSupply"))
	// Deposit and burn in one ordinary transaction exercises the framework's
	// flush of pending EVM value transfers before reading the SDK bank balance.
	beforeSupply = supply()
	send(keys[0], &targetAddress, amount(3), targetInput("fundAndBurn", amount(3)), true)
	require.Equal(t, math.NewIntFromBigInt(amount(3)), beforeSupply.Sub(supply()))
	require.Equal(t, amount(7), reserve())
	require.Equal(t, amount(7), read("totalSupply"))
	require.Zero(t, read("balanceOf", targetAddress).Sign())
	// An inactive precompile cannot silently turn a WDEL burn into locked DEL.
	activeParams := a.EVMKeeper.GetParams(ctx())
	params := activeParams
	var active []string
	for _, address := range params.ActiveStaticPrecompiles {
		if address != nativeburn.AddressString {
			active = append(active, address)
		}
	}
	params.ActiveStaticPrecompiles = active
	bankFault = func(blockCtx sdk.Context) error { return a.EVMKeeper.SetParams(blockCtx, params) }
	beforeSupply = supply()
	send(keys[0], &wrapperAddress, new(big.Int), input("burn", amount(1)), false)
	require.Equal(t, beforeSupply, supply())
	require.Equal(t, amount(7), reserve())
	require.Equal(t, amount(7), read("totalSupply"))
	bankFault = func(blockCtx sdk.Context) error { return a.EVMKeeper.SetParams(blockCtx, activeParams) }
	send(keys[0], &burnAddress, new(big.Int), zeroBurn, true)
	bankFault = nil
	// Deliberately remove backing in a test-only BeginBlock fault. The next
	// ordinary signed burn must fail without keeping its earlier ERC20 _burn.
	// There is no runtime transaction or exported method for this fault injection.
	beforeSupply = supply()
	sink := accounts[2].GetAddress()
	sinkBefore := a.BankKeeper.GetBalance(ctx(), sink, identity.BaseDenom).Amount
	bankFault = func(blockCtx sdk.Context) error {
		return a.BankKeeper.SendCoins(blockCtx, sdk.AccAddress(wrapperAddress.Bytes()), sink, sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, math.NewIntFromBigInt(amount(7)))))
	}
	send(keys[0], &wrapperAddress, new(big.Int), input("burn", amount(1)), false)
	bankFault = nil
	require.Equal(t, beforeSupply, supply())
	require.Zero(t, reserve().Sign())
	require.Equal(t, amount(7), read("totalSupply"))
	require.Equal(t, amount(7), read("balanceOf", owner))
	require.Equal(t, math.NewIntFromBigInt(amount(7)), a.BankKeeper.GetBalance(ctx(), sink, identity.BaseDenom).Amount.Sub(sinkBefore))
}

type burnHook struct{ enabled *bool }

func (h *burnHook) PostTxProcessing(_ sdk.Context, _ common.Address, _ core.Message, _ *ethtypes.Receipt) error {
	if *h.enabled {
		return fmt.Errorf("fixture rejects burn event")
	}
	return nil
}
