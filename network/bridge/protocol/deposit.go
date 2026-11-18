// Package protocol defines the exact, versioned native-DEL deposit commitment.
// It does not establish finality, verify membership or authorize any payout.
package protocol

import (
	"encoding/binary"
	"errors"

	"github.com/ethereum/go-ethereum/crypto"
)

const DepositRecordBytes = 9 * 32
const DepositDomain = "MINTCANDY_DECIMAL_BRIDGE_V1_DEPOSIT"

// Deposit is a fixed-width record. Amount and Nonce are unsigned big-endian
// uint256 words. Zero is a valid nonce; Amount must be strictly positive.
// IDs use the network's supported uint64 EVM signing-domain profile.
type Deposit struct {
	SourceEVMChainID      uint64
	DestinationEVMChainID uint64
	SourceEscrow          [20]byte
	DestinationBridgeID   [32]byte
	Sender                [20]byte
	Recipient             [20]byte
	Amount                [32]byte
	Nonce                 [32]byte
}

func DomainHash() [32]byte { return crypto.Keccak256Hash([]byte(DepositDomain)) }

func (d Deposit) Validate() error {
	if d.SourceEVMChainID == 0 || d.DestinationEVMChainID == 0 || d.SourceEVMChainID == d.DestinationEVMChainID {
		return errors.New("deposit requires distinct positive EVM chain IDs")
	}
	if d.SourceEscrow == ([20]byte{}) || d.DestinationBridgeID == ([32]byte{}) ||
		d.Sender == ([20]byte{}) || d.Recipient == ([20]byte{}) || d.Amount == ([32]byte{}) {
		return errors.New("deposit requires nonzero escrow, destination bridge, sender, recipient and amount")
	}
	return nil
}

// CanonicalRecord encodes exactly Solidity abi.encode(domain, sourceEVM,
// destinationEVM, sourceEscrow, destinationBridge, sender, recipient, amount,
// nonce): nine 32-byte words, not abi.encodePacked and not JSON.
func CanonicalRecord(d Deposit) ([DepositRecordBytes]byte, error) {
	var out [DepositRecordBytes]byte
	if err := d.Validate(); err != nil {
		return out, err
	}
	domain := DomainHash()
	copy(out[0:32], domain[:])
	binary.BigEndian.PutUint64(out[56:64], d.SourceEVMChainID)
	binary.BigEndian.PutUint64(out[88:96], d.DestinationEVMChainID)
	copy(out[108:128], d.SourceEscrow[:])
	copy(out[128:160], d.DestinationBridgeID[:])
	copy(out[172:192], d.Sender[:])
	copy(out[204:224], d.Recipient[:])
	copy(out[224:256], d.Amount[:])
	copy(out[256:288], d.Nonce[:])
	return out, nil
}

func OperationID(d Deposit) ([32]byte, error) {
	record, err := CanonicalRecord(d)
	if err != nil {
		return [32]byte{}, err
	}
	return crypto.Keccak256Hash(record[:]), nil
}

// CommitmentSlot is the EVM storage slot of mapping(bytes32=>bytes32) at slot
// zero. The expected value at this exact slot is the operation ID itself.
func CommitmentSlot(operationID [32]byte) [32]byte {
	var preimage [64]byte
	copy(preimage[:32], operationID[:])
	return crypto.Keccak256Hash(preimage[:])
}

// SenderNonceSlot is mapping(address=>uint256) at slot one, for EVM tests and
// reproducible layout checks. A nonce observation never authorizes a transfer.
func SenderNonceSlot(sender [20]byte) [32]byte {
	var preimage [64]byte
	copy(preimage[12:32], sender[:])
	preimage[63] = 1
	return crypto.Keccak256Hash(preimage[:])
}
