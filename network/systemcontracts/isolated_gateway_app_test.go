package systemcontracts_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"reflect"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// Actual Unified Gateway, not the vault's small test controller. Each private
// fixture uses a signed, explicit owner; no production chain is contacted.
func runCanonicalIsolatedGateway(t *testing.T, e isolatedBackendEnv, rejectBurn func(bool)) {
	t.Helper()
	defer rejectBurn(false)
	registry, proxy, bootstrap := e.load("DecimalContractCenter"), e.load("DecimalContractCenterProxy"), e.load("NewNetworkProxyBootstrap")
	factory, vault, origin, master := e.load("NewNetworkNFTCenter"), e.load("MintCandyNFTPositionVault"), e.load("MintCandyIsolatedDRC1155"), e.load("NewNetworkMasterValidator")
	library := e.load("MintCandyDelegationNFTLib")
	libraryOwner := crypto.PubkeyToAddress(e.keys[0].PublicKey)
	libAddress := crypto.CreateAddress(libraryOwner, e.app.EVMKeeper.GetNonce(e.ctx(), libraryOwner))
	libraryCode, err := library.Deployment()
	require.NoError(t, err)
	e.send(e.keys[0], nil, new(big.Int), libraryCode, true)
	expectedLibrary, err := contracts.ExpectedRuntime("MintCandyDelegationNFTLib", nil, map[string]common.Hash{"owned/DecimalDelegation.sol:DelegationNFTLib.library_deploy_address": common.BytesToHash(libAddress.Bytes())})
	require.NoError(t, err)
	require.Equal(t, expectedLibrary, e.app.EVMKeeper.GetCode(e.ctx(), e.app.EVMKeeper.GetCodeHash(e.ctx(), libAddress)))
	gateway, err := contracts.LoadLinked("MintCandyIsolatedDelegation", map[string]common.Address{"owned/DecimalDelegation.sol:DelegationNFTLib": libAddress})
	require.NoError(t, err)
	registryImpl, gatewayImpl, masterImpl := e.deploy(registry), e.deploy(gateway), e.deploy(master)
	factoryImpl, vaultImpl, originImpl := e.deploy(factory), e.deploy(vault), e.deploy(origin)
	wrapped := e.deploy(e.load("MintCandyWrappedDEL"))
	from, owner := crypto.PubkeyToAddress(e.keys[0].PublicKey), crypto.PubkeyToAddress(e.keys[1].PublicKey)
	nonce := e.app.EVMKeeper.GetNonce(e.ctx(), from)
	registryAddress := crypto.CreateAddress(from, nonce)
	roles := map[string]common.Address{}
	for i, role := range []string{"delegation", "master-validator", "nft-center", "nft-position-vault"} {
		roles[role] = crypto.CreateAddress(from, nonce+1+uint64(i))
	}
	var config [][]byte
	for _, binding := range []struct {
		role   string
		target common.Address
	}{{"delegation", roles["delegation"]}, {"delegation-nft", roles["delegation"]}, {"master-validator", roles["master-validator"]}, {"nft-center", roles["nft-center"]}, {"nft-position-vault", roles["nft-position-vault"]}, {"wdel", wrapped}} {
		data, err := registry.ABI.Pack("setContractAddress", binding.role, binding.target)
		require.NoError(t, err)
		config = append(config, data)
	}
	init, err := bootstrap.ABI.Pack("bootstrapConfigured", owner, registryImpl, []byte{}, config)
	require.NoError(t, err)
	require.Equal(t, registryAddress, e.deploy(proxy, e.setup, init))
	gatewayConfig, err := gateway.ABI.Pack("setContractCenter", registryAddress)
	require.NoError(t, err)
	init, err = bootstrap.ABI.Pack("bootstrapConfigured", owner, gatewayImpl, []byte{}, [][]byte{gatewayConfig})
	require.NoError(t, err)
	require.Equal(t, roles["delegation"], e.deploy(proxy, e.setup, init))
	masterInit, err := master.ABI.Pack("initializeFresh", registryAddress)
	require.NoError(t, err)
	init, err = bootstrap.ABI.Pack("bootstrapConfigured", owner, masterImpl, masterInit, [][]byte{})
	require.NoError(t, err)
	require.Equal(t, roles["master-validator"], e.deploy(proxy, e.setup, init))
	factoryInit, err := factory.ABI.Pack("initializeFresh", registryAddress)
	require.NoError(t, err)
	factoryConfig, err := factory.ABI.Pack("upgradeNFT", originImpl, uint8(1), true)
	require.NoError(t, err)
	init, err = bootstrap.ABI.Pack("bootstrapConfigured", owner, factoryImpl, factoryInit, [][]byte{factoryConfig})
	require.NoError(t, err)
	require.Equal(t, roles["nft-center"], e.deploy(proxy, e.setup, init))
	vaultInit, err := vault.ABI.Pack("initializeFresh", registryAddress, roles["nft-center"])
	require.NoError(t, err)
	init, err = bootstrap.ABI.Pack("bootstrapConfigured", owner, vaultImpl, vaultInit, [][]byte{})
	require.NoError(t, err)
	require.Equal(t, roles["nft-position-vault"], e.deploy(proxy, e.setup, init))
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
		ctx.GasMeter().ConsumeGas(used, "isolated gateway bounded read")
		values, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		return values
	}
	gaddr, vaddr, maddr := roles["delegation"], roles["nft-position-vault"], roles["master-validator"]
	position := func(id *big.Int) isolatedPosition {
		return *abi.ConvertType(query(vault, vaddr, "getPosition", id)[0], new(isolatedPosition)).(*isolatedPosition)
	}
	nftStake := func(id *big.Int, user int, validator common.Address) reflect.Value {
		return reflect.ValueOf(query(gateway, gaddr, "getNFTStake", vaddr, id, crypto.PubkeyToAddress(e.keys[user].PublicKey), validator, new(big.Int))[0])
	}
	amount := func(v int64) *big.Int { return new(big.Int).Mul(big.NewInt(v), big.NewInt(1_000_000_000_000_000_000)) }
	bank := func(target common.Address) *big.Int {
		return e.app.BankKeeper.GetBalance(e.ctx(), sdk.AccAddress(target.Bytes()), identity.BaseDenom).Amount.BigInt()
	}
	call(1, gateway, gaddr, new(big.Int), true, "setFreezeTime", new(big.Int), new(big.Int))
	type collection struct {
		Creator                   common.Address
		Symbol, Name, ContractURI string
		Refundable, Burnable      bool
	}
	created := call(0, factory, roles["nft-center"], new(big.Int), true, "createDRC1155", collection{from, "GWISO", "Gateway isolated origin", "ipfs://gateway-origin", true, true})
	originAddress := integerAddress(t, factory, "createDRC1155", created)
	call(0, origin, originAddress, new(big.Int), true, "setApprovalForAll", gaddr, true)
	call(2, origin, originAddress, new(big.Int), true, "setApprovalForAll", gaddr, true)
	primitive, err := contracts.CompiledABI("IMintCandyNativeBurn")
	require.NoError(t, err)

	assertSupplyDelta := func(before sdkmath.Int) sdkmath.Int {
		t.Helper()
		minted, burned := sdkmath.ZeroInt(), sdkmath.ZeroInt()
		for _, event := range e.events() {
			if event.Type != banktypes.EventTypeCoinMint && event.Type != banktypes.EventTypeCoinBurn {
				continue
			}
			for _, attr := range event.Attributes {
				if attr.Key != sdk.AttributeKeyAmount {
					continue
				}
				coins, err := sdk.ParseCoinsNormalized(attr.Value)
				require.NoError(t, err)
				if event.Type == banktypes.EventTypeCoinMint {
					minted = minted.Add(coins.AmountOf(identity.BaseDenom))
				} else {
					burned = burned.Add(coins.AmountOf(identity.BaseDenom))
				}
			}
		}
		require.Equal(t, before.Add(minted).Sub(burned), e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount, "actual SDK supply accounts separately for financial loss and block/fee mint-burn events")
		return burned
	}

	var addressBindingValidator common.Address
	for order := 0; order < 2; order++ {
		t.Run([]string{"A exits first", "B exits first"}[order], func(t *testing.T) {
			validators := []common.Address{common.BytesToAddress(crypto.Keccak256([]byte{byte(0xA0 + order)}, []byte("isolated-validator-A"))[12:]), common.BytesToAddress(crypto.Keccak256([]byte{byte(0xB0 + order)}, []byte("isolated-validator-B"))[12:])}
			addressBindingValidator = validators[1]
			type validatorMeta struct {
				Validator common.Address
				Meta      string
			}
			var meta []validatorMeta
			for i, v := range validators {
				operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(v.Bytes())
				require.NoError(t, err)
				payload, err := json.Marshal(map[string]interface{}{"operator_address": operator, "reward_address": v.Hex(), "consensus_pubkey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{byte(0x91 + order*2 + i)}, 32)), "description": map[string]string{"moniker": "isolated-gateway"}, "commission": "10"})
				require.NoError(t, err)
				meta = append(meta, validatorMeta{v, string(payload)})
			}
			call(1, master, maddr, new(big.Int), true, "addValidators", meta)
			originID := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 200), big.NewInt(int64(order+31)))
			call(0, origin, originAddress, amount(20), true, "mintByDEL", from, originID, big.NewInt(2), "ipfs://gateway-item")
			userB := crypto.PubkeyToAddress(e.keys[2].PublicKey)
			call(0, origin, originAddress, new(big.Int), true, "safeTransferFrom", from, userB, originID, big.NewInt(1), []byte{})
			ids := make([]*big.Int, 2)
			for i, user := range []int{0, 2} {
				ids[i] = new(big.Int).Set(query(vault, vaddr, "nextPositionId")[0].(*big.Int))
				receipt := call(user, gateway, gaddr, new(big.Int), true, "delegateNFT1155", validators[i], originAddress, originID, big.NewInt(1))
				marker, deposit, burn, mint := -1, -1, -1, -1
				for n, log := range receipt.Logs {
					if len(log.Topics) == 0 {
						continue
					}
					topic := common.HexToHash(log.Topics[0])
					target := common.HexToAddress(log.Address)
					if target == gaddr && topic == gateway.ABI.Events["NFTIsolationStarted"].ID {
						require.Equal(t, -1, marker)
						marker = n
						require.Equal(t, common.BigToHash(ids[i]), common.HexToHash(log.Topics[1]))
						require.Equal(t, crypto.PubkeyToAddress(e.keys[user].PublicKey), common.BytesToAddress(common.HexToHash(log.Topics[2]).Bytes()[12:]))
						require.Equal(t, originAddress, common.BytesToAddress(common.HexToHash(log.Topics[3]).Bytes()[12:]))
						values, err := gateway.ABI.Events["NFTIsolationStarted"].Inputs.NonIndexed().Unpack(log.Data)
						require.NoError(t, err)
						require.Equal(t, originID, values[0].(*big.Int))
						require.Equal(t, big.NewInt(1), values[1].(*big.Int))
					}
					if target == originAddress && topic == origin.ABI.Events["TransferSingle"].ID {
						if common.BytesToAddress(common.HexToHash(log.Topics[3]).Bytes()[12:]) == gaddr {
							deposit = n
						}
						if common.BytesToAddress(common.HexToHash(log.Topics[3]).Bytes()[12:]) == (common.Address{}) {
							burn = n
						}
					}
					if target == vaddr && topic == vault.ABI.Events["Transfer"].ID {
						mint = n
					}
				}
				require.GreaterOrEqual(t, marker, 0)
				require.Greater(t, deposit, marker)
				require.Greater(t, burn, deposit)
				require.Greater(t, mint, burn)
				p := position(ids[i])
				require.Equal(t, gaddr, p.PhysicalOwner)
				require.Equal(t, originAddress, p.OriginContract)
				require.Equal(t, originID, p.OriginTokenId)
				require.Equal(t, big.NewInt(1), p.OriginQuantity)
				require.Equal(t, amount(10), p.Reserve.Amount)
				s := nftStake(ids[i], user, validators[i])
				require.Equal(t, big.NewInt(1), s.FieldByName("Amount").Interface())
				require.Equal(t, uint8(2), s.FieldByName("NftType").Interface())
				require.Equal(t, vaddr, s.FieldByName("NftContract").Interface())
				require.True(t, s.FieldByName("IsActive").Bool())
			}
			require.Equal(t, amount(20), bank(vaddr))
			require.Zero(t, bank(originAddress).Sign())
			call(1, master, maddr, new(big.Int), true, "addPenalty", validators[0], uint16(10))
			stakeID := query(gateway, gaddr, "getNFTStakeId", vaddr, ids[0], from, validators[0], new(big.Int))[0].([32]byte)
			// A normal receipt-validation error after the actual contract burn must
			// restore both native bank collateral and EVM/companion position state.
			beforeFailedSupply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
			rejectBurn(true)
			call(1, gateway, gaddr, new(big.Int), false, "applyPenaltiesToNFTStakes", [][32]byte{stakeID})
			rejectBurn(false)
			assertSupplyDelta(beforeFailedSupply)
			require.Equal(t, amount(20), bank(vaddr))
			require.Equal(t, amount(10), position(ids[0]).Reserve.Amount)
			require.Equal(t, amount(10), nftStake(ids[0], 0, validators[0]).FieldByName("ReserveAmount").Interface())
			beforeSupply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
			settled := call(1, gateway, gaddr, new(big.Int), true, "applyPenaltiesToNFTStakes", [][32]byte{stakeID})
			burned := assertSupplyDelta(beforeSupply)
			require.True(t, burned.GTE(sdkmath.NewIntFromBigInt(amount(1))), "genuine SDK bank supply burn covers the realized position loss")
			burns := 0
			for _, log := range settled.Logs {
				if common.HexToAddress(log.Address) == common.HexToAddress("0x900") && len(log.Topics) > 0 && common.HexToHash(log.Topics[0]) == primitive.Events["NativeBurn"].ID {
					values, err := primitive.Events["NativeBurn"].Inputs.NonIndexed().Unpack(log.Data)
					require.NoError(t, err)
					require.Equal(t, amount(1), values[0].(*big.Int))
					burns++
				}
			}
			require.Equal(t, 1, burns)
			require.Equal(t, amount(19), bank(vaddr))
			require.Equal(t, amount(9), position(ids[0]).Reserve.Amount)
			require.Equal(t, amount(10), position(ids[1]).Reserve.Amount)
			require.Equal(t, amount(9), nftStake(ids[0], 0, validators[0]).FieldByName("ReserveAmount").Interface())
			require.Equal(t, amount(10), nftStake(ids[1], 2, validators[1]).FieldByName("ReserveAmount").Interface())
			sequence := []int{0, 1}
			if order == 1 {
				sequence = []int{1, 0}
			}
			for _, i := range sequence {
				user := []int{0, 2}[i]
				beneficiary := crypto.PubkeyToAddress(e.keys[user].PublicKey)
				response := call(user, gateway, gaddr, new(big.Int), true, "withdrawNFT", validators[i], vaddr, ids[i])
				var frozen *big.Int
				event := gateway.ABI.Events["WithdrawRequest"]
				for _, log := range response.Logs {
					if common.HexToAddress(log.Address) == gaddr && len(log.Topics) > 0 && common.HexToHash(log.Topics[0]) == event.ID {
						values, err := event.Inputs.NonIndexed().Unpack(log.Data)
						require.NoError(t, err)
						frozen = values[1].(*big.Int)
					}
				}
				require.NotNil(t, frozen)
				call(1, gateway, gaddr, new(big.Int), true, "complete", []*big.Int{frozen})
				require.Equal(t, beneficiary, position(ids[i]).PhysicalOwner)
				claim := amount(10)
				if i == 0 {
					claim = amount(9)
				}
				e.credit.Set(claim)
				call(user, vault, vaddr, new(big.Int), true, "redeemPosition", ids[i], beneficiary)
				require.Equal(t, uint8(2), position(ids[i]).Status)
			}
			require.Zero(t, bank(vaddr).Sign())
			require.Zero(t, query(vault, vaddr, "declaredReserve", uint8(1), common.Address{})[0].(*big.Int).Sign())
			t.Log("actual Unified Gateway: A9 DEL/B10 DEL, two owners/two validators, exact native burn, origin uint256 and marker order retained")
		})
	}
	t.Run("authoritative token address bindings", func(t *testing.T) {
		runExactTokenAddressBindings(t, e, registryAddress, gaddr, roles["nft-center"], originAddress, wrapped, addressBindingValidator, gateway)
	})
}
