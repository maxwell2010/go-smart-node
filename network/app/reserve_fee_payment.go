package app

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	coinkeeper "github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

type reserveFeeCallKey struct{}

// An unexported capability scoped to this single exact-input sale. A request,
// token allowance, or ordinary signed EVM call cannot supply this capability.
type reserveFeeCall struct {
	message core.Message
	quote   DRC20FeeQuote
	payer   common.Address
}

func (c *withdrawalController) authorizeReserveFeeCommit(ctx sdk.Context, msg core.Message, logs []*ethtypes.Log) (bool, error) {
	capability, ok := ctx.Value(reserveFeeCallKey{}).(*reserveFeeCall)
	if !ok {
		return false, nil
	}
	if capability == nil || c.customCollateral == nil || !c.profile.EnableNativeConsensus ||
		msg.To == nil || capability.message.To == nil || *msg.To != *capability.message.To ||
		msg.From != capability.payer || msg.GasLimit != capability.message.GasLimit ||
		msg.Value == nil || msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 ||
		!bytes.Equal(msg.Data, capability.message.Data) {
		return true, errors.New("internal DRC20 fee sale differs from its private capability")
	}
	q := capability.quote
	if len(logs) != 2 {
		return true, errors.New("DRC20 fee sale requires exactly burn and reserve logs")
	}
	burn, reserve := logs[0], logs[1]
	if burn == nil || burn.Removed || burn.Address != q.Token || len(burn.Topics) != 3 ||
		burn.Topics[0] != curveTransferTopic || burn.Topics[1] != customAddressWord(capability.payer) ||
		burn.Topics[2] != (common.Hash{}) || len(burn.Data) != 32 ||
		common.BytesToHash(burn.Data) != common.BigToHash(q.ExactInput) {
		return true, errors.New("DRC20 fee sale did not burn the exact authorized input")
	}
	if reserve == nil || reserve.Removed || reserve.Address != q.Token || len(reserve.Topics) != 1 ||
		reserve.Topics[0] != curveReserveTopic || len(reserve.Data) != 96 ||
		common.BytesToHash(reserve.Data[:32]) != common.BigToHash(q.Reserve) ||
		common.BytesToHash(reserve.Data[32:64]) != common.BigToHash(new(big.Int).Sub(q.Supply, q.ExactInput)) {
		return true, errors.New("DRC20 fee reserve log differs from the authenticated curve")
	}
	return true, nil
}

// redeemDRC20Fee exchanges only an explicitly signed exact input. It credits
// existing native DEL to the payer; it neither mints native DEL nor deducts
// the transaction fee. The caller must consume its dedicated fee nonce and
// run this operation plus ordinary fee deduction in one outer ante cache.
func (app *App) redeemDRC20Fee(ctx sdk.Context, verified authorization.Verified) (nativeOutput *big.Int, gasUsed uint64, err error) {
	if !verified.Valid() || verified.ConversionGasLimit() == 0 || verified.ConversionGasLimit() > authorization.MaxConversionGasLimit {
		return nil, 0, errors.New("DRC20 fee redemption requires verified bounded authorization")
	}
	child, write := ctx.CacheContext()
	meter := storetypes.NewGasMeter(verified.ConversionGasLimit())
	child = child.WithGasMeter(meter).WithKVGasConfig(storetypes.KVGasConfig()).WithTransientKVGasConfig(storetypes.TransientGasConfig())
	charged := false
	defer func() {
		if recovered := recover(); recovered != nil {
			if charged {
				panic(recovered)
			}
			if _, ok := recovered.(storetypes.ErrorOutOfGas); !ok {
				panic(recovered)
			}
			nativeOutput, err = nil, errors.New("DRC20 fee redemption exceeded signed conversion gas")
		}
		gasUsed = meter.GasConsumedToLimit()
		if !charged {
			ctx.GasMeter().ConsumeGas(gasUsed, "DRC20 fee exact-input redemption")
		}
	}()
	payer, token := verified.Payer(), verified.Token()
	quote, err := app.QuoteDRC20Fee(child, token, verified.ExactInput(), verified.MinimumNativeOutput(),
		new(big.Int).SetUint64(verified.Deadline()), verified.NativeFeeBudget())
	if err != nil {
		return nil, 0, err
	}
	if len(app.EVMKeeper.GetCode(child, app.EVMKeeper.GetCodeHash(child, payer))) != 0 {
		return nil, 0, errors.New("DRC20 fee payer must be a direct EOA without delegated code")
	}
	r := app.withdrawals.customCollateral
	beforeCoin, metadataErr := r.keeper.GetCoinByDRC(child, token.Hex())
	if metadataErr != nil && !errors.Is(metadataErr, coinkeeper.ErrNotFound) {
		return nil, 0, metadataErr
	}
	if metadataErr == nil {
		if err := beforeCoin.Validate(); err != nil {
			return nil, 0, err
		}
	}
	if metadataErr == nil && (beforeCoin.Volume.BigInt().Cmp(quote.Supply) != 0 || beforeCoin.Reserve.BigInt().Cmp(quote.Reserve) != 0) {
		return nil, 0, errors.New("DRC20 fee metadata differs from the authenticated pre-sale state")
	}
	account, reserveAccount := sdk.AccAddress(payer[:]), sdk.AccAddress(token[:])
	beforeBalance := app.BankKeeper.GetBalance(child, account, identity.BaseDenom).Amount
	beforeNativeSupply := app.BankKeeper.GetSupply(child, identity.BaseDenom).Amount
	beforeNonce := app.EVMKeeper.GetNonce(child, payer)
	balanceSlot := crypto.Keccak256Hash(common.LeftPadBytes(payer[:], 32), customERC20Slot[:])
	beforeTokenBalance := app.EVMKeeper.GetState(child, token, balanceSlot).Big()
	if beforeTokenBalance.Cmp(quote.ExactInput) < 0 {
		return nil, 0, errors.New("insufficient DRC20 fee token balance")
	}
	data := append(bytes.Clone(crypto.Keccak256([]byte("sellExactTokensForDEL(uint256,uint256,address)"))[:4]),
		common.LeftPadBytes(quote.ExactInput.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(quote.MinimumNativeOutput.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(payer[:], 32)...)
	if meter.GasConsumed() >= meter.Limit() {
		return nil, 0, errors.New("DRC20 fee conversion has no remaining execution budget")
	}
	message := core.Message{From: payer, To: &token, Value: new(big.Int), GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int),
		GasLimit: meter.Limit() - meter.GasConsumed(), Nonce: beforeNonce, Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
	child = child.WithValue(reserveFeeCallKey{}, &reserveFeeCall{message: message, quote: quote, payer: payer})
	state := statedb.New(child, app.EVMKeeper, statedb.NewEmptyTxConfig())
	result, err := app.EVMKeeper.ApplyMessage(child, state, message, nil, true, false, true)
	if err != nil {
		return nil, 0, fmt.Errorf("execute DRC20 fee sale: %w", err)
	}
	if result == nil {
		return nil, 0, errors.New("DRC20 fee sale returned no execution result")
	}
	// Charge pre-refund execution work. Internal EVM gas refunds must not turn
	// expensive conversion into free ante CPU work.
	if result.MaxUsedGas > message.GasLimit {
		return nil, 0, errors.New("DRC20 fee EVM gas accounting exceeds its cap")
	}
	meter.ConsumeGas(result.MaxUsedGas, "DRC20 fee EVM execution before refund")
	if result.VmError != "" {
		return nil, 0, fmt.Errorf("DRC20 fee sale reverted: %s", result.VmError)
	}
	output := new(big.Int).Set(quote.NativeOutput)
	if !app.BankKeeper.GetBalance(child, account, identity.BaseDenom).Amount.Equal(beforeBalance.Add(sdkmath.NewIntFromBigInt(output))) ||
		app.BankKeeper.GetBalance(child, reserveAccount, identity.BaseDenom).Amount.BigInt().Cmp(new(big.Int).Sub(quote.Reserve, output)) != 0 ||
		!app.BankKeeper.GetSupply(child, identity.BaseDenom).Amount.Equal(beforeNativeSupply) ||
		app.EVMKeeper.GetNonce(child, payer) != beforeNonce ||
		app.EVMKeeper.GetState(child, token, balanceSlot).Big().Cmp(new(big.Int).Sub(beforeTokenBalance, quote.ExactInput)) != 0 ||
		app.EVMKeeper.GetState(child, token, customSlotOffset(customERC20Slot, 2)).Big().Cmp(new(big.Int).Sub(quote.Supply, quote.ExactInput)) != 0 {
		return nil, 0, errors.New("DRC20 fee sale differs from exact token/native accounting")
	}
	if metadataErr == nil {
		if err := r.keeper.UpdateCoinVR(child, beforeCoin.Denom,
			sdkmath.NewIntFromBigInt(new(big.Int).Sub(quote.Supply, quote.ExactInput)),
			sdkmath.NewIntFromBigInt(new(big.Int).Sub(quote.Reserve, output))); err != nil {
			return nil, 0, err
		}
	}
	// Fee logs remain ante evidence even if the subsequent user operation
	// reverts; they are intentionally not merged with reverted EVM logs.
	child.EventManager().EmitEvent(sdk.NewEvent("mintcandy_drc20_fee_conversion",
		sdk.NewAttribute("payer", payer.Hex()), sdk.NewAttribute("token", token.Hex()),
		sdk.NewAttribute("input", quote.ExactInput.String()), sdk.NewAttribute("native_output", output.String()),
		sdk.NewAttribute("transaction", verified.TxHash().Hex())))
	gasUsed = meter.GasConsumedToLimit()
	// Charge before any cache write, so an exhausted parent also leaves this
	// function's cache uncommitted when it is called outside a transaction.
	charged = true
	ctx.GasMeter().ConsumeGas(gasUsed, "DRC20 fee exact-input redemption")
	write()
	return output, 0, nil
}
