package app

import (
	"bytes"
	"errors"
	"math"
	"math/big"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	evmfeeante "github.com/cosmos/evm/ante/evm"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	feegas "github.com/maxwell2010/go-smart-node/network/fees"
	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
	"github.com/maxwell2010/go-smart-node/network/fees/envelope"
	"github.com/maxwell2010/go-smart-node/network/fees/nonce"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const (
	DRC20FeeMaxConversionGas uint64 = 6_000_000
	// The bounded bank settlement is prepaid at this deterministic SDK tariff.
	// It covers nonce finalization, bank I/O and the fee event, not arbitrary calls.
	DRC20FeeSettlementGas uint64 = 100_000
)

func DRC20FeeModuleAddress() common.Address {
	return common.BytesToAddress(authtypes.NewModuleAddress("drc20fees"))
}

// DRC20FeePolicy describes the maximum native reservation signed by the owner.
// Actual conversion fees use metered work plus the bounded settlement tariff.
// Unspent reservation and conversion surplus remain native coins of the payer.
func (app *App) DRC20FeePolicy(tx *ethtypes.Transaction, conversionGas uint64) (authorization.Policy, error) {
	if app == nil || tx == nil || conversionGas <= DRC20FeeSettlementGas || conversionGas > DRC20FeeMaxConversionGas {
		return authorization.Policy{}, errors.New("invalid DRC20 conversion gas allowance")
	}
	return authorization.Policy{CosmosChainID: app.chainID, EVMChainID: new(big.Int).SetUint64(app.evmChainID),
		VerifyingModule: DRC20FeeModuleAddress(), MaxConversionGasLimit: DRC20FeeMaxConversionGas,
		ConversionNativeFee: new(big.Int).Mul(new(big.Int).SetUint64(conversionGas), tx.GasFeeCap())}, nil
}

func (app *App) drc20FeeNonceKeeper() (*nonce.Keeper, error) {
	return nonce.NewKeeper(runtime.NewKVStoreService(app.keys[validatortypes.StoreKey]))
}

func (app *App) DRC20FeeNonce(ctx sdk.Context, payer common.Address) (*big.Int, error) {
	k, err := app.drc20FeeNonceKeeper()
	if err != nil {
		return nil, err
	}
	return k.Current(ctx, payer)
}

func (app *App) conversionGasPrice(ctx sdk.Context, tx *ethtypes.Transaction) (*big.Int, error) {
	price := new(big.Int).Set(tx.GasFeeCap())
	if tx.Type() == ethtypes.DynamicFeeTxType {
		base := app.FeeMarketKeeper.GetBaseFee(ctx)
		if base.IsNil() || base.IsNegative() {
			return nil, errors.New("fee market base fee is unavailable")
		}
		if tx.GasFeeCap().Cmp(base.TruncateInt().BigInt()) < 0 {
			return nil, errors.New("fee cap is below base fee")
		}
		candidate := new(big.Int).Add(base.TruncateInt().BigInt(), tx.GasTipCap())
		if candidate.Cmp(price) < 0 {
			price = candidate
		}
	}
	if price.Sign() <= 0 || price.BitLen() > 256 {
		return nil, errors.New("DRC20 conversion requires positive native gas price")
	}
	return price, nil
}

// drc20FeeAnte is inside BaseApp's ante branch. No child state is committed
// until BOTH conversion and the ordinary Ethereum ante handler have succeeded.
// Message execution is a later cache: an EVM REVERT cannot undo this payment.
func (app *App) drc20FeeAnte(next sdk.AnteHandler) sdk.AnteHandler {
	return func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		outer, opted := tx.(*envelope.Tx)
		if !opted {
			return next(ctx, tx, simulate)
		}
		if !app.usesNativeConsensus(ctx) || app.withdrawals == nil || app.withdrawals.customCollateral == nil {
			return ctx, errors.New("DRC20 fees require the native reserve profile")
		}
		inner := outer.InnerTx()
		if _, err := evmfeeante.ValidateTx(inner); err != nil {
			return ctx, err
		}
		msgs := inner.GetMsgs()
		if len(msgs) != 1 {
			return ctx, errors.New("DRC20 fee envelope requires exactly one Ethereum message")
		}
		msg, ok := msgs[0].(*evmtypes.MsgEthereumTx)
		if !ok || msg == nil {
			return ctx, errors.New("DRC20 fee envelope supports Ethereum transactions only")
		}
		raw := msg.AsTransaction()
		auth := outer.Authorization()
		policy, err := app.DRC20FeePolicy(raw, auth.ConversionGasLimit)
		if err != nil {
			return ctx, err
		}
		if raw.Gas() > math.MaxUint64-auth.ConversionGasLimit {
			return ctx, errors.New("combined fee gas overflows")
		}
		wanted := raw.Gas() + auth.ConversionGasLimit
		params := ctx.ConsensusParams()
		if params.Block == nil || params.Block.MaxGas <= 0 || wanted > uint64(params.Block.MaxGas) {
			return ctx, errors.New("combined transaction and conversion exceed finite block gas")
		}
		if meter := ctx.BlockGasMeter(); meter != nil && meter.GasConsumedToLimit() > uint64(params.Block.MaxGas)-wanted {
			return ctx, errors.New("block has insufficient gas for DRC20 conversion")
		}
		child, write := ctx.CacheContext()
		conversionMeter := storetypes.NewGasMeter(auth.ConversionGasLimit)
		completed := false
		defer func() {
			if !completed {
				// Rejected ante cannot retain financial writes, but the CPU work
				// must still reach BaseApp's error/block gas accounting.
				ctx.GasMeter().ConsumeGas(conversionMeter.GasConsumedToLimit(), "rejected DRC20 conversion work")
			}
		}()
		conversionCtx := child.WithGasMeter(conversionMeter)
		verified, err := authorization.Verify(policy, raw, auth, outer.FeeSignature(), ctx.BlockTime(), conversionMeter)
		if err != nil {
			return ctx, err
		}
		if !bytes.Equal(msg.GetFrom(), verified.Payer().Bytes()) {
			return ctx, errors.New("composite envelope must encode its verified Ethereum sender")
		}
		// Reject a stale Ethereum nonce before executing a costly curve call.
		if app.EVMKeeper.GetNonce(conversionCtx, verified.Payer()) != raw.Nonce() {
			return ctx, errors.New("DRC20 fee Ethereum nonce differs from account")
		}
		nonces, err := app.drc20FeeNonceKeeper()
		if err != nil {
			return ctx, err
		}
		if err := nonces.Consume(conversionCtx, verified); err != nil {
			return ctx, err
		}
		price, err := app.conversionGasPrice(conversionCtx, raw)
		if err != nil {
			return ctx, err
		}
		output, _, err := app.redeemDRC20Fee(conversionCtx, verified)
		if err != nil {
			return ctx, err
		}
		conversionMeter.ConsumeGas(DRC20FeeSettlementGas, "DRC20 bounded native fee settlement")
		paidGas := conversionMeter.GasConsumed()
		fee := new(big.Int).Mul(new(big.Int).SetUint64(paidGas), price)
		if fee.Sign() <= 0 || fee.BitLen() > 256 {
			return ctx, errors.New("invalid native conversion fee")
		}
		// Work after the price is finalized has its own prepaid finite budget.
		settlement := child.WithGasMeter(storetypes.NewGasMeter(DRC20FeeSettlementGas))
		if err := app.BankKeeper.SendCoinsFromAccountToModule(settlement, sdk.AccAddress(verified.Payer().Bytes()),
			authtypes.FeeCollectorName, sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntFromBigInt(fee)))); err != nil {
			return ctx, err
		}
		settlement.EventManager().EmitEvent(sdk.NewEvent("mintcandy_drc20_fee",
			sdk.NewAttribute("payer", verified.Payer().Hex()), sdk.NewAttribute("token", verified.Token().Hex()),
			sdk.NewAttribute("transaction", verified.TxHash().Hex()), sdk.NewAttribute("fee_nonce", verified.FeeNonce().String()),
			sdk.NewAttribute("token_input", verified.ExactInput().String()), sdk.NewAttribute("native_output", output.String()),
			sdk.NewAttribute("conversion_fee", fee.String()), sdk.NewAttribute("conversion_gas", new(big.Int).SetUint64(paidGas).String())))
		// The original SDK/Ethereum envelope is preserved verbatim for existing
		// signature, balance, fee, nonce and fee-market validation.
		result, err := next(child.WithGasMeter(ctx.GasMeter()), inner, simulate)
		if err != nil {
			return ctx, err
		}
		transient := app.FeeMarketKeeper.GetTransientGasWanted(result)
		if transient > math.MaxUint64-auth.ConversionGasLimit {
			return ctx, errors.New("block gas wanted overflows")
		}
		if _, err := app.FeeMarketKeeper.AddTransientGasWanted(result, auth.ConversionGasLimit); err != nil {
			return ctx, err
		}
		meter, err := feegas.WithReservedGas(result.GasMeter(), paidGas)
		if err != nil {
			return ctx, err
		}
		write()
		completed = true
		// Use the parent's committed store, retaining the meter and metadata
		// returned by ordinary ante. A child cache must not escape this method.
		return result.WithMultiStore(ctx.MultiStore()).WithEventManager(ctx.EventManager()).WithGasMeter(meter), nil
	}
}
