package app

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"

	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/oasisprotocol/curve25519-voi/curve"
)

const (
	nativeValidatorRegistrationNamespace = "mintcandy-validator-registration/"
	nativeValidatorRegistrationSchema    = nativeValidatorRegistrationNamespace + "v1:"
	nativeValidatorRegistrationPoPDomain = "mintcandy:validator-registration-pop:v1\x00"
	nativeValidatorRegistrationHeader    = 8 + 1
	nativeValidatorRegistrationKeyEnd    = nativeValidatorRegistrationHeader + ed25519.PublicKeySize
	nativeValidatorRegistrationWireSize  = nativeValidatorRegistrationKeyEnd + ed25519.SignatureSize
)

// nativeValidatorRegistration is an authenticated V1 candidate. The native
// operator and reward account derive from the same authenticated proposer
// bytes. ExpiresHeight is the first height where the proof is invalid.
type nativeValidatorRegistration struct {
	Operator          [20]byte
	ConsensusPubKey   [ed25519.PublicKeySize]byte
	ExpiresHeight     int64
	CommissionPercent uint8
}

// BuildNativeValidatorRegistrationMetadata signs a registration ownership
// proof for the proposer account with its separate Ed25519 consensus key.
func BuildNativeValidatorRegistrationMetadata(chainID string, evmChainID uint64, proposer []byte, expiresHeight int64, commissionPercent uint8, key ed25519.PrivateKey) (string, error) {
	if len(key) != ed25519.PrivateKeySize {
		return "", errors.New("invalid Ed25519 consensus private key size")
	}
	canonicalKey := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
	defer clear(canonicalKey)
	if !key.Equal(canonicalKey) {
		return "", errors.New("Ed25519 consensus private key does not match its public key")
	}
	candidate := nativeValidatorRegistration{ExpiresHeight: expiresHeight, CommissionPercent: commissionPercent}
	if len(proposer) != len(candidate.Operator) {
		return "", errors.New("native validator registration requires a 20-byte proposer address")
	}
	copy(candidate.Operator[:], proposer)
	copy(candidate.ConsensusPubKey[:], key[ed25519.SeedSize:])
	message, err := candidate.signBytes(chainID, evmChainID)
	if err != nil {
		return "", err
	}
	if candidate.ExpiresHeight <= 1 {
		return "", errors.New("native validator registration proof must expire after height 1")
	}
	data := binary.BigEndian.AppendUint64(make([]byte, 0, nativeValidatorRegistrationWireSize), uint64(candidate.ExpiresHeight))
	data = append(data, candidate.CommissionPercent)
	data = append(data, candidate.ConsensusPubKey[:]...)
	data = append(data, ed25519.Sign(key, message)...)
	return nativeValidatorRegistrationSchema + base64.RawURLEncoding.EncodeToString(data), nil
}

// validateNativeValidatorRegistrationProposal authenticates only the versioned
// registration metadata. The caller provides the SDK-authenticated proposer's
// raw address and must check registration uniqueness against state.
func validateNativeValidatorRegistrationProposal(metadata, chainID string, evmChainID uint64, proposer []byte, height int64) (*nativeValidatorRegistration, error) {
	if !strings.HasPrefix(metadata, nativeValidatorRegistrationNamespace) {
		return nil, nil
	}
	encoded, ok := strings.CutPrefix(metadata, nativeValidatorRegistrationSchema)
	if !ok {
		return nil, errors.New("unsupported native validator registration proposal schema")
	}
	// The compact canonical envelope fits the SDK's 255-byte metadata cap.
	encoding := base64.RawURLEncoding.Strict()
	if len(encoded) != encoding.EncodedLen(nativeValidatorRegistrationWireSize) {
		return nil, errors.New("invalid native validator registration metadata size")
	}
	data, err := encoding.DecodeString(encoded)
	if err != nil || len(data) != nativeValidatorRegistrationWireSize || encoding.EncodeToString(data) != encoded {
		return nil, errors.New("invalid native validator registration metadata encoding")
	}
	candidate := nativeValidatorRegistration{
		ExpiresHeight: int64(binary.BigEndian.Uint64(data[:8])), CommissionPercent: data[8],
	}
	if len(proposer) != len(candidate.Operator) {
		return nil, errors.New("native validator registration requires a 20-byte proposer address")
	}
	copy(candidate.Operator[:], proposer)
	copy(candidate.ConsensusPubKey[:], data[nativeValidatorRegistrationHeader:nativeValidatorRegistrationKeyEnd])
	message, err := candidate.signBytes(chainID, evmChainID)
	if err != nil {
		return nil, err
	}
	if height <= 0 || height >= candidate.ExpiresHeight {
		return nil, errors.New("native validator registration proof is expired or has no valid execution height")
	}
	publicKey := candidate.ConsensusPubKey[:]
	if !validNativeValidatorRegistrationKey(publicKey) ||
		!ed25519.Verify(ed25519.PublicKey(publicKey), message, data[nativeValidatorRegistrationKeyEnd:]) {
		return nil, errors.New("invalid native validator registration consensus ownership proof")
	}
	return &candidate, nil
}

func (m nativeValidatorRegistration) signBytes(chainID string, evmChainID uint64) ([]byte, error) {
	if err := identity.ValidateChainIDs(chainID, evmChainID); err != nil {
		return nil, err
	}
	if m.Operator == ([20]byte{}) || m.ExpiresHeight <= 0 || m.CommissionPercent > 100 {
		return nil, errors.New("invalid native validator registration operator, expiry or commission")
	}
	message := []byte(nativeValidatorRegistrationPoPDomain)
	message = append(message, byte(len(chainID)))
	message = append(message, chainID...)
	message = binary.BigEndian.AppendUint64(message, evmChainID)
	message = append(message, m.Operator[:]...)
	message = binary.BigEndian.AppendUint64(message, uint64(m.ExpiresHeight))
	message = append(message, m.CommissionPercent)
	return append(message, m.ConsensusPubKey[:]...), nil
}

func validNativeValidatorRegistrationKey(publicKey []byte) bool {
	var compressed curve.CompressedEdwardsY
	if _, err := compressed.SetBytes(publicKey); err != nil || !compressed.IsCanonicalVartime() {
		return false
	}
	var point curve.EdwardsPoint
	if _, err := point.SetCompressedY(&compressed); err != nil {
		return false
	}
	// Consensus vote verification permits more encodings. Ownership requires
	// a canonical non-identity point in the prime-order subgroup.
	return !point.IsIdentity() && point.IsTorsionFree()
}
