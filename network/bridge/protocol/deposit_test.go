package protocol

import (
	"bytes"
	"math"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func fixture() Deposit {
	d := Deposit{SourceEVMChainID: 75, DestinationEVMChainID: 990012}
	d.SourceEscrow[19], d.DestinationBridgeID[31], d.Sender[19], d.Recipient[19], d.Amount[31] = 1, 2, 3, 4, 5
	return d
}

func TestCanonicalDepositUsesNineABIWords(t *testing.T) {
	d := fixture()
	d.SourceEVMChainID = math.MaxUint64
	d.Amount[0] = 0x80
	d.Nonce[0] = 0xff
	word, _ := abi.NewType("bytes32", "", nil)
	number, _ := abi.NewType("uint256", "", nil)
	address, _ := abi.NewType("address", "", nil)
	args := abi.Arguments{{Type: word}, {Type: number}, {Type: number}, {Type: address}, {Type: word}, {Type: address}, {Type: address}, {Type: number}, {Type: number}}
	want, err := args.Pack(DomainHash(), new(big.Int).SetUint64(d.SourceEVMChainID), new(big.Int).SetUint64(d.DestinationEVMChainID),
		common.Address(d.SourceEscrow), d.DestinationBridgeID, common.Address(d.Sender), common.Address(d.Recipient), new(big.Int).SetBytes(d.Amount[:]), new(big.Int).SetBytes(d.Nonce[:]))
	if err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalRecord(d)
	if err != nil || len(want) != 288 || !bytes.Equal(got[:], want) {
		t.Fatalf("canonical ABI mismatch: %v", err)
	}
	id, err := OperationID(d)
	if err != nil || id != crypto.Keccak256Hash(want) {
		t.Fatal("operation ID mismatch", err)
	}
}

func TestDepositValidationAndDomainSeparation(t *testing.T) {
	original := fixture()
	base, _ := OperationID(original)
	mutations := map[string]func(*Deposit){
		"source": func(d *Deposit) { d.SourceEVMChainID++ }, "destination": func(d *Deposit) { d.DestinationEVMChainID++ },
		"escrow": func(d *Deposit) { d.SourceEscrow[0]++ }, "bridge": func(d *Deposit) { d.DestinationBridgeID[0]++ },
		"sender": func(d *Deposit) { d.Sender[0]++ }, "recipient": func(d *Deposit) { d.Recipient[0]++ },
		"amount": func(d *Deposit) { d.Amount[0]++ }, "nonce": func(d *Deposit) { d.Nonce[31]++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			d := original
			mutate(&d)
			id, err := OperationID(d)
			if err != nil || id == base {
				t.Fatal("domain was not separated", err)
			}
		})
	}
	invalid := []Deposit{{}, {SourceEVMChainID: 75, DestinationEVMChainID: 75}}
	for _, mutate := range []func(*Deposit){func(d *Deposit) { d.SourceEVMChainID = 0 }, func(d *Deposit) { d.DestinationEVMChainID = 0 },
		func(d *Deposit) { d.SourceEscrow = [20]byte{} }, func(d *Deposit) { d.DestinationBridgeID = [32]byte{} }, func(d *Deposit) { d.Sender = [20]byte{} },
		func(d *Deposit) { d.Recipient = [20]byte{} }, func(d *Deposit) { d.Amount = [32]byte{} }} {
		d := original
		mutate(&d)
		invalid = append(invalid, d)
	}
	for _, d := range invalid {
		if id, err := OperationID(d); err == nil || id != ([32]byte{}) {
			t.Fatal("invalid deposit accepted")
		}
	}
}

func TestCommitmentAndNonceMappingSlots(t *testing.T) {
	d := fixture()
	id, _ := OperationID(d)
	word, _ := abi.NewType("bytes32", "", nil)
	number, _ := abi.NewType("uint256", "", nil)
	address, _ := abi.NewType("address", "", nil)
	packed, err := (abi.Arguments{{Type: word}, {Type: number}}).Pack(id, new(big.Int))
	if err != nil || CommitmentSlot(id) != crypto.Keccak256Hash(packed) {
		t.Fatal("slot zero mapping mismatch", err)
	}
	packed, err = (abi.Arguments{{Type: address}, {Type: number}}).Pack(common.Address(d.Sender), big.NewInt(1))
	if err != nil || SenderNonceSlot(d.Sender) != crypto.Keccak256Hash(packed) {
		t.Fatal("slot one mapping mismatch", err)
	}
}
