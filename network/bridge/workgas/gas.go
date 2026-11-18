// Package workgas reserves deterministic SDK gas before bridge CPU work.
// This is a conservative experimental v1 tariff, not a benchmark-derived or
// production-calibrated schedule. Storage I/O and banking retain their normal
// SDK gas charges. Callers MUST charge every verification pass they execute.
// SDK out-of-gas panics intentionally propagate; this package never recovers
// them, performs cryptography, marshals protobufs, or authorizes a transfer.
package workgas

import (
	"errors"
	"math"
	"reflect"

	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	cmttypes "github.com/cometbft/cometbft/types"
)

const (
	TariffVersion           = "bridge/workgas/v1"
	BaseGas          uint64 = 1000
	ByteGas          uint64 = 1
	ValidatorGas     uint64 = 1000
	SignaturePassGas uint64 = 10000
	ProofNodeGas     uint64 = 3000
	DepositGas       uint64 = 5000

	MaxValidators     = 1000
	MaxBlockBytes     = 1024 * 1024
	MaxRecordBytes    = 2 * 1024 * 1024
	MaxAccountBytes   = 16 * 1024
	MaxProofOpBytes   = 32 * 1024
	MaxProofBytes     = 64 * 1024
	MaxProofOps       = 2
	MaxProofPathNodes = 128
)

// Meter is satisfied by the SDK GasMeter; it is not an application callback.
type Meter interface{ ConsumeGas(uint64, string) }

func base(m Meter, operation string) error {
	if m == nil {
		return errors.New("bridge work gas requires a meter")
	}
	value := reflect.ValueOf(m)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return errors.New("bridge work gas requires a nonnil meter")
		}
	}
	m.ConsumeGas(BaseGas, TariffVersion+"/"+operation+"/base")
	return nil
}

type item struct{ units, rate uint64 }

func reserve(m Meter, operation string, items ...item) error {
	var total uint64
	for _, charge := range items {
		if charge.rate != 0 && charge.units > math.MaxUint64/charge.rate {
			return errors.New("bridge work gas multiplication overflow")
		}
		cost := charge.units * charge.rate
		if total > math.MaxUint64-cost {
			return errors.New("bridge work gas addition overflow")
		}
		total += cost
	}
	m.ConsumeGas(total, TariffVersion+"/"+operation+"/reserved-work")
	return nil
}

// ChargeDecode reserves per-byte CPU work before decoding a stored client or
// route record. Bounds are checked using the length, never by parsing bytes.
func ChargeDecode(m Meter, recordBytes int) error {
	if err := base(m, "decode"); err != nil {
		return err
	}
	if recordBytes <= 0 || recordBytes > MaxRecordBytes {
		return errors.New("bridge record exceeds decode profile")
	}
	return reserve(m, "decode", item{uint64(recordBytes), ByteGas})
}

func blockBounds(block *cmttypes.LightBlock) (uint64, error) {
	if block == nil || block.SignedHeader == nil || block.Header == nil || block.Commit == nil || block.ValidatorSet == nil {
		return 0, errors.New("incomplete bridge light block")
	}
	validators := block.ValidatorSet.Validators
	if len(validators) == 0 || len(validators) > MaxValidators || len(block.Commit.Signatures) != len(validators) {
		return 0, errors.New("bridge light block exceeds validator/signature profile")
	}
	h := block.Header
	if len(h.ChainID) == 0 || len(h.ChainID) > 50 || len(h.ProposerAddress) > 20 {
		return 0, errors.New("oversized bridge header identity")
	}
	for _, hash := range [][]byte{h.LastBlockID.Hash, h.LastBlockID.PartSetHeader.Hash, h.LastCommitHash, h.DataHash,
		h.ValidatorsHash, h.NextValidatorsHash, h.ConsensusHash, h.AppHash, h.LastResultsHash, h.EvidenceHash,
		block.Commit.BlockID.Hash, block.Commit.BlockID.PartSetHeader.Hash} {
		if len(hash) > 32 {
			return 0, errors.New("oversized bridge header hash")
		}
	}
	// Only the already-supported fixed-size Ed25519 profile is inspected here.
	// Type assertions and lengths do not invoke PubKey hashing or verification.
	bounded := func(validator *cmttypes.Validator) bool {
		if validator == nil || len(validator.Address) > 20 {
			return false
		}
		key, ok := validator.PubKey.(ed25519.PubKey)
		return ok && len(key) == ed25519.PubKeySize
	}
	for _, validator := range validators {
		if !bounded(validator) {
			return 0, errors.New("oversized or unsupported bridge validator")
		}
	}
	if !bounded(block.ValidatorSet.Proposer) {
		return 0, errors.New("unsupported bridge proposer")
	}
	for _, signature := range block.Commit.Signatures {
		if len(signature.ValidatorAddress) > 20 || len(signature.Signature) > ed25519.SignatureSize {
			return 0, errors.New("oversized bridge commit signature")
		}
	}
	// With these bounds the fixed-size Comet fields fit below 1MiB. Reserve
	// the ENTIRE allowance instead of calling ToProto/Size before charging.
	return uint64(len(validators)), nil
}

// ChargeUpdateInput performs the cheap bounded candidate preflight before a
// client loads/decodes its current trust. ChargeUpdate is still required before
// the actual update verification. This intentionally charges an additional base.
func ChargeUpdateInput(m Meter, candidate *cmttypes.LightBlock) error {
	if err := base(m, "update-input"); err != nil {
		return err
	}
	_, err := blockBounds(candidate)
	return err
}

// ChargeBootstrap covers one complete VerifyCommit pass and validator handling,
// plus the full supported protobuf byte allowance, including absent signers.
func ChargeBootstrap(m Meter, block *cmttypes.LightBlock) error {
	if err := base(m, "bootstrap"); err != nil {
		return err
	}
	count, err := blockBounds(block)
	if err != nil {
		return err
	}
	return reserve(m, "bootstrap", item{MaxBlockBytes, ByteGas}, item{count, ValidatorGas}, item{count, SignaturePassGas})
}

// ChargeUpdate covers BOTH sets: the trusted VerifyCommit pass and up to three
// candidate signature passes (light verification, trusted overlap, full commit).
// It does not rely on Comet's mutable cached total power or early quorum exit.
func ChargeUpdate(m Meter, trusted, candidate *cmttypes.LightBlock) error {
	if err := base(m, "update"); err != nil {
		return err
	}
	oldCount, err := blockBounds(trusted)
	if err != nil {
		return err
	}
	newCount, err := blockBounds(candidate)
	if err != nil {
		return err
	}
	return reserve(m, "update", item{2 * MaxBlockBytes, ByteGas},
		item{oldCount, ValidatorGas}, item{newCount, ValidatorGas},
		item{oldCount, SignaturePassGas}, item{newCount, 3 * SignaturePassGas})
}

// ChargeMembership reserves two complete 128-node proof paths before any
// CommitmentProof protobuf decode. It reads only bounded outer lengths, never
// parses proof bytes or trusts a claimed path length inside them.
func ChargeMembership(m Meter, rawValue []byte, ops *cmtcrypto.ProofOps) error {
	if err := base(m, "membership"); err != nil {
		return err
	}
	if len(rawValue) == 0 || len(rawValue) > MaxAccountBytes || ops == nil || len(ops.Ops) != MaxProofOps {
		return errors.New("bridge membership inputs exceed bounded profile")
	}
	var proofBytes uint64
	for _, op := range ops.Ops {
		if len(op.Data) == 0 || len(op.Data) > MaxProofOpBytes || len(op.Key) > 53 || len(op.Type) > 32 {
			return errors.New("bridge membership operation exceeds bounded profile")
		}
		// Each addend is bounded above before conversion/addition.
		proofBytes += uint64(len(op.Data))
	}
	if proofBytes > MaxProofBytes {
		return errors.New("bridge membership proofs exceed total profile")
	}
	return reserve(m, "membership", item{uint64(len(rawValue)), ByteGas}, item{proofBytes, ByteGas},
		item{MaxProofOps * MaxProofPathNodes, ProofNodeGas})
}

// ChargeDeposit covers the fixed-format deposit hash and primitive CPU work.
// It does not replace bank or persistent KV gas accounting.
func ChargeDeposit(m Meter) error {
	if err := base(m, "deposit"); err != nil {
		return err
	}
	return reserve(m, "deposit", item{1, DepositGas})
}
