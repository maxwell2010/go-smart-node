package systemcontracts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"strings"
)

// ExpectedRuntime derives code from compiler templates and explicit constructor
// values. Observed chain code is never used as its own approval source.
func ExpectedRuntime(name string, libraries map[string]common.Address, values map[string]common.Hash) ([]byte, error) {
	artifact, err := LoadLinked(name, libraries)
	if err != nil {
		return nil, err
	}
	data, err := compiled.ReadFile("artifacts/" + name + ".json")
	if err != nil {
		return nil, err
	}
	var source struct {
		References map[string][]linkReference `json:"immutableReferences"`
		Variables  map[string]struct {
			Key  string `json:"key"`
			Type string `json:"type"`
		} `json:"immutableVariables"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, err
	}
	runtime := append([]byte(nil), artifact.Runtime...)
	used := map[string]bool{}
	occupied := map[int]bool{}
	for id, refs := range source.References {
		variable, ok := source.Variables[id]
		if !ok || variable.Key == "" {
			return nil, fmt.Errorf("compiler immutable lacks declared variable identity")
		}
		value, ok := values[variable.Key]
		if !ok {
			return nil, fmt.Errorf("missing constructor immutable %q", variable.Key)
		}
		used[variable.Key] = true
		addressType := variable.Type == "address" || strings.HasPrefix(variable.Type, "contract ")
		if addressType && !bytes.Equal(value[:12], make([]byte, 12)) {
			return nil, fmt.Errorf("invalid constructor address encoding")
		}
		if variable.Type == "uint8" || strings.HasPrefix(variable.Type, "enum ") {
			if !bytes.Equal(value[:31], make([]byte, 31)) {
				return nil, fmt.Errorf("invalid constructor small integer")
			}
		}
		if variable.Type == "bool" {
			if !bytes.Equal(value[:31], make([]byte, 31)) || value[31] > 1 {
				return nil, fmt.Errorf("invalid constructor boolean")
			}
		}
		for _, ref := range refs {
			if ref.Start < 0 || ref.Length <= 0 || ref.Length > 32 || ref.Start > len(runtime)-ref.Length || (ref.Length != 32 && !(addressType && ref.Length == 20)) {
				return nil, fmt.Errorf("invalid compiler immutable range")
			}
			for index := ref.Start; index < ref.Start+ref.Length; index++ {
				if occupied[index] {
					return nil, fmt.Errorf("overlapping compiler immutable ranges")
				}
				occupied[index] = true
			}
			copy(runtime[ref.Start:ref.Start+ref.Length], value[32-ref.Length:])
		}
	}
	for key := range values {
		if !used[key] {
			return nil, fmt.Errorf("unused constructor immutable %q", key)
		}
	}
	return runtime, nil
}

// PauseAwareProxyRuntime uses only known factory constructor arguments and the
// exact compiler immutable references. family0/1 is ERC20;2..5 is NFT.
func PauseAwareProxyRuntime(beacon, factory common.Address, family uint8) ([]byte, error) {
	if beacon == (common.Address{}) || factory == (common.Address{}) || family > 5 {
		return nil, fmt.Errorf("invalid pause-aware proxy constructor")
	}
	name, base := "NewNetworkTokenReadableProxy", "owned/DecimalTokenCenter.sol:BeaconProxy._beacon"
	if family >= 2 {
		name = "NewNetworkNFTReadableProxy"
		base = "owned/DecimalNFTCenter.sol:BeaconProxy._beacon"
	}
	prefix := "NewNetworkReadableProxies.sol:" + name + "."
	return ExpectedRuntime(name, nil, map[string]common.Hash{base: common.BytesToHash(beacon.Bytes()), prefix + "FACTORY": common.BytesToHash(factory.Bytes()), prefix + "FAMILY": common.BytesToHash([]byte{family})})
}

func NFTBeaconRuntime(factory common.Address, kind uint8, withReserve bool) ([]byte, error) {
	if factory == (common.Address{}) || kind > 1 {
		return nil, fmt.Errorf("invalid NFT beacon constructor")
	}
	var reserved common.Hash
	if withReserve {
		reserved[31] = 1
	}
	prefix := "owned/DecimalNFTCenter.sol:BeaconNFT."
	return ExpectedRuntime("BeaconNFT", nil, map[string]common.Hash{prefix + "NFT_CENTER": common.BytesToHash(factory.Bytes()), prefix + "TYPE_NFT": common.BytesToHash([]byte{kind}), prefix + "WITH_RESERVE": reserved})
}

func ReservelessTokenBeaconRuntime(factory common.Address) ([]byte, error) {
	if factory == (common.Address{}) {
		return nil, fmt.Errorf("invalid token beacon constructor")
	}
	return ExpectedRuntime("NewNetworkReservelessTokenBeacon", nil, map[string]common.Hash{"NewNetworkTokenCenter.sol:NewNetworkReservelessTokenBeacon.TOKEN_CENTER": common.BytesToHash(factory.Bytes())})
}
