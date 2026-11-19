package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func avatarTransferFixture(t *testing.T) ([]byte, validatorAvatarTransfer) {
	t.Helper()
	codec := address.NewBech32Codec(identity.Bech32PrefixValAddr)
	acc := address.NewBech32Codec(identity.Bech32PrefixAccAddr)
	genesis := validatortypes.DefaultGenesisState()
	var operators []string
	for _, value := range []byte{1, 2} {
		raw := []byte(strings.Repeat(string([]byte{value}), 20))
		operator, err := codec.BytesToString(raw)
		require.NoError(t, err)
		reward, err := acc.BytesToString(raw)
		require.NoError(t, err)
		validator, err := validatortypes.NewValidator(operator, reward, ed25519.GenPrivKey().PubKey(), validatortypes.Description{Moniker: "Preserved operator"}, sdkmath.LegacyZeroDec())
		require.NoError(t, err)
		genesis.Validators = append(genesis.Validators, validator)
		operators = append(operators, operator)
	}
	genesis.ValidatorAvatars = []validatortypes.ValidatorAvatar{{OperatorAddress: operators[1], AvatarURI: "https://example.invalid/preserved.png"}}
	encoded, err := ownedapp.MakeEncodingConfig(990001).Codec.MarshalJSON(genesis)
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]interface{}{
		"chain_id": "mintcandy_990001-1", "initial_height": "1", "consensus_params": map[string]string{"sentinel": "unchanged"},
		"app_state": map[string]interface{}{validatortypes.ModuleName: json.RawMessage(encoded), "bank": map[string]string{"sentinel": "unchanged"}},
	})
	require.NoError(t, err)
	transfer := validatorAvatarTransfer{Format: avatarTransferFormat, MetadataOnly: true, ChainID: "mintcandy_990001-1", EVMChainID: 990001, Height: 100,
		SourceBlockHash: strings.Repeat("a", 64), Operators: operators[:1], Avatars: []validatortypes.ValidatorAvatar{{OperatorAddress: operators[0], AvatarURI: "ipfs://bafyfixture/avatar.png"}}}
	return raw, transfer
}

func TestValidatorAvatarCLIImportChangesOnlyAvatarMetadata(t *testing.T) {
	raw, transfer := avatarTransferFixture(t)
	artifact, err := json.Marshal(transfer)
	require.NoError(t, err)
	result, err := importValidatorAvatars(raw, artifact, 990001)
	require.NoError(t, err)
	before, err := readAvatarGenesis(raw, 990001)
	require.NoError(t, err)
	after, err := readAvatarGenesis(result, 990001)
	require.NoError(t, err)
	require.Len(t, after.module.ValidatorAvatars, 2)
	require.Equal(t, before.module.Validators, after.module.Validators)
	require.JSONEq(t, string(before.document["consensus_params"]), string(after.document["consensus_params"]))
	require.JSONEq(t, string(before.state["bank"]), string(after.state["bank"]))
	delete(before.native, "validator_avatars")
	delete(after.native, "validator_avatars")
	beforeFields, err := json.Marshal(before.native)
	require.NoError(t, err)
	afterFields, err := json.Marshal(after.native)
	require.NoError(t, err)
	require.JSONEq(t, string(beforeFields), string(afterFields), "all other validator fields must stay identical")
	// No record for an explicitly included operator is an intentional clear.
	transfer.Avatars = nil
	artifact, err = json.Marshal(transfer)
	require.NoError(t, err)
	cleared, err := importValidatorAvatars(result, artifact, 990001)
	require.NoError(t, err)
	got, err := readAvatarGenesis(cleared, 990001)
	require.NoError(t, err)
	require.Equal(t, before.module.ValidatorAvatars, got.module.ValidatorAvatars)
}

func TestValidatorAvatarCLIRejectsMismatchedOrAmbiguousArtifacts(t *testing.T) {
	raw, good := avatarTransferFixture(t)
	for name, modify := range map[string]func(*validatorAvatarTransfer){
		"financial artifact": func(v *validatorAvatarTransfer) { v.MetadataOnly = false },
		"wrong domain":       func(v *validatorAvatarTransfer) { v.ChainID, v.EVMChainID = "mintcandy_990002-1", 990002 },
		"duplicate operator": func(v *validatorAvatarTransfer) { v.Operators = append(v.Operators, v.Operators[0]) },
		"duplicate avatar": func(v *validatorAvatarTransfer) {
			v.Operators = append(v.Operators, good.Avatars[0].OperatorAddress)
			v.Avatars = append(v.Avatars, v.Avatars[0])
		},
		"unsupported URI":      func(v *validatorAvatarTransfer) { v.Avatars[0].AvatarURI = "javascript:bad" },
		"unknown avatar owner": func(v *validatorAvatarTransfer) { v.Avatars[0].OperatorAddress = "unknown" },
		"invalid provenance":   func(v *validatorAvatarTransfer) { v.SourceBlockHash = strings.Repeat("z", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(good)
			require.NoError(t, err)
			var changed validatorAvatarTransfer
			require.NoError(t, json.Unmarshal(data, &changed))
			modify(&changed)
			data, err = json.Marshal(changed)
			require.NoError(t, err)
			_, err = importValidatorAvatars(raw, data, 990001)
			require.Error(t, err)
		})
	}
	_, err := importValidatorAvatars(raw, []byte(`{"format":"unsupported","unknown":1}`), 990001)
	require.Error(t, err)
	for _, invalid := range [][]byte{[]byte(`{"format":"a","\u0066ormat":"b"}`), []byte(`{"validator_avatars":[{"avatar_uri":"a","avatar_uri":"b"}]}`), {0xff}, []byte(`{} {}`)} {
		require.Error(t, validateAvatarTransferJSON(invalid))
	}
}

func TestValidatorAvatarCLIImportNeverOverwritesInput(t *testing.T) {
	raw, transfer := avatarTransferFixture(t)
	artifact, err := json.Marshal(transfer)
	require.NoError(t, err)
	dir := t.TempDir()
	input := filepath.Join(dir, "genesis.json")
	avatars := filepath.Join(dir, "avatars.json")
	require.NoError(t, os.WriteFile(input, raw, 0600))
	require.NoError(t, os.WriteFile(avatars, artifact, 0600))
	cmd := newValidatorControlCmd()
	cmd.SetArgs([]string{"import-avatars", "--genesis-file", input, "--avatar-file", avatars, "--evm-chain-id", "990001", "--output-file", input})
	require.Error(t, cmd.Execute())
	unchanged, err := os.ReadFile(input)
	require.NoError(t, err)
	require.Equal(t, raw, unchanged)
}

func TestValidatorAvatarCLIHandlesProtobufAliasesWithoutDuplicateFields(t *testing.T) {
	raw, transfer := avatarTransferFixture(t)
	doc, err := readAvatarGenesis(raw, 990001)
	require.NoError(t, err)
	artifact, err := json.Marshal(transfer)
	require.NoError(t, err)
	doc.native["validatorAvatars"] = doc.native["validator_avatars"]
	delete(doc.native, "validator_avatars")
	encode := func() []byte {
		doc.state[validatortypes.ModuleName], err = json.Marshal(doc.native)
		require.NoError(t, err)
		doc.document["app_state"], err = json.Marshal(doc.state)
		require.NoError(t, err)
		data, err := json.Marshal(doc.document)
		require.NoError(t, err)
		return data
	}
	result, err := importValidatorAvatars(encode(), artifact, 990001)
	require.NoError(t, err)
	after, err := readAvatarGenesis(result, 990001)
	require.NoError(t, err)
	require.Len(t, after.module.ValidatorAvatars, 2)
	require.NotContains(t, after.native, "validatorAvatars")
	doc.native["validator_avatars"] = doc.native["validatorAvatars"]
	_, err = importValidatorAvatars(encode(), artifact, 990001)
	require.ErrorContains(t, err, "ambiguous protobuf aliases")
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(artifact, &fields))
	fields["Format"] = fields["format"]
	bad, err := json.Marshal(fields)
	require.NoError(t, err)
	_, err = importValidatorAvatars(raw, bad, 990001)
	require.ErrorContains(t, err, "field names must match exactly")
	delete(fields, "Format")
	fields["validator_avatars"] = json.RawMessage(`[{"operator_address":"x","avatar_uri":"https://example.invalid/a","Avatar_URI":"https://example.invalid/b"}]`)
	bad, err = json.Marshal(fields)
	require.NoError(t, err)
	_, err = importValidatorAvatars(raw, bad, 990001)
	require.ErrorContains(t, err, "record field names must match exactly")
}
