package app

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	// These are deterministic work bounds, not a calibrated fee tariff.
	DRC20FeeQuoteSDKGas uint64 = 6_000_000
	DRC20FeeQuoteEVMGas uint64 = 300_000
)

// DRC20FeeQuote is a detached observation of the current canonical curve.
// It neither reserves liquidity nor authorizes a debit. A future fee handler
// must recheck the signed limits against its own execution state atomically.
type DRC20FeeQuote struct {
	Token               common.Address
	Symbol              string
	ExactInput          *big.Int
	NativeOutput        *big.Int
	MinimumNativeOutput *big.Int
	NativeFeeBudget     *big.Int
	Supply              *big.Int
	Reserve             *big.Int
	MinimumSupply       *big.Int
	MinimumReserve      *big.Int
	SDKGasUsed          uint64
}

// QuoteDRC20Fee admits any canonical backed FAMILY=0 asset of the pinned
// governance-owned TokenCenter. The collateral genesis manifest is deliberately
// not a fee-token allowlist. Reserveless assets and WDEL are never eligible.
// No ante handler or fee payment is installed by this read-only primitive.
func (app *App) QuoteDRC20Fee(ctx sdk.Context, token common.Address, exactInput, minimumNativeOutput,
	deadline, nativeFeeBudget *big.Int) (quote DRC20FeeQuote, err error) {
	if app == nil || app.withdrawals == nil || app.withdrawals.customCollateral == nil || app.EVMKeeper == nil {
		return quote, errors.New("canonical DRC20 fee profile is not installed")
	}
	if ctx.ChainID() != app.chainID || ctx.BlockTime().IsZero() || ctx.BlockTime().Unix() < 0 {
		return quote, errors.New("DRC20 fee quote requires the application chain and block time")
	}
	child, _ := ctx.CacheContext()
	meter := storetypes.NewGasMeter(DRC20FeeQuoteSDKGas)
	child = child.WithGasMeter(meter)
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(storetypes.ErrorOutOfGas); !ok {
				panic(recovered)
			}
			quote = DRC20FeeQuote{}
			err = errors.New("DRC20 fee quote SDK gas limit exceeded")
		}
		used := meter.GasConsumedToLimit()
		ctx.GasMeter().ConsumeGas(used, "bounded DRC20 fee quote")
		if err == nil {
			quote.SDKGasUsed = used
		}
	}()
	meter.ConsumeGas(1_000, "DRC20 fee quote admission")
	for _, value := range []*big.Int{exactInput, minimumNativeOutput, nativeFeeBudget} {
		if value == nil || value.Sign() <= 0 || value.BitLen() > 256 {
			return quote, errors.New("DRC20 fee input, minOut and native fee budget must be positive uint256")
		}
	}
	if deadline == nil || !deadline.IsInt64() || deadline.Sign() < 0 || deadline.Int64() < ctx.BlockTime().Unix() {
		return quote, errors.New("DRC20 fee quote deadline expired or invalid")
	}
	r := app.withdrawals.customCollateral
	if token == (common.Address{}) || token == r.profile.WrappedDEL {
		return quote, errors.New("DRC20 fee payment requires a backed custom token")
	}
	if err = r.authenticateRoles(child); err != nil {
		return quote, err
	}
	// FACTORY and FAMILY are embedded immutables of this exact runtime.
	if !r.exactCode(child, token, r.coinProxyCode) ||
		r.evm.GetState(child, token, customBeaconSlot) != customAddressWord(r.profile.TokenCenter) ||
		r.evm.GetState(child, token, customImplementationSlot) != (common.Hash{}) {
		return quote, errors.New("DRC20 fee token is not the canonical backed FAMILY=0 proxy")
	}
	symbolWord := r.evm.GetState(child, token, customSlotOffset(customERC20Slot, 4))
	symbol, err := reserveFeeSymbol(symbolWord)
	if err != nil {
		return quote, err
	}
	mapping := customSlotOffset(customTokenCenterSlot, 3)
	if r.evm.GetState(child, r.profile.TokenCenter, crypto.Keccak256Hash([]byte(symbol), mapping[:])) != customAddressWord(token) {
		return quote, errors.New("DRC20 fee symbol is not bound to this exact TokenCenter asset")
	}
	paused, err := r.reserveFeeReadWord(child, r.profile.TokenCenter, crypto.Keccak256([]byte("paused()"))[:4])
	if err != nil || paused.Sign() != 0 {
		return quote, errors.New("DRC20 fee reserve is paused or unreadable")
	}
	supply := r.evm.GetState(child, token, customSlotOffset(customERC20Slot, 2)).Big()
	minimumSupply := r.evm.GetState(child, token, customSlotOffset(customTokenSlot, 1)).Big()
	maximumSupply := r.evm.GetState(child, token, customSlotOffset(customTokenSlot, 2)).Big()
	crr := r.evm.GetState(child, token, customTokenSlot).Big()
	minimumReserve := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil) // pinned DRC20.MIN_RESERVE
	maximumSupportedSupply := new(big.Int).Mul(big.NewInt(1_000_000_000_000_000), minimumReserve)
	if !crr.IsUint64() || crr.Uint64() < 10 || crr.Uint64() > 100 || minimumSupply.Cmp(minimumReserve) < 0 ||
		maximumSupply.Cmp(maximumSupportedSupply) > 0 || minimumSupply.Cmp(maximumSupply) > 0 ||
		supply.Cmp(minimumSupply) < 0 || supply.Cmp(maximumSupply) > 0 ||
		exactInput.Cmp(new(big.Int).Sub(supply, minimumSupply)) > 0 {
		return quote, errors.New("DRC20 fee conversion exceeds the canonical curve supply or minimum volume")
	}
	reserve, err := r.spendableReserve(child, token)
	if err != nil {
		return quote, err
	}
	if reserve.IsNil() || reserve.IsNegative() || reserve.BigInt().BitLen() > 256 || reserve.BigInt().Cmp(minimumReserve) < 0 {
		return quote, errors.New("DRC20 fee reserve is below its native DEL floor")
	}
	data := append(bytes.Clone(crypto.Keccak256([]byte("calculateSellOutput(uint256)"))[:4]), common.LeftPadBytes(exactInput.Bytes(), 32)...)
	output, err := r.reserveFeeReadWord(child, token, data)
	if err != nil {
		return quote, err
	}
	if output.Sign() <= 0 || output.Cmp(minimumNativeOutput) < 0 || output.Cmp(nativeFeeBudget) < 0 ||
		new(big.Int).Sub(reserve.BigInt(), output).Cmp(minimumReserve) < 0 {
		return quote, errors.New("DRC20 fee quote cannot cover minOut, native fee budget or remaining reserve floor")
	}
	return DRC20FeeQuote{Token: token, Symbol: symbol, ExactInput: new(big.Int).Set(exactInput), NativeOutput: output,
		MinimumNativeOutput: new(big.Int).Set(minimumNativeOutput), NativeFeeBudget: new(big.Int).Set(nativeFeeBudget),
		Supply: supply, Reserve: new(big.Int).Set(reserve.BigInt()), MinimumSupply: minimumSupply, MinimumReserve: minimumReserve}, nil
}

// Canonical symbols are 3..10 lowercase ASCII letters/digits, hence Solidity
// short strings. Reject malformed padding/length instead of allocating from a
// caller-controlled long-string length.
func reserveFeeSymbol(word common.Hash) (string, error) {
	n := int(word[31]) / 2
	if word[31]%2 != 0 || n < 3 || n > 10 {
		return "", errors.New("DRC20 fee symbol is not canonical")
	}
	for _, c := range word[:n] {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return "", errors.New("DRC20 fee symbol is not canonical")
		}
	}
	if !bytes.Equal(word[n:31], make([]byte, 31-n)) {
		return "", errors.New("DRC20 fee symbol has noncanonical storage padding")
	}
	return string(word[:n]), nil
}

func (r *customCollateralRegistry) reserveFeeReadWord(ctx sdk.Context, target common.Address, data []byte) (*big.Int, error) {
	child, _ := ctx.CacheContext()
	cfg, err := r.evm.EVMConfig(child, child.BlockHeader().ProposerAddress)
	if err != nil {
		return nil, err
	}
	message := core.Message{From: common.Address{}, To: &target, Value: new(big.Int), GasPrice: new(big.Int),
		GasFeeCap: new(big.Int), GasTipCap: new(big.Int), GasLimit: DRC20FeeQuoteEVMGas, Data: bytes.Clone(data),
		SkipNonceChecks: true, SkipFromEOACheck: true}
	stateDB := statedb.New(child, r.evm, statedb.NewEmptyTxConfig())
	evm := r.evm.NewEVM(child, message, cfg, nil, stateDB)
	// Actual STATICCALL forbids mutating opcodes. The SDK child is discarded as
	// a second boundary, including native-precompile speculative state/events.
	ret, left, err := evm.StaticCall(common.Address{}, target, message.Data, DRC20FeeQuoteEVMGas)
	if left > DRC20FeeQuoteEVMGas {
		return nil, errors.New("DRC20 fee getter returned invalid gas accounting")
	}
	ctx.GasMeter().ConsumeGas(DRC20FeeQuoteEVMGas-left, "DRC20 fee static EVM getter")
	if err != nil {
		return nil, fmt.Errorf("DRC20 fee static getter: %w", err)
	}
	if len(ret) != common.HashLength {
		return nil, errors.New("DRC20 fee getter returned malformed output")
	}
	return new(big.Int).SetBytes(ret), nil
}
