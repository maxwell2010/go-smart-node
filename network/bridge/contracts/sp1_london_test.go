//go:build bridgecontracts && sp1contracts

package contracts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/core/vm/runtime"
)

//go:embed testdata/sp1-v6.0.0/*
var sp1Fixture embed.FS

const sp1SourceCommit = "d3629729c3216eb51bd4859d027a8eb729399fa4"
const sp1ManifestSHA = "4602fc178be169567b53d99446bad329e271f00aaeac40be6adde9d275dc7f5e"
const sp1FixtureRoot = "testdata/sp1-v6.0.0/"

type sp1ProofVector struct {
	Repository   string `json:"source_repository"`
	Commit       string `json:"source_commit"`
	SourceFile   string `json:"source_file"`
	Version      string `json:"verifier_version"`
	ProgramVKey  string `json:"program_vkey"`
	PublicValues string `json:"public_values"`
	Proof        string `json:"proof"`
}

func sp1PinnedSources(t *testing.T) (map[string]interface{}, sp1ProofVector) {
	t.Helper()
	manifest, err := sp1Fixture.ReadFile(sp1FixtureRoot + "UPSTREAM.json")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(manifest)) != sp1ManifestSHA {
		t.Fatal("SP1 provenance manifest changed")
	}
	var provenance struct {
		Repository string `json:"repository"`
		Commit     string `json:"commit"`
		Version    string `json:"verifier_version"`
		Files      []struct {
			File         string `json:"file"`
			UpstreamPath string `json:"upstream_path"`
			SHA256       string `json:"sha256"`
			Bytes        int    `json:"bytes"`
			License      string `json:"license"`
		} `json:"files"`
	}
	if err := json.Unmarshal(manifest, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.Commit != sp1SourceCommit || provenance.Repository != "https://github.com/succinctlabs/sp1-contracts" || provenance.Version != "v6.0.0" || len(provenance.Files) != 5 {
		t.Fatal("SP1 source pin or version differs")
	}
	sources := map[string]interface{}{}
	for _, file := range provenance.Files {
		data, err := sp1Fixture.ReadFile(sp1FixtureRoot + file.File)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != file.Bytes || fmt.Sprintf("%x", sha256.Sum256(data)) != file.SHA256 || file.License != "MIT" {
			t.Fatal("SP1 upstream source hash or license mismatch", file.File)
		}
		if file.File == "ISP1Verifier.sol" || file.File == "SP1VerifierGroth16.sol" || file.File == "Groth16Verifier.sol" {
			if !bytes.HasPrefix(data, []byte("// SPDX-License-Identifier: MIT")) {
				t.Fatal("SP1 source license notice missing")
			}
			sources[file.UpstreamPath] = map[string]string{"content": string(data)}
		}
	}
	if len(sources) != 3 {
		t.Fatal("unexpected SP1 Solidity dependency set")
	}
	data, err := sp1Fixture.ReadFile(sp1FixtureRoot + "proof.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector sp1ProofVector
	if err := json.Unmarshal(data, &vector); err != nil {
		t.Fatal(err)
	}
	if vector.Repository != provenance.Repository || vector.Commit != sp1SourceCommit || vector.Version != "v6.0.0" || vector.SourceFile != "contracts/test/SP1VerifierGroth16V6.sol" {
		t.Fatal("SP1 proof source/version differs")
	}
	return sources, vector
}

func compileOriginalSP1London(t *testing.T, sources map[string]interface{}) compiledEscrow {
	t.Helper()
	compiler := os.Getenv("MCND_BRIDGE_SOLC")
	if compiler == "" {
		t.Fatal("SP1 London test requires the CI-supplied MCND_BRIDGE_SOLC")
	}
	raw, err := os.ReadFile(compiler)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != compilerSHA {
		t.Fatal("SP1 test compiler checksum differs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, compiler, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(version), "0.8.20+commit.a1b79de6.Linux.g++") {
		t.Fatal("SP1 compiler version mismatch", err)
	}
	input := map[string]interface{}{"language": "Solidity", "sources": sources, "settings": map[string]interface{}{
		"optimizer": map[string]interface{}{"enabled": true, "runs": 200}, "evmVersion": "london", "metadata": map[string]string{"bytecodeHash": "none"},
		"outputSelection": map[string]interface{}{"*": map[string]interface{}{"*": []string{"abi", "evm.bytecode.object"}}}}}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, compiler, "--standard-json")
	command.Stdin = bytes.NewReader(encoded)
	out, err := command.Output()
	if err != nil {
		t.Fatal("original SP1 London compilation failed", err)
	}
	var compiled struct {
		Errors []struct {
			Severity string `json:"severity"`
			Message  string `json:"formattedMessage"`
		} `json:"errors"`
		Contracts map[string]map[string]struct {
			ABI json.RawMessage `json:"abi"`
			EVM struct {
				Bytecode struct {
					Object string `json:"object"`
				} `json:"bytecode"`
			} `json:"evm"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(out, &compiled); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range compiled.Errors {
		if diagnostic.Severity == "error" {
			t.Fatal(diagnostic.Message)
		}
	}
	result, ok := compiled.Contracts["contracts/src/v6.0.0/SP1VerifierGroth16.sol"]["SP1Verifier"]
	if !ok {
		t.Fatal("missing canonical SP1 verifier output")
	}
	parsed, err := abi.JSON(bytes.NewReader(result.ABI))
	if err != nil {
		t.Fatal(err)
	}
	code, err := hex.DecodeString(result.EVM.Bytecode.Object)
	if err != nil || len(code) == 0 {
		t.Fatal("missing SP1 creation bytecode", err)
	}
	return compiledEscrow{ABI: parsed, Creation: code}
}

func sp1Hex(t *testing.T, text string) []byte {
	t.Helper()
	if !strings.HasPrefix(text, "0x") {
		t.Fatal("fixture hex prefix missing")
	}
	decoded, err := hex.DecodeString(text[2:])
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

// This verifies a genuine upstream sample-program proof, not a Tendermint header
// or Decimal state proof. No production prover version is selected by this test.
func TestSP1V600CanonicalProofOnLondonEVM(t *testing.T) {
	sources, vector := sp1PinnedSources(t)
	artifact := compileOriginalSP1London(t, sources)
	keyBytes, publicValues, proof := sp1Hex(t, vector.ProgramVKey), sp1Hex(t, vector.PublicValues), sp1Hex(t, vector.Proof)
	if len(keyBytes) != 32 || len(publicValues) != 96 || len(proof) != 356 {
		t.Fatal("canonical v6 proof sizes changed")
	}
	var programKey [32]byte
	copy(programKey[:], keyBytes)
	cfg := londonConfig()
	cfg.GasLimit = transactionGasLimit - intrinsic(artifact.Creation, true)
	_, address, left, err := runtime.Create(artifact.Creation, cfg)
	if err != nil {
		t.Fatal("canonical SP1 verifier did not deploy on the London fixture", err)
	}
	if transactionGasLimit-left >= transactionGasLimit {
		t.Fatal("SP1 deploy exceeds ten million gas")
	}
	t.Logf("canonical SP1 v6.0.0 London deploy gas including intrinsic=%d", transactionGasLimit-left)
	versionCall, err := artifact.ABI.Pack("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	cfg.GasLimit = transactionGasLimit - intrinsic(versionCall, false)
	versionResult, _, err := runtime.Call(address, versionCall, cfg)
	if err != nil {
		t.Fatal(err)
	}
	versionValues, err := artifact.ABI.Unpack("VERSION", versionResult)
	if err != nil || len(versionValues) != 1 || versionValues[0] != "v6.0.0" {
		t.Fatal("runtime is not the pinned v6.0.0 verifier", err)
	}
	// The generated base exposes an overloaded verifyProof. Select the exact
	// ISP1Verifier ABI rather than trusting ABI overload ordering.
	methodName := ""
	for name, method := range artifact.ABI.Methods {
		if method.Sig == "verifyProof(bytes32,bytes,bytes)" {
			methodName = name
		}
	}
	if methodName == "" {
		t.Fatal("canonical SP1 public ABI missing")
	}
	for _, mode := range []string{"canonical", "tampered_proof", "tampered_values", "wrong_program_vkey", "wrong_verifier_selector", "truncated_proof"} {
		t.Run(mode, func(t *testing.T) {
			key := programKey
			values := append([]byte{}, publicValues...)
			candidate := append([]byte{}, proof...)
			switch mode {
			case "tampered_proof":
				candidate[len(candidate)-1] ^= 1
			case "tampered_values":
				values[len(values)-1] ^= 1
			case "wrong_program_vkey":
				key[31] ^= 1
			case "wrong_verifier_selector":
				candidate[0] ^= 1
			case "truncated_proof":
				candidate = candidate[:3]
			}
			input, err := artifact.ABI.Pack(methodName, key, values, candidate)
			if err != nil {
				t.Fatal(err)
			}
			cfg.GasLimit = transactionGasLimit - intrinsic(input, false)
			out, left, err := runtime.Call(address, input, cfg)
			if mode == "canonical" {
				if err != nil || len(out) != 0 {
					t.Fatalf("canonical upstream proof rejected on London: %x, %v", out, err)
				}
				used := transactionGasLimit - left
				if used >= transactionGasLimit {
					t.Fatal("known SP1 proof exceeds ten million gas")
				}
				t.Logf("canonical SP1 v6.0.0 proof London gas including intrinsic=%d", used)
			} else if err == nil {
				t.Fatal("modified SP1 input was accepted", mode)
			}
		})
	}
}
