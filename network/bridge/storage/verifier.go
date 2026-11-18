// Package storage verifies Decimal-compatible ICS-23 EVM storage commitments.
// It does not establish source-chain trust, fetch RPC data, deploy escrow,
// authorize a transfer, or modify bank balances, supply, or replay state.
//
// Callers MUST obtain the checkpoint through finality.VerifyUpdate from their
// authenticated trust anchor. A manually constructed VerifiedCheckpoint does
// not establish trust merely because it has that exported Go type. Production
// callers must also pin the allowed source chain, escrow address, code policy,
// slot layout and commitment semantics independently of a submitted claim.
package storage

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"regexp"

	cmtcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	commitmenttypes "github.com/cosmos/ibc-go/v10/modules/core/23-commitment/types"

	"github.com/maxwell2010/go-smart-node/network/bridge/finality"
)

const (
	// DecimalEthermintStorageLayout identifies the fixed evm-store key and
	// proof profile. It does not select or authenticate a source chain.
	DecimalEthermintStorageLayout = "decimal-ethermint-storage-ics23-v1"

	// Bounds apply before CommitmentProof protobuf decoding. This profile
	// accepts exactly one IAVL membership proof and one multistore proof.
	MaxProofOpBytes = 32 * 1024
	MaxProofBytes   = 64 * 1024
	MaxInnerOps     = 128
)

var chainIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,49}$`)

// StorageClaim identifies one exact nonzero EVM commitment word at state H.
// Slot is the already computed Solidity slot, not a mapping key or its hex text.
type StorageClaim struct {
	ChainID     string
	StateHeight int64
	Address     [20]byte
	Slot        [32]byte
	Value       [32]byte
}

// VerifyStorageMembership proves claim.Value under the checkpoint AppHash.
// The state at H is committed by the authenticated Comet header at H+1.
// An error never authorizes any mint or release. Absence and zero words are
// deliberately outside this positive-commitment-only profile.
func VerifyStorageMembership(checkpoint finality.VerifiedCheckpoint, claim StorageClaim,
	ops *cmtcrypto.ProofOps) error {
	if !chainIDPattern.MatchString(claim.ChainID) || claim.ChainID != checkpoint.ChainID {
		return errors.New("storage claim chain differs from the authenticated checkpoint")
	}
	if claim.StateHeight <= 0 || claim.StateHeight == math.MaxInt64 || checkpoint.Height != claim.StateHeight+1 {
		return errors.New("require positive state height and authenticated header height H+1")
	}
	if claim.Address == ([20]byte{}) || claim.Value == ([32]byte{}) ||
		checkpoint.AppHash == ([32]byte{}) || checkpoint.Hash == ([32]byte{}) {
		return errors.New("require nonzero contract, commitment word and checkpoint hashes")
	}
	if ops == nil || len(ops.Ops) != 2 {
		return errors.New("require exactly two ICS-23 proof operations")
	}
	key := make([]byte, 1+len(claim.Address)+len(claim.Slot))
	key[0] = 0x02
	copy(key[1:21], claim.Address[:])
	copy(key[21:], claim.Slot[:])
	if ops.Ops[0].Type != "ics23:iavl" || !bytes.Equal(ops.Ops[0].Key, key) ||
		ops.Ops[1].Type != "ics23:simple" || !bytes.Equal(ops.Ops[1].Key, []byte("evm")) {
		return errors.New("unexpected ICS-23 operation type, order, storage key or store name")
	}
	total := 0
	for _, op := range ops.Ops {
		if len(op.Data) == 0 || len(op.Data) > MaxProofOpBytes {
			return errors.New("ICS-23 proof operation exceeds the permitted size")
		}
		total += len(op.Data)
	}
	if total > MaxProofBytes {
		return errors.New("ICS-23 proof exceeds the permitted total size")
	}
	proof, err := commitmenttypes.ConvertProofs(ops)
	if err != nil {
		return fmt.Errorf("decode ICS-23 proof: %w", err)
	}
	for _, entry := range proof.Proofs {
		member := entry.GetExist()
		if member == nil || member.Leaf == nil || len(member.Path) > MaxInnerOps {
			return errors.New("require bounded ordinary membership proofs only")
		}
		for _, inner := range member.Path {
			if inner == nil {
				return errors.New("membership path contains an undefined inner operation")
			}
		}
	}
	path := commitmenttypes.NewMerklePath([]byte("evm"), key)
	root := commitmenttypes.NewMerkleRoot(checkpoint.AppHash[:])
	if err := proof.VerifyMembership(commitmenttypes.GetSDKSpecs(), root, path, claim.Value[:]); err != nil {
		return fmt.Errorf("verify exact EVM storage commitment: %w", err)
	}
	return nil
}
