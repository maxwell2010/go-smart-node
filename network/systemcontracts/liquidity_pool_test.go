package systemcontracts

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/core/vm/runtime"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

func TestLiquidityPoolRuntimeAndOneShotBinding(t *testing.T) {
	pool, err := Load("MintCandyLiquidityPool")
	require.NoError(t, err)
	wrapped, err := Load("MintCandyWrappedDEL")
	require.NoError(t, err)
	authority := common.Address{0: 1}
	deployer := common.Address{0: 2}
	chain := *params.AllEthashProtocolChanges
	chain.ChainID = big.NewInt(31337)
	config := &runtime.Config{ChainConfig: &chain, Origin: deployer, GasLimit: 10_000_000, Value: new(big.Int)}
	deploy := func(artifact Artifact, args ...any) ([]byte, common.Address) {
		t.Helper()
		input, err := artifact.Deployment(args...)
		require.NoError(t, err)
		code, address, _, err := runtime.Create(input, config)
		require.NoError(t, err)
		return code, address
	}
	_, first := deploy(wrapped)
	_, second := deploy(wrapped)
	compiled, address := deploy(pool, authority)
	canonical, err := LiquidityPoolRuntime(authority)
	require.NoError(t, err)
	require.Equal(t, canonical, compiled)
	require.Equal(t, canonical, config.State.GetCode(address))
	wrong, err := LiquidityPoolRuntime(deployer)
	require.NoError(t, err)
	require.NotEqual(t, wrong, canonical)
	_, err = LiquidityPoolRuntime(common.Address{})
	require.Error(t, err)

	call := func(method string, args ...any) ([]byte, error) {
		t.Helper()
		input, err := pool.ABI.Pack(method, args...)
		require.NoError(t, err)
		output, _, err := runtime.Call(address, input, config)
		return output, err
	}
	_, err = call("initialize", first, second)
	require.Error(t, err)
	config.Origin = authority
	_, err = call("initialize", first, first)
	require.Error(t, err)
	_, err = call("initialize", first, address)
	require.Error(t, err)
	_, err = call("initialize", first, second)
	require.NoError(t, err)
	_, err = call("initialize", second, first)
	require.Error(t, err)
	for method, want := range map[string]any{"token0": first, "token1": second, "feeBps": uint16(30)} {
		result, err := call(method)
		require.NoError(t, err)
		values, err := pool.ABI.Unpack(method, result)
		require.NoError(t, err)
		require.Equal(t, []any{want}, values)
	}

	amount0, amount1, shares, deadline := big.NewInt(4), big.NewInt(9), big.NewInt(6), big.NewInt(10)
	for _, provider := range []common.Address{authority, deployer} {
		config.Origin = provider
		config.State.SetBalance(provider, uint256.NewInt(100), tracing.BalanceChangeUnspecified)
		for _, asset := range []struct {
			address common.Address
			amount  *big.Int
		}{{first, amount0}, {second, amount1}} {
			deposit, err := wrapped.ABI.Pack("deposit")
			require.NoError(t, err)
			config.Value = asset.amount
			_, _, err = runtime.Call(asset.address, deposit, config)
			require.NoError(t, err)
			config.Value = new(big.Int)
			approve, err := wrapped.ABI.Pack("approve", address, asset.amount)
			require.NoError(t, err)
			_, _, err = runtime.Call(asset.address, approve, config)
			require.NoError(t, err)
		}
	}
	checkPool := func(want0, want1, wantShares int64) {
		t.Helper()
		for method, want := range map[string]int64{"reserve0": want0, "reserve1": want1, "totalShares": wantShares} {
			result, err := call(method)
			require.NoError(t, err)
			values, err := pool.ABI.Unpack(method, result)
			require.NoError(t, err)
			require.Zero(t, values[0].(*big.Int).Cmp(big.NewInt(want)))
		}
	}
	rejectOpening := func() {
		t.Helper()
		config.Origin = deployer
		output, err := call("addLiquidity", amount0, amount1, shares, deadline)
		require.ErrorIs(t, err, vm.ErrExecutionReverted)
		invalidPool := pool.ABI.Errors["InvalidPool"].ID
		require.Equal(t, invalidPool[:4], output)
		checkPool(0, 0, 0)
	}
	rejectOpening()
	config.Origin = authority
	_, err = call("addLiquidity", amount0, amount1, shares, deadline)
	require.NoError(t, err)
	checkPool(4, 9, 6)
	config.Origin = deployer
	_, err = call("addLiquidity", amount0, amount1, shares, deadline)
	require.NoError(t, err)
	checkPool(8, 18, 12)
	_, err = call("removeLiquidity", shares, amount0, amount1, deadline)
	require.NoError(t, err)
	config.Origin = authority
	_, err = call("removeLiquidity", shares, amount0, amount1, deadline)
	require.NoError(t, err)
	rejectOpening()
}
