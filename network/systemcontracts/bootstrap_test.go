package systemcontracts

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm/runtime"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

// This executes the selected fork's genuine EVM constructor/delegatecall paths.
// It is a bootstrap recipe unit test, not an ABCI, signing or fee integration test.
func TestFreshRegistryExplicitOwnerBootstrap(t *testing.T) {
	registry, err := Load("DecimalContractCenter")
	require.NoError(t, err)
	proxy, err := Load("DecimalContractCenterProxy")
	require.NoError(t, err)
	bootstrap, err := Load("NewNetworkProxyBootstrap")
	require.NoError(t, err)
	chain := *params.AllDevChainProtocolChanges
	chain.ChainID = big.NewInt(990001)
	deployer := common.HexToAddress("0x1000000000000000000000000000000000000001")
	owner := common.HexToAddress("0x2000000000000000000000000000000000000002")
	other := common.HexToAddress("0x3000000000000000000000000000000000000003")
	cfg := &runtime.Config{ChainConfig: &chain, Origin: deployer, GasLimit: 10_000_000, BlockNumber: big.NewInt(1), Time: 1, Value: new(big.Int)}
	deploy := func(artifact Artifact, args ...interface{}) common.Address {
		t.Helper()
		code, err := artifact.Deployment(args...)
		require.NoError(t, err)
		_, address, _, err := runtime.Create(code, cfg)
		require.NoError(t, err)
		require.Equal(t, artifact.Runtime, cfg.State.GetCode(address), "creation code must install the exact pinned runtime")
		return address
	}
	implementation := deploy(registry)
	setup := deploy(bootstrap)
	initialization, err := bootstrap.ABI.Pack("bootstrap", owner, implementation, []byte{})
	require.NoError(t, err)
	address := deploy(proxy, setup, initialization)
	readAddress := func(method string, args ...interface{}) common.Address {
		t.Helper()
		input, err := registry.ABI.Pack(method, args...)
		require.NoError(t, err)
		result, _, err := runtime.Call(address, input, cfg)
		require.NoError(t, err)
		decoded, err := registry.ABI.Unpack(method, result)
		require.NoError(t, err)
		require.Len(t, decoded, 1)
		return decoded[0].(common.Address)
	}
	require.Equal(t, owner, readAddress("owner"), "owner must come from explicit initialization, not deployer or historical state")
	require.Equal(t, implementation, readAddress("getImpl"))
	input, err := registry.ABI.Pack("setContractAddress", "fixture-contract", implementation)
	require.NoError(t, err)
	cfg.Origin = owner
	_, _, err = runtime.Call(address, input, cfg)
	require.NoError(t, err)
	require.Equal(t, implementation, readAddress("getContractAddress", "fixture-contract"))
	// The original setup implementation cannot initialize itself, and the proxy
	// already executes only the final registry implementation after construction.
	_, _, err = runtime.Call(setup, initialization, cfg)
	require.Error(t, err)
	_, _, err = runtime.Call(address, initialization, cfg)
	require.Error(t, err)
	require.Equal(t, owner, readAddress("owner"))
	cfg.Origin = other
	_, _, err = runtime.Call(address, input, cfg)
	require.Error(t, err)
	require.Equal(t, implementation, readAddress("getContractAddress", "fixture-contract"))
	// A zero-owner constructor must leave no executable proxy behind.
	cfg.Origin = deployer
	invalid, err := bootstrap.ABI.Pack("bootstrap", common.Address{}, implementation, []byte{})
	require.NoError(t, err)
	code, err := proxy.Deployment(setup, invalid)
	require.NoError(t, err)
	expected := crypto.CreateAddress(deployer, cfg.State.GetNonce(deployer))
	_, _, _, err = runtime.Create(code, cfg)
	require.Error(t, err)
	require.Empty(t, cfg.State.GetCode(expected))
}
