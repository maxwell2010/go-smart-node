package app

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	registrationProofTestChain = "mintcandy_990001-1"
	registrationProofTestEVM   = 990001
)

func validatorRegistrationProofFixture(t *testing.T, commission uint8) (nativeValidatorRegistration, []byte) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	candidate := nativeValidatorRegistration{
		Operator: [20]byte{19: 3}, ExpiresHeight: 50, CommissionPercent: commission,
	}
	copy(candidate.ConsensusPubKey[:], key.Public().(ed25519.PublicKey))
	message, err := candidate.signBytes(registrationProofTestChain, registrationProofTestEVM)
	require.NoError(t, err)
	data := binary.BigEndian.AppendUint64(nil, uint64(candidate.ExpiresHeight))
	data = append(data, candidate.CommissionPercent)
	data = append(data, candidate.ConsensusPubKey[:]...)
	data = append(data, ed25519.Sign(key, message)...)
	return candidate, data
}

func encodeValidatorRegistrationProofForTest(data []byte) string {
	return nativeValidatorRegistrationSchema + base64.RawURLEncoding.EncodeToString(data)
}

func TestValidateNativeValidatorRegistrationProposal(t *testing.T) {
	for _, commission := range []uint8{0, 7, 100} {
		want, data := validatorRegistrationProofFixture(t, commission)
		metadata := encodeValidatorRegistrationProofForTest(data)
		require.LessOrEqual(t, len(metadata), 255)
		got, err := validateNativeValidatorRegistrationProposal(metadata, registrationProofTestChain, registrationProofTestEVM, want.Operator[:], want.ExpiresHeight-1)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, want, *got)
	}
	got, err := validateNativeValidatorRegistrationProposal("ordinary governance text", "", 0, nil, 0)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestNativeValidatorRegistrationProofBindsTermsAndSigningDomains(t *testing.T) {
	candidate, data := validatorRegistrationProofFixture(t, 7)
	metadata := encodeValidatorRegistrationProofForTest(data)
	otherOperator := [20]byte{19: 4}
	for _, test := range []struct {
		name     string
		chain    string
		evm      uint64
		operator []byte
	}{
		{"other chain revision", "mintcandy_990001-2", 990001, candidate.Operator[:]},
		{"other signing domains", "mintcandy_990002-1", 990002, candidate.Operator[:]},
		{"different operator", registrationProofTestChain, registrationProofTestEVM, otherOperator[:]},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := validateNativeValidatorRegistrationProposal(metadata, test.chain, test.evm, test.operator, 1)
			require.Nil(t, got)
			require.ErrorContains(t, err, "consensus ownership proof")
		})
	}
	_, err := validateNativeValidatorRegistrationProposal(metadata, registrationProofTestChain, 990002, candidate.Operator[:], 1)
	require.ErrorContains(t, err, "chain IDs do not match")

	changed := bytes.Clone(data)
	changed[8]++
	_, err = validateNativeValidatorRegistrationProposal(encodeValidatorRegistrationProofForTest(changed), registrationProofTestChain, registrationProofTestEVM, candidate.Operator[:], 1)
	require.ErrorContains(t, err, "consensus ownership proof")
	changed = bytes.Clone(data)
	binary.BigEndian.PutUint64(changed[:8], uint64(candidate.ExpiresHeight+1))
	_, err = validateNativeValidatorRegistrationProposal(encodeValidatorRegistrationProofForTest(changed), registrationProofTestChain, registrationProofTestEVM, candidate.Operator[:], 1)
	require.ErrorContains(t, err, "consensus ownership proof")
}

func TestNativeValidatorRegistrationRejectsMalformedAndExpiredProofs(t *testing.T) {
	candidate, data := validatorRegistrationProofFixture(t, 7)
	metadata := encodeValidatorRegistrationProofForTest(data)
	validate := func(input string, operator []byte, height int64) error {
		got, err := validateNativeValidatorRegistrationProposal(input, registrationProofTestChain, registrationProofTestEVM, operator, height)
		require.Nil(t, got)
		return err
	}
	require.ErrorContains(t, validate(metadata, candidate.Operator[:], candidate.ExpiresHeight), "expired")
	require.ErrorContains(t, validate(metadata, candidate.Operator[:], 0), "execution height")
	require.ErrorContains(t, validate(metadata, candidate.Operator[:19], 1), "20-byte proposer")
	require.ErrorContains(t, validate(metadata, make([]byte, 20), 1), "operator, expiry or commission")
	require.ErrorContains(t, validate(nativeValidatorRegistrationNamespace+"v2:", candidate.Operator[:], 1), "unsupported")
	require.ErrorContains(t, validate(metadata+"=", candidate.Operator[:], 1), "metadata size")
	require.ErrorContains(t, validate(metadata[:len(metadata)-1]+"!", candidate.Operator[:], 1), "metadata encoding")

	changed := bytes.Clone(data)
	changed[8] = 101
	require.ErrorContains(t, validate(encodeValidatorRegistrationProofForTest(changed), candidate.Operator[:], 1), "operator, expiry or commission")
	changed = bytes.Clone(data)
	binary.BigEndian.PutUint64(changed[:8], ^uint64(0))
	require.ErrorContains(t, validate(encodeValidatorRegistrationProofForTest(changed), candidate.Operator[:], 1), "operator, expiry or commission")
	changed = bytes.Clone(data)
	key := changed[nativeValidatorRegistrationHeader:nativeValidatorRegistrationKeyEnd]
	clear(key)
	require.False(t, validNativeValidatorRegistrationKey(key))
	require.ErrorContains(t, validate(encodeValidatorRegistrationProofForTest(changed), candidate.Operator[:], 1), "consensus ownership proof")
}
