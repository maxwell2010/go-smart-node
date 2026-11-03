package systemcontracts_test

import (
	"math/big"
	"testing"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// All tokens are actual pinned factory DRC20 or the actual native-backed WDEL.
// The small controller only exercises ordinary calls and receiver/outer rollback;
// it is not a production Gateway and does not install any App financial route.
func runCurveConverterFinancial(t *testing.T, e isolatedBackendEnv) {
	t.Helper()
	registry, proxy, bootstrap := e.load("DecimalContractCenter"), e.load("DecimalContractCenterProxy"), e.load("NewNetworkProxyBootstrap")
	center, coin, freeCoin := e.load("NewNetworkTokenCenter"), e.load("DRC20"), e.load("DRC20Reserveless")
	converter, controller, wrapper := e.load("MintCandyCurveConverter"), e.load("CurveConverterTestController"), e.load("MintCandyWrappedDEL")
	owner := crypto.PubkeyToAddress(e.keys[0].PublicKey)
	registryImpl, centerImpl, coinImpl, freeImpl := e.deploy(registry), e.deploy(center), e.deploy(coin), e.deploy(freeCoin)
	init, err := bootstrap.ABI.Pack("bootstrapConfigured", e.govOwner, registryImpl, []byte{}, [][]byte{})
	require.NoError(t, err)
	registryAddress := e.deploy(proxy, e.setup, init)
	deployImmutable := func(artifact contracts.Artifact, name string, values map[string]common.Hash, args ...interface{}) common.Address {
		t.Helper()
		target := crypto.CreateAddress(owner, e.app.EVMKeeper.GetNonce(e.ctx(), owner))
		code, err := artifact.Deployment(args...)
		require.NoError(t, err)
		e.send(e.keys[0], nil, new(big.Int), code, true)
		expected, err := contracts.ExpectedRuntime(name, nil, values)
		require.NoError(t, err)
		require.Equal(t, expected, e.app.EVMKeeper.GetCode(e.ctx(), e.app.EVMKeeper.GetCodeHash(e.ctx(), target)))
		return target
	}
	converterAddress := deployImmutable(converter, "MintCandyCurveConverter", map[string]common.Hash{"MintCandyCurveConverter.sol:MintCandyCurveConverter.REGISTRY": common.BytesToHash(registryAddress.Bytes())}, registryAddress)
	controllers := make([]common.Address, 2)
	for i := range controllers {
		controllers[i] = deployImmutable(controller, "CurveConverterTestController", map[string]common.Hash{"CurveConverterTestController.sol:CurveConverterTestController.OWNER": common.BytesToHash(owner.Bytes()), "CurveConverterTestController.sol:CurveConverterTestController.CONVERTER": common.BytesToHash(converterAddress.Bytes())}, owner, converterAddress)
	}
	wrapped := e.deploy(wrapper)
	centerInit, err := center.ABI.Pack("initializeFresh", registryAddress)
	require.NoError(t, err)
	reserveImpl, err := center.ABI.Pack("upgradeToken", coinImpl)
	require.NoError(t, err)
	reservlessImpl, err := center.ABI.Pack("upgradeTokenReserveless", freeImpl)
	require.NoError(t, err)
	init, err = bootstrap.ABI.Pack("bootstrapConfigured", e.govOwner, centerImpl, centerInit, [][]byte{reserveImpl, reservlessImpl})
	require.NoError(t, err)
	centerAddress := e.deploy(proxy, e.setup, init)
	call := func(key int, artifact contracts.Artifact, target common.Address, value *big.Int, success bool, method string, args ...interface{}) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		// Fees in this App are transfers to the fee collector, not supply burns.
		// Account only for independently emitted Begin/EndBlock inflation here;
		// accepting transaction mint/burn events would conceal an entry-time burn.
		beforeSupply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount
		response := e.send(e.keys[key], &target, value, data, success)
		expectedSupply := beforeSupply
		for _, event := range e.blockEvents() {
			if event.Type != banktypes.EventTypeCoinMint && event.Type != banktypes.EventTypeCoinBurn {
				continue
			}
			for _, attribute := range event.Attributes {
				if attribute.Key != sdk.AttributeKeyAmount {
					continue
				}
				coins, err := sdk.ParseCoinsNormalized(attribute.Value)
				require.NoError(t, err)
				delta := coins.AmountOf(identity.BaseDenom)
				if event.Type == banktypes.EventTypeCoinMint {
					expectedSupply = expectedSupply.Add(delta)
				} else {
					expectedSupply = expectedSupply.Sub(delta)
				}
			}
		}
		require.Equal(t, expectedSupply, e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom).Amount, "conversion, WDEL unwrap and reverted transactions must preserve bank DEL supply apart from block-level issuance")
		return response
	}
	gov := func(target common.Address, artifact contracts.Artifact, method string, args ...interface{}) {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		ctx, commit := e.ctx().CacheContext()
		ctx = ctx.WithGasMeter(storetypes.NewGasMeter(3_000_000))
		message := core.Message{From: e.govOwner, To: &target, Value: new(big.Int), GasLimit: 500000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := e.app.EVMKeeper.ApplyMessage(ctx, statedb.New(ctx, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, true, false, true)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.Empty(t, response.VmError)
		commit()
	}
	for _, binding := range []struct {
		role   string
		target common.Address
	}{{"delegation", controllers[0]}, {"delegation-nft", controllers[0]}, {"nft-position-vault", controllers[1]}, {"token-center", centerAddress}, {"wdel", wrapped}, {"curve-converter", converterAddress}} {
		gov(registryAddress, registry, "setContractAddress", binding.role, binding.target)
	}
	query := func(artifact contracts.Artifact, target common.Address, method string, args ...interface{}) []interface{} {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		ctx, _ := e.ctx().CacheContext()
		ctx = ctx.WithGasMeter(storetypes.NewGasMeter(5_000_000))
		message := core.Message{From: owner, To: &target, Value: new(big.Int), GasLimit: 500000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := e.app.EVMKeeper.ApplyMessage(ctx, statedb.New(ctx, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, false, false, true)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.Empty(t, response.VmError)
		used := response.GasUsed
		if response.MaxUsedGas > used {
			used = response.MaxUsedGas
		}
		require.LessOrEqual(t, used, message.GasLimit)
		ctx.GasMeter().ConsumeGas(used, "ordinary converter bounded read")
		values, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		return values
	}
	integer := func(artifact contracts.Artifact, target common.Address, method string, args ...interface{}) *big.Int {
		return query(artifact, target, method, args...)[0].(*big.Int)
	}
	amount := func(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), big.NewInt(1_000_000_000_000_000_000)) }
	native := func(target common.Address) *big.Int {
		bank := e.app.BankKeeper.GetBalance(e.ctx(), sdk.AccAddress(target.Bytes()), identity.BaseDenom).Amount.BigInt()
		require.Zero(t, bank.Cmp(e.app.EVMKeeper.GetBalance(e.ctx(), target).ToBig()), "bank and EVM native balances must agree numerically")
		return bank
	}
	deadline := func() *big.Int { return big.NewInt(e.ctx().BlockTime().Unix() + 300) }
	type coinMeta struct {
		InitialMint, MinTotalSupply, MaxTotalSupply *big.Int
		Creator                                     common.Address
		Crr                                         uint8
		Identity, Symbol, Name                      string
	}
	mintCurve := func(symbol string, reserve, minimum *big.Int, crr uint8) common.Address {
		commission := integer(center, centerAddress, "getCommissionSymbol", symbol)
		call(0, center, centerAddress, new(big.Int).Add(new(big.Int).Set(reserve), commission), true, "createToken", coinMeta{amount(10), minimum, amount(100), owner, crr, "converter-financial-control", symbol, "Converter reserve coin"})
		target := query(center, centerAddress, "tokens", symbol)[0].(common.Address)
		require.True(t, query(center, centerAddress, "isTokenExists", target)[0].(bool))
		require.Equal(t, reserve, native(target))
		return target
	}
	currency := mintCurve("convertcx", amount(10), amount(5), 100)
	call(0, coin, currency, new(big.Int), true, "transfer", controllers[0], amount(6))
	// Existing unsolicited balances must remain separate from this conversion.
	call(0, coin, currency, new(big.Int), true, "transfer", converterAddress, big.NewInt(5))
	e.send(e.keys[0], &converterAddress, big.NewInt(7), nil, true)
	type state struct{ Supply, CurveNative, CallerToken, CallerNative, ConverterToken, ConverterNative, Allowance *big.Int }
	snapshot := func(tokenArtifact contracts.Artifact, token, caller common.Address) state {
		return state{integer(tokenArtifact, token, "totalSupply"), native(token), integer(tokenArtifact, token, "balanceOf", caller), native(caller), integer(tokenArtifact, token, "balanceOf", converterAddress), native(converterAddress), integer(tokenArtifact, token, "allowance", caller, converterAddress)}
	}
	requireError := func(response *evmtypes.MsgEthereumTxResponse, name string) {
		signature := converter.ABI.Errors[name].ID
		require.NotEqual(t, common.Hash{}, signature)
		require.Equal(t, signature.Bytes()[:4], response.Ret)
		require.Empty(t, response.Logs)
	}
	noNativeBurn := func(response *evmtypes.MsgEthereumTxResponse) {
		for _, log := range response.Logs {
			require.NotEqual(t, common.HexToAddress("0x900"), common.HexToAddress(log.Address))
		}
	}
	before := snapshot(coin, currency, controllers[0])
	require.Equal(t, amount(2), integer(converter, converterAddress, "preview", currency, amount(2), amount(2), deadline()))
	// No arbitrary upper deadline was approved; expiry is still enforced.
	require.Equal(t, amount(2), integer(converter, converterAddress, "preview", currency, amount(2), amount(2), big.NewInt(e.ctx().BlockTime().Unix()+2*86400)))
	response := call(0, controller, controllers[0], new(big.Int), true, "convert", currency, amount(2), amount(2), deadline())
	values, err := controller.ABI.Unpack("convert", response.Ret)
	require.NoError(t, err)
	require.Equal(t, amount(2), values[0])
	noNativeBurn(response)
	after := snapshot(coin, currency, controllers[0])
	require.Equal(t, amount(8), after.Supply)
	require.Equal(t, amount(8), after.CurveNative)
	require.Equal(t, amount(4), after.CallerToken)
	require.Equal(t, new(big.Int).Add(before.CallerNative, amount(2)), after.CallerNative)
	require.Equal(t, big.NewInt(5), after.ConverterToken)
	require.Equal(t, big.NewInt(7), after.ConverterNative)
	require.Zero(t, after.Allowance.Sign())
	event := converter.ABI.Events["CollateralConverted"]
	events := 0
	for _, log := range response.Logs {
		if common.HexToAddress(log.Address) != converterAddress {
			continue
		}
		require.Len(t, log.Topics, 4)
		require.Equal(t, event.ID, common.HexToHash(log.Topics[0]))
		require.Equal(t, controllers[0], common.BytesToAddress(common.HexToHash(log.Topics[1]).Bytes()[12:]))
		require.Equal(t, currency, common.BytesToAddress(common.HexToHash(log.Topics[2]).Bytes()[12:]))
		decoded, err := event.Inputs.NonIndexed().Unpack(log.Data)
		require.NoError(t, err)
		require.Equal(t, amount(2), decoded[0])
		require.Equal(t, amount(2), decoded[1])
		require.Equal(t, false, decoded[4])
		events++
	}
	require.Equal(t, 1, events)
	for _, test := range []struct {
		name                    string
		input, minimum, expires *big.Int
		errorName               string
	}{{"slippage", amount(1), new(big.Int).Add(amount(1), big.NewInt(1)), deadline(), "ConversionLiquidityUnavailable"}, {"minimum supply", amount(4), amount(1), deadline(), "ConversionLiquidityUnavailable"}, {"expired", amount(1), amount(1), big.NewInt(1), "ConversionDeadlineExpired"}, {"zero amount", new(big.Int), amount(1), deadline(), "InvalidConversionLimits"}, {"zero minimum", amount(1), new(big.Int), deadline(), "InvalidConversionLimits"}} {
		t.Run(test.name, func(t *testing.T) {
			before := snapshot(coin, currency, controllers[0])
			result := call(0, controller, controllers[0], new(big.Int), false, "convert", currency, test.input, test.minimum, test.expires)
			requireError(result, test.errorName)
			require.Equal(t, before, snapshot(coin, currency, controllers[0]))
		})
	}
	t.Run("unauthorized source", func(t *testing.T) {
		before := snapshot(coin, currency, controllers[0])
		result := call(0, converter, converterAddress, new(big.Int), false, "convert", currency, amount(1), amount(1), deadline())
		requireError(result, "UnauthorizedController")
		require.Equal(t, before, snapshot(coin, currency, controllers[0]))
	})
	t.Run("mismatched source aliases", func(t *testing.T) {
		gov(registryAddress, registry, "setContractAddress", "delegation-nft", controllers[1])
		before := snapshot(coin, currency, controllers[0])
		result := call(0, controller, controllers[0], new(big.Int), false, "convert", currency, amount(1), amount(1), deadline())
		requireError(result, "UnauthorizedController")
		require.Equal(t, before, snapshot(coin, currency, controllers[0]))
		gov(registryAddress, registry, "setContractAddress", "delegation-nft", controllers[0])
	})
	t.Run("paused registered curve", func(t *testing.T) {
		gov(centerAddress, center, "pause")
		before := snapshot(coin, currency, controllers[0])
		preview := call(0, converter, converterAddress, new(big.Int), false, "preview", currency, amount(1), amount(1), deadline())
		requireError(preview, "UnapprovedReserveCurve")
		result := call(0, controller, controllers[0], new(big.Int), false, "convert", currency, amount(1), amount(1), deadline())
		// The actual factory proxy rejects the controller's approve before the
		// converter is reached. Assert that independent financial pause gate.
		paused := e.load("NewNetworkTokenReadableProxy").ABI.Errors["FactoryPaused"].ID
		require.NotEqual(t, common.Hash{}, paused)
		require.Equal(t, paused.Bytes()[:4], result.Ret)
		require.Empty(t, result.Logs)
		require.Equal(t, before, snapshot(coin, currency, controllers[0]))
		gov(centerAddress, center, "unpause")
	})
	t.Run("recipient rejects native", func(t *testing.T) {
		call(0, controller, controllers[0], new(big.Int), true, "setRejectNative", true)
		before := snapshot(coin, currency, controllers[0])
		result := call(0, controller, controllers[0], new(big.Int), false, "convert", currency, amount(1), amount(1), deadline())
		requireError(result, "ConversionNativeTransferFailed")
		require.Equal(t, before, snapshot(coin, currency, controllers[0]))
		call(0, controller, controllers[0], new(big.Int), true, "setRejectNative", false)
	})
	t.Run("ordinary late outer revert", func(t *testing.T) {
		before := snapshot(coin, currency, controllers[0])
		result := call(0, controller, controllers[0], new(big.Int), false, "convertThenRevert", currency, amount(1), amount(1), deadline())
		require.Empty(t, result.Logs)
		require.Equal(t, before, snapshot(coin, currency, controllers[0]))
	})
	t.Run("reserve floor", func(t *testing.T) {
		low := mintCurve("lowliqxx", amount(1), amount(1), 100)
		call(0, coin, low, new(big.Int), true, "transfer", controllers[0], amount(1))
		before := snapshot(coin, low, controllers[0])
		result := call(0, controller, controllers[0], new(big.Int), false, "convert", low, amount(1), big.NewInt(1), deadline())
		requireError(result, "ConversionLiquidityUnavailable")
		require.Equal(t, before, snapshot(coin, low, controllers[0]))
	})
	t.Run("unregistered reserveless token", func(t *testing.T) {
		created := call(0, center, centerAddress, new(big.Int), true, "createTokenReserveless", "Converter free control", "freeconvx", true, true, amount(10), amount(100), "converter-control")
		free := integerAddress(t, center, "createTokenReserveless", created)
		call(0, freeCoin, free, new(big.Int), true, "transfer", controllers[0], amount(1))
		before := snapshot(freeCoin, free, controllers[0])
		result := call(0, controller, controllers[0], new(big.Int), false, "convert", free, amount(1), amount(1), deadline())
		requireError(result, "UnapprovedReserveCurve")
		require.Equal(t, before, snapshot(freeCoin, free, controllers[0]))
	})
	t.Run("nonlinear registered CRR50 curve", func(t *testing.T) {
		curved := mintCurve("curvedcx", amount(10), amount(5), 50)
		input := amount(1)
		call(0, coin, curved, new(big.Int), true, "transfer", controllers[0], amount(2))
		// Expected output comes from the actual registered curve, independently
		// of the converter preview; it must differ from a linear 1:1 sale.
		quote := integer(coin, curved, "calculateSellOutput", input)
		require.Positive(t, quote.Sign())
		require.NotZero(t, quote.Cmp(input))
		require.Equal(t, quote, integer(converter, converterAddress, "preview", curved, input, quote, deadline()))
		before := snapshot(coin, curved, controllers[0])
		result := call(0, controller, controllers[0], new(big.Int), true, "convert", curved, input, quote, deadline())
		decoded, err := controller.ABI.Unpack("convert", result.Ret)
		require.NoError(t, err)
		require.Equal(t, quote, decoded[0])
		noNativeBurn(result)
		after := snapshot(coin, curved, controllers[0])
		require.Equal(t, new(big.Int).Sub(before.Supply, input), after.Supply)
		require.Equal(t, new(big.Int).Sub(before.CurveNative, quote), after.CurveNative)
		require.Equal(t, new(big.Int).Sub(before.CallerToken, input), after.CallerToken)
		require.Equal(t, new(big.Int).Add(before.CallerNative, quote), after.CallerNative)
		require.Equal(t, before.ConverterToken, after.ConverterToken)
		require.Equal(t, before.ConverterNative, after.ConverterNative)
		require.Zero(t, after.Allowance.Sign())
		t.Logf("actual CRR50 sell: input=%s DEL output=%s", input, quote)
	})
	t.Run("WDEL one to one through vault caller", func(t *testing.T) {
		call(0, wrapper, wrapped, amount(3), true, "deposit")
		call(0, wrapper, wrapped, new(big.Int), true, "transfer", controllers[1], amount(3))
		before := snapshot(wrapper, wrapped, controllers[1])
		result := call(0, controller, controllers[1], new(big.Int), true, "convert", wrapped, amount(3), amount(3), deadline())
		noNativeBurn(result)
		after := snapshot(wrapper, wrapped, controllers[1])
		require.Zero(t, after.Supply.Sign())
		require.Zero(t, after.CurveNative.Sign())
		require.Zero(t, after.CallerToken.Sign())
		require.Equal(t, new(big.Int).Add(before.CallerNative, amount(3)), after.CallerNative)
		require.Equal(t, big.NewInt(7), after.ConverterNative)
		require.Zero(t, after.ConverterToken.Sign())
		require.Zero(t, after.Allowance.Sign())
	})
	t.Log("actual registered curve and WDEL converter: exact deltas, slippage/liquidity/expiry/access guards, receiver rollback and outer rollback passed")
}
