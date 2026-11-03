package systemcontracts_test

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type isolatedBackendEnv struct {
	app             *ownedapp.App
	ctx             func() sdk.Context
	send            func(*ecdsa.PrivateKey, *common.Address, *big.Int, []byte, bool) *evmtypes.MsgEthereumTxResponse
	load            func(string) contracts.Artifact
	deploy          func(contracts.Artifact, ...interface{}) common.Address
	keys            []*ecdsa.PrivateKey
	credit          *big.Int
	events          func() []abci.Event
	setup, govOwner common.Address
}

type isolatedReserve struct {
	Token       common.Address
	Amount      *big.Int
	ReserveType uint8
}
type isolatedPosition struct {
	OriginContract   common.Address
	OriginTokenId    *big.Int
	OriginStandard   uint8
	OriginQuantity   *big.Int
	Reserve          isolatedReserve
	ParentPositionId *big.Int
	PhysicalOwner    common.Address
	Status           uint8
	Refundable       bool
	Burnable         bool
}

func exactABIMethod(t *testing.T, contract abi.ABI, signature string) string {
	t.Helper()
	found := ""
	for key, method := range contract.Methods {
		if method.Sig == signature {
			require.Empty(t, found)
			found = key
		}
	}
	require.NotEmpty(t, found, signature)
	return found
}

func runIsolatedVaultBackend(t *testing.T, e isolatedBackendEnv) {
	t.Helper()
	registry, proxy, bootstrap := e.load("DecimalContractCenter"), e.load("DecimalContractCenterProxy"), e.load("NewNetworkProxyBootstrap")
	factory, vault, origin, controller := e.load("NewNetworkNFTCenter"), e.load("MintCandyNFTPositionVault"), e.load("MintCandyIsolatedDRC1155"), e.load("IsolatedNFTTestController")
	registryImpl, factoryImpl, vaultImpl, originImpl := e.deploy(registry), e.deploy(factory), e.deploy(vault), e.deploy(origin)
	wrapped := e.deploy(e.load("MintCandyWrappedDEL"))
	owner := crypto.PubkeyToAddress(e.keys[0].PublicKey)
	nonce := e.app.EVMKeeper.GetNonce(e.ctx(), owner)
	controllerAddress := crypto.CreateAddress(owner, nonce)
	registryAddress := crypto.CreateAddress(owner, nonce+1)
	factoryAddress := crypto.CreateAddress(owner, nonce+2)
	vaultAddress := crypto.CreateAddress(owner, nonce+3)
	code, err := controller.Deployment(owner, vaultAddress)
	require.NoError(t, err)
	e.send(e.keys[0], nil, new(big.Int), code, true)
	expectedController, err := contracts.ExpectedRuntime("IsolatedNFTTestController", nil, map[string]common.Hash{
		"IsolatedNFTTestController.sol:IsolatedNFTTestController.OWNER": common.BytesToHash(owner.Bytes()),
		"IsolatedNFTTestController.sol:IsolatedNFTTestController.VAULT": common.BytesToHash(vaultAddress.Bytes()),
	})
	require.NoError(t, err)
	require.Equal(t, expectedController, e.app.EVMKeeper.GetCode(e.ctx(), e.app.EVMKeeper.GetCodeHash(e.ctx(), controllerAddress)))
	var config [][]byte
	for _, binding := range []struct {
		label   string
		address common.Address
	}{{"delegation", controllerAddress}, {"delegation-nft", controllerAddress}, {"nft-center", factoryAddress}, {"nft-position-vault", vaultAddress}, {"wdel", wrapped}} {
		call, err := registry.ABI.Pack("setContractAddress", binding.label, binding.address)
		require.NoError(t, err)
		config = append(config, call)
	}
	init, err := bootstrap.ABI.Pack("bootstrapConfigured", e.govOwner, registryImpl, []byte{}, config)
	require.NoError(t, err)
	require.Equal(t, registryAddress, e.deploy(proxy, e.setup, init))
	factoryInit, err := factory.ABI.Pack("initializeFresh", registryAddress)
	require.NoError(t, err)
	childConfig, err := factory.ABI.Pack("upgradeNFT", originImpl, uint8(1), true)
	require.NoError(t, err)
	init, err = bootstrap.ABI.Pack("bootstrapConfigured", e.govOwner, factoryImpl, factoryInit, [][]byte{childConfig})
	require.NoError(t, err)
	require.Equal(t, factoryAddress, e.deploy(proxy, e.setup, init))
	vaultInit, err := vault.ABI.Pack("initializeFresh", registryAddress, factoryAddress)
	require.NoError(t, err)
	init, err = bootstrap.ABI.Pack("bootstrapConfigured", e.govOwner, vaultImpl, vaultInit, [][]byte{})
	require.NoError(t, err)
	require.Equal(t, vaultAddress, e.deploy(proxy, e.setup, init))
	query := func(artifact contracts.Artifact, target common.Address, method string, args ...interface{}) []interface{} {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		ctx, _ := e.ctx().CacheContext()
		ctx = ctx.WithGasMeter(storetypes.NewGasMeter(3_000_000))
		message := core.Message{From: owner, To: &target, Value: new(big.Int), GasLimit: 500000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := e.app.EVMKeeper.ApplyMessage(ctx, statedb.New(ctx, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, false, false, true)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.Empty(t, response.VmError)
		require.LessOrEqual(t, len(response.Ret), 4096)
		used := response.GasUsed
		if response.MaxUsedGas > used {
			used = response.MaxUsedGas
		}
		require.LessOrEqual(t, used, message.GasLimit)
		ctx.GasMeter().ConsumeGas(used, "isolated backend bounded EVM read")
		values, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		return values
	}
	call := func(key int, artifact contracts.Artifact, target common.Address, value *big.Int, success bool, method string, args ...interface{}) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		return e.send(e.keys[key], &target, value, data, success)
	}

	// Test setup invokes the actual EVM from the already configured Gov module
	// authority. Financial assertions below still use signed ordinary user txs.
	govSetup := func(artifact contracts.Artifact, target common.Address, method string) {
		t.Helper()
		data, err := artifact.ABI.Pack(method)
		require.NoError(t, err)
		child, commit := e.ctx().CacheContext()
		child = child.WithGasMeter(storetypes.NewGasMeter(3_000_000))
		message := core.Message{From: e.govOwner, To: &target, Value: new(big.Int), GasLimit: 500000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := e.app.EVMKeeper.ApplyMessage(child, statedb.New(child, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, true, false, true)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.Empty(t, response.VmError)
		commit()
	}
	position := func(id *big.Int) isolatedPosition {
		t.Helper()
		values := query(vault, vaultAddress, "getPosition", id)
		return *abi.ConvertType(values[0], new(isolatedPosition)).(*isolatedPosition)
	}
	integerResult := func(artifact contracts.Artifact, method string, response *evmtypes.MsgEthereumTxResponse) *big.Int {
		t.Helper()
		values, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		return values[0].(*big.Int)
	}
	bankBalance := func(address common.Address) *big.Int {
		return e.app.BankKeeper.GetBalance(e.ctx(), sdk.AccAddress(address.Bytes()), identity.BaseDenom).Amount.BigInt()
	}
	assertVault := func(amount *big.Int) {
		t.Helper()
		require.Zero(t, amount.Cmp(bankBalance(vaultAddress)))
		require.Zero(t, amount.Cmp(e.app.EVMKeeper.GetBalance(e.ctx(), vaultAddress).ToBig()))
		require.Zero(t, amount.Cmp(query(vault, vaultAddress, "declaredReserve", uint8(1), common.Address{})[0].(*big.Int)))
	}
	require.Equal(t, e.govOwner, query(vault, vaultAddress, "owner")[0])
	require.Equal(t, uint64(nfttypes.MaxIsolatedOriginUnits), query(vault, vaultAddress, "maxOriginQuantity")[0].(*big.Int).Uint64())
	call(0, vault, vaultAddress, new(big.Int), false, "pause")
	type nftMeta struct {
		Creator                   common.Address
		Symbol, Name, ContractURI string
		Refundable, Burnable      bool
	}
	create, err := factory.ABI.Pack("createDRC1155", nftMeta{owner, "ISOLATED", "Isolated origin", "ipfs://isolated-origin", true, true})
	require.NoError(t, err)
	created := e.send(e.keys[0], &factoryAddress, new(big.Int), create, true)
	originAddress := integerAddress(t, factory, "createDRC1155", created)
	ten, _ := new(big.Int).SetString("10000000000000000000", 10)
	twenty := new(big.Int).Mul(ten, big.NewInt(2))
	oneDEL := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	primitive, err := contracts.CompiledABI("IMintCandyNativeBurn")
	require.NoError(t, err)
	burnAccount := vaultAddress
	assertSupply := func(before sdkmath.Int, response *evmtypes.MsgEthereumTxResponse, loss *big.Int) {
		t.Helper()
		minted, burned := sdkmath.ZeroInt(), sdkmath.ZeroInt()
		for _, event := range e.events() {
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
		require.Equal(t, before.Add(minted).Sub(burned), e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount)
		found := 0
		for _, log := range response.Logs {
			if common.HexToAddress(log.Address) != common.HexToAddress("0x0000000000000000000000000000000000000900") || len(log.Topics) != 2 || common.HexToHash(log.Topics[0]) != primitive.Events["NativeBurn"].ID {
				continue
			}
			require.Equal(t, burnAccount, common.BytesToAddress(common.HexToHash(log.Topics[1]).Bytes()[12:]))
			values, err := primitive.Events["NativeBurn"].Inputs.NonIndexed().Unpack(log.Data)
			require.NoError(t, err)
			require.Zero(t, loss.Cmp(values[0].(*big.Int)))
			found++
		}
		if loss.Sign() > 0 {
			require.Equal(t, 1, found)
			require.True(t, burned.GTE(sdkmath.NewIntFromBigInt(loss)))
		} else {
			require.Zero(t, found)
		}
	}
	for order := 0; order < 2; order++ {
		t.Run([]string{"A then B", "B then A"}[order], func(t *testing.T) {
			id := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 200), big.NewInt(int64(order+1)))
			call(0, origin, originAddress, twenty, true, "mintByDEL", controllerAddress, id, big.NewInt(2), "ipfs://origin-item")
			beforeNext := query(vault, vaultAddress, "nextPositionId")[0].(*big.Int)
			beforeSupply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
			firstResponse := call(0, controller, controllerAddress, new(big.Int), true, "isolate", originAddress, id, big.NewInt(1))
			assertSupply(beforeSupply, firstResponse, new(big.Int))
			first := integerResult(controller, "isolate", firstResponse)
			beforeSupply = e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
			secondResponse := call(0, controller, controllerAddress, new(big.Int), true, "isolate", originAddress, id, big.NewInt(1))
			assertSupply(beforeSupply, secondResponse, new(big.Int))
			second := integerResult(controller, "isolate", secondResponse)
			require.Zero(t, beforeNext.Cmp(first))
			require.Zero(t, new(big.Int).Add(first, big.NewInt(1)).Cmp(second))
			require.Zero(t, ten.Cmp(position(first).Reserve.Amount))
			require.Zero(t, ten.Cmp(position(second).Reserve.Amount))
			require.Equal(t, originAddress, position(first).OriginContract)
			require.Zero(t, id.Cmp(position(first).OriginTokenId))
			require.Equal(t, uint8(1), position(first).Reserve.ReserveType)
			assertVault(twenty)
			require.Zero(t, query(origin, originAddress, exactABIMethod(t, origin.ABI, "totalSupply(uint256)"), id)[0].(*big.Int).Sign())
			require.Zero(t, ten.Cmp(query(origin, originAddress, exactABIMethod(t, origin.ABI, "rate(uint256)"), id)[0].(*big.Int)), "empty origin retains its uniform unit price")
			beforeSupply = e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
			penalized := call(0, controller, controllerAddress, new(big.Int), true, "penalty", first, uint16(10))
			assertSupply(beforeSupply, penalized, oneDEL)
			require.Zero(t, oneDEL.Cmp(integerResult(controller, "penalty", penalized)))
			nine := new(big.Int).Sub(ten, oneDEL)
			require.Zero(t, nine.Cmp(position(first).Reserve.Amount))
			require.Zero(t, ten.Cmp(position(second).Reserve.Amount), "B must retain exactly10DEL")
			assertVault(new(big.Int).Add(nine, ten))
			userA, userB := crypto.PubkeyToAddress(e.keys[1].PublicKey), crypto.PubkeyToAddress(e.keys[2].PublicKey)
			call(0, controller, controllerAddress, new(big.Int), true, "release", first, userA)
			call(0, controller, controllerAddress, new(big.Int), true, "release", second, userB)
			sequence := []struct {
				id      *big.Int
				key     int
				reserve *big.Int
			}{{first, 1, nine}, {second, 2, ten}}
			if order == 1 {
				sequence[0], sequence[1] = sequence[1], sequence[0]
			}
			remaining := new(big.Int).Add(nine, ten)
			for _, item := range sequence {
				beforeSupply = e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
				e.credit.Set(item.reserve)
				response := call(item.key, vault, vaultAddress, new(big.Int), true, "redeemPosition", item.id, crypto.PubkeyToAddress(e.keys[item.key].PublicKey))
				assertSupply(beforeSupply, response, new(big.Int))
				remaining.Sub(remaining, item.reserve)
				assertVault(remaining)
				require.Equal(t, uint8(2), position(item.id).Status)
			}
			t.Log("isolated positions return A=9DEL and B=10DEL; only A reserve and real bank supply lose1DEL")
		})
	}
	t.Run("ordinary late isolation error restores all sides", func(t *testing.T) {
		id := big.NewInt(77)
		call(0, origin, originAddress, ten, true, "mintByDEL", controllerAddress, id, big.NewInt(1), "ipfs://rollback-origin")
		next := query(vault, vaultAddress, "nextPositionId")[0].(*big.Int)
		beforeSupply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
		response := call(0, controller, controllerAddress, new(big.Int), false, "isolateThenRevert", originAddress, id, big.NewInt(1))
		assertSupply(beforeSupply, response, new(big.Int))
		require.Zero(t, next.Cmp(query(vault, vaultAddress, "nextPositionId")[0].(*big.Int)))
		require.Equal(t, int64(1), query(origin, originAddress, "balanceOf", controllerAddress, id)[0].(*big.Int).Int64())
		require.Zero(t, ten.Cmp(bankBalance(originAddress)))
		assertVault(new(big.Int))
	})
	t.Run("split rounding zero backed kind and full split", func(t *testing.T) {
		id := big.NewInt(78)
		call(0, origin, originAddress, twenty, true, "mintByDEL", controllerAddress, id, big.NewInt(2), "ipfs://split-origin")
		parent := integerResult(controller, "isolate", call(0, controller, controllerAddress, new(big.Int), true, "isolate", originAddress, id, big.NewInt(2)))
		beforeSupply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
		response := call(0, controller, controllerAddress, new(big.Int), true, "penalty", parent, uint16(100))
		assertSupply(beforeSupply, response, twenty)
		require.Equal(t, uint8(1), position(parent).Reserve.ReserveType)
		require.Zero(t, position(parent).Reserve.Amount.Sign())
		call(0, controller, controllerAddress, big.NewInt(1), true, "topup", parent)
		child := integerResult(controller, "split", call(0, controller, controllerAddress, new(big.Int), true, "split", parent, big.NewInt(1), controllerAddress))
		require.Zero(t, position(child).Reserve.Amount.Sign())
		require.Equal(t, uint8(1), position(child).Reserve.ReserveType)
		require.Equal(t, int64(1), position(parent).Reserve.Amount.Int64())
		grandchild := integerResult(controller, "split", call(0, controller, controllerAddress, new(big.Int), true, "split", parent, big.NewInt(1), controllerAddress))
		require.Equal(t, uint8(2), position(parent).Status)
		require.Equal(t, int64(1), position(grandchild).Reserve.Amount.Int64())
		require.Zero(t, parent.Cmp(position(grandchild).ParentPositionId))
		assertVault(big.NewInt(1))
		beforeSupply = e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
		response = call(0, controller, controllerAddress, new(big.Int), true, "penalty", grandchild, uint16(100))
		assertSupply(beforeSupply, response, big.NewInt(1))
		assertVault(new(big.Int))
		for _, receipt := range []*big.Int{child, grandchild} {
			recipient := crypto.PubkeyToAddress(e.keys[3].PublicKey)
			call(0, controller, controllerAddress, new(big.Int), true, "release", receipt, recipient)
			call(3, vault, vaultAddress, new(big.Int), true, "redeemPosition", receipt, recipient)
		}
		call(0, origin, originAddress, new(big.Int), false, "mintByDEL", controllerAddress, id, big.NewInt(1), "ipfs://no-auto-rejoin")
	})
	t.Run("explicit1024 limit and rejection1025", func(t *testing.T) {
		id := big.NewInt(79)
		quantity := big.NewInt(1025)
		reserve := new(big.Int).Mul(oneDEL, quantity)
		call(0, origin, originAddress, reserve, true, "mintByDEL", controllerAddress, id, quantity, "ipfs://large-lineage")
		beforeNext := query(vault, vaultAddress, "nextPositionId")[0].(*big.Int)
		beforeOrigin := bankBalance(originAddress)
		call(0, controller, controllerAddress, new(big.Int), false, "isolate", originAddress, id, quantity)
		require.Zero(t, beforeNext.Cmp(query(vault, vaultAddress, "nextPositionId")[0].(*big.Int)))
		require.Zero(t, beforeOrigin.Cmp(bankBalance(originAddress)))
		require.Equal(t, int64(1025), query(origin, originAddress, "balanceOf", controllerAddress, id)[0].(*big.Int).Int64())
		assertVault(new(big.Int))
		response := call(0, controller, controllerAddress, new(big.Int), true, "isolate", originAddress, id, big.NewInt(1024))
		receipt := integerResult(controller, "isolate", response)
		require.Equal(t, int64(1024), position(receipt).OriginQuantity.Int64())
		require.Equal(t, int64(1), query(origin, originAddress, "balanceOf", controllerAddress, id)[0].(*big.Int).Int64())
		amount := new(big.Int).Mul(oneDEL, big.NewInt(1024))
		assertVault(amount)
		t.Logf("1024-unit isolated backend: receipt response=%d bytes, signed gasUsed=%d maxUsedGas=%d; native lineage admission remains a separate integration gate", len(response.Ret), response.GasUsed, response.MaxUsedGas)
		recipient := crypto.PubkeyToAddress(e.keys[4].PublicKey)
		call(0, controller, controllerAddress, new(big.Int), true, "release", receipt, recipient)
		e.credit.Set(amount)
		call(4, vault, vaultAddress, new(big.Int), true, "redeemPosition", receipt, recipient)
		assertVault(new(big.Int))
	})

	t.Run("factory and vault pause restore isolation after recovery", func(t *testing.T) {
		id := big.NewInt(83)
		call(0, origin, originAddress, ten, true, "mintByDEL", controllerAddress, id, big.NewInt(1), "ipfs://paused-origin")
		next := query(vault, vaultAddress, "nextPositionId")[0].(*big.Int)
		originBefore := bankBalance(originAddress)
		for _, target := range []struct {
			artifact contracts.Artifact
			address  common.Address
		}{{factory, factoryAddress}, {vault, vaultAddress}} {
			govSetup(target.artifact, target.address, "pause")
			require.Equal(t, true, query(target.artifact, target.address, "paused")[0])
			call(0, controller, controllerAddress, new(big.Int), false, "isolate", originAddress, id, big.NewInt(1))
			require.Zero(t, next.Cmp(query(vault, vaultAddress, "nextPositionId")[0].(*big.Int)))
			require.Zero(t, originBefore.Cmp(bankBalance(originAddress)))
			require.Equal(t, int64(1), query(origin, originAddress, "balanceOf", controllerAddress, id)[0].(*big.Int).Int64())
			govSetup(target.artifact, target.address, "unpause")
		}
		receipt := integerResult(controller, "isolate", call(0, controller, controllerAddress, new(big.Int), true, "isolate", originAddress, id, big.NewInt(1)))
		// Position views remain live under its own pause; financial changes fail.
		govSetup(vault, vaultAddress, "pause")
		require.Zero(t, ten.Cmp(position(receipt).Reserve.Amount))
		call(0, controller, controllerAddress, new(big.Int), false, "penalty", receipt, uint16(10))
		require.Zero(t, ten.Cmp(position(receipt).Reserve.Amount))
		govSetup(vault, vaultAddress, "unpause")
		recipient := crypto.PubkeyToAddress(e.keys[3].PublicKey)
		call(0, controller, controllerAddress, new(big.Int), true, "release", receipt, recipient)
		e.credit.Set(ten)
		call(3, vault, vaultAddress, new(big.Int), true, "redeemPosition", receipt, recipient)
		assertVault(new(big.Int))
	})
	t.Run("uniform origin funding stays exact", func(t *testing.T) {
		id := big.NewInt(80)
		originBefore := bankBalance(originAddress)
		bad := new(big.Int).Add(twenty, big.NewInt(1))
		call(0, origin, originAddress, bad, false, "mintByDEL", controllerAddress, id, big.NewInt(2), "ipfs://uniform-origin")
		require.Zero(t, originBefore.Cmp(bankBalance(originAddress)))
		require.Zero(t, query(origin, originAddress, exactABIMethod(t, origin.ABI, "totalSupply(uint256)"), id)[0].(*big.Int).Sign())
		call(0, origin, originAddress, twenty, true, "mintByDEL", controllerAddress, id, big.NewInt(2), "ipfs://uniform-origin")
		call(0, origin, originAddress, big.NewInt(1), false, "addReserveByDEL", id)
		require.Zero(t, ten.Cmp(query(origin, originAddress, exactABIMethod(t, origin.ABI, "rate(uint256)"), id)[0].(*big.Int)))
		call(0, origin, originAddress, big.NewInt(2), true, "addReserveByDEL", id)
		unit := new(big.Int).Add(ten, big.NewInt(1))
		require.Zero(t, unit.Cmp(query(origin, originAddress, exactABIMethod(t, origin.ABI, "rate(uint256)"), id)[0].(*big.Int)))
		call(0, origin, originAddress, ten, false, "mintByDEL", controllerAddress, id, big.NewInt(1), "ipfs://uniform-origin")
		call(0, origin, originAddress, unit, true, "mintByDEL", controllerAddress, id, big.NewInt(1), "ipfs://uniform-origin")
		receipt := integerResult(controller, "isolate", call(0, controller, controllerAddress, new(big.Int), true, "isolate", originAddress, id, big.NewInt(3)))
		amount := new(big.Int).Mul(unit, big.NewInt(3))
		require.Zero(t, amount.Cmp(position(receipt).Reserve.Amount))
		assertVault(amount)
		require.Zero(t, originBefore.Cmp(bankBalance(originAddress)))
		recipient := crypto.PubkeyToAddress(e.keys[3].PublicKey)
		call(0, controller, controllerAddress, new(big.Int), true, "release", receipt, recipient)
		e.credit.Set(amount)
		call(3, vault, vaultAddress, new(big.Int), true, "redeemPosition", receipt, recipient)
		assertVault(new(big.Int))
	})
	for order := 0; order < 2; order++ {
		t.Run([]string{"WDEL A then B", "WDEL B then A"}[order], func(t *testing.T) {
			wrapper := e.load("MintCandyWrappedDEL")
			id := big.NewInt(int64(81 + order))
			call(0, wrapper, wrapped, twenty, true, "deposit")
			call(0, wrapper, wrapped, new(big.Int), true, "approve", originAddress, twenty)
			call(0, origin, originAddress, new(big.Int), true, "mint", controllerAddress, id, big.NewInt(2), "ipfs://wrapped-origin", twenty, wrapped)
			require.Zero(t, twenty.Cmp(query(wrapper, wrapped, "balanceOf", originAddress)[0].(*big.Int)))
			// Native-currency minting cannot change the fixed WDEL currency of this ID.
			call(0, origin, originAddress, ten, false, "mintByDEL", controllerAddress, id, big.NewInt(1), "ipfs://wrapped-origin")
			beforeNext := query(vault, vaultAddress, "nextPositionId")[0].(*big.Int)
			call(0, controller, controllerAddress, new(big.Int), false, "isolateThenRevert", originAddress, id, big.NewInt(1))
			require.Zero(t, beforeNext.Cmp(query(vault, vaultAddress, "nextPositionId")[0].(*big.Int)))
			require.Zero(t, twenty.Cmp(query(wrapper, wrapped, "balanceOf", originAddress)[0].(*big.Int)))
			require.Zero(t, query(wrapper, wrapped, "balanceOf", vaultAddress)[0].(*big.Int).Sign())
			first := integerResult(controller, "isolate", call(0, controller, controllerAddress, new(big.Int), true, "isolate", originAddress, id, big.NewInt(1)))
			second := integerResult(controller, "isolate", call(0, controller, controllerAddress, new(big.Int), true, "isolate", originAddress, id, big.NewInt(1)))
			assertVault(new(big.Int))
			require.Equal(t, uint8(2), position(first).Reserve.ReserveType)
			require.Equal(t, wrapped, position(first).Reserve.Token)
			beforeSupply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
			response := call(0, controller, controllerAddress, new(big.Int), true, "penalty", first, uint16(10))
			burnAccount = wrapped
			assertSupply(beforeSupply, response, oneDEL)
			burnAccount = vaultAddress
			nine := new(big.Int).Sub(ten, oneDEL)
			nineteen := new(big.Int).Add(nine, ten)
			require.Zero(t, nine.Cmp(position(first).Reserve.Amount))
			require.Zero(t, ten.Cmp(position(second).Reserve.Amount))
			for _, balance := range []*big.Int{bankBalance(wrapped), e.app.EVMKeeper.GetBalance(e.ctx(), wrapped).ToBig(), query(wrapper, wrapped, "totalSupply")[0].(*big.Int), query(wrapper, wrapped, "balanceOf", vaultAddress)[0].(*big.Int), query(vault, vaultAddress, "declaredReserve", uint8(2), wrapped)[0].(*big.Int)} {
				require.Zero(t, nineteen.Cmp(balance))
			}
			sequence := []struct {
				id     *big.Int
				key    int
				amount *big.Int
			}{{first, 1, nine}, {second, 2, ten}}
			if order == 1 {
				sequence[0], sequence[1] = sequence[1], sequence[0]
			}
			for _, item := range sequence {
				recipient := crypto.PubkeyToAddress(e.keys[item.key].PublicKey)
				call(0, controller, controllerAddress, new(big.Int), true, "release", item.id, recipient)
				call(item.key, vault, vaultAddress, new(big.Int), true, "redeemPosition", item.id, recipient)
				require.Zero(t, item.amount.Cmp(query(wrapper, wrapped, "balanceOf", recipient)[0].(*big.Int)))
				e.credit.Set(item.amount)
				call(item.key, wrapper, wrapped, new(big.Int), true, "withdraw", item.amount)
			}
			require.Zero(t, bankBalance(wrapped).Sign())
			require.Zero(t, query(wrapper, wrapped, "totalSupply")[0].(*big.Int).Sign())
			require.Zero(t, query(vault, vaultAddress, "declaredReserve", uint8(2), wrapped)[0].(*big.Int).Sign())
		})
	}
	t.Run("surplus never funds a forged position", func(t *testing.T) {
		e.send(e.keys[0], &vaultAddress, big.NewInt(7), nil, true)
		require.Equal(t, int64(7), bankBalance(vaultAddress).Int64())
		require.Zero(t, query(vault, vaultAddress, "declaredReserve", uint8(1), common.Address{})[0].(*big.Int).Sign())
		next := query(vault, vaultAddress, "nextPositionId")[0].(*big.Int)
		call(0, vault, vaultAddress, new(big.Int), false, "mintFromOrigin", big.NewInt(1), big.NewInt(1), owner)
		require.Zero(t, next.Cmp(query(vault, vaultAddress, "nextPositionId")[0].(*big.Int)))
		require.Equal(t, int64(7), bankBalance(vaultAddress).Int64())
	})

	t.Run("original refund and burn policy survives isolation", func(t *testing.T) {
		createPolicy := func(symbol string, refundable, burnable bool) common.Address {
			data, err := factory.ABI.Pack("createDRC1155", nftMeta{owner, symbol, symbol, "ipfs://policy", refundable, burnable})
			require.NoError(t, err)
			return integerAddress(t, factory, "createDRC1155", e.send(e.keys[0], &factoryAddress, new(big.Int), data, true))
		}
		nonrefundable := createPolicy("NOREFUND", false, true)
		call(0, origin, nonrefundable, oneDEL, true, "mintByDEL", controllerAddress, big.NewInt(1), big.NewInt(1), "ipfs://nonrefundable")
		receipt := integerResult(controller, "isolate", call(0, controller, controllerAddress, new(big.Int), true, "isolate", nonrefundable, big.NewInt(1), big.NewInt(1)))
		require.False(t, position(receipt).Refundable)
		require.True(t, position(receipt).Burnable)
		recipient := crypto.PubkeyToAddress(e.keys[3].PublicKey)
		call(0, controller, controllerAddress, new(big.Int), true, "release", receipt, recipient)
		beforeSupply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
		response := call(3, vault, vaultAddress, new(big.Int), true, "redeemPosition", receipt, recipient)
		assertSupply(beforeSupply, response, oneDEL)
		require.Equal(t, int64(7), bankBalance(vaultAddress).Int64(), "unsolicited surplus is not destroyed as collateral")
		require.Zero(t, query(vault, vaultAddress, "declaredReserve", uint8(1), common.Address{})[0].(*big.Int).Sign())
		// Ordinary origin holder burn must destroy ERC20 supply as well as backing.
		wrapper := e.load("MintCandyWrappedDEL")
		call(0, wrapper, wrapped, oneDEL, true, "deposit")
		call(0, wrapper, wrapped, new(big.Int), true, "approve", nonrefundable, oneDEL)
		call(0, origin, nonrefundable, new(big.Int), true, "mint", owner, big.NewInt(2), big.NewInt(1), "ipfs://nonrefundable-wdel", oneDEL, wrapped)
		beforeSupply = e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
		response = call(0, origin, nonrefundable, new(big.Int), true, "burn", big.NewInt(2), big.NewInt(1))
		burnAccount = wrapped
		assertSupply(beforeSupply, response, oneDEL)
		burnAccount = vaultAddress
		require.Zero(t, query(wrapper, wrapped, "totalSupply")[0].(*big.Int).Sign())
		require.Zero(t, bankBalance(wrapped).Sign())
		// Isolating collateral is permitted even if cash redemption is disabled;
		// the receipt retains that policy and remains fully backed and transferable.
		nonburnable := createPolicy("NOBURN", true, false)
		call(0, origin, nonburnable, oneDEL, true, "mintByDEL", owner, big.NewInt(1), big.NewInt(1), "ipfs://nonburnable")
		response = call(0, origin, nonburnable, new(big.Int), true, "isolate", big.NewInt(1), big.NewInt(1), owner)
		receipt = integerResult(origin, "isolate", response)
		require.False(t, position(receipt).Burnable)
		require.True(t, position(receipt).Refundable)
		call(0, vault, vaultAddress, new(big.Int), false, "redeemPosition", receipt, owner)
		require.Equal(t, uint8(1), position(receipt).Status)
		require.Zero(t, oneDEL.Cmp(position(receipt).Reserve.Amount))
		require.Zero(t, new(big.Int).Add(oneDEL, big.NewInt(7)).Cmp(bankBalance(vaultAddress)))
	})

}

func integerAddress(t *testing.T, artifact contracts.Artifact, method string, response *evmtypes.MsgEthereumTxResponse) common.Address {
	t.Helper()
	values, err := artifact.ABI.Unpack(method, response.Ret)
	require.NoError(t, err)
	return values[0].(common.Address)
}
