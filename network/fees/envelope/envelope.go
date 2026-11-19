// Package envelope transports an original Cosmos-wrapped Ethereum transaction
// and its separate signed fee authorization without modifying the inner tx.
// Decoding grants no authority: the ante handler must verify both signatures,
// consume the fee nonce and execute actual reserve-backed fee payment.
package envelope

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/x/auth/ante"
	"github.com/cosmos/cosmos-sdk/x/auth/signing"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
)

const (
	Magic            = "\x00MCND_DRC20_FEE_V1\x00"
	MaxInnerBytes    = 192 * 1024
	MaxEnvelopeBytes = 512 * 1024
)

type protoProvider interface{ GetProtoTx() *txtypes.Tx }
type originalTx interface {
	signing.Tx
	ante.HasExtensionOptionsTx
	protoProvider
}

// Tx forwards all supported SDK/EVM ante interfaces to the ORIGINAL tx.
// The Comet hash is the outer envelope hash; Ethereum's hash remains unchanged.
type Tx struct {
	originalTx
	auth                              authorization.Authorization
	sig, original, raw, originalProto []byte
}

func clone(a authorization.Authorization) authorization.Authorization {
	copyInt := func(n *big.Int) *big.Int {
		if n == nil {
			return nil
		}
		return new(big.Int).Set(n)
	}
	a.ExactInput, a.MinimumNativeOutput = copyInt(a.ExactInput), copyInt(a.MinimumNativeOutput)
	a.NativeFeeBudget, a.FeeNonce = copyInt(a.NativeFeeBudget), copyInt(a.FeeNonce)
	return a
}
func (t *Tx) Authorization() authorization.Authorization { return clone(t.auth) }
func (t *Tx) FeeSignature() []byte                       { return bytes.Clone(t.sig) }
func (t *Tx) OriginalBytes() []byte                      { return bytes.Clone(t.original) }

// InnerTx is passed to standard EVM ante checks, whose gas/fee/proto fields
// must retain the original Ethereum transaction's exact signed values.
func (t *Tx) InnerTx() sdk.Tx { return t.originalTx }

// GetGas reserves conversion work in outer proposal/mempool resource accounting.
// Standard EVM ante receives InnerTx, not this aggregate resource limit.
func (t *Tx) GetGas() uint64 { return t.originalTx.GetGas() + t.auth.ConversionGasLimit }

type wireAuthorization struct {
	TxHash              string `json:"tx_hash"`
	Payer               string `json:"payer"`
	Token               string `json:"token"`
	ExactInput          string `json:"exact_input"`
	MinimumNativeOutput string `json:"minimum_native_output"`
	NativeFeeBudget     string `json:"native_fee_budget"`
	ConversionGasLimit  uint64 `json:"conversion_gas_limit"`
	Deadline            uint64 `json:"deadline"`
	FeeNonce            string `json:"fee_nonce"`
}
type wireEnvelope struct {
	Version       int               `json:"version"`
	Original      []byte            `json:"original_tx"`
	Authorization wireAuthorization `json:"authorization"`
	Signature     []byte            `json:"fee_signature"`
}

func decimal(s string, positive bool) (*big.Int, error) {
	if len(s) == 0 || len(s) > 78 || (len(s) > 1 && s[0] == '0') {
		return nil, errors.New("invalid canonical uint256")
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return nil, errors.New("nondecimal uint256")
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.BitLen() > 256 || (positive && n.Sign() == 0) {
		return nil, errors.New("uint256 outside fee envelope bounds")
	}
	return n, nil
}
func exactHex(s string, size int) ([]byte, error) {
	if len(s) != 2+2*size || s[:2] != "0x" {
		return nil, errors.New("invalid fixed-width hex")
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil || "0x"+hex.EncodeToString(b) != s {
		return nil, errors.New("noncanonical hex")
	}
	return b, nil
}
func decodeAuthorization(w wireAuthorization) (a authorization.Authorization, err error) {
	h, e := exactHex(w.TxHash, 32)
	if e != nil {
		return a, e
	}
	copy(a.TxHash[:], h)
	p, e := exactHex(w.Payer, 20)
	if e != nil {
		return a, e
	}
	copy(a.Payer[:], p)
	p, e = exactHex(w.Token, 20)
	if e != nil {
		return a, e
	}
	copy(a.Token[:], p)
	a.ExactInput, err = decimal(w.ExactInput, true)
	if err != nil {
		return a, err
	}
	a.MinimumNativeOutput, err = decimal(w.MinimumNativeOutput, true)
	if err != nil {
		return a, err
	}
	a.NativeFeeBudget, err = decimal(w.NativeFeeBudget, true)
	if err != nil {
		return a, err
	}
	a.FeeNonce, err = decimal(w.FeeNonce, false)
	if err != nil {
		return a, err
	}
	a.ConversionGasLimit, a.Deadline = w.ConversionGasLimit, w.Deadline
	if a.TxHash == (common.Hash{}) || a.Payer == (common.Address{}) || a.Token == (common.Address{}) ||
		a.ConversionGasLimit == 0 || a.ConversionGasLimit > authorization.MaxConversionGasLimit || a.Deadline == 0 || a.Deadline > math.MaxInt64 || a.MinimumNativeOutput.Cmp(a.NativeFeeBudget) < 0 {
		return a, errors.New("invalid fee envelope authorization bounds")
	}
	return a, nil
}
func makeWire(original []byte, a authorization.Authorization, sig []byte) (wireEnvelope, error) {
	var zero wireEnvelope
	if len(original) == 0 || len(original) > MaxInnerBytes || bytes.HasPrefix(original, []byte(Magic)) || len(sig) != 65 || sig[64] > 1 {
		return zero, errors.New("invalid original transaction or fee signature bounds")
	}
	for _, n := range []*big.Int{a.ExactInput, a.MinimumNativeOutput, a.NativeFeeBudget, a.FeeNonce} {
		if n == nil {
			return zero, errors.New("nil fee authorization integer")
		}
	}
	w := wireAuthorization{"0x" + hex.EncodeToString(a.TxHash[:]), "0x" + hex.EncodeToString(a.Payer[:]), "0x" + hex.EncodeToString(a.Token[:]), a.ExactInput.String(), a.MinimumNativeOutput.String(), a.NativeFeeBudget.String(), a.ConversionGasLimit, a.Deadline, a.FeeNonce.String()}
	if _, err := decodeAuthorization(w); err != nil {
		return zero, err
	}
	return wireEnvelope{1, bytes.Clone(original), w, bytes.Clone(sig)}, nil
}

// Marshal is an offline transport helper. It does not verify signatures or
// establish whether original is an acceptable transaction for this network.
func Marshal(original []byte, a authorization.Authorization, sig []byte) ([]byte, error) {
	w, err := makeWire(original, a, sig)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	if len(raw)+len(Magic) > MaxEnvelopeBytes {
		return nil, errors.New("fee envelope exceeds transport bound")
	}
	return append([]byte(Magic), raw...), nil
}

// Decoder preserves ordinary SDK transactions verbatim. The new magic is
// invalid ordinary protobuf input, so no successful fallback is permitted.
func Decoder(base sdk.TxDecoder) sdk.TxDecoder {
	return func(raw []byte) (sdk.Tx, error) {
		if base == nil {
			return nil, errors.New("missing base transaction decoder")
		}
		if !bytes.HasPrefix(raw, []byte(Magic)) {
			return base(raw)
		}
		if len(raw) > MaxEnvelopeBytes {
			return nil, errors.New("oversized fee envelope")
		}
		var w wireEnvelope
		d := json.NewDecoder(bytes.NewReader(raw[len(Magic):]))
		d.DisallowUnknownFields()
		if err := d.Decode(&w); err != nil {
			return nil, err
		}
		if d.Decode(new(any)) != io.EOF {
			return nil, errors.New("trailing fee envelope data")
		}
		a, err := decodeAuthorization(w.Authorization)
		if err != nil {
			return nil, err
		}
		canonical, err := Marshal(w.Original, a, w.Signature)
		if err != nil {
			return nil, err
		}
		if w.Version != 1 || !bytes.Equal(raw, canonical) {
			return nil, errors.New("noncanonical fee envelope")
		}
		inner, err := base(w.Original)
		if err != nil {
			return nil, err
		}
		if _, nested := inner.(*Tx); nested {
			return nil, errors.New("nested decoded fee envelope")
		}
		original, ok := inner.(originalTx)
		if !ok || original == nil {
			return nil, errors.New("fee envelope requires complete SDK signing and proto interfaces")
		}
		msgs := original.GetMsgs()
		if len(msgs) != 1 {
			return nil, errors.New("fee envelope requires one Ethereum transaction")
		}
		if _, ok := msgs[0].(*evmtypes.MsgEthereumTx); !ok {
			return nil, errors.New("fee envelope cannot wrap a non-Ethereum transaction")
		}
		p := original.GetProtoTx()
		if p == nil || p.Body == nil || p.AuthInfo == nil || p.AuthInfo.Fee == nil {
			return nil, errors.New("incomplete original proto transaction")
		}
		if original.GetGas() > math.MaxUint64-a.ConversionGasLimit {
			return nil, errors.New("aggregate fee envelope gas overflows")
		}
		protoBytes, err := p.Marshal()
		if err != nil {
			return nil, err
		}
		return &Tx{originalTx: original, auth: clone(a), sig: bytes.Clone(w.Signature), original: bytes.Clone(w.Original), raw: bytes.Clone(raw), originalProto: protoBytes}, nil
	}
}

// Encoder checks that forwarding interfaces have not been used to mutate the
// original tx behind its authorization, and returns the original envelope bytes.
func Encoder(base sdk.TxEncoder) sdk.TxEncoder {
	return func(tx sdk.Tx) ([]byte, error) {
		if base == nil {
			return nil, errors.New("missing base transaction encoder")
		}
		if t, ok := tx.(*Tx); ok {
			if t == nil || t.originalTx == nil {
				return nil, errors.New("nil fee envelope transaction")
			}
			inner, err := base(t.originalTx)
			p := t.originalTx.GetProtoTx()
			if p == nil {
				return nil, errors.New("missing original proto transaction")
			}
			pb, pbErr := p.Marshal()
			if pbErr != nil {
				return nil, pbErr
			}
			if !bytes.Equal(pb, t.originalProto) {
				return nil, errors.New("original fee proto was mutated")
			}
			if err != nil {
				return nil, err
			}
			if !bytes.Equal(inner, t.original) {
				return nil, errors.New("original fee transaction was mutated")
			}
			return bytes.Clone(t.raw), nil
		}
		return base(tx)
	}
}
