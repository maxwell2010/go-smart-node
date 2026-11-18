package storage

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	cmtcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	commitmenttypes "github.com/cosmos/ibc-go/v10/modules/core/23-commitment/types"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/maxwell2010/go-smart-node/network/bridge/finality"
)

const (
	// MaxAccountAnyBytes bounds the legacy account value before proof checking
	// or protobuf interpretation. This does not permit arbitrary Any unpacking.
	MaxAccountAnyBytes  = 16 * 1024
	legacyEthAccountURL = "/ethermint.types.v1.EthAccount"
)

// AccountCodeClaim contains trusted route policy, not user-selectable code
// authorization. Production callers MUST load Address and ExpectedCodeHash from
// immutable route state and obtain checkpoint through the stored light client.
// A code hash pins bytes; auditing the approved runtime as non-proxy/immutable
// remains a separate policy decision, not an assertion this function proves.
type AccountCodeClaim struct {
	ChainID          string
	StateHeight      int64
	Address          [20]byte
	ExpectedCodeHash [32]byte
}

// VerifyAccountCodeMembership proves exact raw account bytes under the same
// H+1 AppHash as storage, then interprets the legacy EthAccount code hash.
// It never accepts the JSON-RPC codeHash field or an unauthenticated RPC root.
// This is an internal primitive: constructing VerifiedCheckpoint alone does
// not establish trust. It does not deploy code or authorize mint/release.
func VerifyAccountCodeMembership(checkpoint finality.VerifiedCheckpoint, claim AccountCodeClaim,
	accountAny []byte, ops *cmtcrypto.ProofOps) error {
	if !chainIDPattern.MatchString(claim.ChainID) || claim.ChainID != checkpoint.ChainID ||
		claim.StateHeight <= 0 || claim.StateHeight == math.MaxInt64 || checkpoint.Height != claim.StateHeight+1 {
		return errors.New("account claim requires authenticated chain and header H+1")
	}
	if claim.Address == ([20]byte{}) || checkpoint.Hash == ([32]byte{}) || checkpoint.AppHash == ([32]byte{}) ||
		claim.ExpectedCodeHash == ([32]byte{}) || claim.ExpectedCodeHash == [32]byte(gethcrypto.Keccak256Hash(nil)) {
		return errors.New("require pinned contract code hash and nonzero checkpoint")
	}
	if len(accountAny) == 0 || len(accountAny) > MaxAccountAnyBytes || ops == nil || len(ops.Ops) != 2 {
		return errors.New("account membership inputs exceed bounded profile")
	}
	key := make([]byte, 21)
	key[0] = 0x01
	copy(key[1:], claim.Address[:])
	if ops.Ops[0].Type != "ics23:iavl" || !bytes.Equal(ops.Ops[0].Key, key) ||
		ops.Ops[1].Type != "ics23:simple" || !bytes.Equal(ops.Ops[1].Key, []byte("acc")) {
		return errors.New("account proof operations do not identify the exact acc store key")
	}
	total := 0
	for _, op := range ops.Ops {
		if len(op.Data) == 0 || len(op.Data) > MaxProofOpBytes {
			return errors.New("account proof operation exceeds permitted size")
		}
		total += len(op.Data)
	}
	if total > MaxProofBytes {
		return errors.New("account proof exceeds total size")
	}
	proof, err := commitmenttypes.ConvertProofs(ops)
	if err != nil {
		return fmt.Errorf("decode account ICS-23 proof: %w", err)
	}
	for _, entry := range proof.Proofs {
		member := entry.GetExist()
		if member == nil || member.Leaf == nil || len(member.Path) > MaxInnerOps {
			return errors.New("account proof requires bounded ordinary membership")
		}
		for _, inner := range member.Path {
			if inner == nil {
				return errors.New("undefined account proof inner operation")
			}
		}
	}
	path := commitmenttypes.NewMerklePath([]byte("acc"), key)
	if err := proof.VerifyMembership(commitmenttypes.GetSDKSpecs(),
		commitmenttypes.NewMerkleRoot(checkpoint.AppHash[:]), path, accountAny); err != nil {
		return fmt.Errorf("authenticate raw legacy account bytes: %w", err)
	}
	// Interpret only the authenticated bytes. This adapter reads the fixed
	// SDK0.46/Ethermint0.22 wire schema without importing the legacy SDK family.
	anyFields, err := legacyAccountFields(accountAny, []protowire.Type{0, protowire.BytesType, protowire.BytesType})
	if err != nil || string(anyFields[1].data) != legacyEthAccountURL || !anyFields[2].present {
		return errors.New("authenticated account is not the supported legacy EthAccount Any")
	}
	eth, err := legacyAccountFields(anyFields[2].data, []protowire.Type{0, protowire.BytesType, protowire.BytesType})
	if err != nil || !eth[1].present || !eth[2].present {
		return errors.New("invalid authenticated EthAccount envelope")
	}
	base, err := legacyAccountFields(eth[1].data,
		[]protowire.Type{0, protowire.BytesType, protowire.BytesType, protowire.VarintType, protowire.VarintType})
	if err != nil || !base[1].present {
		return errors.New("invalid authenticated legacy BaseAccount")
	}
	prefix, address, err := bech32.DecodeAndConvert(string(base[1].data))
	if err != nil || prefix != "d0" || len(address) != 20 || !bytes.Equal(address, claim.Address[:]) {
		return errors.New("authenticated account address differs from pinned Decimal contract")
	}
	text := string(eth[2].data)
	if len(text) != 66 || text[:2] != "0x" {
		return errors.New("legacy account code hash is not a strict 32-byte hex value")
	}
	codeHash, err := hex.DecodeString(text[2:])
	if err != nil || !bytes.Equal(codeHash, claim.ExpectedCodeHash[:]) {
		return errors.New("authenticated contract code hash differs from immutable route policy")
	}
	return nil
}

type legacyAccountField struct {
	present bool
	data    []byte
}

// Strict bounded wire adapter for Any, EthAccount and BaseAccount only. Known
// optional BaseAccount pub_key bytes remain opaque; they confer no authority.
// Unknown/duplicate fields and nonminimal protobuf encodings are rejected.
func legacyAccountFields(raw []byte, schema []protowire.Type) ([]legacyAccountField, error) {
	fields := make([]legacyAccountField, len(schema))
	fail := func() ([]legacyAccountField, error) {
		return fields, errors.New("unsupported or malformed legacy account wire encoding")
	}
	if len(raw) > MaxAccountAnyBytes {
		return fail()
	}
	for len(raw) > 0 {
		number, wire, size := protowire.ConsumeTag(raw)
		if size < 0 || number <= 0 || int(number) >= len(schema) || fields[number].present || wire != schema[number] {
			return fail()
		}
		if !bytes.Equal(raw[:size], protowire.AppendTag(nil, number, wire)) {
			return fail()
		}
		raw = raw[size:]
		switch wire {
		case protowire.BytesType:
			value, n := protowire.ConsumeBytes(raw)
			if n < 0 || n-len(value) != protowire.SizeVarint(uint64(len(value))) {
				return fail()
			}
			fields[number] = legacyAccountField{present: true, data: value}
			raw = raw[n:]
		case protowire.VarintType:
			value, n := protowire.ConsumeVarint(raw)
			if n < 0 || n != protowire.SizeVarint(value) {
				return fail()
			}
			fields[number].present = true
			raw = raw[n:]
		default:
			return fail()
		}
	}
	return fields, nil
}
