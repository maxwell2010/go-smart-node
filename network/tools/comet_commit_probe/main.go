// comet_commit_probe verifies every commit signature from an isolated fixture.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
)

type request struct {
	ChainID        string          `json:"chainId"`
	Commit         json.RawMessage `json:"commit"`
	Keys           []string        `json:"publicKeys"`
	VotingPowers   []int64         `json:"votingPowers,omitempty"`
	AllowedMissing []string        `json:"allowedMissing,omitempty"`
}

func verify(input request) ([]string, error) {
	switch input.ChainID {
	case "mintcandy_990001-1", "mintcandy_990012-1", "mintcandy_990013-1", "mintcandy_990014-1":
	default:
		return nil, errors.New("expected an isolated consensus fixture chain ID")
	}
	if len(input.Keys) != 4 {
		return nil, errors.New("expected four fixture public keys")
	}
	if len(input.VotingPowers) != 0 && len(input.VotingPowers) != len(input.Keys) {
		return nil, errors.New("voting powers must match the four fixture public keys")
	}
	keys := make(map[string]ed25519.PubKey, len(input.Keys))
	powers := make(map[string]int64, len(input.Keys))
	var totalPower int64
	for index, encoded := range input.Keys {
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
		power := int64(1)
		if len(input.VotingPowers) != 0 {
			power = input.VotingPowers[index]
		}
		if power <= 0 || totalPower > types.MaxTotalVotingPower-power {
			return nil, errors.New("invalid or overflowing fixture voting power")
		}
		totalPower += power
		powers[address] = power
	}
	missing := make(map[string]bool, len(input.AllowedMissing))
	for _, address := range input.AllowedMissing {
		if _, exists := keys[address]; !exists || missing[address] {
			return nil, errors.New("unknown or repeated allowed missing validator")
		}
		missing[address] = true
	}
	var commit types.Commit
	if err := cmtjson.Unmarshal(input.Commit, &commit); err != nil {
		return nil, err
	}
	if err := commit.ValidateBasic(); err != nil {
		return nil, err
	}
	if len(commit.Signatures) != len(keys) || commit.Height <= 0 || commit.BlockID.IsZero() {
		return nil, errors.New("commit does not contain four fixture validators")
	}
	addresses := make([]string, 0, len(keys))
	var signedPower int64
	for _, signature := range commit.Signatures {
		// An absent slot has no address or signature. Match the remaining key
		// set against the caller's exact expected absences after verification.
		if signature.BlockIDFlag == types.BlockIDFlagAbsent {
			continue
		}
		address := signature.ValidatorAddress.String()
		key, exists := keys[address]
		if signature.BlockIDFlag != types.BlockIDFlagCommit || !exists || missing[address] {
			return nil, errors.New("commit has a missing, repeated or unknown validator")
		}
		vote := cmtproto.Vote{Type: cmtproto.PrecommitType, Height: commit.Height, Round: commit.Round,
			BlockID: commit.BlockID.ToProto(), Timestamp: signature.Timestamp}
		if !key.VerifySignature(types.VoteSignBytes(input.ChainID, &vote), signature.Signature) {
			return nil, fmt.Errorf("invalid canonical precommit from %s", address)
		}
		signedPower += powers[address]
		delete(keys, address)
		addresses = append(addresses, address)
	}
	if len(keys) != len(missing) {
		return nil, errors.New("commit absences differ from the expected fixture validators")
	}
	for address := range keys {
		if !missing[address] {
			return nil, errors.New("an undeclared fixture validator did not sign")
		}
	}
	// Avoid multiplication overflow while preserving the strict +2/3 rule.
	threshold := totalPower/3*2 + totalPower%3*2/3 + 1
	if signedPower < threshold {
		return nil, errors.New("verified signatures do not exceed two thirds of fixture voting power")
	}
	return addresses, nil
}

func main() {
	genesis := flag.String("founding-genesis", "", "Path to the approved founding genesis")
	digest := flag.String("approved-genesis-sha256", "", "Independently verified founding genesis SHA256")
	flag.Parse()
	if flag.NArg() != 0 || (*genesis == "") != (*digest == "") {
		fmt.Fprintln(os.Stderr, "use both --founding-genesis and --approved-genesis-sha256, or neither")
		os.Exit(2)
	}
	if *genesis != "" {
		if err := runFoundingProbe(*genesis, *digest, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "comet-commit-probe:", err)
			os.Exit(1)
		}
		return
	}
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
