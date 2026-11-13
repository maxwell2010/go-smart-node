// comet_commit_probe verifies every commit signature from an isolated fixture.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
)

type request struct {
	ChainID string          `json:"chainId"`
	Commit  json.RawMessage `json:"commit"`
	Keys    []string        `json:"publicKeys"`
}

func verify(input request) ([]string, error) {
	if input.ChainID != "mintcandy_990001-1" || len(input.Keys) != 4 {
		return nil, errors.New("expected four public keys and the isolated consensus fixture chain ID")
	}
	keys := make(map[string]ed25519.PubKey, len(input.Keys))
	for _, encoded := range input.Keys {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != ed25519.PubKeySize {
			return nil, errors.New("invalid fixture Ed25519 public key")
		}
		public := ed25519.PubKey(key)
		address := public.Address().String()
		if _, duplicate := keys[address]; duplicate {
			return nil, errors.New("duplicate fixture validator")
		}
		keys[address] = public
	}
	var commit types.Commit
	if err := cmtjson.Unmarshal(input.Commit, &commit); err != nil {
		return nil, err
	}
	if err := commit.ValidateBasic(); err != nil {
		return nil, err
	}
	if len(commit.Signatures) != len(keys) || commit.Height <= 0 {
		return nil, errors.New("commit does not contain four fixture validators")
	}
	addresses := make([]string, 0, len(keys))
	for _, signature := range commit.Signatures {
		address := signature.ValidatorAddress.String()
		key, exists := keys[address]
		if signature.BlockIDFlag != types.BlockIDFlagCommit || !exists {
			return nil, errors.New("commit has a missing, repeated or unknown validator")
		}
		vote := cmtproto.Vote{Type: cmtproto.PrecommitType, Height: commit.Height, Round: commit.Round,
			BlockID: commit.BlockID.ToProto(), Timestamp: signature.Timestamp}
		if !key.VerifySignature(types.VoteSignBytes(input.ChainID, &vote), signature.Signature) {
			return nil, fmt.Errorf("invalid canonical precommit from %s", address)
		}
		delete(keys, address)
		addresses = append(addresses, address)
	}
	return addresses, nil
}

func main() {
	var input request
	reader := json.NewDecoder(io.LimitReader(os.Stdin, 64*1024))
	reader.DisallowUnknownFields()
	err := reader.Decode(&input)
	if err == nil {
		var trailing any
		if reader.Decode(&trailing) != io.EOF {
			err = errors.New("unexpected trailing commit probe input")
		}
	}
	if err == nil {
		var addresses []string
		addresses, err = verify(input)
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(addresses)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "comet-commit-probe:", err)
		os.Exit(1)
	}
}
