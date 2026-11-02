package systemcontracts

import (
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
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
	switch name {
	case "DecimalContractCenter", "DecimalContractCenterProxy", "NewNetworkProxyBootstrap":
	default:
		return Artifact{}, fmt.Errorf("unknown pinned system-contract artifact %q", name)
	}
	data, err := compiled.ReadFile("artifacts/" + name + ".json")
	if err != nil {
		return Artifact{}, err
	}
	var source struct {
		ContractName string          `json:"contractName"`
		ABI          json.RawMessage `json:"abi"`
		Creation     string          `json:"creationBytecode"`
		Runtime      string          `json:"runtimeBytecode"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return Artifact{}, err
	}
	if source.ContractName != name {
		return Artifact{}, fmt.Errorf("system-contract artifact name mismatch")
	}
	contractABI, err := abi.JSON(strings.NewReader(string(source.ABI)))
	if err != nil {
		return Artifact{}, err
	}
	creation, err := hex.DecodeString(source.Creation)
	if err != nil {
		return Artifact{}, err
	}
	runtime, err := hex.DecodeString(source.Runtime)
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
