package systemcontracts

import (
	"fmt"
	"github.com/ethereum/go-ethereum/common"
)

type ConstructorCall struct {
	Method    string
	Arguments []interface{}
}

// PackConstructorConfiguration uses compiler ABIs and a narrow fresh-setup
// method allowlist. It does not execute transactions or authenticate an address.
func PackConstructorConfiguration(contract string, calls ...ConstructorCall) ([][]byte, error) {
	allowed := map[string]map[string]bool{
		"DecimalContractCenter":     {"setContractAddress": true},
		"DecimalDelegation":         {"setContractCenter": true},
		"DecimalDelegationNFT":      {"setContractCenter": true},
		"DecimalTokenCenter":        {"setContractCenter": true, "upgradeToken": true, "upgradeTokenReserveless": true},
		"DecimalNFTCenter":          {"setContractCenter": true, "upgradeNFT": true},
		"NewNetworkMasterValidator": {},
	}
	methods, ok := allowed[contract]
	if !ok {
		return nil, fmt.Errorf("contract %q has no reviewed constructor configuration", contract)
	}
	compiled, err := CompiledABI(contract)
	if err != nil {
		return nil, err
	}
	encoded := make([][]byte, 0, len(calls))
	for _, call := range calls {
		if !methods[call.Method] {
			return nil, fmt.Errorf("method %q is not constructor configuration for %q", call.Method, contract)
		}
		data, err := compiled.Pack(call.Method, call.Arguments...)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, data)
	}
	if err := checkConfiguration(encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

func checkConfiguration(calls [][]byte) error {
	if len(calls) > 32 {
		return fmt.Errorf("too many constructor configuration calls")
	}
	size := 0
	for _, call := range calls {
		if len(call) < 4 || len(call) > 32768-size {
			return fmt.Errorf("invalid constructor configuration length")
		}
		size += len(call)
	}
	return nil
}

// ConfiguredProxyDeployment returns unsigned constructor data. The genesis
// manifest must independently bind every supplied address to reviewed code and
// the final authority to the actual governance module account.
func ConfiguredProxyDeployment(setup, implementation, finalOwner common.Address, initialization []byte, calls [][]byte) ([]byte, error) {
	if setup == (common.Address{}) || implementation == (common.Address{}) || finalOwner == (common.Address{}) {
		return nil, fmt.Errorf("configured proxy requires nonzero explicit addresses")
	}
	if err := checkConfiguration(calls); err != nil {
		return nil, err
	}
	bootstrap, err := Load("NewNetworkProxyBootstrap")
	if err != nil {
		return nil, err
	}
	init, err := bootstrap.ABI.Pack("bootstrapConfigured", finalOwner, implementation, initialization, calls)
	if err != nil {
		return nil, err
	}
	proxy, err := Load("DecimalContractCenterProxy")
	if err != nil {
		return nil, err
	}
	deployment, err := proxy.Deployment(setup, init)
	if err != nil {
		return nil, err
	}
	if len(deployment) > 49152 {
		return nil, fmt.Errorf("proxy creation exceeds EIP3860 initcode size limit")
	}
	return deployment, nil
}
