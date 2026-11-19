package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/fees/envelope"
	"github.com/stretchr/testify/require"
)

func feeCLIArgs(t *testing.T) ([]string, string) {
	t.Helper()
	privateHex := "7b181d51dc41a06e32d4c1aa30c0eeeb3130067d92758b1cb9e6bd48340a77f4"
	key, err := crypto.HexToECDSA(privateHex)
	require.NoError(t, err)
	to := common.HexToAddress("0x2000000000000000000000000000000000000001")
	tx, err := ethtypes.SignTx(ethtypes.NewTransaction(2, to, new(big.Int), 21000, big.NewInt(100), nil), ethtypes.NewEIP155Signer(big.NewInt(990001)), key)
	require.NoError(t, err)
	raw, err := tx.MarshalBinary()
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "signed.hex")
	require.NoError(t, os.WriteFile(path, []byte("0x"+hex.EncodeToString(raw)+"\n"), 0600))
	return []string{"--tx-file", path, "--cosmos-chain-id", "mintcandy_990001-1", "--evm-id", "990001", "--token", to.Hex(), "--exact-input", "100000000", "--min-native-output", "100000000", "--conversion-gas", "300000", "--deadline", "1900000000", "--fee-nonce", "0"}, privateHex
}

func runFeeCLI(args []string) (string, error) {
	cmd := newFeeEnvelopeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestFeeEnvelopeOfflinePayloadAndEncodeRoundTrip(t *testing.T) {
	args, keyHex := feeCLIArgs(t)
	text, err := runFeeCLI(append([]string{"payload"}, args...))
	require.NoError(t, err)
	var payload struct {
		TypedData apitypes.TypedData `json:"typed_data"`
		Digest    common.Hash        `json:"digest"`
		Notice    string             `json:"notice"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &payload))
	require.Contains(t, payload.Notice, "Offline")
	independent, _, err := apitypes.TypedDataAndHash(payload.TypedData)
	require.NoError(t, err)
	require.Equal(t, payload.Digest.Bytes(), independent)
	require.NotContains(t, text, keyHex, "the CLI must not emit any private signing key")
	key, err := crypto.HexToECDSA(keyHex)
	require.NoError(t, err)
	sig, err := crypto.Sign(payload.Digest[:], key)
	require.NoError(t, err)
	sigPath := filepath.Join(t.TempDir(), "fee.sig")
	require.NoError(t, os.WriteFile(sigPath, []byte("0x"+hex.EncodeToString(sig)), 0600))
	encodeArgs := append(append([]string{"encode"}, args...), "--fee-signature-file", sigPath)
	encoded, err := runFeeCLI(encodeArgs)
	require.NoError(t, err)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	require.NoError(t, err)
	decoded, err := ownedapp.MakeEncodingConfig(990001).TxConfig.TxDecoder()(raw)
	require.NoError(t, err)
	outer, ok := decoded.(*envelope.Tx)
	require.True(t, ok)
	require.Equal(t, crypto.PubkeyToAddress(key.PublicKey), outer.Authorization().Payer)
	require.Equal(t, sig, outer.FeeSignature())
	require.Equal(t, "100000000", outer.Authorization().ExactInput.String())
	_, originalEthereum, err := evmtypes.UnpackEthMsg(outer.GetMsgs()[0])
	require.NoError(t, err)
	require.Equal(t, originalEthereum.Hash(), outer.Authorization().TxHash)
	path := filepath.Join(t.TempDir(), "envelope.bin")
	_, err = runFeeCLI(append(encodeArgs, "--out", path))
	require.NoError(t, err)
	file, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, raw, file)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
	_, err = runFeeCLI(append(encodeArgs, "--out", path))
	require.Error(t, err)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, raw, after)
}

func TestFeeEnvelopeCLIRejectsWrongDomainAmountsAndSignature(t *testing.T) {
	base, _ := feeCLIArgs(t)
	for _, pair := range [][2]string{{"--cosmos-chain-id", "decimal_75-1"}, {"--evm-id", "990002"}, {"--token", "0x0000000000000000000000000000000000000000"}, {"--exact-input", "-1"}, {"--fee-nonce", "01"}, {"--min-native-output", "1"}, {"--conversion-gas", "100000"}} {
		args := append([]string(nil), base...)
		for i := range args {
			if args[i] == pair[0] {
				args[i+1] = pair[1]
				break
			}
		}
		_, err := runFeeCLI(append([]string{"payload"}, args...))
		require.Error(t, err, pair)
	}
	path := filepath.Join(t.TempDir(), "bad.sig")
	require.NoError(t, os.WriteFile(path, []byte("0x"+strings.Repeat("00", 65)), 0600))
	_, err := runFeeCLI(append(append([]string{"encode"}, base...), "--fee-signature-file", path))
	require.Error(t, err)
	large := filepath.Join(t.TempDir(), "large.hex")
	require.NoError(t, os.WriteFile(large, []byte("0x"+strings.Repeat("ff", 128*1024+1)), 0600))
	base[1] = large
	_, err = runFeeCLI(append([]string{"payload"}, base...))
	require.Error(t, err)
}
