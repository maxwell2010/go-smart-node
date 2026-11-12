package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/cometbft/cometbft/privval"
	"github.com/cosmos/cosmos-sdk/codec/address"
	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestValidatorRegistrationMetadataCommand(t *testing.T) {
	const chainID = "mintcandy_990002-3"
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv_validator_key.json")
	statePath := filepath.Join(dir, "priv_validator_state.json")
	pv := privval.GenFilePV(keyPath, statePath)
	pv.LastSignState.Height = 12
	pv.Save()
	keyBefore, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	stateBefore, err := os.ReadFile(statePath)
	require.NoError(t, err)
	operator := bytes.Repeat([]byte{3}, 20)
	operatorAddr, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(operator)
	require.NoError(t, err)
	home := filepath.Join(dir, "unused-node-home")
	var stdout, stderr bytes.Buffer
	run := func(args ...string) error {
		root := NewRootCmd()
		stdout.Reset()
		stderr.Reset()
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs(append([]string{nativeValidatorRegistrationMetadataCommandName, "--home", home}, args...))
		return svrcmd.Execute(root, "MCND", defaultNodeHome())
	}
	require.NoError(t, run("--help"))
	require.Contains(t, stdout.String(), "offline")
	require.Contains(t, stdout.String(), "--priv-validator-key-file")
	require.Contains(t, stdout.String(), "--expires-height")

	args := []string{"--chain-id", chainID, "--operator", operatorAddr, "--expires-height", "850", "--commission-percent", "0"}
	require.NoError(t, run(append(args, "--priv-validator-key-file", keyPath)...))
	privateKey := pv.Key.PrivKey.Bytes()
	want, err := ownedapp.BuildNativeValidatorRegistrationMetadata(chainID, 990002, operator, 850, 0, ed25519.PrivateKey(privateKey))
	require.NoError(t, err)
	require.Equal(t, want+"\n", stdout.String())
	require.Empty(t, stderr.String())
	require.NotContains(t, stdout.String(), base64.StdEncoding.EncodeToString(privateKey))

	malformed := filepath.Join(dir, "malformed.json")
	const secret = "fixture-secret-never-print"
	require.NoError(t, os.WriteFile(malformed, []byte(`{"priv_key":{"type":"tendermint/PrivKeyEd25519","value":"`+secret+`"}}`), 0o600))
	err = run(append(args, "--priv-validator-key-file", malformed)...)
	require.ErrorContains(t, err, "invalid CometBFT consensus key file")
	require.NotContains(t, err.Error()+stdout.String()+stderr.String(), secret)
	require.NotContains(t, stdout.String(), "mintcandy-validator-registration/v1:")

	keyAfter, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	require.Equal(t, keyBefore, keyAfter)
	stateAfter, err := os.ReadFile(statePath)
	require.NoError(t, err)
	require.Equal(t, stateBefore, stateAfter)
	_, err = os.Stat(home)
	require.ErrorIs(t, err, os.ErrNotExist)
}
