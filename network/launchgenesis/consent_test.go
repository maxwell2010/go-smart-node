package launchgenesis

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func consentFixture(t *testing.T) (consentManifest, []byte, map[string]*ecdsa.PrivateKey) {
	t.Helper()
	validators := make([]validatorDeclaration, 0, 4)
	delegations := make([]validatortypes.Delegation, 0, 4)
	keys := make(map[string]*ecdsa.PrivateKey, 4)
	for index := range 4 {
		key, err := crypto.GenerateKey()
		require.NoError(t, err)
		operator, err := address.NewBech32Codec("d0valoper").BytesToString(crypto.PubkeyToAddress(key.PublicKey).Bytes())
		require.NoError(t, err)
		row := validatorDeclaration{Operator: operator, Online: true, Commission: sdkmath.LegacyMustNewDecFromStr("0.05")}
		row.PubKey.Type = "/cosmos.crypto.ed25519.PubKey"
		row.PubKey.Key = bytes.Repeat([]byte{byte(index + 1)}, 32)
		validators = append(validators, row)
		delegations = append(delegations, validatortypes.Delegation{Validator: operator, Stake: validatortypes.Stake{
			Type: validatortypes.StakeType_Coin, Stake: sdk.NewInt64Coin("del", 10000),
		}})
		keys[operator] = key
	}
	state, err := json.Marshal(map[string]any{"validator": map[string]any{"validators": validators, "delegations": delegations}})
	require.NoError(t, err)
	stamp := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	document := cmttypes.GenesisDoc{ChainID: "mintcandy_990014-1", GenesisTime: stamp, InitialHeight: 1, AppState: state}
	genesis, err := cmtjson.Marshal(document)
	require.NoError(t, err)
	return consentManifest{SchemaVersion: 1, Scope: "mintcandy-public-genesis", ChainID: document.ChainID, EVMChainID: 990014,
		GenesisTimeUTC: stamp.Format(time.RFC3339), GenesisSHA256: fmt.Sprintf("%x", sha256.Sum256(genesis)), Validators: 4}, genesis, keys
}

func TestLaunchConsentVerifiesFourExactMessages(t *testing.T) {
	manifest, genesis, keys := consentFixture(t)
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	consent, err := NewLaunchConsent(raw, genesis, strings.ToUpper(hash))
	require.NoError(t, err)
	signatures := make([]OperatorConsent, 0, len(keys))
	for operator, key := range keys {
		body, err := consent.Payload(operator)
		require.NoError(t, err)
		require.Contains(t, string(body), "Manifest SHA256: "+hash)
		require.Contains(t, string(body), "Genesis time UTC: 2030-01-01T00:00:00Z")
		signature, err := crypto.Sign(accounts.TextHash(body), key)
		require.NoError(t, err)
		// Include both wallet and library recovery-byte encodings.
		if len(signatures)%2 == 0 {
			signature[64] += 27
		}
		signatures = append(signatures, OperatorConsent{OperatorAddress: operator, Signature: "0x" + hex.EncodeToString(signature)})
	}
	encoded, err := json.Marshal(signatures)
	require.NoError(t, err)
	decoded, err := ReadOperatorConsents(bytes.NewReader(encoded))
	require.NoError(t, err)
	require.Equal(t, signatures, decoded)
	report, err := consent.Verify(decoded)
	require.NoError(t, err)
	require.Equal(t, "FOUR_OPERATOR_ACKNOWLEDGMENTS_VERIFIED", report.Status)
	require.Len(t, report.VerifiedOperators, 4)
	require.True(t, slices.IsSorted(report.VerifiedOperators))
	require.False(t, report.NetworkLaunchAuthorized)

	_, err = consent.Verify(decoded[:3])
	require.ErrorContains(t, err, "each of the four")
	duplicate := slices.Clone(decoded)
	duplicate[1] = duplicate[0]
	_, err = consent.Verify(duplicate)
	require.ErrorContains(t, err, "duplicate confirmation")
	wrongSigner := slices.Clone(decoded)
	wrongSigner[0].Signature = wrongSigner[1].Signature
	_, err = consent.Verify(wrongSigner)
	require.ErrorContains(t, err, "signature does not match operator")

	remarshaled := append(slices.Clone(raw), '\n')
	changedHash := fmt.Sprintf("%x", sha256.Sum256(remarshaled))
	modified, err := NewLaunchConsent(remarshaled, genesis, changedHash)
	require.NoError(t, err)
	_, err = modified.Verify(decoded)
	require.ErrorContains(t, err, "signature does not match operator")
	_, err = NewLaunchConsent(remarshaled, genesis, hash)
	require.ErrorContains(t, err, "independently approved SHA256")
	_, err = NewLaunchConsent(raw, append(slices.Clone(genesis), '\n'), hash)
	require.ErrorContains(t, err, "genesis differs from its manifest SHA256")
}

func TestLaunchConsentRejectsDuplicateFieldsAndMalleableSignature(t *testing.T) {
	_, err := ReadOperatorConsents(strings.NewReader(`[{"operatorAddress":"d0valoper1","signature":"one","Signature":"two"}]`))
	require.ErrorContains(t, err, "duplicate")
	manifest, genesis, keys := consentFixture(t)
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	consent, err := NewLaunchConsent(raw, genesis, fmt.Sprintf("%x", sha256.Sum256(raw)))
	require.NoError(t, err)
	for operator, key := range keys {
		body, err := consent.Payload(operator)
		require.NoError(t, err)
		signature, err := crypto.Sign(accounts.TextHash(body), key)
		require.NoError(t, err)
		highS := new(big.Int).Sub(crypto.S256().Params().N, new(big.Int).SetBytes(signature[32:64]))
		highS.FillBytes(signature[32:64])
		signature[64] ^= 1
		err = verifyOperatorSignature(OperatorConsent{OperatorAddress: operator, Signature: "0x" + hex.EncodeToString(signature)}, body)
		require.ErrorContains(t, err, "noncanonical")
		break
	}
}

func TestLaunchConsentUsesEVMOperatorIdentityWithoutCountingAliasesTwice(t *testing.T) {
	manifest, genesis, keys := consentFixture(t)
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	consent, err := NewLaunchConsent(raw, genesis, fmt.Sprintf("%x", sha256.Sum256(raw)))
	require.NoError(t, err)
	var evmSignatures, legacySignatures []OperatorConsent
	for legacy, key := range keys {
		evm := crypto.PubkeyToAddress(key.PublicKey).Hex()
		body, err := consent.Payload(evm)
		require.NoError(t, err)
		require.Contains(t, string(body), "Operator: "+strings.ToLower(evm)+"\n")
		require.NotContains(t, string(body), legacy)
		sign, err := crypto.Sign(accounts.TextHash(body), key)
		require.NoError(t, err)
		evmSignatures = append(evmSignatures, OperatorConsent{evm, "0x" + hex.EncodeToString(sign)})

		body, err = consent.Payload(legacy)
		require.NoError(t, err)
		sign, err = crypto.Sign(accounts.TextHash(body), key)
		require.NoError(t, err)
		legacySignatures = append(legacySignatures, OperatorConsent{legacy, "0x" + hex.EncodeToString(sign)})
	}
	report, err := consent.Verify(evmSignatures)
	require.NoError(t, err)
	require.Len(t, report.VerifiedOperators, 4)
	for _, operator := range report.VerifiedOperators {
		require.Regexp(t, "^0x[0-9a-f]{40}$", operator)
	}
	duplicate := slices.Clone(evmSignatures)
	duplicate[1] = legacySignatures[0]
	_, err = consent.Verify(duplicate)
	require.ErrorContains(t, err, "duplicate confirmation")
}
