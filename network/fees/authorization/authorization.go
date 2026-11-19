// Package authorization verifies an explicit EIP-712 authorization to exchange
// a payer's DRC20 for native transaction fees. Verification is NOT payment:
// the caller must check an approved open reserve, atomically consume FeeNonce,
// perform the exact-input sale, and pay the native fee in the ante cache.
// No account, token, nonce, bank balance, or transaction is changed here.
package authorization

import (
	"errors"
	"math"
	"math/big"
	"reflect"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	DomainName                       = "MintCandy DRC20 Fee"
	DomainVersion                    = "1"
	DomainType                       = "EIP712Domain(string name,string version,uint256 chainId,address verifyingContract,bytes32 salt)"
	AuthorizationType                = "FeeAuthorization(bytes32 txHash,address payer,address token,uint256 exactInput,uint256 minimumNativeOutput,uint256 nativeFeeBudget,uint256 conversionGasLimit,uint256 deadline,uint256 feeNonce)"
	MaxChainIDBytes                  = 128
	MaxSignedTransactionBytes        = 128 * 1024
	MaxAccessListEntries             = 1024
	MaxAccessListKeys                = 2048
	MaxConversionGasLimit     uint64 = 24_000_000
	// Experimental conservative CPU reservation, not a production benchmark.
	VerificationGas uint64 = 61_000
)

// Policy must come from trusted module configuration, never from a request.
// VerifyingModule is a fixed domain address; it need not be an EVM contract.
// ConversionNativeFee is an explicit protocol charge in native atomic units.
// Token code, reserve openness, liquidity, and eligibility are caller checks.
type Policy struct {
	CosmosChainID         string
	EVMChainID            *big.Int
	VerifyingModule       common.Address
	MaxConversionGasLimit uint64
	ConversionNativeFee   *big.Int
}

// Authorization uses only fixed-width values or bounded uint256 integers.
// ExactInput is the amount sold, never a maximum for an exact-output trade.
// NativeFeeBudget must cover gasLimit*maxFeePerGas plus ConversionNativeFee;
// unused gas and conversion surplus can only be refunded in native DEL.
type Authorization struct {
	TxHash              common.Hash
	Payer               common.Address
	Token               common.Address
	ExactInput          *big.Int
	MinimumNativeOutput *big.Int
	NativeFeeBudget     *big.Int
	ConversionGasLimit  uint64
	Deadline            uint64
	FeeNonce            *big.Int
}

// Meter is satisfied by an SDK GasMeter. Callers must supply ctx.GasMeter(). Out-of-gas
// panics propagate naturally and must never be converted into admission.
type Meter interface{ ConsumeGas(uint64, string) }

// Verified cannot be constructed with authorized fields by another package.
// Its zero value is invalid. It is only a verified signature/binding, NOT a
// replay-safe capability: consume the dedicated fee nonce in the ante cache.
type Verified struct {
	auth  Authorization
	valid bool
}

func (v Verified) Valid() bool                   { return v.valid }
func (v Verified) Payer() common.Address         { return v.auth.Payer }
func (v Verified) Token() common.Address         { return v.auth.Token }
func (v Verified) TxHash() common.Hash           { return v.auth.TxHash }
func (v Verified) ExactInput() *big.Int          { return copyInt(v.auth.ExactInput) }
func (v Verified) MinimumNativeOutput() *big.Int { return copyInt(v.auth.MinimumNativeOutput) }
func (v Verified) NativeFeeBudget() *big.Int     { return copyInt(v.auth.NativeFeeBudget) }
func (v Verified) FeeNonce() *big.Int            { return copyInt(v.auth.FeeNonce) }
func (v Verified) ConversionGasLimit() uint64    { return v.auth.ConversionGasLimit }
func (v Verified) Deadline() uint64              { return v.auth.Deadline }

func copyInt(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}
	return new(big.Int).Set(value)
}

func uint256(value *big.Int, positive bool) bool {
	return value != nil && value.BitLen() <= 256 && value.Sign() >= 0 && (!positive || value.Sign() > 0)
}

func validate(policy Policy, auth Authorization) error {
	if len(policy.CosmosChainID) == 0 || len(policy.CosmosChainID) > MaxChainIDBytes ||
		strings.TrimSpace(policy.CosmosChainID) != policy.CosmosChainID {
		return errors.New("invalid configured Cosmos chain ID")
	}
	for _, c := range []byte(policy.CosmosChainID) {
		if c < 33 || c > 126 {
			return errors.New("Cosmos chain ID must be printable ASCII without whitespace")
		}
	}
	if !uint256(policy.EVMChainID, true) || policy.VerifyingModule == (common.Address{}) ||
		!uint256(policy.ConversionNativeFee, false) || policy.MaxConversionGasLimit == 0 ||
		policy.MaxConversionGasLimit > MaxConversionGasLimit {
		return errors.New("invalid trusted fee authorization policy")
	}
	if auth.TxHash == (common.Hash{}) || auth.Payer == (common.Address{}) || auth.Token == (common.Address{}) ||
		!uint256(auth.ExactInput, true) || !uint256(auth.MinimumNativeOutput, true) ||
		!uint256(auth.NativeFeeBudget, true) || !uint256(auth.FeeNonce, false) ||
		auth.ConversionGasLimit == 0 || auth.ConversionGasLimit > policy.MaxConversionGasLimit ||
		auth.Deadline == 0 || auth.Deadline > math.MaxInt64 {
		return errors.New("malformed or out-of-bounds fee authorization")
	}
	if auth.MinimumNativeOutput.Cmp(auth.NativeFeeBudget) < 0 {
		return errors.New("minimum native output is below signed native fee budget")
	}
	return nil
}

func wordInt(value *big.Int) []byte           { return value.FillBytes(make([]byte, 32)) }
func wordUint(value uint64) []byte            { return wordInt(new(big.Int).SetUint64(value)) }
func wordAddress(value common.Address) []byte { return common.LeftPadBytes(value[:], 32) }

// SigningDigest is the fixed v1 EIP-712 digest for a wallet to sign. The Cosmos
// chain ID is bound as keccak256(UTF8(chainID)) in the domain's bytes32 salt.
// This function does not establish freshness, transaction validity, or payment.
func SigningDigest(policy Policy, auth Authorization) (common.Hash, error) {
	if err := validate(policy, auth); err != nil {
		return common.Hash{}, err
	}
	domain := crypto.Keccak256Hash(
		crypto.Keccak256([]byte(DomainType)), crypto.Keccak256([]byte(DomainName)),
		crypto.Keccak256([]byte(DomainVersion)), wordInt(policy.EVMChainID),
		wordAddress(policy.VerifyingModule), crypto.Keccak256([]byte(policy.CosmosChainID)),
	)
	message := crypto.Keccak256Hash(
		crypto.Keccak256([]byte(AuthorizationType)), auth.TxHash[:], wordAddress(auth.Payer),
		wordAddress(auth.Token), wordInt(auth.ExactInput), wordInt(auth.MinimumNativeOutput),
		wordInt(auth.NativeFeeBudget), wordUint(auth.ConversionGasLimit),
		wordUint(auth.Deadline), wordInt(auth.FeeNonce),
	)
	return crypto.Keccak256Hash([]byte{0x19, 0x01}, domain[:], message[:]), nil
}

func charge(meter Meter) error {
	if meter == nil {
		return errors.New("fee authorization requires a gas meter")
	}
	value := reflect.ValueOf(meter)
	switch value.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		if value.IsNil() {
			return errors.New("typed nil fee authorization gas meter")
		}
	}
	meter.ConsumeGas(VerificationGas, "fees/authorization/v1/two-signature-verification")
	return nil
}

func validateTransaction(tx *types.Transaction) error {
	if tx == nil {
		return errors.New("missing signed Ethereum transaction")
	}
	if tx.Type() != types.LegacyTxType && tx.Type() != types.AccessListTxType && tx.Type() != types.DynamicFeeTxType {
		return errors.New("unsupported Ethereum transaction type for fee authorization")
	}
	if len(tx.Data()) > MaxSignedTransactionBytes || len(tx.AccessList()) > MaxAccessListEntries {
		return errors.New("oversized signed Ethereum transaction")
	}
	keys := 0
	for _, tuple := range tx.AccessList() {
		if len(tuple.StorageKeys) > MaxAccessListKeys-keys {
			return errors.New("oversized Ethereum access list")
		}
		keys += len(tuple.StorageKeys)
	}
	v, r, s := tx.RawSignatureValues()
	if v == nil || r == nil || s == nil || v.Sign() < 0 || v.BitLen() > 260 ||
		!uint256(r, false) || !uint256(s, false) {
		return errors.New("oversized Ethereum signature values")
	}
	if !uint256(tx.ChainId(), true) || !tx.Protected() || tx.Gas() == 0 ||
		!uint256(tx.GasFeeCap(), true) || !uint256(tx.GasTipCap(), false) ||
		tx.GasTipCap().Cmp(tx.GasFeeCap()) > 0 || !uint256(tx.Value(), false) || tx.Size() > MaxSignedTransactionBytes {
		return errors.New("invalid bounded protected Ethereum transaction")
	}
	return nil
}

// Verify checks two independent signatures: the full protected Ethereum
// transaction and a canonical low-S EIP-712 fee authorization by its sender.
// NativeFeeBudget is exactly gasLimit*feeCap + the trusted conversion charge.
// The result remains subject to current account/transaction checks, token
// eligibility, fee-nonce consumption, actual sale, and actual native payment.
// Pass the consensus SDK block time, never wall-clock or RPC time.
func Verify(policy Policy, tx *types.Transaction, auth Authorization, signature []byte, now time.Time, meter Meter) (Verified, error) {
	if err := charge(meter); err != nil {
		return Verified{}, err
	}
	if err := validate(policy, auth); err != nil {
		return Verified{}, err
	}
	if err := validateTransaction(tx); err != nil {
		return Verified{}, err
	}
	if now.IsZero() || now.Unix() < 0 || auth.Deadline < uint64(now.Unix()) {
		return Verified{}, errors.New("expired fee authorization or invalid consensus block time")
	}
	if len(signature) != crypto.SignatureLength || signature[64] > 1 {
		return Verified{}, errors.New("fee authorization requires exactly 65 bytes with recovery ID 0 or 1")
	}
	r, s := new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:64])
	if !crypto.ValidateSignatureValues(signature[64], r, s, true) {
		return Verified{}, errors.New("noncanonical fee authorization signature")
	}
	if tx.ChainId().Cmp(policy.EVMChainID) != 0 || tx.Hash() != auth.TxHash {
		return Verified{}, errors.New("fee authorization does not bind this signed Ethereum transaction and chain")
	}
	budget := new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas()), tx.GasFeeCap())
	budget.Add(budget, policy.ConversionNativeFee)
	if !uint256(budget, true) || auth.NativeFeeBudget.Cmp(budget) != 0 {
		return Verified{}, errors.New("signed native fee budget differs from required transaction and conversion budget")
	}
	digest, err := SigningDigest(policy, auth)
	if err != nil {
		return Verified{}, err
	}
	publicKey, err := crypto.SigToPub(digest[:], signature)
	if err != nil || crypto.PubkeyToAddress(*publicKey) != auth.Payer {
		return Verified{}, errors.New("fee authorization signer is not payer")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(policy.EVMChainID), tx)
	if err != nil || sender != auth.Payer {
		return Verified{}, errors.New("token payer is not verified Ethereum transaction sender")
	}
	copyAuth := auth
	copyAuth.ExactInput = copyInt(auth.ExactInput)
	copyAuth.MinimumNativeOutput = copyInt(auth.MinimumNativeOutput)
	copyAuth.NativeFeeBudget = copyInt(auth.NativeFeeBudget)
	copyAuth.FeeNonce = copyInt(auth.FeeNonce)
	return Verified{auth: copyAuth, valid: true}, nil
}
