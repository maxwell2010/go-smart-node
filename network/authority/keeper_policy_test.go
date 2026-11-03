package authority

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	evmkeeper "github.com/cosmos/evm/x/vm/keeper"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type policyOnlyHook struct{}

func (policyOnlyHook) PostTxProcessing(sdk.Context, common.Address, core.Message, *ethtypes.Receipt) error {
	return nil
}

func policyFixture() Manifest {
	proxy := common.HexToHash("0x11")
	registry := Role{Address: common.HexToAddress("0x100"), ProxyCodeHash: proxy, ApprovedImplementations: []common.Hash{common.HexToHash("0x12")}}
	first, second := common.HexToHash("0x21"), common.HexToHash("0x22")
	return Manifest{Registry: registry, Roles: map[string]Role{
		"delegation":         {Address: common.HexToAddress("0x200"), ProxyCodeHash: proxy, ApprovedImplementations: []common.Hash{first, second}},
		"delegation-nft":     {Address: common.HexToAddress("0x200"), ProxyCodeHash: proxy, ApprovedImplementations: []common.Hash{second, first}},
		"nft-position-vault": {Address: common.HexToAddress("0x300"), ProxyCodeHash: proxy, ApprovedImplementations: []common.Hash{common.HexToHash("0x31")}},
	}}
}

// Constructor-only controls do not claim that the placeholder keeper authenticates
// code. The separate integration package uses deployed artifacts and canonical gov.
func TestOnlyConsistentGatewayAliasesMayShareAnAuthorityTarget(t *testing.T) {
	for iteration := 0; iteration < 16; iteration++ {
		manifest := policyFixture()
		keeper, err := NewKeeper(new(evmkeeper.Keeper), manifest, policyOnlyHook{})
		require.NoError(t, err)
		caller := manifest.Roles["delegation"]
		caller.ApprovedImplementations[0] = common.HexToHash("0x99")
		require.Equal(t, common.HexToHash("0x21"), keeper.manifest.Roles["delegation"].ApprovedImplementations[0], "caller slices must remain detached")
	}
	for _, mode := range []string{"proxy hash", "implementation policy", "registry collision", "vault collision", "third role collision", "child policy"} {
		t.Run(mode, func(t *testing.T) {
			manifest := policyFixture()
			role := manifest.Roles["delegation-nft"]
			switch mode {
			case "proxy hash":
				role.ProxyCodeHash = common.HexToHash("0x99")
			case "implementation policy":
				role.ApprovedImplementations = []common.Hash{common.HexToHash("0x21")}
			case "registry collision":
				role.Address = manifest.Registry.Address
			case "vault collision":
				role.Address = manifest.Roles["nft-position-vault"].Address
			case "third role collision":
				manifest.Roles["master-validator"] = role
			case "child policy":
				role.ApprovedChildren = map[string][]common.Hash{"upgradeToken": {common.HexToHash("0x32")}}
			}
			manifest.Roles["delegation-nft"] = role
			_, err := NewKeeper(new(evmkeeper.Keeper), manifest, policyOnlyHook{})
			require.Error(t, err)
		})
	}
}

func TestPositionVaultRoleUsesItsPinnedAdministrativeABI(t *testing.T) {
	name, known := roleABI("nft-position-vault")
	require.True(t, known)
	compiled, err := contracts.CompiledABI(name)
	require.NoError(t, err)
	for method, signature := range map[string]string{"pause": "pause()", "unpause": "unpause()", "upgrade": "upgrade(address,bytes)"} {
		require.Equal(t, signature, compiled.Methods[method].Sig)
	}
	// These real ABI methods exist, but authority integration must reject them.
	require.Contains(t, compiled.Methods, "transferOwnership")
	require.Contains(t, compiled.Methods, "splitPosition")
}
