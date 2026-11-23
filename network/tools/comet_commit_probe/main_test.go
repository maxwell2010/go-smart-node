package main

import (
	"encoding/base64"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/crypto/tmhash"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func fixture(t *testing.T, absent ...int) (request, *types.Commit) {
	t.Helper()
	return fixtureChain(t, "mintcandy_990012-1", absent...)
}

func fixtureChain(t *testing.T, chainID string, absent ...int) (request, *types.Commit) {
	t.Helper()
	input := request{ChainID: chainID}
	commit := &types.Commit{
		Height: 12, Round: 3,
		BlockID: types.BlockID{Hash: tmhash.Sum([]byte("isolated-quorum-block")),
			PartSetHeader: types.PartSetHeader{Total: 1, Hash: tmhash.Sum([]byte("isolated-parts"))}},
	}
	absences := make(map[int]bool)
	for _, index := range absent {
		absences[index] = true
	}
	for index := 0; index < 4; index++ {
		private := ed25519.GenPrivKeyFromSecret([]byte(fmt.Sprintf("disposable-quorum-test-%d", index)))
		public := private.PubKey()
		input.Keys = append(input.Keys, base64.StdEncoding.EncodeToString(public.Bytes()))
		if absences[index] {
			commit.Signatures = append(commit.Signatures, types.CommitSig{BlockIDFlag: types.BlockIDFlagAbsent})
			input.AllowedMissing = append(input.AllowedMissing, public.Address().String())
			continue
		}
		stamp := time.Date(2026, 11, 18, 0, 0, index, 0, time.UTC)
		vote := cmtproto.Vote{Type: cmtproto.PrecommitType, Height: commit.Height, Round: commit.Round,
			BlockID: commit.BlockID.ToProto(), Timestamp: stamp}
		signature, err := private.Sign(types.VoteSignBytes(input.ChainID, &vote))
		if err != nil {
			t.Fatal(err)
		}
		commit.Signatures = append(commit.Signatures, types.CommitSig{BlockIDFlag: types.BlockIDFlagCommit,
			ValidatorAddress: public.Address(), Timestamp: stamp, Signature: signature})
	}
	marshalCommit(t, &input, commit)
	return input, commit
}

func TestLegacyFourSignatureSchema(t *testing.T) {
	input, _ := fixtureChain(t, "mintcandy_990001-1")
	if len(input.VotingPowers) != 0 || len(input.AllowedMissing) != 0 {
		t.Fatal("legacy fixture unexpectedly requires new fields")
	}
	got, err := verify(input)
	if err != nil || len(got) != 4 {
		t.Fatalf("legacy four-signature schema regressed: %v, %v", got, err)
	}
}

func marshalCommit(t *testing.T, input *request, commit *types.Commit) {
	t.Helper()
	raw, err := cmtjson.Marshal(commit)
	if err != nil {
		t.Fatal(err)
	}
	input.Commit = raw
}

func TestVerifiedQuorum(t *testing.T) {
	domains := []string{"mintcandy_990012-1", "mintcandy_990013-1", "mintcandy_990014-1"}
	if identity.ProfileChainID != "" {
		domains = append(domains, identity.ProfileChainID)
	}
	for _, domain := range domains {
		t.Run(domain, func(t *testing.T) {
			for _, absent := range [][]int{nil, {3}} {
				input, _ := fixtureChain(t, domain, absent...)
				input.VotingPowers = []int64{10000, 10000, 10000, 10000}
				got, err := verify(input)
				if err != nil || len(got) != 4-len(absent) {
					t.Fatalf("valid fixture quorum failed: %v, %v", got, err)
				}
			}
		})
	}
	for _, domain := range []string{identity.MainnetChainID, identity.TestnetChainID} {
		if domain == identity.ProfileChainID {
			continue
		}
		input, _ := fixtureChain(t, domain)
		if _, err := verify(input); err == nil {
			t.Fatalf("accepted a quorum outside the compiled profile: %s", domain)
		}
	}
}

func TestInsufficientVotingPower(t *testing.T) {
	for _, test := range []struct {
		name   string
		absent []int
		powers []int64
	}{
		{"two_of_four", []int{2, 3}, nil},
		{"exact_two_thirds", []int{3}, []int64{1, 1, 2, 2}},
		{"three_signers_low_weight", []int{3}, []int64{3, 3, 3, 91}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, _ := fixture(t, test.absent...)
			input.VotingPowers = test.powers
			if _, err := verify(input); err == nil {
				t.Fatal("accepted signatures below strict voting-power quorum")
			}
		})
	}
}

func TestRejectTamperedCommit(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*request, *types.Commit)
	}{
		{"signature", func(_ *request, c *types.Commit) { c.Signatures[0].Signature[0] ^= 1 }},
		{"height", func(_ *request, c *types.Commit) { c.Height++ }},
		{"round", func(_ *request, c *types.Commit) { c.Round++ }},
		{"block", func(_ *request, c *types.Commit) { c.BlockID.Hash[0] ^= 1 }},
		{"timestamp", func(_ *request, c *types.Commit) {
			c.Signatures[0].Timestamp = c.Signatures[0].Timestamp.Add(time.Second)
		}},
		{"repeated_signer", func(_ *request, c *types.Commit) { c.Signatures[1] = c.Signatures[0] }},
		{"nil_vote", func(_ *request, c *types.Commit) { c.Signatures[0].BlockIDFlag = types.BlockIDFlagNil }},
		{"different_signing_domain", func(r *request, _ *types.Commit) { r.ChainID = "mintcandy_990014-1" }},
		{"mintcandy_production_domain", func(r *request, _ *types.Commit) { r.ChainID = "mintcandy_9905401-1" }},
		{"production_domain", func(r *request, _ *types.Commit) { r.ChainID = "decimal_75-1" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, commit := fixture(t)
			test.change(&input, commit)
			marshalCommit(t, &input, commit)
			if _, err := verify(input); err == nil {
				t.Fatal("accepted tampered canonical precommit")
			}
		})
	}
}

func TestRejectInvalidFixture(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*request)
	}{
		{"missing_key", func(r *request) { r.Keys = r.Keys[:3] }},
		{"duplicate_key", func(r *request) { r.Keys[1] = r.Keys[0] }},
		{"invalid_key", func(r *request) { r.Keys[0] = "invalid" }},
		{"negative_power", func(r *request) { r.VotingPowers = []int64{1, 1, 1, -1} }},
		{"zero_power", func(r *request) { r.VotingPowers = []int64{1, 1, 1, 0} }},
		{"overflow", func(r *request) { r.VotingPowers = []int64{math.MaxInt64, 1, 1, 1} }},
		{"wrong_power_count", func(r *request) { r.VotingPowers = []int64{1} }},
		{"unknown_missing", func(r *request) { r.AllowedMissing = []string{"UNKNOWN"} }},
		{"declared_signer_missing", func(r *request) {
			key, _ := base64.StdEncoding.DecodeString(r.Keys[0])
			r.AllowedMissing = []string{ed25519.PubKey(key).Address().String()}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, _ := fixture(t)
			test.change(&input)
			if _, err := verify(input); err == nil {
				t.Fatal("accepted invalid fixture")
			}
		})
	}
	input, _ := fixture(t, 3)
	input.AllowedMissing = nil
	if _, err := verify(input); err == nil {
		t.Fatal("accepted undeclared absence")
	}
	input, _ = fixture(t, 3)
	input.AllowedMissing = append(input.AllowedMissing, input.AllowedMissing[0])
	if _, err := verify(input); err == nil {
		t.Fatal("accepted repeated absence")
	}
}
