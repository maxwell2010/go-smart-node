package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	"github.com/cometbft/cometbft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const (
	foundingPower = 10000
	foundingStake = "10000000000000000000000"
)

type foundingReport struct {
	Status        string `json:"status"`
	ChainID       string `json:"chainId"`
	Height        int64  `json:"height"`
	GenesisSHA256 string `json:"genesisSha256"`
	BlockHash     string `json:"blockHash"`
	AppHash       string `json:"appHash"`
	ValidatorHash string `json:"validatorsHash"`
	Signers       int    `json:"verifiedSignatures"`
	SignedPower   int64  `json:"signedPower"`
	TotalPower    int64  `json:"totalPower"`
}

func runFoundingProbe(path, expectedHash string, commit io.Reader, output io.Writer) error {
	genesis, err := os.Open(path)
	if err != nil {
		return err
	}
	defer genesis.Close()
	const genesisLimit = 24 * 1024 * 1024
	raw, err := io.ReadAll(io.LimitReader(genesis, genesisLimit+1))
	if err != nil {
		return err
	}
	if len(raw) == 0 || len(raw) > genesisLimit {
		return errors.New("invalid genesis size")
	}
	const commitLimit = 64 * 1024
	response, err := io.ReadAll(io.LimitReader(commit, commitLimit+1))
	if err != nil {
		return err
	}
	if len(response) == 0 || len(response) > commitLimit {
		return errors.New("invalid commit response size")
	}
	result, err := verifyFoundingBlock(raw, expectedHash, response)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func verifyFoundingBlock(genesis []byte, expectedHash string, response []byte) (foundingReport, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256(genesis))
	if !strings.EqualFold(hash, expectedHash) {
		return foundingReport{}, errors.New("genesis differs from its independently approved SHA256")
	}
	doc, err := types.GenesisDocFromJSON(genesis)
	if err != nil {
		return foundingReport{}, err
	}
	evmDomain, _, _ := strings.Cut(strings.TrimPrefix(doc.ChainID, "mintcandy_"), "-")
	evmID, _ := strconv.ParseUint(evmDomain, 10, 64)
	if err := identity.ValidateChainIDs(doc.ChainID, evmID); err != nil {
		return foundingReport{}, err
	}
	if doc.InitialHeight != 1 || len(doc.Validators) != 0 {
		return foundingReport{}, errors.New("expected the native founding genesis at height 1")
	}
	trusted, err := foundingValidatorSet(doc.AppState)
	if err != nil {
		return foundingReport{}, err
	}
	var rpc struct {
		Result struct {
			SignedHeader types.SignedHeader `json:"signed_header"`
			Canonical    bool               `json:"canonical"`
		} `json:"result"`
	}
	if err := cmtjson.Unmarshal(response, &rpc); err != nil {
		return foundingReport{}, err
	}
	signed := rpc.Result.SignedHeader
	if !rpc.Result.Canonical || signed.Header == nil || signed.Commit == nil {
		return foundingReport{}, errors.New("expected a canonical signed RPC commit")
	}
	if err := signed.ValidateBasic(doc.ChainID); err != nil {
		return foundingReport{}, err
	}
	if signed.Height != 1 || !signed.Time.Equal(doc.GenesisTime) || !bytes.Equal(signed.ValidatorsHash, trusted.Hash()) {
		return foundingReport{}, errors.New("first block height, genesis time or validator set differs")
	}
	// The SDK has not committed application state before the first block.
	if len(signed.AppHash) != 0 && len(signed.AppHash) != sha256.Size {
		return foundingReport{}, errors.New("unexpected AppHash size in the first block")
	}
	if err := trusted.VerifyCommit(doc.ChainID, signed.Commit.BlockID, signed.Height, signed.Commit); err != nil {
		return foundingReport{}, err
	}
	signers := 0
	for _, sig := range signed.Commit.Signatures {
		if sig.BlockIDFlag == types.BlockIDFlagCommit {
			signers++
		}
	}
	return foundingReport{Status: "FOUNDING_COMMIT_VERIFIED", ChainID: doc.ChainID, Height: signed.Height,
		GenesisSHA256: hash, BlockHash: signed.Commit.BlockID.Hash.String(), AppHash: fmt.Sprintf("%X", signed.AppHash),
		ValidatorHash: signed.ValidatorsHash.String(), Signers: signers, SignedPower: int64(signers) * foundingPower,
		TotalPower: trusted.TotalVotingPower()}, nil
}

func foundingValidatorSet(state json.RawMessage) (*types.ValidatorSet, error) {
	var app struct {
		Validator struct {
			Validators []struct {
				Operator string `json:"operator_address"`
				Online   bool   `json:"online"`
				Key      struct {
					Type string `json:"@type"`
					Raw  []byte `json:"key"`
				} `json:"consensus_pubkey"`
			} `json:"validators"`
			Delegations []struct {
				Operator string `json:"validator"`
				Stake    struct {
					Coin struct {
						Denom  string `json:"denom"`
						Amount string `json:"amount"`
					} `json:"stake"`
				} `json:"stake"`
			} `json:"delegations"`
		} `json:"validator"`
	}
	if err := json.Unmarshal(state, &app); err != nil {
		return nil, err
	}
	if len(app.Validator.Validators) != 4 || len(app.Validator.Delegations) != 4 {
		return nil, errors.New("expected four founding validators and four equal native delegations")
	}
	staked := make(map[string]bool, 4)
	for _, row := range app.Validator.Delegations {
		if row.Operator == "" || staked[row.Operator] || row.Stake.Coin.Denom != identity.BaseDenom ||
			row.Stake.Coin.Amount != foundingStake {
			return nil, errors.New("unexpected founding delegation")
		}
		staked[row.Operator] = true
	}
	validators := make([]*types.Validator, 0, 4)
	seen := make(map[string]bool, 4)
	for _, row := range app.Validator.Validators {
		if !staked[row.Operator] || !row.Online || row.Key.Type != "/cosmos.crypto.ed25519.PubKey" ||
			len(row.Key.Raw) != ed25519.PubKeySize || bytes.Equal(row.Key.Raw, make([]byte, ed25519.PubKeySize)) {
			return nil, errors.New("invalid or inactive founding validator")
		}
		validator := types.NewValidator(ed25519.PubKey(row.Key.Raw), foundingPower)
		if seen[string(validator.Address)] {
			return nil, errors.New("repeated founding consensus key")
		}
		seen[string(validator.Address)] = true
		delete(staked, row.Operator)
		validators = append(validators, validator)
	}
	return types.NewValidatorSet(validators), nil
}
