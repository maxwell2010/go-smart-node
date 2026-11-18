package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	cmtcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	ics23 "github.com/cosmos/ics23/go"

	"github.com/maxwell2010/go-smart-node/network/bridge/finality"
)

// This fixed RPC fixture tests Merkle-format conformance ONLY. Its header was
// not authenticated by a light-client update and is NOT a bootstrap anchor or
// bridge deposit. It reads the existing ContractCenter implementation slot.
func historicalConformanceFixture(t *testing.T) (finality.VerifiedCheckpoint, StorageClaim, *cmtcrypto.ProofOps) {
	t.Helper()
	body, err := os.ReadFile("testdata/decimal_contract_center_34604902.json")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(body)) != "a6162a20e4606c7e921fa0a8350dfd05b64b1421a842f9b5c8f240133d349103" {
		t.Fatal("historical public conformance fixture changed")
	}
	var fixture struct {
		Purpose         string `json:"purpose"`
		ChainID         string `json:"chain_id"`
		StateHeight     int64  `json:"state_height"`
		Address         string `json:"address"`
		Slot            string `json:"slot"`
		Value           string `json:"value"`
		HeaderBlockHash string `json:"header_block_hash"`
		Header          struct {
			ChainID string    `json:"chain_id"`
			Height  string    `json:"height"`
			AppHash string    `json:"app_hash"`
			Time    time.Time `json:"time"`
		} `json:"header"`
		ProofOps cmtcrypto.ProofOps `json:"proof_ops"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	decode := func(dst []byte, text string) {
		t.Helper()
		value, err := hex.DecodeString(text)
		if err != nil || len(value) != len(dst) {
			t.Fatal("invalid fixed hexadecimal fixture field")
		}
		copy(dst, value)
	}
	height, err := strconv.ParseInt(fixture.Header.Height, 10, 64)
	if err != nil || height != fixture.StateHeight+1 || fixture.Header.ChainID != fixture.ChainID {
		t.Fatal("invalid fixture header/state height relationship")
	}
	checkpoint := finality.VerifiedCheckpoint{Checkpoint: finality.Checkpoint{
		ChainID: fixture.ChainID, Height: height}, Time: fixture.Header.Time}
	decode(checkpoint.Hash[:], fixture.HeaderBlockHash)
	decode(checkpoint.AppHash[:], fixture.Header.AppHash)
	claim := StorageClaim{ChainID: fixture.ChainID, StateHeight: fixture.StateHeight}
	decode(claim.Address[:], fixture.Address)
	decode(claim.Slot[:], fixture.Slot)
	decode(claim.Value[:], fixture.Value)
	if len(fixture.Purpose) == 0 || claim.Value == ([32]byte{}) {
		t.Fatal("fixture must explicitly identify its purpose and a nonzero storage word")
	}
	return checkpoint, claim, &fixture.ProofOps
}

func TestHistoricalDecimalMembershipConformanceNotAuthenticatedBridge(t *testing.T) {
	checkpoint, claim, ops := historicalConformanceFixture(t)
	if err := VerifyStorageMembership(checkpoint, claim, ops); err != nil {
		t.Fatalf("historical Decimal ICS-23 membership conformance failed: %v", err)
	}
}

func TestStorageRejectsTamperedClaimAndCheckpoint(t *testing.T) {
	tests := map[string]func(*finality.VerifiedCheckpoint, *StorageClaim){
		"value":            func(_ *finality.VerifiedCheckpoint, claim *StorageClaim) { claim.Value[0] ^= 1 },
		"address":          func(_ *finality.VerifiedCheckpoint, claim *StorageClaim) { claim.Address[0] ^= 1 },
		"slot":             func(_ *finality.VerifiedCheckpoint, claim *StorageClaim) { claim.Slot[0] ^= 1 },
		"claim_chain":      func(_ *finality.VerifiedCheckpoint, claim *StorageClaim) { claim.ChainID = "other-1" },
		"checkpoint_chain": func(cp *finality.VerifiedCheckpoint, _ *StorageClaim) { cp.ChainID = "other-1" },
		"state_height":     func(_ *finality.VerifiedCheckpoint, claim *StorageClaim) { claim.StateHeight-- },
		"header_height":    func(cp *finality.VerifiedCheckpoint, _ *StorageClaim) { cp.Height-- },
		"height_overflow":  func(_ *finality.VerifiedCheckpoint, claim *StorageClaim) { claim.StateHeight = math.MaxInt64 },
		"zero_height":      func(cp *finality.VerifiedCheckpoint, claim *StorageClaim) { claim.StateHeight, cp.Height = 0, 1 },
		"zero_value":       func(_ *finality.VerifiedCheckpoint, claim *StorageClaim) { claim.Value = [32]byte{} },
		"zero_address":     func(_ *finality.VerifiedCheckpoint, claim *StorageClaim) { claim.Address = [20]byte{} },
		"root":             func(cp *finality.VerifiedCheckpoint, _ *StorageClaim) { cp.AppHash[0] ^= 1 },
		"zero_root":        func(cp *finality.VerifiedCheckpoint, _ *StorageClaim) { cp.AppHash = [32]byte{} },
		"zero_header_hash": func(cp *finality.VerifiedCheckpoint, _ *StorageClaim) { cp.Hash = [32]byte{} },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			checkpoint, claim, ops := historicalConformanceFixture(t)
			change(&checkpoint, &claim)
			if err := VerifyStorageMembership(checkpoint, claim, ops); err == nil {
				t.Fatal("tampered storage claim was accepted")
			}
		})
	}
}

func TestStorageRejectsAbsenceAndUnboundedPathBeforeMembership(t *testing.T) {
	for _, name := range []string{"absence", "path_depth"} {
		t.Run(name, func(t *testing.T) {
			checkpoint, claim, ops := historicalConformanceFixture(t)
			var decoded ics23.CommitmentProof
			if err := decoded.Unmarshal(ops.Ops[0].Data); err != nil {
				t.Fatal(err)
			}
			if name == "absence" {
				decoded.Proof = &ics23.CommitmentProof_Nonexist{Nonexist: &ics23.NonExistenceProof{
					Key: ops.Ops[0].Key}}
			} else {
				member := decoded.GetExist()
				if member == nil || len(member.Path) == 0 {
					t.Fatal("expected ordinary membership path in public fixture")
				}
				for len(member.Path) <= MaxInnerOps {
					member.Path = append(member.Path, member.Path[0])
				}
			}
			var err error
			ops.Ops[0].Data, err = decoded.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyStorageMembership(checkpoint, claim, ops); err == nil {
				t.Fatal("nonmembership or excessive-depth proof was accepted as a commitment")
			}
		})
	}
}

func TestStorageRejectsMalformedProofOperations(t *testing.T) {
	tests := map[string]func(*cmtcrypto.ProofOps){
		"empty":             func(ops *cmtcrypto.ProofOps) { ops.Ops = nil },
		"extra":             func(ops *cmtcrypto.ProofOps) { ops.Ops = append(ops.Ops, ops.Ops[1]) },
		"reversed":          func(ops *cmtcrypto.ProofOps) { ops.Ops[0], ops.Ops[1] = ops.Ops[1], ops.Ops[0] },
		"storage_type":      func(ops *cmtcrypto.ProofOps) { ops.Ops[0].Type = "ics23:simple" },
		"store_type":        func(ops *cmtcrypto.ProofOps) { ops.Ops[1].Type = "ics23:iavl" },
		"storage_key":       func(ops *cmtcrypto.ProofOps) { ops.Ops[0].Key[1] ^= 1 },
		"store_key":         func(ops *cmtcrypto.ProofOps) { ops.Ops[1].Key = []byte("ibc") },
		"empty_data":        func(ops *cmtcrypto.ProofOps) { ops.Ops[0].Data = nil },
		"truncated_storage": func(ops *cmtcrypto.ProofOps) { ops.Ops[0].Data = []byte{0x0a, 0xff} },
		"truncated_store":   func(ops *cmtcrypto.ProofOps) { ops.Ops[1].Data = ops.Ops[1].Data[:len(ops.Ops[1].Data)-1] },
		"oversized":         func(ops *cmtcrypto.ProofOps) { ops.Ops[0].Data = make([]byte, MaxProofOpBytes+1) },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			checkpoint, claim, ops := historicalConformanceFixture(t)
			change(ops)
			if err := VerifyStorageMembership(checkpoint, claim, ops); err == nil {
				t.Fatal("malformed storage proof was accepted")
			}
		})
	}
	cp, claim, _ := historicalConformanceFixture(t)
	if err := VerifyStorageMembership(cp, claim, nil); err == nil {
		t.Fatal("nil proof was accepted")
	}
}

func TestStorageRejectsEmptySerializedMembershipWithoutPanic(t *testing.T) {
	for _, name := range []string{"empty_exist", "empty_leaf", "empty_inner_path"} {
		t.Run(name, func(t *testing.T) {
			checkpoint, claim, ops := historicalConformanceFixture(t)
			var decoded ics23.CommitmentProof
			if err := decoded.Unmarshal(ops.Ops[0].Data); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "empty_exist":
				decoded.Proof = &ics23.CommitmentProof_Exist{Exist: &ics23.ExistenceProof{}}
			case "empty_leaf":
				decoded.GetExist().Leaf = &ics23.LeafOp{}
			case "empty_inner_path":
				decoded.GetExist().Path = append(decoded.GetExist().Path, &ics23.InnerOp{})
			}
			var err error
			ops.Ops[0].Data, err = decoded.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("serialized malformed membership panicked: %v", recovered)
				}
			}()
			if err := VerifyStorageMembership(checkpoint, claim, ops); err == nil {
				t.Fatal("empty serialized membership operation was accepted")
			}
		})
	}
}
