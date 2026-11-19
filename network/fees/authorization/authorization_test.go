package authorization_test

import (
	"crypto/ecdsa"
	"math/big"
	"strings"
	"testing"
	"time"

	storetypes "cosmossdk.io/store/types"
	"github.com/ethereum/go-ethereum/common"
	ethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/stretchr/testify/require"

	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
)

type fixture struct {
	policy    authorization.Policy
	auth      authorization.Authorization
	tx        *types.Transaction
	key       *ecdsa.PrivateKey
	signature []byte
	now       time.Time
}

func setup(t *testing.T) fixture {
	t.Helper()
	key, err := crypto.HexToECDSA("7b181d51dc41a06e32d4c1aa30c0eeeb3130067d92758b1cb9e6bd48340a77f4")
	require.NoError(t, err)
	policy := authorization.Policy{
		CosmosChainID: "mintcandy-test-1", EVMChainID: big.NewInt(20261118),
		VerifyingModule:       common.HexToAddress("0x10000000000000000000000000000000000000fe"),
		MaxConversionGasLimit: 400000, ConversionNativeFee: big.NewInt(700),
	}
	to := common.HexToAddress("0x2000000000000000000000000000000000000001")
	tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
		ChainID: policy.EVMChainID, Nonce: 9, GasTipCap: big.NewInt(3), GasFeeCap: big.NewInt(100),
		Gas: 50000, To: &to, Value: big.NewInt(0), Data: []byte{0xde, 0xad, 0xbe, 0xef},
	}), types.LatestSignerForChainID(policy.EVMChainID), key)
	require.NoError(t, err)
	auth := authorization.Authorization{
		TxHash: tx.Hash(), Payer: crypto.PubkeyToAddress(key.PublicKey),
		Token:      common.HexToAddress("0x3000000000000000000000000000000000000001"),
		ExactInput: big.NewInt(12000000), MinimumNativeOutput: big.NewInt(6000000),
		NativeFeeBudget: big.NewInt(5000700), ConversionGasLimit: 300000,
		Deadline: 1800000300, FeeNonce: big.NewInt(17),
	}
	f := fixture{policy: policy, auth: auth, tx: tx, key: key, now: time.Unix(1800000000, 0)}
	f.sign(t)
	return f
}

func (f *fixture) sign(t *testing.T) {
	t.Helper()
	digest, err := authorization.SigningDigest(f.policy, f.auth)
	require.NoError(t, err)
	f.signature, err = crypto.Sign(digest[:], f.key)
	require.NoError(t, err)
}

func (f fixture) verify() (authorization.Verified, error) {
	return authorization.Verify(f.policy, f.tx, f.auth, f.signature, f.now, storetypes.NewGasMeter(authorization.VerificationGas))
}

// This vector encodes the complete schema independently using geth's typed-data
// implementation, rather than reusing the verifier's word-encoding helpers.
func TestDigestMatchesIndependentGethTypedData(t *testing.T) {
	f := setup(t)
	chainID := ethmath.HexOrDecimal256(*f.policy.EVMChainID)
	typed := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain":     {{Name: "name", Type: "string"}, {Name: "version", Type: "string"}, {Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"}, {Name: "salt", Type: "bytes32"}},
			"FeeAuthorization": {{Name: "txHash", Type: "bytes32"}, {Name: "payer", Type: "address"}, {Name: "token", Type: "address"}, {Name: "exactInput", Type: "uint256"}, {Name: "minimumNativeOutput", Type: "uint256"}, {Name: "nativeFeeBudget", Type: "uint256"}, {Name: "conversionGasLimit", Type: "uint256"}, {Name: "deadline", Type: "uint256"}, {Name: "feeNonce", Type: "uint256"}},
		}, PrimaryType: "FeeAuthorization",
		Domain: apitypes.TypedDataDomain{Name: "MintCandy DRC20 Fee", Version: "1", ChainId: &chainID,
			VerifyingContract: f.policy.VerifyingModule.Hex(), Salt: crypto.Keccak256Hash([]byte(f.policy.CosmosChainID)).Hex()},
		Message: apitypes.TypedDataMessage{
			"txHash": f.auth.TxHash.Hex(), "payer": f.auth.Payer.Hex(), "token": f.auth.Token.Hex(),
			"exactInput": "12000000", "minimumNativeOutput": "6000000", "nativeFeeBudget": "5000700",
			"conversionGasLimit": "300000", "deadline": "1800000300", "feeNonce": "17",
		},
	}
	independent, _, err := apitypes.TypedDataAndHash(typed)
	require.NoError(t, err)
	ours, err := authorization.SigningDigest(f.policy, f.auth)
	require.NoError(t, err)
	require.Equal(t, common.BytesToHash(independent), ours)
	// Sign the independent digest as well: this also exercises recovery binding.
	f.signature, err = crypto.Sign(independent, f.key)
	require.NoError(t, err)
	verified, err := f.verify()
	require.NoError(t, err)
	require.True(t, verified.Valid())
}

func TestVerifiedFieldsAndNoBigIntegerAliases(t *testing.T) {
	f := setup(t)
	meter := storetypes.NewGasMeter(authorization.VerificationGas)
	verified, err := authorization.Verify(f.policy, f.tx, f.auth, f.signature, f.now, meter)
	require.NoError(t, err)
	require.Equal(t, authorization.VerificationGas, meter.GasConsumed())
	require.True(t, verified.Valid())
	require.Equal(t, f.auth.Payer, verified.Payer())
	require.Equal(t, f.auth.Token, verified.Token())
	require.Equal(t, f.tx.Hash(), verified.TxHash())
	require.Equal(t, f.auth.Deadline, verified.Deadline())
	require.Equal(t, f.auth.ConversionGasLimit, verified.ConversionGasLimit())
	for _, pair := range []struct {
		original *big.Int
		read     func() *big.Int
	}{
		{f.auth.ExactInput, verified.ExactInput}, {f.auth.MinimumNativeOutput, verified.MinimumNativeOutput},
		{f.auth.NativeFeeBudget, verified.NativeFeeBudget}, {f.auth.FeeNonce, verified.FeeNonce},
	} {
		expected := new(big.Int).Set(pair.original)
		pair.original.SetInt64(0)
		require.Equal(t, expected, pair.read())
		pair.read().SetInt64(0)
		require.Equal(t, expected, pair.read())
	}
	require.False(t, (authorization.Verified{}).Valid())
}

func TestSignedFieldAndDomainTamperingRejected(t *testing.T) {
	cases := map[string]func(*fixture){
		"cosmos chain":               func(f *fixture) { f.policy.CosmosChainID = "other-test-1" },
		"evm chain":                  func(f *fixture) { f.policy.EVMChainID = big.NewInt(20261119) },
		"module domain":              func(f *fixture) { f.policy.VerifyingModule[19]++ },
		"full tx hash":               func(f *fixture) { f.auth.TxHash[0] ^= 1 },
		"payer":                      func(f *fixture) { f.auth.Payer[19]++ },
		"token":                      func(f *fixture) { f.auth.Token[19]++ },
		"exact input":                func(f *fixture) { f.auth.ExactInput.Add(f.auth.ExactInput, big.NewInt(1)) },
		"min out":                    func(f *fixture) { f.auth.MinimumNativeOutput.Add(f.auth.MinimumNativeOutput, big.NewInt(1)) },
		"native budget":              func(f *fixture) { f.auth.NativeFeeBudget.Add(f.auth.NativeFeeBudget, big.NewInt(1)) },
		"conversion gas":             func(f *fixture) { f.auth.ConversionGasLimit++ },
		"deadline":                   func(f *fixture) { f.auth.Deadline++ },
		"fee nonce":                  func(f *fixture) { f.auth.FeeNonce.Add(f.auth.FeeNonce, big.NewInt(1)) },
		"conversion protocol charge": func(f *fixture) { f.policy.ConversionNativeFee.Add(f.policy.ConversionNativeFee, big.NewInt(1)) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			mutate(&f)
			v, err := f.verify()
			require.Error(t, err)
			require.False(t, v.Valid())
		})
	}
}

func TestSignatureMustBeExplicitCanonicalFeeAuthorization(t *testing.T) {
	cases := map[string]func(*fixture){
		"ordinary Ethereum signature": func(f *fixture) {
			h := types.LatestSignerForChainID(f.policy.EVMChainID).Hash(f.tx)
			f.signature, _ = crypto.Sign(h[:], f.key)
		},
		"wrong key": func(f *fixture) {
			key, err := crypto.GenerateKey()
			require.NoError(t, err)
			digest, err := authorization.SigningDigest(f.policy, f.auth)
			require.NoError(t, err)
			f.signature, err = crypto.Sign(digest[:], key)
			require.NoError(t, err)
		},
		"27 recovery ID":      func(f *fixture) { f.signature[64] += 27 },
		"invalid recovery ID": func(f *fixture) { f.signature[64] = 2 },
		"high S": func(f *fixture) {
			s := new(big.Int).SetBytes(f.signature[32:64])
			s.Sub(crypto.S256().Params().N, s)
			s.FillBytes(f.signature[32:64])
			f.signature[64] ^= 1
		},
		"zero R": func(f *fixture) { clear(f.signature[:32]) },
		"short":  func(f *fixture) { f.signature = f.signature[:64] },
		"long":   func(f *fixture) { f.signature = append(f.signature, 0) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			mutate(&f)
			v, err := f.verify()
			require.Error(t, err)
			require.False(t, v.Valid())
		})
	}
	// Even two valid signatures do not authorize another token owner's tx.
	f := setup(t)
	other, err := crypto.GenerateKey()
	require.NoError(t, err)
	f.key = other
	f.auth.Payer = crypto.PubkeyToAddress(other.PublicKey)
	f.sign(t)
	_, err = f.verify()
	require.ErrorContains(t, err, "transaction sender")
}

func TestMalformedAndExpiredBounds(t *testing.T) {
	cases := map[string]func(*fixture){
		"nil tx":                   func(f *fixture) { f.tx = nil },
		"nil chain":                func(f *fixture) { f.policy.EVMChainID = nil },
		"nil charge":               func(f *fixture) { f.policy.ConversionNativeFee = nil },
		"negative charge":          func(f *fixture) { f.policy.ConversionNativeFee = big.NewInt(-1) },
		"huge chain":               func(f *fixture) { f.policy.EVMChainID = new(big.Int).Lsh(big.NewInt(1), 256) },
		"huge input":               func(f *fixture) { f.auth.ExactInput = new(big.Int).Lsh(big.NewInt(1), 256) },
		"negative input":           func(f *fixture) { f.auth.ExactInput = big.NewInt(-1) },
		"zero input":               func(f *fixture) { f.auth.ExactInput = big.NewInt(0) },
		"nil nonce":                func(f *fixture) { f.auth.FeeNonce = nil },
		"huge nonce":               func(f *fixture) { f.auth.FeeNonce = new(big.Int).Lsh(big.NewInt(1), 256) },
		"zero module":              func(f *fixture) { f.policy.VerifyingModule = common.Address{} },
		"zero token":               func(f *fixture) { f.auth.Token = common.Address{} },
		"zero payer":               func(f *fixture) { f.auth.Payer = common.Address{} },
		"expired":                  func(f *fixture) { f.now = time.Unix(int64(f.auth.Deadline)+1, 0) },
		"zero time":                func(f *fixture) { f.now = time.Time{} },
		"negative time":            func(f *fixture) { f.now = time.Unix(-1, 0) },
		"deadline overflow":        func(f *fixture) { f.auth.Deadline = ^uint64(0) },
		"conversion gas exceeded":  func(f *fixture) { f.auth.ConversionGasLimit = f.policy.MaxConversionGasLimit + 1 },
		"zero conversion gas":      func(f *fixture) { f.auth.ConversionGasLimit = 0 },
		"min out under budget":     func(f *fixture) { f.auth.MinimumNativeOutput = big.NewInt(1) },
		"overlong chain":           func(f *fixture) { f.policy.CosmosChainID = strings.Repeat("a", 129) },
		"invalid chain whitespace": func(f *fixture) { f.policy.CosmosChainID = "test\nchain" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) { f := setup(t); mutate(&f); _, err := f.verify(); require.Error(t, err) })
	}
	// uint256 maximum nonce is a valid signature value. The future nonce store
	// must reject it when it cannot atomically increment, rather than wrap.
	f := setup(t)
	f.auth.FeeNonce = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	f.sign(t)
	_, err := f.verify()
	require.NoError(t, err)
	f = setup(t)
	f.now = time.Unix(int64(f.auth.Deadline), 0)
	_, err = f.verify()
	require.NoError(t, err)
}

func TestMeterRequiredAndOutOfGasPropagates(t *testing.T) {
	f := setup(t)
	_, err := authorization.Verify(f.policy, f.tx, f.auth, f.signature, f.now, nil)
	require.Error(t, err)
	var typedNil *countMeter
	_, err = authorization.Verify(f.policy, f.tx, f.auth, f.signature, f.now, typedNil)
	require.Error(t, err)
	require.Panics(t, func() {
		_, _ = authorization.Verify(f.policy, f.tx, f.auth, f.signature, f.now, storetypes.NewGasMeter(authorization.VerificationGas-1))
	})
	meter := storetypes.NewGasMeter(authorization.VerificationGas)
	_, err = authorization.Verify(f.policy, nil, f.auth, f.signature, f.now, meter)
	require.Error(t, err)
	require.Equal(t, authorization.VerificationGas, meter.GasConsumed())
}

type countMeter struct{}

func (*countMeter) ConsumeGas(uint64, string) { panic("typed nil meter must not be called") }

func TestEthereumTransactionBoundsAndProtectedSignature(t *testing.T) {
	f := setup(t)
	to := *f.tx.To()
	for name, inner := range map[string]types.TxData{
		"unsigned":            &types.DynamicFeeTx{ChainID: f.policy.EVMChainID, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(100), Gas: 50000, To: &to},
		"oversized data":      &types.DynamicFeeTx{ChainID: f.policy.EVMChainID, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(100), Gas: 50000, To: &to, Data: make([]byte, authorization.MaxSignedTransactionBytes+1)},
		"access list entries": &types.AccessListTx{ChainID: f.policy.EVMChainID, GasPrice: big.NewInt(100), Gas: 50000, To: &to, AccessList: make(types.AccessList, authorization.MaxAccessListEntries+1)},
		"access list keys":    &types.AccessListTx{ChainID: f.policy.EVMChainID, GasPrice: big.NewInt(100), Gas: 50000, To: &to, AccessList: types.AccessList{{Address: to, StorageKeys: make([]common.Hash, authorization.MaxAccessListKeys+1)}}},
		"zero gas":            &types.DynamicFeeTx{ChainID: f.policy.EVMChainID, GasFeeCap: big.NewInt(100), Gas: 0, To: &to},
	} {
		t.Run(name, func(t *testing.T) {
			g := setup(t)
			g.tx = types.NewTx(inner)
			g.auth.TxHash = g.tx.Hash()
			g.sign(t)
			_, err := g.verify()
			require.Error(t, err)
		})
	}
	legacy, err := types.SignTx(types.NewTx(&types.LegacyTx{Nonce: 9, GasPrice: big.NewInt(100), Gas: 50000, To: &to}), types.HomesteadSigner{}, f.key)
	require.NoError(t, err)
	f.tx = legacy
	f.auth.TxHash = legacy.Hash()
	f.sign(t)
	_, err = f.verify()
	require.Error(t, err)
	// Both protected legacy and access-list signatures use the same binding.
	for _, inner := range []types.TxData{
		&types.LegacyTx{Nonce: 9, GasPrice: big.NewInt(100), Gas: 50000, To: &to},
		&types.AccessListTx{ChainID: f.policy.EVMChainID, Nonce: 9, GasPrice: big.NewInt(100), Gas: 50000, To: &to},
	} {
		g := setup(t)
		g.tx, err = types.SignTx(types.NewTx(inner), types.LatestSignerForChainID(g.policy.EVMChainID), g.key)
		require.NoError(t, err)
		g.auth.TxHash = g.tx.Hash()
		g.sign(t)
		_, err = g.verify()
		require.NoError(t, err)
	}
}
