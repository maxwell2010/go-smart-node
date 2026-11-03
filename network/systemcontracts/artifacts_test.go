package systemcontracts_test

import (
	"github.com/ethereum/go-ethereum/common"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPinnedABIsAndExplicitFreshLibraryLinking(t *testing.T) {
	for _, name := range []string{"DRC20", "DRC20Reserveless", "DRC721", "DRC1155", "DecimalTokenCenter", "DecimalNFTCenter", "DecimalMasterValidator", "DecimalDelegationNFT", "MintCandyWrappedDEL"} {
		artifact, err := contracts.Load(name)
		require.NoError(t, err, name)
		require.NotEmpty(t, artifact.Creation, name)
		contractABI, err := contracts.CompiledABI(name)
		require.NoError(t, err, name)
		require.NotEmpty(t, contractABI.Methods, name)
	}
	coin, err := contracts.CompiledABI("DRC20")
	require.NoError(t, err)
	require.Equal(t, "ReserveUpdated(uint256,uint256,uint256)", coin.Events["ReserveUpdated"].Sig)
	_, err = contracts.Load("DecimalDelegation")
	require.ErrorContains(t, err, "missing nonzero fresh library")
	_, err = contracts.CompiledABI("DecimalDelegation")
	require.NoError(t, err)
	key := "sources/DecimalDelegation.sol:DelegationNFTLib"
	linked, err := contracts.LoadLinked("DecimalDelegation", map[string]common.Address{key: common.HexToAddress("0x1234567890123456789012345678901234567890")})
	require.NoError(t, err)
	require.NotEmpty(t, linked.Creation)
	require.NotEmpty(t, linked.Runtime)
	_, err = contracts.LoadLinked("DecimalDelegation", map[string]common.Address{key: {}})
	require.Error(t, err)
	_, err = contracts.LoadLinked("DRC20", map[string]common.Address{key: common.HexToAddress("0x1234567890123456789012345678901234567890")})
	require.ErrorContains(t, err, "unused library")
	_, err = contracts.CompiledABI("../DRC20")
	require.Error(t, err)
}

func TestConstructorConfigurationPolicy(t *testing.T) {
	address := common.HexToAddress("0x1234567890123456789012345678901234567890")
	calls, err := contracts.PackConstructorConfiguration("DecimalTokenCenter", contracts.ConstructorCall{Method: "setContractCenter", Arguments: []interface{}{address}}, contracts.ConstructorCall{Method: "upgradeToken", Arguments: []interface{}{address}})
	require.NoError(t, err)
	require.Len(t, calls, 2)
	_, err = contracts.ConfiguredProxyDeployment(address, address, address, nil, calls)
	require.NoError(t, err)
	for _, method := range []string{"upgrade", "transferOwnership", "renounceOwnership", "unknown"} {
		_, err = contracts.PackConstructorConfiguration("DecimalTokenCenter", contracts.ConstructorCall{Method: method, Arguments: []interface{}{address}})
		require.Error(t, err)
	}
	_, err = contracts.ConfiguredProxyDeployment(address, address, common.Address{}, nil, calls)
	require.Error(t, err)
	_, err = contracts.ConfiguredProxyDeployment(address, address, address, nil, [][]byte{{1}})
	require.Error(t, err)
	_, err = contracts.ConfiguredProxyDeployment(address, address, address, nil, make([][]byte, 33))
	require.Error(t, err)
	_, err = contracts.ConfiguredProxyDeployment(address, address, address, make([]byte, 49152), nil)
	require.Error(t, err)
}
