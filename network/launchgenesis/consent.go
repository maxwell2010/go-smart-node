package launchgenesis

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strings"
	"time"

	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const MaxConsentBytes = 8 * 1024
const foundingValidatorCount = 4

type consentManifest struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Scope          string `json:"scope"`
	ChainID        string `json:"chainId"`
	EVMChainID     uint64 `json:"evmChainId,string"`
	GenesisTimeUTC string `json:"genesisTimeUtc"`
	GenesisSHA256  string `json:"genesisSha256"`
	Validators     int    `json:"validators"`
}

type LaunchConsent struct {
	manifest     consentManifest
	manifestHash string
	operators    map[string]validatorDeclaration
}

type OperatorConsent struct {
	OperatorAddress string `json:"operatorAddress"`
	Signature       string `json:"signature"`
}

type ConsentReport struct {
	Status                  string   `json:"status"`
	ChainID                 string   `json:"chainId"`
	EVMChainID              uint64   `json:"evmChainId,string"`
	GenesisTimeUTC          string   `json:"genesisTimeUtc"`
	GenesisSHA256           string   `json:"genesisSha256"`
	ManifestSHA256          string   `json:"manifestSha256"`
	VerifiedOperators       []string `json:"verifiedOperators"`
	NetworkLaunchAuthorized bool     `json:"networkLaunchAuthorized"`
}

// NewLaunchConsent uses the independently approved manifest digest. The caller
// first authenticates the manifest and its publisher through the OpenPGP verifier.
func NewLaunchConsent(manifestBytes, genesis []byte, approvedManifestHash string) (LaunchConsent, error) {
	var result LaunchConsent
	manifestHash := fmt.Sprintf("%x", sha256.Sum256(manifestBytes))
	if len(manifestBytes) == 0 || len(manifestBytes) > MaxConsentBytes ||
		!strings.EqualFold(manifestHash, approvedManifestHash) {
		return result, fmt.Errorf("manifest differs from the independently approved SHA256")
	}
	var fields map[string]json.RawMessage
	if err := decodeLedgerJSON(manifestBytes, &fields); err != nil {
		return result, fmt.Errorf("invalid genesis manifest: %w", err)
	}
	var manifest consentManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return result, err
	}
	if manifest.SchemaVersion != 1 || manifest.Scope != "mintcandy-public-genesis" || manifest.Validators != foundingValidatorCount {
		return result, fmt.Errorf("expected the four-validator MintCandy genesis manifest")
	}
	if err := identity.ValidateProfileChainIDs(manifest.ChainID, manifest.EVMChainID); err != nil {
		return result, err
	}
	if len(genesis) == 0 || len(genesis) > MaxCandidateBytes || manifest.GenesisSHA256 != fmt.Sprintf("%x", sha256.Sum256(genesis)) {
		return result, fmt.Errorf("genesis differs from its manifest SHA256")
	}
	document, err := cmttypes.GenesisDocFromJSON(genesis)
	if err != nil {
		return result, err
	}
	stamp, err := time.Parse(time.RFC3339Nano, manifest.GenesisTimeUTC)
	if err != nil || !strings.HasSuffix(manifest.GenesisTimeUTC, "Z") || !document.GenesisTime.Equal(stamp) ||
		document.ChainID != manifest.ChainID || document.InitialHeight != 1 || len(document.Validators) != 0 {
		return result, fmt.Errorf("genesis identity, time or initial roster format differs")
	}
	var appState struct {
		Validator struct {
			Validators  []validatorDeclaration `json:"validators"`
			Delegations []struct {
				Validator string `json:"validator"`
				Stake     struct {
					Coin sdk.Coin `json:"stake"`
				} `json:"stake"`
			} `json:"delegations"`
		} `json:"validator"`
	}
	if err := json.Unmarshal(document.AppState, &appState); err != nil {
		return result, err
	}
	if len(appState.Validator.Validators) != manifest.Validators {
		return result, fmt.Errorf("genesis roster differs from the manifest")
	}
	staked := make(map[string]bool)
	for _, row := range appState.Validator.Delegations {
		if row.Stake.Coin.Denom == identity.BaseDenom && !row.Stake.Coin.Amount.IsNil() && row.Stake.Coin.Amount.IsPositive() {
			staked[row.Validator] = true
		}
	}
	operators := make(map[string]validatorDeclaration, foundingValidatorCount)
	keys := make(map[string]bool)
	for _, row := range appState.Validator.Validators {
		if _, err := nativeAddress(row.Operator, "d0valoper"); err != nil {
			return result, err
		}
		_, duplicate := operators[row.Operator]
		key := string(row.PubKey.Key)
		if duplicate || keys[key] || !row.Online || !staked[row.Operator] ||
			row.PubKey.Type != "/cosmos.crypto.ed25519.PubKey" || len(row.PubKey.Key) != 32 {
			return result, fmt.Errorf("genesis contains an invalid, duplicate or inactive operator")
		}
		operators[row.Operator], keys[key] = row, true
	}
	return LaunchConsent{manifest: manifest, manifestHash: manifestHash, operators: operators}, nil
}

func (c LaunchConsent) Payload(operator string) ([]byte, error) {
	row, found := c.operators[operator]
	if !found {
		return nil, fmt.Errorf("operator is outside the verified genesis roster")
	}
	return fmt.Appendf(nil, "MintCandy Network - genesis acknowledgment v1\n"+
		"I reviewed this genesis and confirm readiness for launch coordination.\n"+
		"Chain ID: %s\nEVM chain ID: %d\nGenesis time UTC: %s\nGenesis SHA256: %s\n"+
		"Manifest SHA256: %s\nOperator: %s\nConsensus public key: %s",
		c.manifest.ChainID, c.manifest.EVMChainID, c.manifest.GenesisTimeUTC, c.manifest.GenesisSHA256,
		c.manifestHash, operator, base64.StdEncoding.EncodeToString(row.PubKey.Key)), nil
}

func ReadOperatorConsents(source io.Reader) ([]OperatorConsent, error) {
	raw, err := io.ReadAll(io.LimitReader(source, MaxConsentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > MaxConsentBytes {
		return nil, fmt.Errorf("invalid operator confirmations byte count")
	}
	var signatures []OperatorConsent
	err = decodeLedgerJSON(raw, &signatures)
	return signatures, err
}

func (c LaunchConsent) Verify(signatures []OperatorConsent) (ConsentReport, error) {
	if len(signatures) != foundingValidatorCount || len(c.operators) != foundingValidatorCount {
		return ConsentReport{}, fmt.Errorf("one confirmation from each of the four founding operators is required")
	}
	verified := make([]string, 0, foundingValidatorCount)
	for _, row := range signatures {
		if slices.Contains(verified, row.OperatorAddress) {
			return ConsentReport{}, fmt.Errorf("duplicate confirmation for operator %s", row.OperatorAddress)
		}
		body, err := c.Payload(row.OperatorAddress)
		if err != nil {
			return ConsentReport{}, err
		}
		if err := verifyOperatorSignature(row, body); err != nil {
			return ConsentReport{}, err
		}
		verified = append(verified, row.OperatorAddress)
	}
	slices.Sort(verified)
	return ConsentReport{Status: "FOUR_OPERATOR_ACKNOWLEDGMENTS_VERIFIED", ChainID: c.manifest.ChainID,
		EVMChainID: c.manifest.EVMChainID, GenesisTimeUTC: c.manifest.GenesisTimeUTC,
		GenesisSHA256: c.manifest.GenesisSHA256, ManifestSHA256: c.manifestHash, VerifiedOperators: verified}, nil
}

func verifyOperatorSignature(row OperatorConsent, body []byte) error {
	signer, err := recoverMessageSigner(body, row.Signature)
	if err != nil {
		return err
	}
	address, err := nativeAddress(row.OperatorAddress, "d0valoper")
	if err != nil || signer != address {
		return fmt.Errorf("signature does not match operator %s for this genesis and manifest", row.OperatorAddress)
	}
	return nil
}

func recoverMessageSigner(body []byte, encoded string) (common.Address, error) {
	if len(encoded) != 132 || !strings.HasPrefix(encoded, "0x") {
		return common.Address{}, fmt.Errorf("a 65-byte 0x-prefixed EIP-191 signature is required")
	}
	signature, err := hex.DecodeString(encoded[2:])
	if err != nil {
		return common.Address{}, fmt.Errorf("invalid message signature encoding")
	}
	if signature[64] == 27 || signature[64] == 28 {
		signature[64] -= 27
	}
	if !crypto.ValidateSignatureValues(signature[64], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:64]), true) {
		return common.Address{}, fmt.Errorf("invalid or noncanonical message signature")
	}
	public, err := crypto.SigToPub(accounts.TextHash(body), signature)
	if err != nil {
		return common.Address{}, fmt.Errorf("invalid message signature: %w", err)
	}
	return crypto.PubkeyToAddress(*public), nil
}
