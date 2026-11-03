package systemcontracts_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type customMinimumEnv struct {
	base                                     isolatedBackendEnv
	registry, gateway, vault, origin, master common.Address
	call                                     func(int, contracts.Artifact, common.Address, *big.Int, bool, string, ...interface{}) *evmtypes.MsgEthereumTxResponse
	gov                                      func(contracts.Artifact, common.Address, string, ...interface{})
	reader                                   *nftview.Reader
}

// Real factory-created reserve-backed DRC20, real Unified Gateway and isolated
// vault. The admitted-currency policy remains DEL/WDEL only; this ordinary case
// measures the custom coin gate without changing its monetary/minimum rules.
func runRegisteredCustomMinimum(t *testing.T, e customMinimumEnv) {
	t.Helper()
	b := e.base
	owner := crypto.PubkeyToAddress(b.keys[0].PublicKey)
	registry, proxy, bootstrap := b.load("DecimalContractCenter"), b.load("DecimalContractCenterProxy"), b.load("NewNetworkProxyBootstrap")
	center, token := b.load("NewNetworkTokenCenter"), b.load("DRC20")
	gatewayABI, err := contracts.CompiledABI("MintCandyIsolatedDelegation")
	require.NoError(t, err)
	gateway := contracts.Artifact{ABI: gatewayABI}
	vault, origin, master := b.load("MintCandyNFTPositionVault"), b.load("MintCandyIsolatedDRC1155"), b.load("NewNetworkMasterValidator")
	centerImpl, tokenImpl := b.deploy(center), b.deploy(token)
	initCenter, err := center.ABI.Pack("initializeFresh", e.registry)
	require.NoError(t, err)
	childConfig, err := center.ABI.Pack("upgradeToken", tokenImpl)
	require.NoError(t, err)
	configured, err := bootstrap.ABI.Pack("bootstrapConfigured", b.govOwner, centerImpl, initCenter, [][]byte{childConfig})
	require.NoError(t, err)
	centerAddress := b.deploy(proxy, b.setup, configured)
	e.gov(registry, e.registry, "setContractAddress", "token-center", centerAddress)
	e.gov(gateway, e.gateway, "setFreezeTime", new(big.Int), new(big.Int))
	e.call(0, origin, e.origin, new(big.Int), true, "setApprovalForAll", e.gateway, true)
	query := func(artifact contracts.Artifact, target common.Address, method string, args ...interface{}) []interface{} {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		ctx, _ := b.ctx().CacheContext()
		ctx = ctx.WithGasMeter(storetypes.NewGasMeter(5_000_000))
		message := core.Message{From: owner, To: &target, Value: new(big.Int), GasLimit: 500000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := b.app.EVMKeeper.ApplyMessage(ctx, statedb.New(ctx, b.app.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, false, false, true)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.Empty(t, response.VmError)
		used := response.GasUsed
		if response.MaxUsedGas > used {
			used = response.MaxUsedGas
		}
		require.LessOrEqual(t, used, message.GasLimit)
		ctx.GasMeter().ConsumeGas(used, "ordinary custom minimum bounded read")
		values, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		return values
	}
	integer := func(artifact contracts.Artifact, target common.Address, method string, args ...interface{}) *big.Int {
		return query(artifact, target, method, args...)[0].(*big.Int)
	}
	amount := func(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), big.NewInt(1_000_000_000_000_000_000)) }
	bank := func(target common.Address) *big.Int {
		return b.app.BankKeeper.GetBalance(b.ctx(), sdk.AccAddress(target.Bytes()), identity.BaseDenom).Amount.BigInt()
	}
	position := func(id *big.Int) isolatedPosition {
		values := query(vault, e.vault, "getPosition", id)
		return *abi.ConvertType(values[0], new(isolatedPosition)).(*isolatedPosition)
	}
	for _, scenario := range []struct {
		name, symbol string
		reserve      int64
		success      bool
	}{{"cross minimum rejects atomically", "minblockx", 6, false}, {"exact minimum succeeds with repricing", "minpassxx", 5, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			type coinMeta struct {
				InitialMint, MinTotalSupply, MaxTotalSupply *big.Int
				Creator                                     common.Address
				Crr                                         uint8
				Identity, Symbol, Name                      string
			}
			metadata := coinMeta{amount(10), amount(5), amount(100), owner, 100, "isolated-minimum-control", scenario.symbol, "Isolated minimum reserve coin"}
			commission := integer(center, centerAddress, "getCommissionSymbol", scenario.symbol)
			e.call(0, center, centerAddress, new(big.Int).Add(amount(10), commission), true, "createToken", metadata)
			currency := query(center, centerAddress, "tokens", scenario.symbol)[0].(common.Address)
			require.NotEqual(t, common.Address{}, currency)
			expectedProxy, err := contracts.PauseAwareProxyRuntime(centerAddress, centerAddress, 0)
			require.NoError(t, err)
			require.Equal(t, expectedProxy, b.app.EVMKeeper.GetCode(b.ctx(), b.app.EVMKeeper.GetCodeHash(b.ctx(), currency)))
			require.True(t, query(center, centerAddress, "isTokenExists", currency)[0].(bool))
			require.Equal(t, amount(10), integer(token, currency, "totalSupply"))
			require.Equal(t, amount(5), integer(token, currency, "minTotalSupply"))
			require.Equal(t, amount(10), bank(currency))
			require.Equal(t, amount(10), integer(token, currency, "reserve"))
			require.Equal(t, amount(1), integer(token, currency, "getCurrentPrice"))
			validator := common.BytesToAddress(crypto.Keccak256([]byte("minimum-validator:" + scenario.symbol))[12:])
			operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validator.Bytes())
			require.NoError(t, err)
			meta, err := json.Marshal(map[string]interface{}{"operator_address": operator, "reward_address": validator.Hex(), "consensus_pubkey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xB9}, 32)), "description": map[string]string{"moniker": "minimum-control"}, "commission": "10"})
			require.NoError(t, err)
			type validatorMeta struct {
				Validator common.Address
				Meta      string
			}
			e.gov(master, e.master, "addValidators", []validatorMeta{{validator, string(meta)}})
			originID := big.NewInt(600 + scenario.reserve)
			locked := amount(scenario.reserve)
			e.call(0, token, currency, new(big.Int), true, "approve", e.origin, locked)
			e.call(0, origin, e.origin, new(big.Int), true, "mint", owner, originID, big.NewInt(1), "ipfs://custom-minimum", locked, currency)
			receiptID := integer(vault, e.vault, "nextPositionId")
			e.call(0, gateway, e.gateway, new(big.Int), true, "delegateNFT1155", validator, e.origin, originID, big.NewInt(1))
			require.Equal(t, locked, position(receiptID).Reserve.Amount)
			require.Equal(t, e.gateway, position(receiptID).PhysicalOwner)
			require.Equal(t, locked, integer(token, currency, "balanceOf", e.vault))
			_, err = e.reader.Position(b.ctx(), common.BigToHash(receiptID))
			require.ErrorContains(t, err, "no approved backing reader", "current native currency policy must reject genuine custom collateral too")
			stakeID := query(gateway, e.gateway, "getNFTStakeId", e.vault, receiptID, owner, validator, new(big.Int))[0].([32]byte)
			beforeNFT := query(gateway, e.gateway, "getNFTStake", e.vault, receiptID, owner, validator, new(big.Int))[0]
			beforeCoin := query(gateway, e.gateway, "getStake", validator, owner, currency)[0]
			beforeValidator := query(gateway, e.gateway, "getValidatorReserve", validator, currency)[0]
			beforePosition := position(receiptID)
			require.Equal(t, locked, integer(vault, e.vault, "declaredReserve", uint8(2), currency))
			e.gov(master, e.master, "addPenalty", validator, uint16(100))
			response := e.call(0, gateway, e.gateway, new(big.Int), scenario.success, "applyPenaltiesToNFTStakes", [][32]byte{stakeID})
			require.Equal(t, amount(10), bank(currency), "custom token burn is not a native reserve burn")
			require.Equal(t, amount(10), integer(token, currency, "reserve"))
			require.Equal(t, amount(10), b.app.EVMKeeper.GetBalance(b.ctx(), currency).ToBig())
			for _, log := range response.Logs {
				require.NotEqual(t, common.HexToAddress("0x900"), common.HexToAddress(log.Address), "custom supply burn must not be presented as native supply destruction")
			}
			if !scenario.success {
				signature := token.ABI.Errors["NewSupplyTooSmall"].ID
				require.Equal(t, signature.Bytes()[:4], response.Ret)
				require.Empty(t, response.Logs)
				require.Equal(t, amount(10), integer(token, currency, "totalSupply"))
				require.Equal(t, locked, integer(token, currency, "balanceOf", e.vault))
				require.Equal(t, beforePosition, position(receiptID))
				require.Equal(t, locked, integer(vault, e.vault, "declaredReserve", uint8(2), currency))
				require.Equal(t, beforeNFT, query(gateway, e.gateway, "getNFTStake", e.vault, receiptID, owner, validator, new(big.Int))[0])
				require.Equal(t, beforeCoin, query(gateway, e.gateway, "getStake", validator, owner, currency)[0])
				require.Equal(t, beforeValidator, query(gateway, e.gateway, "getValidatorReserve", validator, currency)[0])
				require.Zero(t, integer(gateway, e.gateway, "getNFTStakePenaltyIndex", stakeID).Sign())
				withdrawal := e.call(0, gateway, e.gateway, new(big.Int), false, "withdrawNFT", validator, e.vault, receiptID)
				require.Equal(t, signature.Bytes()[:4], withdrawal.Ret)
				require.Empty(t, withdrawal.Logs)
				require.Equal(t, beforePosition, position(receiptID))
				require.Equal(t, locked, integer(token, currency, "balanceOf", e.vault))
				require.Equal(t, amount(1), integer(token, currency, "getCurrentPrice"))
				t.Log("actual registered DRC20 supply10/min5/NFTreserve6:100% penalty reverts NewSupplyTooSmall; reserve, supply, custody and all Gateway mirrors restore")
			} else {
				require.Equal(t, amount(5), integer(token, currency, "totalSupply"))
				require.Zero(t, integer(token, currency, "balanceOf", e.vault).Sign())
				after := position(receiptID)
				require.Equal(t, uint8(2), after.Reserve.ReserveType)
				require.Equal(t, currency, after.Reserve.Token)
				require.Zero(t, after.Reserve.Amount.Sign())
				require.Equal(t, amount(2), integer(token, currency, "getCurrentPrice"))
				quote := integer(token, currency, exactABIMethod(t, token.ABI, "calculateSellOutput(uint256)"), amount(1))
				require.Equal(t, amount(2), quote)
				require.Equal(t, int64(1), integer(gateway, e.gateway, "getNFTStakePenaltyIndex", stakeID).Int64())
				require.Zero(t, integer(vault, e.vault, "declaredReserve", uint8(2), currency).Sign())
				require.Equal(t, amount(5), integer(token, currency, "balanceOf", owner))
				sale := e.call(0, token, currency, new(big.Int), false, "sellExactTokensForDEL", amount(1), new(big.Int), owner)
				signature := token.ABI.Errors["NewSupplyTooSmall"].ID
				require.Equal(t, signature.Bytes()[:4], sale.Ret)
				require.Empty(t, sale.Logs)
				require.Equal(t, amount(5), integer(token, currency, "totalSupply"))
				require.Equal(t, amount(10), bank(currency))
				require.Equal(t, amount(2), integer(token, currency, "getCurrentPrice"))
				t.Log("actual registered DRC20 supply10/min5/NFTreserve5:100% penalty leaves supply5 and native reserve10; per-token price1 to2 and1token sell quote2nativeDEL, but actual sell1 at minimum reverts and restores reserve; no monetary policy changed")
			}
		})
	}
}
