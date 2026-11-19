package systemcontracts

import (
	"fmt"
	"math"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type EmptyLaunchContracts struct {
	Registry, Gateway, Library, WrappedDEL    common.Address
	NFTCenter, PositionVault, MasterValidator common.Address
	TokenCenter, CurveConverter               common.Address
}

// DeployEmptyLaunchContracts installs the initial system suite and factory
// implementations. The caller supplies a disposable EVM state and discards it
// on error. Deploy must execute CREATE from deployer and verify artifact.Runtime
// against the deployed code. This routine creates no user tokens or NFTs.
func DeployEmptyLaunchContracts(
	deployer, authority common.Address, nonce uint64, deploy func(Artifact, ...any) (common.Address, error),
) (EmptyLaunchContracts, error) {
	var empty EmptyLaunchContracts
	if deployer == (common.Address{}) || authority == (common.Address{}) || deployer == authority ||
		nonce > math.MaxUint64-32 || deploy == nil {
		return empty, fmt.Errorf("invalid empty launch deployment context")
	}
	create := func(name string, artifact Artifact, args ...any) (common.Address, error) {
		expected := crypto.CreateAddress(deployer, nonce)
		actual, err := deploy(artifact, args...)
		if err != nil {
			return common.Address{}, fmt.Errorf("deploy %s at nonce %d: %w", name, nonce, err)
		}
		if actual != expected {
			return common.Address{}, fmt.Errorf("deploy %s: got %s, expected %s", name, actual, expected)
		}
		nonce++
		return actual, nil
	}

	const libraryName = "MintCandyDelegationNFTLib"
	const librarySource = "owned/DecimalDelegation.sol:DelegationNFTLib"
	library, err := Load(libraryName)
	if err != nil {
		return empty, err
	}
	libraryAddress := crypto.CreateAddress(deployer, nonce)
	library.Runtime, err = ExpectedRuntime(libraryName, nil, map[string]common.Hash{
		librarySource + ".library_deploy_address": common.BytesToHash(libraryAddress.Bytes()),
	})
	if err != nil {
		return empty, err
	}
	if _, err := create(libraryName, library); err != nil {
		return empty, err
	}
	const gatewayName = "MintCandyIsolatedDelegation"
	linkedGateway, err := LoadLinked(gatewayName, map[string]common.Address{librarySource: libraryAddress})
	if err != nil {
		return empty, err
	}

	const (
		wrapperName   = "MintCandyWrappedDEL"
		bootstrapName = "NewNetworkProxyBootstrap"
		registryName  = "DecimalContractCenter"
		nftCenterName = "NewNetworkNFTCenter"
		vaultName     = "MintCandyNFTPositionVault"
		masterName    = "NewNetworkMasterValidator"
		tokenName     = "NewNetworkTokenCenter"
		converterName = "MintCandyCurveConverter"
	)
	nftFamilies := []struct {
		name    string
		kind    uint8
		reserve bool
	}{
		{"MintCandyIsolatedDRC1155", 1, true},
		{"NewNetworkDRC721", 0, true},
		{"DRC721Reserveless", 0, false},
		{"DRC1155Reserveless", 1, false},
	}
	targets := []string{wrapperName, bootstrapName, registryName, gatewayName, nftCenterName, vaultName,
		masterName, tokenName, "DRC20", "DRC20Reserveless"}
	for _, family := range nftFamilies {
		targets = append(targets, family.name)
	}
	code := map[string]Artifact{gatewayName: linkedGateway}
	addresses := make(map[string]common.Address, len(targets))
	for _, name := range targets {
		artifact, ok := code[name]
		if !ok {
			artifact, err = Load(name)
			if err != nil {
				return empty, err
			}
			code[name] = artifact
		}
		addresses[name], err = create(name, artifact)
		if err != nil {
			return empty, err
		}
	}

	roles := EmptyLaunchContracts{
		Library: libraryAddress, WrappedDEL: addresses[wrapperName],
		Registry: crypto.CreateAddress(deployer, nonce), Gateway: crypto.CreateAddress(deployer, nonce+1),
		NFTCenter: crypto.CreateAddress(deployer, nonce+2), PositionVault: crypto.CreateAddress(deployer, nonce+3),
		MasterValidator: crypto.CreateAddress(deployer, nonce+4), TokenCenter: crypto.CreateAddress(deployer, nonce+5),
		CurveConverter: crypto.CreateAddress(deployer, nonce+6),
	}
	var registryConfig []ConstructorCall
	for _, role := range []struct {
		name string
		addr common.Address
	}{
		{"delegation", roles.Gateway}, {"delegation-nft", roles.Gateway}, {"nft-center", roles.NFTCenter},
		{"nft-position-vault", roles.PositionVault}, {"wdel", roles.WrappedDEL}, {"master-validator", roles.MasterValidator},
		{"token-center", roles.TokenCenter}, {"curve-converter", roles.CurveConverter},
	} {
		registryConfig = append(registryConfig, ConstructorCall{"setContractAddress", []any{role.name, role.addr}})
	}
	var nftConfig []ConstructorCall
	for _, family := range nftFamilies {
		nftConfig = append(nftConfig, ConstructorCall{"upgradeNFT", []any{addresses[family.name], family.kind, family.reserve}})
	}
	proxy, err := Load("DecimalContractCenterProxy")
	if err != nil {
		return empty, err
	}
	for _, target := range []struct {
		name   string
		init   []any
		config []ConstructorCall
	}{
		{name: registryName, config: registryConfig},
		{name: gatewayName, config: []ConstructorCall{{"setContractCenter", []any{roles.Registry}}}},
		{name: nftCenterName, init: []any{roles.Registry}, config: nftConfig},
		{name: vaultName, init: []any{roles.Registry, roles.NFTCenter}},
		{name: masterName, init: []any{roles.Registry}},
		{name: tokenName, init: []any{roles.Registry}, config: []ConstructorCall{
			{"upgradeToken", []any{addresses["DRC20"]}}, {"upgradeTokenReserveless", []any{addresses["DRC20Reserveless"]}},
		}},
	} {
		var init, calls []byte
		if target.init != nil {
			init, err = code[target.name].ABI.Pack("initializeFresh", target.init...)
			if err != nil {
				return empty, err
			}
		}
		var configuration [][]byte
		if len(target.config) > 0 {
			configuration, err = PackConstructorConfiguration(target.name, target.config...)
			if err != nil {
				return empty, err
			}
		}
		calls, err = code[bootstrapName].ABI.Pack("bootstrapConfigured", authority, addresses[target.name], init, configuration)
		if err != nil {
			return empty, err
		}
		if _, err := create(target.name+" proxy", proxy, addresses[bootstrapName], calls); err != nil {
			return empty, err
		}
	}
	converter, err := Load(converterName)
	if err != nil {
		return empty, err
	}
	converter.Runtime, err = ExpectedRuntime(converterName, nil, map[string]common.Hash{
		"MintCandyCurveConverter.sol:MintCandyCurveConverter.REGISTRY": common.BytesToHash(roles.Registry.Bytes()),
	})
	if err != nil {
		return empty, err
	}
	if _, err := create(converterName, converter, roles.Registry); err != nil {
		return empty, err
	}
	return roles, nil
}
