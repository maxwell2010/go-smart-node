package envelope_test

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	"github.com/cosmos/evm/encoding"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
	"github.com/maxwell2010/go-smart-node/network/fees/envelope"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) (sdk.TxDecoder, sdk.TxEncoder, []byte, authorization.Authorization, []byte) {
	t.Helper()
	key, err := crypto.HexToECDSA("7b181d51dc41a06e32d4c1aa30c0eeeb3130067d92758b1cb9e6bd48340a77f4")
	require.NoError(t, err)
	to := common.HexToAddress("0x2000000000000000000000000000000000000001")
	eth, err := ethtypes.SignTx(ethtypes.NewTransaction(2, to, big.NewInt(0), 50000, big.NewInt(100), []byte{1, 2}), ethtypes.NewEIP155Signer(big.NewInt(990001)), key)
	require.NoError(t, err)
	msg := &evmtypes.MsgEthereumTx{}
	require.NoError(t, msg.FromSignedEthereumTx(eth, ethtypes.NewEIP155Signer(big.NewInt(990001))))
	cfg := encoding.MakeConfig(990001)
	evmtypes.RegisterInterfaces(cfg.InterfaceRegistry)
	builder := cfg.TxConfig.NewTxBuilder()
	require.NoError(t, builder.SetMsgs(msg))
	builder.SetGasLimit(50000)
	builder.SetFeeAmount(sdk.NewCoins(sdk.NewInt64Coin("del", 5000000)))
	opt, err := codectypes.NewAnyWithValue(&evmtypes.ExtensionOptionsEthereumTx{})
	require.NoError(t, err)
	builder.(authtx.ExtensionOptionsTxBuilder).SetExtensionOptions(opt)
	original, err := cfg.TxConfig.TxEncoder()(builder.GetTx())
	require.NoError(t, err)
	a := authorization.Authorization{TxHash: eth.Hash(), Payer: crypto.PubkeyToAddress(key.PublicKey), Token: to, ExactInput: big.NewInt(100), MinimumNativeOutput: big.NewInt(5000001), NativeFeeBudget: big.NewInt(5000001), FeeNonce: big.NewInt(0), ConversionGasLimit: 300000, Deadline: 1800000000}
	digest, err := authorization.SigningDigest(authorization.Policy{CosmosChainID: "mintcandy_990001-1", EVMChainID: big.NewInt(990001), VerifyingModule: to, MaxConversionGasLimit: 400000, ConversionNativeFee: big.NewInt(1)}, a)
	require.NoError(t, err)
	sig, err := crypto.Sign(digest[:], key)
	require.NoError(t, err)
	return cfg.TxConfig.TxDecoder(), cfg.TxConfig.TxEncoder(), original, a, sig
}

func TestRealSDKOriginalAndSeparateFeeEnvelopeRoundTrip(t *testing.T) {
	baseD, baseE, original, a, sig := fixture(t)
	plain, err := envelope.Decoder(baseD)(original)
	require.NoError(t, err)
	b, err := envelope.Encoder(baseE)(plain)
	require.NoError(t, err)
	require.Equal(t, original, b)
	raw, err := envelope.Marshal(original, a, sig)
	require.NoError(t, err)
	tx, err := envelope.Decoder(baseD)(raw)
	require.NoError(t, err)
	wrapped := tx.(*envelope.Tx)
	require.Equal(t, uint64(350000), wrapped.GetGas())
	require.Equal(t, uint64(50000), wrapped.InnerTx().(sdk.FeeTx).GetGas())
	require.Equal(t, uint64(50000), wrapped.GetProtoTx().AuthInfo.Fee.GasLimit)
	require.Empty(t, wrapped.GetMemo())
	require.Len(t, wrapped.GetExtensionOptions(), 1)
	plainMsg, err := plain.GetMsgs()[0].(*evmtypes.MsgEthereumTx).Marshal()
	require.NoError(t, err)
	wrappedMsg, err := wrapped.GetMsgs()[0].(*evmtypes.MsgEthereumTx).Marshal()
	require.NoError(t, err)
	require.Equal(t, plainMsg, wrappedMsg, "message wire bytes must survive transport; decoded Any caches are not wire data")
	b, err = envelope.Encoder(baseE)(wrapped)
	require.NoError(t, err)
	require.Equal(t, raw, b)
	require.Equal(t, a.TxHash, wrapped.Authorization().TxHash)
	copyA := wrapped.Authorization()
	copyA.ExactInput.SetInt64(0)
	copyA.FeeNonce.SetInt64(9)
	require.Equal(t, "100", wrapped.Authorization().ExactInput.String())
	require.Zero(t, wrapped.Authorization().FeeNonce.Sign())
	copyS := wrapped.FeeSignature()
	copyS[0] ^= 1
	require.Equal(t, sig, wrapped.FeeSignature())
	copyO := wrapped.OriginalBytes()
	copyO[0] ^= 1
	require.Equal(t, original, wrapped.OriginalBytes())
	wrapped.GetProtoTx().Body.Memo = "forbidden mutation"
	_, err = envelope.Encoder(baseE)(wrapped)
	require.Error(t, err)
}

func TestMalformedTransportRejectedBeforeInnerDecode(t *testing.T) {
	_, _, original, a, sig := fixture(t)
	raw, err := envelope.Marshal(original, a, sig)
	require.NoError(t, err)
	jsonBody := raw[len(envelope.Magic):]
	var wire map[string]any
	require.NoError(t, json.Unmarshal(jsonBody, &wire))
	checks := [][]byte{
		[]byte(envelope.Magic + "{}"),
		append(bytes.Clone(raw), ' '),
		append(bytes.Clone(raw), []byte("{}")...),
		[]byte(envelope.Magic + strings.Repeat("x", envelope.MaxEnvelopeBytes)),
		append([]byte(envelope.Magic+`{"version":1,"version":1,`), jsonBody[len(`{"version":1,`):]...),
		bytes.Replace(raw, []byte(`"version":1`), []byte(`"unknown":true,"version":1`), 1),
		bytes.Replace(raw, []byte(`"exact_input":"100"`), []byte(`"exact_input":"0100"`), 1),
		bytes.Replace(raw, []byte(`"conversion_gas_limit":300000`), []byte(`"conversion_gas_limit":0`), 1),
	}
	for i, bad := range checks {
		t.Run(big.NewInt(int64(i)).String(), func(t *testing.T) {
			called := false
			_, err := envelope.Decoder(func([]byte) (sdk.Tx, error) { called = true; return nil, nil })(bad)
			require.Error(t, err)
			require.False(t, called)
		})
	}
	_, err = envelope.Marshal(raw, a, sig)
	require.Error(t, err, "nested transport must be rejected")
	_, err = envelope.Marshal(original, a, sig[:64])
	require.Error(t, err)
	_, err = envelope.Decoder(nil)(raw)
	require.Error(t, err)
}
