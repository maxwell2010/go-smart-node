package systemcontracts_test

import (
	"math/big"
	"reflect"
	"testing"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// Extend the existing signed, EOA-owned Gateway fixture with ordinary token
// admission controls. This does not establish production Gov/manifest admission.
func runExactTokenAddressBindings(t *testing.T, e isolatedBackendEnv, registryAddress, gatewayAddress, nftCenterAddress, originAddress, wrapped, validator common.Address, gateway contracts.Artifact) {
	t.Helper()
	registry, proxy, bootstrap := e.load("DecimalContractCenter"), e.load("DecimalContractCenterProxy"), e.load("NewNetworkProxyBootstrap")
	center, coin, freeCoin := e.load("NewNetworkTokenCenter"), e.load("DRC20"), e.load("DRC20Reserveless")
	nftCenter, origin, vault, wrapper := e.load("NewNetworkNFTCenter"), e.load("MintCandyIsolatedDRC1155"), e.load("MintCandyNFTPositionVault"), e.load("MintCandyWrappedDEL")
	from := crypto.PubkeyToAddress(e.keys[0].PublicKey)
	owner := crypto.PubkeyToAddress(e.keys[1].PublicKey)
	amount := func(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), big.NewInt(1_000_000_000_000_000_000)) }
	call := func(key int, artifact contracts.Artifact, target common.Address, value *big.Int, success bool, method string, args ...interface{}) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		return e.send(e.keys[key], &target, value, data, success)
	}
	query := func(artifact contracts.Artifact, target common.Address, method string, args ...interface{}) []interface{} {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		ctx, _ := e.ctx().CacheContext()
		ctx = ctx.WithGasMeter(storetypes.NewGasMeter(3_000_000))
		message := core.Message{From: from, To: &target, Value: new(big.Int), GasLimit: 500000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := e.app.EVMKeeper.ApplyMessage(ctx, statedb.New(ctx, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, false, false, true)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.Empty(t, response.VmError)
		used := response.GasUsed
		if response.MaxUsedGas > used {
			used = response.MaxUsedGas
		}
		require.LessOrEqual(t, used, message.GasLimit)
		ctx.GasMeter().ConsumeGas(used, "bounded token address binding read")
		values, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		return values
	}
	integer := func(artifact contracts.Artifact, target common.Address, method string, args ...interface{}) *big.Int {
		return query(artifact, target, method, args...)[0].(*big.Int)
	}
	centerImpl, coinImpl, freeImpl := e.deploy(center), e.deploy(coin), e.deploy(freeCoin)
	centerInit, err := center.ABI.Pack("initializeFresh", registryAddress)
	require.NoError(t, err)
	reserveImpl, err := center.ABI.Pack("upgradeToken", coinImpl)
	require.NoError(t, err)
	reservelessImpl, err := center.ABI.Pack("upgradeTokenReserveless", freeImpl)
	require.NoError(t, err)
	init, err := bootstrap.ABI.Pack("bootstrapConfigured", owner, centerImpl, centerInit, [][]byte{reserveImpl, reservelessImpl})
	require.NoError(t, err)
	centerAddress := e.deploy(proxy, e.setup, init)
	call(1, registry, registryAddress, new(big.Int), true, "setContractAddress", "token-center", centerAddress)
	type coinMeta struct {
		InitialMint, MinTotalSupply, MaxTotalSupply *big.Int
		Creator                                     common.Address
		Crr                                         uint8
		Identity, Symbol, Name                      string
	}
	const symbol = "addrbindx"
	commission := integer(center, centerAddress, "getCommissionSymbol", symbol)
	call(0, center, centerAddress, new(big.Int).Add(amount(10), commission), true, "createToken", coinMeta{amount(10), amount(5), amount(100), from, 100, "exact-address-control", symbol, "Registered binding control"})
	currency := query(center, centerAddress, "tokens", symbol)[0].(common.Address)

	// A genuine pinned proxy may have the same symbol and actual immutable
	// FACTORY/FAMILY while being absent from the authoritative factory mapping.
	readable := e.load("NewNetworkTokenReadableProxy")
	duplicate := crypto.CreateAddress(from, e.app.EVMKeeper.GetNonce(e.ctx(), from))
	code, err := readable.Deployment(centerAddress, []byte{}, centerAddress, uint8(0))
	require.NoError(t, err)
	e.send(e.keys[0], nil, new(big.Int), code, true)
	expected, err := contracts.ExpectedRuntime("NewNetworkTokenReadableProxy", nil, map[string]common.Hash{
		"owned/DecimalTokenCenter.sol:BeaconProxy._beacon":                   common.BytesToHash(centerAddress.Bytes()),
		"NewNetworkReadableProxies.sol:NewNetworkTokenReadableProxy.FACTORY": common.BytesToHash(centerAddress.Bytes()),
		"NewNetworkReadableProxies.sol:NewNetworkTokenReadableProxy.FAMILY":  {},
	})
	require.NoError(t, err)
	require.Equal(t, expected, e.app.EVMKeeper.GetCode(e.ctx(), e.app.EVMKeeper.GetCodeHash(e.ctx(), duplicate)))
	call(0, coin, duplicate, amount(10), true, "initialize", "Separate unregistered coin", symbol, from, uint8(100), amount(10), amount(5), amount(100), "same-symbol-address-control")
	require.NotEqual(t, currency, duplicate)
	require.Equal(t, centerAddress, query(readable, duplicate, "FACTORY")[0])
	require.Equal(t, uint8(0), query(readable, duplicate, "FAMILY")[0])
	require.True(t, query(center, centerAddress, "isTokenExists", duplicate)[0].(bool))
	require.Equal(t, currency, query(center, centerAddress, "tokens", symbol)[0])
	require.Equal(t, symbol, query(coin, duplicate, "symbol")[0])

	assertInvalid := func(artifact contracts.Artifact, response *evmtypes.MsgEthereumTxResponse) {
		t.Helper()
		signature := artifact.ABI.Errors["InvalidToken"].ID
		require.NotEqual(t, common.Hash{}, signature)
		require.Equal(t, signature.Bytes()[:4], response.Ret)
		require.Empty(t, response.Logs)
	}
	balance := func(token, holder common.Address) *big.Int { return integer(coin, token, "balanceOf", holder) }
	t.Run("Gateway rejects same symbol unregistered real proxy", func(t *testing.T) {
		call(0, coin, duplicate, new(big.Int), true, "approve", gatewayAddress, amount(1))
		supply, wallet, escrow := integer(coin, duplicate, "totalSupply"), balance(duplicate, from), balance(duplicate, gatewayAddress)
		allowance := integer(coin, duplicate, "allowance", from, gatewayAddress)
		response := call(0, gateway, gatewayAddress, new(big.Int), false, "delegate", validator, duplicate, amount(1))
		assertInvalid(gateway, response)
		require.Equal(t, supply, integer(coin, duplicate, "totalSupply"))
		require.Equal(t, wallet, balance(duplicate, from))
		require.Equal(t, escrow, balance(duplicate, gatewayAddress))
		require.Equal(t, allowance, integer(coin, duplicate, "allowance", from, gatewayAddress))
	})
	t.Run("fresh NFT factory rejects same symbol before funding", func(t *testing.T) {
		call(0, nftCenter, nftCenterAddress, new(big.Int), true, "checkToken", currency)
		response := call(0, nftCenter, nftCenterAddress, new(big.Int), false, "checkToken", duplicate)
		assertInvalid(nftCenter, response)
		id := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 240), big.NewInt(81))
		call(0, coin, duplicate, new(big.Int), true, "approve", originAddress, amount(1))
		wallet, backing := balance(duplicate, from), balance(duplicate, originAddress)
		allowance := integer(coin, duplicate, "allowance", from, originAddress)
		response = call(0, origin, originAddress, new(big.Int), false, "mint", from, id, big.NewInt(1), "ipfs://rejected-binding", amount(1), duplicate)
		assertInvalid(nftCenter, response)
		require.Zero(t, integer(origin, originAddress, exactABIMethod(t, origin.ABI, "totalSupply(uint256)"), id).Sign())
		require.Equal(t, wallet, balance(duplicate, from))
		require.Equal(t, backing, balance(duplicate, originAddress))
		require.Equal(t, allowance, integer(coin, duplicate, "allowance", from, originAddress))
	})
	t.Run("existing DEL WDEL and registered custom routes stay available", func(t *testing.T) {
		call(0, gateway, gatewayAddress, amount(1), true, "delegateDEL", validator)
		call(0, wrapper, wrapped, amount(2), true, "deposit")
		call(0, wrapper, wrapped, new(big.Int), true, "approve", gatewayAddress, amount(1))
		call(0, gateway, gatewayAddress, new(big.Int), true, "delegate", validator, wrapped, amount(1))
		call(0, coin, currency, new(big.Int), true, "approve", gatewayAddress, amount(1))
		call(0, gateway, gatewayAddress, new(big.Int), true, "delegate", validator, currency, amount(1))
		require.Equal(t, amount(1), balance(currency, gatewayAddress))
		require.Equal(t, amount(2), integer(wrapper, wrapped, "balanceOf", gatewayAddress))
		vaultAddress := query(registry, registryAddress, "getContractAddress", "nft-position-vault")[0].(common.Address)
		for i, token := range []common.Address{currency, wrapped} {
			id := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 240), big.NewInt(int64(91+i)))
			call(0, coin, token, new(big.Int), true, "approve", originAddress, amount(1))
			call(0, origin, originAddress, new(big.Int), true, "mint", from, id, big.NewInt(1), "ipfs://registered-backing", amount(1), token)
			receiptID := new(big.Int).Set(integer(vault, vaultAddress, "nextPositionId"))
			call(0, gateway, gatewayAddress, new(big.Int), true, "delegateNFT1155", validator, originAddress, id, big.NewInt(1))
			stake := reflect.ValueOf(query(gateway, gatewayAddress, "getNFTStake", vaultAddress, receiptID, from, validator, new(big.Int))[0])
			require.Equal(t, amount(1), stake.FieldByName("ReserveAmount").Interface(), "library companion admission preserves registered custom and exact WDEL")
		}
	})
	t.Log("exact TokenCenter address binding: genuine registered collateral remains supported; same-symbol genuine unregistered proxy rejected atomically")
}
