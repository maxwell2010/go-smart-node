package main

import (
	"bytes"
	"context"
	sdkmath "cosmossdk.io/math"
	"encoding/hex"
	"encoding/json"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/evm/crypto/hd"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

func TestValidatorControlStatusUsesSDKCodecForConsensusAny(t *testing.T) {
	enc := ownedapp.MakeEncodingConfig(9905401)
	op, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(bytes.Repeat([]byte{1}, 20))
	require.NoError(t, err)
	reward, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(bytes.Repeat([]byte{2}, 20))
	require.NoError(t, err)
	row, err := validatortypes.NewValidator(op, reward, ed25519.GenPrivKeyFromSecret([]byte("disposable status fixture")).PubKey(), validatortypes.Description{Moniker: "Status fixture"}, sdkmath.LegacyZeroDec())
	require.NoError(t, err)
	raw, err := marshalValidatorControlStatus(enc.Codec, 42, row, "ipfs://fixture/avatar.png")
	require.NoError(t, err)
	var result struct {
		Height    int64           `json:"height"`
		Validator json.RawMessage `json:"validator"`
		Avatar    string          `json:"avatar_uri"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.EqualValues(t, 42, result.Height)
	var decoded validatortypes.Validator
	require.NoError(t, enc.Codec.UnmarshalJSON(result.Validator, &decoded))
	require.Equal(t, row.OperatorAddress, decoded.OperatorAddress)
	require.Equal(t, row.ConsensusPubkey.TypeUrl, decoded.ConsensusPubkey.TypeUrl)
	require.Equal(t, row.ConsensusPubkey.Value, decoded.ConsensusPubkey.Value)
}

func TestValidatorControlCommandUsesLocalEthereumKeyring(t *testing.T) {
	enc := ownedapp.MakeEncodingConfig(9905401)
	keys := keyring.NewInMemory(enc.Codec, hd.EthSecp256k1Option())
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	require.NoError(t, keys.ImportPrivKeyHex("operator", hex.EncodeToString(crypto.FromECDSA(key)), "eth_secp256k1"))
	intent, err := buildValidatorControlIntent("mintcandy_9905401-1", 9905401, crypto.PubkeyToAddress(key.PublicKey).Hex(), "0x0000000000000000000000000000000000001234", "disable", 9, 250000, "1000000000000", nil)
	require.NoError(t, err)
	raw, err := json.Marshal(intent)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "intent.json")
	require.NoError(t, os.WriteFile(path, raw, 0600))
	cmd := newValidatorControlCmd()
	cmd.SetContext(context.Background())
	var output bytes.Buffer
	cmd.SetOut(&output)
	ctx := client.Context{}.WithKeyring(keys).WithHomeDir(t.TempDir())
	require.NoError(t, client.SetCmdClientContext(cmd, ctx))
	cmd.SetArgs([]string{"sign", "--intent-file", path, "--from", "operator"})
	require.NoError(t, cmd.Execute())
	var result struct {
		RawEthereumTx  string          `json:"raw_ethereum_tx"`
		CosmosTxBase64 string          `json:"cosmos_tx_base64"`
		CosmosTxJSON   json.RawMessage `json:"cosmos_tx_json"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	encoded, err := hex.DecodeString(result.RawEthereumTx[2:])
	require.NoError(t, err)
	var signed ethtypes.Transaction
	require.NoError(t, signed.UnmarshalBinary(encoded))
	sender, err := ethtypes.Sender(ethtypes.NewEIP155Signer(big.NewInt(9905401)), &signed)
	require.NoError(t, err)
	require.Equal(t, crypto.PubkeyToAddress(key.PublicKey), sender)
	require.Equal(t, uint64(9), signed.Nonce())
	require.NotContains(t, output.String(), hex.EncodeToString(crypto.FromECDSA(key)))
	require.NotEmpty(t, result.CosmosTxBase64)
	require.NotEmpty(t, result.CosmosTxJSON)
	decodedCosmos, err := enc.TxConfig.TxJSONDecoder()(result.CosmosTxJSON)
	require.NoError(t, err)
	require.Len(t, decodedCosmos.GetMsgs(), 1)
}

func TestOfflineValidatorControlSigningAndReplayDomain(t *testing.T) {
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	operator := crypto.PubkeyToAddress(key.PublicKey).Hex()
	intent, err := buildValidatorControlIntent("mintcandy_9905401-1", 9905401, operator, "0x0000000000000000000000000000000000001234", "metadata", 7, 250000, "1000000000000", []byte(`{"name":"A","commission":"0.05"}`))
	require.NoError(t, err)
	data, err := json.Marshal(intent)
	require.NoError(t, err)
	parsed, tx, err := parseValidatorControlIntent(data)
	require.NoError(t, err)
	digest := ethtypes.NewEIP155Signer(big.NewInt(9905401)).Hash(tx)
	signature, err := crypto.Sign(digest[:], key)
	require.NoError(t, err)
	signed, err := signValidatorControlIntent(parsed, tx, signature)
	require.NoError(t, err)
	require.Equal(t, uint64(7), signed.Nonce())
	require.Equal(t, int64(9905401), signed.ChainId().Int64())
	stranger, err := crypto.GenerateKey()
	require.NoError(t, err)
	bad, err := crypto.Sign(digest[:], stranger)
	require.NoError(t, err)
	_, err = signValidatorControlIntent(parsed, tx, bad)
	require.ErrorContains(t, err, "not from this validator")
	parsed.EVMChainID++
	_, err = signValidatorControlIntent(parsed, tx, signature)
	require.Error(t, err)
	parsed = intent
	parsed.SigningHash = "0x00"
	data, err = json.Marshal(parsed)
	require.NoError(t, err)
	_, _, err = parseValidatorControlIntent(data)
	require.ErrorContains(t, err, "signing hash differs")
}

func TestOfflineValidatorControlsRejectWrongDomainsAndValues(t *testing.T) {
	_, err := buildValidatorControlIntent("decimal_75-1", 75, "0x0000000000000000000000000000000000001234", "0x0000000000000000000000000000000000005678", "enable", 0, 250000, "1", nil)
	require.Error(t, err)
	_, err = buildValidatorControlIntent("mintcandy_9905401-1", 9905401, "0x0000000000000000000000000000000000001234", "0x0000000000000000000000000000000000005678", "delete", 0, 250000, "1", nil)
	require.Error(t, err)
}
