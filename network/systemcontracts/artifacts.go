package systemcontracts

import (
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

//go:embed artifacts/*.json
var compiled embed.FS

type Artifact struct {
	ABI      abi.ABI
	Creation []byte
	Runtime  []byte
}

// Load returns code, not a deployment or an authority configuration.
func Load(name string) (Artifact, error) {
	return LoadLinked(name, nil)
}

func checkName(name string) error {
	switch name {
	case "MintCandyCurveConverter", "MintCandyIsolatedDelegation", "MintCandyNFTPositionVault", "MintCandyIsolatedDRC1155", "IsolatedNFTTestController":
	case "DecimalContractCenter", "DecimalContractCenterProxy", "NewNetworkProxyBootstrap", "NativeBurnTestTarget", "NewNetworkTokenReadableProxy", "NewNetworkNFTReadableProxy", "BeaconNFT", "NewNetworkDRC721", "NewNetworkDRC1155", "IMintCandyNativeBurn", "NewNetworkNFTCenter", "NewNetworkTokenCenter", "NewNetworkReservelessTokenBeacon", "ConfiguredBootstrapTestTarget", "NewNetworkMasterValidator", "MintCandyWrappedDEL", "WrappedDELTestReceiver", "DecimalMasterValidator", "DecimalDelegation", "DelegationNFTLib", "MintCandyDelegation", "MintCandyDelegationNFTLib", "DecimalDelegationNFT", "DecimalTokenCenter", "DecimalNFTCenter", "DRC20", "DRC20Reserveless", "DRC721", "DRC1155", "DRC721Reserveless", "DRC1155Reserveless":
	default:
		return fmt.Errorf("unknown pinned system-contract artifact %q", name)
	}
	return nil
}

// CompiledABI returns the exact ABI emitted from pinned sources, including linked targets.
func CompiledABI(name string) (abi.ABI, error) {
	if err := checkName(name); err != nil {
		return abi.ABI{}, err
	}
	data, err := compiled.ReadFile("artifacts/" + name + ".json")
	if err != nil {
		return abi.ABI{}, err
	}
	var source struct {
		ABI json.RawMessage `json:"abi"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return abi.ABI{}, err
	}
	return abi.JSON(strings.NewReader(string(source.ABI)))
}

type linkReference struct {
	Start  int `json:"start"`
	Length int `json:"length"`
}
type linkReferences map[string]map[string][]linkReference

// LoadLinked requires fresh explicit addresses for every solc-declared library.
// It never supplies historical deployment addresses or guesses byte offsets.
func LoadLinked(name string, libraries map[string]common.Address) (Artifact, error) {
	if err := checkName(name); err != nil {
		return Artifact{}, err
	}
	data, err := compiled.ReadFile("artifacts/" + name + ".json")
	if err != nil {
		return Artifact{}, err
	}
	var source struct {
		ContractName  string          `json:"contractName"`
		ArtifactName  string          `json:"artifactName"`
		SourceUnit    string          `json:"sourceUnit"`
		ABI           json.RawMessage `json:"abi"`
		Creation      string          `json:"creationBytecode"`
		Runtime       string          `json:"runtimeBytecode"`
		CreationLinks linkReferences  `json:"creationLinks"`
		RuntimeLinks  linkReferences  `json:"runtimeLinks"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return Artifact{}, err
	}
	expected := name
	if name == "MintCandyDelegation" || name == "MintCandyDelegationNFTLib" || name == "MintCandyIsolatedDelegation" {
		expected = map[string]string{"MintCandyDelegation": "DecimalDelegation", "MintCandyDelegationNFTLib": "DelegationNFTLib", "MintCandyIsolatedDelegation": "MintCandyIsolatedDelegation"}[name]
		if source.ArtifactName != name || source.SourceUnit != "owned/DecimalDelegation.sol" {
			return Artifact{}, fmt.Errorf("owned staking artifact provenance mismatch")
		}
	}
	if source.ContractName != expected {
		return Artifact{}, fmt.Errorf("system-contract artifact name mismatch")
	}
	contractABI, err := abi.JSON(strings.NewReader(string(source.ABI)))
	if err != nil {
		return Artifact{}, err
	}
	used := map[string]bool{}
	creationText, err := applyLinks(source.Creation, source.CreationLinks, libraries, used)
	if err != nil {
		return Artifact{}, err
	}
	runtimeText, err := applyLinks(source.Runtime, source.RuntimeLinks, libraries, used)
	if err != nil {
		return Artifact{}, err
	}
	for key := range libraries {
		if !used[key] {
			return Artifact{}, fmt.Errorf("unused library %q", key)
		}
	}
	creation, err := hex.DecodeString(creationText)
	if err != nil {
		return Artifact{}, err
	}
	runtime, err := hex.DecodeString(runtimeText)
	if err != nil {
		return Artifact{}, err
	}
	if len(creation) == 0 || len(runtime) == 0 {
		return Artifact{}, fmt.Errorf("system-contract artifact lacks executable code")
	}
	return Artifact{ABI: contractABI, Creation: creation, Runtime: runtime}, nil
}

func (a Artifact) Deployment(args ...interface{}) ([]byte, error) {
	encoded, err := a.ABI.Constructor.Inputs.Pack(args...)
	if err != nil {
		return nil, err
	}
	return append(append([]byte(nil), a.Creation...), encoded...), nil
}

func applyLinks(code string, references linkReferences, libraries map[string]common.Address, used map[string]bool) (string, error) {
	occupied := map[int]bool{}
	for source, names := range references {
		for name, positions := range names {
			key := source + ":" + name
			address, ok := libraries[key]
			if !ok || address == (common.Address{}) {
				return "", fmt.Errorf("missing nonzero fresh library %q", key)
			}
			used[key] = true
			for _, ref := range positions {
				if ref.Length != common.AddressLength || ref.Start < 0 || ref.Start > len(code)/2-ref.Length {
					return "", fmt.Errorf("invalid library offset for %q", key)
				}
				for i := ref.Start; i < ref.Start+ref.Length; i++ {
					if occupied[i] {
						return "", fmt.Errorf("overlapping library offsets")
					}
					occupied[i] = true
				}
				begin, end := ref.Start*2, (ref.Start+ref.Length)*2
				code = code[:begin] + hex.EncodeToString(address.Bytes()) + code[end:]
			}
		}
	}
	return code, nil
}
