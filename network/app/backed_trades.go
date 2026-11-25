package app

import (
	"bytes"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"sync"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	coinconfig "github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

var (
	buySelector  = crypto.Keccak256([]byte("buy(uint256,address)"))[:4]
	sellSelector = crypto.Keccak256([]byte("sell(uint256,uint256,address)"))[:4]
	tradeABI     = sync.OnceValues(func() (abi.ABI, error) { return contracts.CompiledABI("DRC20") })
)

type reserveBuyCreditKey struct{}
type reserveBuyCredit struct {
	token  common.Address
	amount *big.Int
}

// Only the EVM balance wrapper may credit this exact signed buy into its target reserve.
// Other transfers, including SDK bank, IBC, and credits to a different reserve, keep the existing restriction.
func signedReserveCredit(ctx sdk.Context, from, to sdk.AccAddress, coins sdk.Coins) bool {
	credit, ok := ctx.Value(reserveBuyCreditKey{}).(reserveBuyCredit)
	return ok && bytes.Equal(from, authtypes.NewModuleAddress(evmtypes.ModuleName)) && bytes.Equal(to, credit.token[:]) && len(coins) == 1 && coins[0].Amount.BigInt().Cmp(credit.amount) == 0
}

func (c *withdrawalController) admitReserveTrade(ctx sdk.Context, sender common.Address, msg core.Message, receipt *ethtypes.Receipt, buy bool) error {
	r := c.customCollateral
	if msg.To == nil || sender == (common.Address{}) || sender != msg.From || msg.Value == nil || len(msg.SetCodeAuthorizations) != 0 || receipt == nil || receipt.Status != ethtypes.ReceiptStatusSuccessful || len(receipt.Logs) != 2 {
		return errors.New("MC_TRADE_CALL: invalid direct signed DRC20 trade")
	}
	token := *msg.To
	if r.evm.GetState(ctx, r.profile.TokenCenter, reservelessPauseSlot) != (common.Hash{}) {
		return errors.New("MC_TRADE_PAUSED")
	}
	contract, err := tradeABI()
	if err != nil {
		return err
	}
	name := "sell"
	if buy {
		name = "buy"
	}
	args, err := canonicalReservelessArgs(contract.Methods[name], msg.Data)
	if err != nil {
		return err
	}
	receiver := args[len(args)-1].(common.Address)
	// Limit this wallet route to ordinary recipients. A reserve, module or system
	// contract may only receive native collateral through its own accounting path.
	if receiver == (common.Address{}) || c.delRecipientAllowed == nil || !c.delRecipientAllowed(sdk.AccAddress(receiver.Bytes())) || r.evm.GetCodeHash(ctx, receiver) != ethtypes.EmptyCodeHash {
		return errors.New("MC_TRADE_RECIPIENT")
	}
	old, err := r.keeper.GetCoinByDRC(ctx, token.Hex())
	if err != nil {
		return errors.New("MC_TRADE_UNREGISTERED")
	}
	live, err := r.Coin(ctx, token)
	if err != nil {
		return err
	}
	volume := new(big.Int).Sub(live.Volume.BigInt(), old.Volume.BigInt())
	reserve := new(big.Int).Sub(live.Reserve.BigInt(), old.Reserve.BigInt())
	var transfer *ethtypes.Log
	eventReserve := live.Reserve.BigInt()
	minimum := args[0].(*big.Int)
	if buy {
		if msg.Value.Sign() <= 0 || reserve.Cmp(msg.Value) != 0 || volume.Sign() <= 0 || volume.Cmp(minimum) < 0 {
			return errors.New("MC_TRADE_BUY_DELTA")
		}
		transfer = reservelessLog(token, "Transfer", common.Address{}, receiver, volume)
	} else {
		volume.Neg(volume)
		reserve.Neg(reserve)
		minimum = args[1].(*big.Int)
		if msg.Value.Sign() != 0 || volume.Sign() <= 0 || volume.Cmp(args[0].(*big.Int)) != 0 || reserve.Sign() <= 0 || reserve.Cmp(minimum) < 0 {
			return errors.New("MC_TRADE_SELL_DELTA")
		}
		transfer = reservelessLog(token, "Transfer", sender, common.Address{}, volume)
		// The contract emits ReserveUpdated during ERC20 burn, before the native transfer.
		eventReserve = old.Reserve.BigInt()
	}
	price := new(big.Int).Mul(eventReserve, big.NewInt(1_000_000_000_000_000_000))
	price.Quo(price, live.Volume.BigInt()).Mul(price, big.NewInt(100)).Quo(price, new(big.Int).SetUint64(uint64(live.CRR)))
	event := contract.Events["ReserveUpdated"]
	data, err := event.Inputs.NonIndexed().Pack(eventReserve, live.Volume.BigInt(), price)
	if err != nil {
		return err
	}
	if !equalReservelessLogs(receipt.Logs, []*ethtypes.Log{transfer, {Address: token, Topics: []common.Hash{event.ID}, Data: data}}) {
		return errors.New("MC_TRADE_RECEIPT")
	}
	if err := r.keeper.UpdateCoinVR(ctx, live.Denom, live.Volume, live.Reserve); err != nil {
		return err
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("mintcandy_drc20_trade", sdk.NewAttribute("side", name), sdk.NewAttribute("token", token.Hex()), sdk.NewAttribute("payer", sender.Hex()), sdk.NewAttribute("recipient", receiver.Hex()), sdk.NewAttribute("token_atoms", volume.String()), sdk.NewAttribute("native_atoms", reserve.String()), sdk.NewAttribute("volume", live.Volume.String()), sdk.NewAttribute("reserve", live.Reserve.String())))
	return nil
}

func (app *App) handleReserveMarket(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		feeAPIError(w, 405, "GET required")
		return
	}
	token, err := feeAPIAddress(r.URL.Query().Get("token"))
	if err != nil {
		feeAPIError(w, 400, err.Error())
		return
	}
	var height int64
	if raw := r.URL.Query().Get("height"); raw != "" {
		height, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || height <= 0 {
			feeAPIError(w, 400, "height must be positive")
			return
		}
	}
	ctx, err := app.CreateQueryContext(height, false)
	if err != nil {
		feeAPIError(w, 503, "snapshot unavailable")
		return
	}
	if !testIssuanceActive(ctx.ChainID()) || app.withdrawals == nil || app.withdrawals.customCollateral == nil {
		feeAPIError(w, 404, "test market inactive")
		return
	}
	reg := app.withdrawals.customCollateral
	coin, err := reg.Coin(ctx, token)
	if err != nil {
		feeAPIError(w, 404, "MC_TRADE_UNREGISTERED")
		return
	}
	stored, err := reg.keeper.GetCoinByDRC(ctx, token.Hex())
	if err != nil || !stored.Reserve.Equal(coin.Reserve) || !stored.Volume.Equal(coin.Volume) {
		feeAPIError(w, 409, "reserve projection differs")
		return
	}
	feeAPIJSON(w, 200, map[string]any{"version": 1, "height": ctx.BlockHeight(), "chain_id": ctx.ChainID(), "native_denom": "tmsp", "token": token.Hex(), "symbol": coin.Denom, "crr": coin.CRR, "supply_atoms": stored.Volume.String(), "reserve_atoms": stored.Reserve.String(), "min_supply_atoms": coin.MinVolume.String(), "max_supply_atoms": coin.LimitVolume.String(), "min_reserve_atoms": coinconfig.MinCoinReserve.String()})
}
