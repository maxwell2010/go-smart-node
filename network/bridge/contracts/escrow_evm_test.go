//go:build bridgecontracts

package contracts

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm/runtime"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/maxwell2010/go-smart-node/network/bridge/protocol"
)

//go:embed DecimalNativeDELEscrow.sol
var escrowSource string

const compilerSHA = "0479d44fdf9c501c25337fdc540419f1593b884a87b47f023da4f1c700fda782"
const transactionGasLimit = uint64(10_000_000)

type compiledEscrow struct {
	ABI      abi.ABI
	Creation []byte
}

// Compilation is a CI-only step: there is no compiler/network fallback and no
// skipped success if the pinned compiler was not supplied by the workflow.
func compileEscrow(t *testing.T) compiledEscrow {
	t.Helper()
	compiler := os.Getenv("MCND_BRIDGE_SOLC")
	if compiler == "" {
		t.Fatal("bridgecontracts requires the CI-supplied MCND_BRIDGE_SOLC compiler")
	}
	raw, err := os.ReadFile(compiler)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != compilerSHA {
		t.Fatal("bridge compiler checksum mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, compiler, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(version), "0.8.20+commit.a1b79de6.Linux.g++") {
		t.Fatalf("bridge compiler version: %s: %v", version, err)
	}
	input := map[string]interface{}{"language": "Solidity", "sources": map[string]interface{}{"DecimalNativeDELEscrow.sol": map[string]string{"content": escrowSource}},
		"settings": map[string]interface{}{"optimizer": map[string]interface{}{"enabled": true, "runs": 200}, "evmVersion": "london", "metadata": map[string]string{"bytecodeHash": "none"},
			"outputSelection": map[string]interface{}{"*": map[string]interface{}{"*": []string{"abi", "evm.bytecode.object", "storageLayout"}}}}}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, compiler, "--standard-json")
	command.Stdin = bytes.NewReader(encoded)
	out, err := command.Output()
	if err != nil {
		t.Fatal("bridge Solidity compilation failed", err)
	}
	var result struct {
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
			StorageLayout struct {
				Storage []struct {
					Label string `json:"label"`
					Slot  string `json:"slot"`
				}
			} `json:"storageLayout"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range result.Errors {
		if diagnostic.Severity == "error" {
			t.Fatal(diagnostic.Message)
		}
	}
	artifact, ok := result.Contracts["DecimalNativeDELEscrow.sol"]["DecimalNativeDELEscrow"]
	if !ok {
		t.Fatal("missing compiled escrow")
	}
	fields := artifact.StorageLayout.Storage
	if len(fields) != 2 || fields[0].Label != "commitments" || fields[0].Slot != "0" || fields[1].Label != "senderNonces" || fields[1].Slot != "1" {
		t.Fatal("bridge commitment storage layout changed")
	}
	parsed, err := abi.JSON(bytes.NewReader(artifact.ABI))
	if err != nil {
		t.Fatal(err)
	}
	code, err := hex.DecodeString(artifact.EVM.Bytecode.Object)
	if err != nil || len(code) == 0 {
		t.Fatal("missing creation bytecode", err)
	}
	for _, name := range []string{"release", "withdraw", "refund", "upgradeTo", "setVerifier", "owner"} {
		if _, exists := parsed.Methods[name]; exists {
			t.Fatal("deposit fixture exposed an exit or admin method", name)
		}
	}
	return compiledEscrow{parsed, code}
}

func intrinsic(data []byte, creation bool) uint64 {
	gas := uint64(21000)
	if creation {
		gas = 53000
	}
	for _, value := range data {
		if value == 0 {
			gas += 4
		} else {
			gas += 16
		}
	}
	return gas
}

func londonConfig() *runtime.Config {
	chain := *params.AllEthashProtocolChanges
	chain.ChainID = big.NewInt(75)
	return &runtime.Config{ChainConfig: &chain, Origin: common.HexToAddress("0x1000000000000000000000000000000000000001"),
		GasLimit: transactionGasLimit, BlockNumber: big.NewInt(1), Time: 1, Value: new(big.Int)}
}

func deploy(t *testing.T, artifact compiledEscrow, cfg *runtime.Config, destination uint64, bridge [32]byte) common.Address {
	t.Helper()
	args, err := artifact.ABI.Pack("", destination, bridge)
	if err != nil {
		t.Fatal(err)
	}
	code := append(append([]byte{}, artifact.Creation...), args...)
	cfg.GasLimit = transactionGasLimit - intrinsic(code, true)
	_, address, left, err := runtime.Create(code, cfg)
	if err != nil {
		t.Fatal("London escrow create", err)
	}
	used := transactionGasLimit - left
	if used >= transactionGasLimit {
		t.Fatal("escrow deployment exhausted the Decimal gas budget")
	}
	t.Logf("London deploy gas including intrinsic=%d", used)
	return address
}

func call(t *testing.T, artifact compiledEscrow, cfg *runtime.Config, escrow common.Address, value *big.Int, method string, args ...interface{}) ([]byte, error) {
	t.Helper()
	input, err := artifact.ABI.Pack(method, args...)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Value = new(big.Int).Set(value)
	cfg.GasLimit = transactionGasLimit - intrinsic(input, false)
	out, left, err := runtime.Call(escrow, input, cfg)
	if err == nil {
		t.Logf("London %s gas including intrinsic=%d", method, transactionGasLimit-left)
	}
	return out, err
}

func TestLondonDepositProtocolAndStorageVectors(t *testing.T) {
	artifact := compileEscrow(t)
	cfg := londonConfig()
	bridge := common.HexToHash("0x42")
	escrow := deploy(t, artifact, cfg, 990012, bridge)
	initial := new(big.Int).Mul(big.NewInt(100), big.NewInt(1_000_000_000_000_000_000))
	cfg.State.SetBalance(cfg.Origin, uint256.MustFromBig(initial), tracing.BalanceChangeUnspecified)
	actor := cfg.Origin
	recipient := common.HexToAddress("0x2000000000000000000000000000000000000002")
	amount := big.NewInt(1_000_000_000_000_000_000)
	for nonce := uint64(0); nonce < 2; nonce++ {
		d := protocol.Deposit{SourceEVMChainID: 75, DestinationEVMChainID: 990012, SourceEscrow: escrow, DestinationBridgeID: bridge, Sender: actor, Recipient: recipient}
		amount.FillBytes(d.Amount[:])
		new(big.Int).SetUint64(nonce).FillBytes(d.Nonce[:])
		id, err := protocol.OperationID(d)
		if err != nil {
			t.Fatal(err)
		}
		out, err := call(t, artifact, cfg, escrow, amount, "deposit", recipient)
		if err != nil || !bytes.Equal(out, id[:]) {
			t.Fatalf("Solidity/Go operation ID mismatch nonce %d: %x vs %x (%v)", nonce, out, id, err)
		}
		if value := cfg.State.GetState(escrow, protocol.CommitmentSlot(id)); value != common.Hash(id) {
			t.Fatal("commitment differs at protocol slot", value)
		}
		if value := cfg.State.GetState(escrow, protocol.SenderNonceSlot(actor)); value != common.BigToHash(new(big.Int).SetUint64(nonce+1)) {
			t.Fatal("nonce slot mismatch", value)
		}
		if cfg.State.GetBalance(escrow).ToBig().Cmp(new(big.Int).Mul(amount, new(big.Int).SetUint64(nonce+1))) != 0 {
			t.Fatal("escrow backing mismatch")
		}
		logs := cfg.State.Logs()
		event := artifact.ABI.Events["Deposit"]
		if len(logs) != int(nonce+1) || logs[len(logs)-1].Address != escrow || len(logs[len(logs)-1].Topics) != 4 || logs[len(logs)-1].Topics[0] != event.ID || logs[len(logs)-1].Topics[1] != common.Hash(id) {
			t.Fatal("deposit event disagrees with storage commitment")
		}
		t.Logf("vector nonce=%d operationID=%x storageSlot=%x", nonce, id, protocol.CommitmentSlot(id))
	}
	if cfg.State.GetBalance(actor).ToBig().Cmp(new(big.Int).Sub(initial, new(big.Int).Mul(amount, big.NewInt(2)))) != 0 {
		t.Fatal("sender debit differs from locked principal")
	}
	// A different sender starts at nonce zero and cannot reuse the first sender's ID.
	cfg.Origin = recipient
	cfg.State.SetBalance(recipient, uint256.MustFromBig(amount), tracing.BalanceChangeUnspecified)
	out, err := call(t, artifact, cfg, escrow, amount, "deposit", actor)
	if err != nil || bytes.Equal(out, make([]byte, 32)) || cfg.State.GetState(escrow, protocol.SenderNonceSlot(recipient)) != common.BigToHash(big.NewInt(1)) {
		t.Fatal("per-sender nonce failed", err)
	}
}

func TestLondonDepositErrorsRollbackValueNonceAndLogs(t *testing.T) {
	artifact := compileEscrow(t)
	for _, mode := range []string{"zero_value", "zero_recipient", "nonce_overflow", "collision", "unknown_call"} {
		t.Run(mode, func(t *testing.T) {
			cfg := londonConfig()
			bridge := common.HexToHash("0x42")
			escrow := deploy(t, artifact, cfg, 990012, bridge)
			cfg.State.SetBalance(cfg.Origin, uint256.NewInt(1000), tracing.BalanceChangeUnspecified)
			recipient := common.HexToAddress("0x2000000000000000000000000000000000000002")
			amount := big.NewInt(10)
			if mode == "zero_value" {
				amount.SetInt64(0)
			}
			if mode == "zero_recipient" {
				recipient = common.Address{}
			}
			if mode == "nonce_overflow" {
				cfg.State.SetState(escrow, protocol.SenderNonceSlot(cfg.Origin), common.HexToHash(strings.Repeat("ff", 32)))
			}
			d := protocol.Deposit{SourceEVMChainID: 75, DestinationEVMChainID: 990012, SourceEscrow: escrow, DestinationBridgeID: bridge, Sender: cfg.Origin, Recipient: recipient}
			amount.FillBytes(d.Amount[:])
			if mode == "collision" {
				id, err := protocol.OperationID(d)
				if err != nil {
					t.Fatal(err)
				}
				cfg.State.SetState(escrow, protocol.CommitmentSlot(id), common.Hash(id))
			}
			beforeSender := cfg.State.GetBalance(cfg.Origin).ToBig()
			beforeVault := cfg.State.GetBalance(escrow).ToBig()
			beforeNonce := cfg.State.GetState(escrow, protocol.SenderNonceSlot(cfg.Origin))
			beforeLogs := len(cfg.State.Logs())
			var err error
			if mode == "unknown_call" {
				cfg.Value = amount
				cfg.GasLimit = transactionGasLimit - 21000
				_, _, err = runtime.Call(escrow, []byte{0xff, 0xff, 0xff, 0xff}, cfg)
			} else {
				_, err = call(t, artifact, cfg, escrow, amount, "deposit", recipient)
			}
			if err == nil {
				t.Fatal("invalid deposit succeeded")
			}
			if cfg.State.GetBalance(cfg.Origin).ToBig().Cmp(beforeSender) != 0 || cfg.State.GetBalance(escrow).ToBig().Cmp(beforeVault) != 0 || cfg.State.GetState(escrow, protocol.SenderNonceSlot(cfg.Origin)) != beforeNonce || len(cfg.State.Logs()) != beforeLogs {
				t.Fatal("reverted deposit changed principal, nonce or logs")
			}
		})
	}
}

func TestLondonWideUint256Commitment(t *testing.T) {
	artifact := compileEscrow(t)
	cfg := londonConfig()
	bridge := common.HexToHash("0x42")
	escrow := deploy(t, artifact, cfg, 990012, bridge)
	amount := new(big.Int).Lsh(big.NewInt(1), 200)
	nonce := new(big.Int).Lsh(big.NewInt(1), 180)
	cfg.State.SetBalance(cfg.Origin, uint256.MustFromBig(new(big.Int).Mul(amount, big.NewInt(2))), tracing.BalanceChangeUnspecified)
	cfg.State.SetState(escrow, protocol.SenderNonceSlot(cfg.Origin), common.BigToHash(nonce))
	d := protocol.Deposit{SourceEVMChainID: 75, DestinationEVMChainID: 990012, SourceEscrow: escrow,
		DestinationBridgeID: bridge, Sender: cfg.Origin, Recipient: cfg.Origin}
	amount.FillBytes(d.Amount[:])
	nonce.FillBytes(d.Nonce[:])
	id, err := protocol.OperationID(d)
	if err != nil {
		t.Fatal(err)
	}
	out, err := call(t, artifact, cfg, escrow, amount, "deposit", cfg.Origin)
	if err != nil || !bytes.Equal(out, id[:]) || cfg.State.GetState(escrow, protocol.CommitmentSlot(id)) != common.Hash(id) {
		t.Fatal("full uint256 amount or nonce was truncated", err)
	}
	if cfg.State.GetBalance(escrow).ToBig().Cmp(amount) != 0 ||
		cfg.State.GetState(escrow, protocol.SenderNonceSlot(cfg.Origin)) != common.BigToHash(new(big.Int).Add(nonce, big.NewInt(1))) {
		t.Fatal("full-width backing or next nonce mismatch")
	}
}

func TestLondonOuterRollbackAndSingleRetry(t *testing.T) {
	artifact := compileEscrow(t)
	cfg := londonConfig()
	bridge := common.HexToHash("0x42")
	escrow := deploy(t, artifact, cfg, 990012, bridge)
	cfg.State.SetBalance(cfg.Origin, uint256.NewInt(1000), tracing.BalanceChangeUnspecified)
	d := protocol.Deposit{SourceEVMChainID: 75, DestinationEVMChainID: 990012, SourceEscrow: escrow,
		DestinationBridgeID: bridge, Sender: cfg.Origin, Recipient: cfg.Origin}
	d.Amount[31] = 10
	id, err := protocol.OperationID(d)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := cfg.State.Snapshot()
	if _, err := call(t, artifact, cfg, escrow, big.NewInt(10), "deposit", cfg.Origin); err != nil {
		t.Fatal(err)
	}
	cfg.State.RevertToSnapshot(snapshot)
	if cfg.State.GetState(escrow, protocol.CommitmentSlot(id)) != (common.Hash{}) ||
		cfg.State.GetState(escrow, protocol.SenderNonceSlot(cfg.Origin)) != (common.Hash{}) ||
		!cfg.State.GetBalance(escrow).IsZero() || cfg.State.GetBalance(cfg.Origin).Uint64() != 1000 || len(cfg.State.Logs()) != 0 {
		t.Fatal("outer rollback left a deposit commitment, principal, nonce or event")
	}
	out, err := call(t, artifact, cfg, escrow, big.NewInt(10), "deposit", cfg.Origin)
	if err != nil || !bytes.Equal(out, id[:]) || cfg.State.GetBalance(escrow).Uint64() != 10 ||
		cfg.State.GetBalance(cfg.Origin).Uint64() != 990 || len(cfg.State.Logs()) != 1 ||
		cfg.State.GetState(escrow, protocol.SenderNonceSlot(cfg.Origin)) != common.BigToHash(big.NewInt(1)) {
		t.Fatal("retry did not create exactly one backed deposit", err)
	}
}

func TestLondonConstructorRejectsWrongDomains(t *testing.T) {
	artifact := compileEscrow(t)
	for _, mode := range []string{"same_chain", "zero_destination", "zero_bridge", "oversized_source", "zero_source"} {
		t.Run(mode, func(t *testing.T) {
			cfg := londonConfig()
			dest := uint64(990012)
			bridge := common.HexToHash("0x42")
			switch mode {
			case "same_chain":
				dest = 75
			case "zero_destination":
				dest = 0
			case "zero_bridge":
				bridge = common.Hash{}
			case "oversized_source":
				cfg.ChainConfig.ChainID = new(big.Int).Lsh(big.NewInt(1), 64)
			case "zero_source":
				cfg.ChainConfig.ChainID = new(big.Int)
			}
			args, err := artifact.ABI.Pack("", dest, bridge)
			if err != nil {
				t.Fatal(err)
			}
			code := append(append([]byte{}, artifact.Creation...), args...)
			cfg.GasLimit = transactionGasLimit - intrinsic(code, true)
			_, address, _, err := runtime.Create(code, cfg)
			if err == nil || len(cfg.State.GetCode(address)) != 0 {
				t.Fatal("invalid constructor installed executable escrow")
			}
		})
	}
}

func TestLondonProfileRejectsUnsupportedOpcodes(t *testing.T) {
	for name, code := range map[string]string{"PUSH0": "5f60005260206000f3", "TSTORE": "600160005d60005c60005260206000f3"} {
		t.Run(name, func(t *testing.T) {
			bytes, err := hex.DecodeString(code)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = runtime.Execute(bytes, nil, londonConfig())
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("London fixture unexpectedly accepted %s: %v", name, err)
			}
		})
	}
}
