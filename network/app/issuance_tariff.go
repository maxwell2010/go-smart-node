package app

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"net/http"

	storetypes "cosmossdk.io/store/types"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const issuanceTreasuryMain = "0x40900a48273644768c09183e00e43528c17a29f6"
const issuanceTreasuryTest = "0x00000000000000000000000000000000Cafe9900"
const issuanceActivationKey byte = 0x87

func tariffActivationDomain(chain string) bool {
	return chain == identity.ProfileChainID && (chain == identity.MainnetChainID || (gatedTestnetAssets && chain == identity.TestnetChainID))
}

func testIssuanceActive(chain string) bool {
	return !gatedTestnetAssets && identity.ProfileChainID == identity.TestnetChainID && chain == identity.TestnetChainID
}

// Read only a single fixed key without changing the historical gas charged
// to an issuance that is replayed from a block before tariff activation.
func (r *customCollateralRegistry) issuanceTreasury(ctx sdk.Context) (common.Address, bool) {
	if testIssuanceActive(ctx.ChainID()) {
		return common.HexToAddress(issuanceTreasuryTest), true
	}
	if tariffActivationDomain(ctx.ChainID()) {
		read := ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
		if read.KVStore(r.store).Has([]byte{issuanceActivationKey}) {
			if identity.ProfileChainID == identity.TestnetChainID {
				return common.HexToAddress(issuanceTreasuryTest), true
			}
			return common.HexToAddress(issuanceTreasuryMain), true
		}
	}
	return common.Address{}, false
}

// activateDRC20IssuanceTariff is called inside the coordinated upgrade.
// The index, including zero-supply tokens, and the effective tariff commit together.
func (app *App) activateDRC20IssuanceTariff(ctx sdk.Context) error {
	if !tariffActivationDomain(ctx.ChainID()) ||
		ctx.ExecMode() != sdk.ExecModeFinalize || app.withdrawals == nil || app.withdrawals.customCollateral == nil {
		return errors.New("DRC20 tariff activation requires the mainnet native collateral profile")
	}
	registry := app.withdrawals.customCollateral
	child, write := ctx.CacheContext()
	store := child.KVStore(registry.store)
	if store.Has([]byte{issuanceActivationKey}) {
		return errors.New("DRC20 issuance tariff already initialized")
	}
	entries := store.Iterator([]byte{issuanceSymbolPrefix}, []byte{issuanceSymbolPrefix + 1})
	occupied := entries.Valid()
	if err := entries.Close(); err != nil {
		return err
	}
	if occupied {
		return errors.New("DRC20 issuance index namespace already occupied")
	}
	if err := registry.indexIssuanceSymbols(child); err != nil {
		return err
	}
	store.Set([]byte{issuanceActivationKey}, []byte{1})
	write()
	return nil
}

func issuanceFee(length int) (*big.Int, error) {
	var units int64
	switch length {
	case 3:
		units = 100000
	case 4:
		units = 10000
	case 5:
		units = 1000
	case 6, 7, 8, 9:
		units = 100
	case 10:
		units = 10
	default:
		return nil, errors.New("DRC20 ticker must have 3 to 10 bytes")
	}
	return sdkmath.NewIntWithDecimal(units, 18).BigInt(), nil
}

// A signed, fixed-width trailer authorizes the native debit beyond call value.
// Solidity receives its original ABI call. Admission checks both the exact ABI encoding
// and this trailer, binding destination and total tariff even when call value is zero.
func issuanceApproval(fee *big.Int, treasury common.Address) []byte {
	b := crypto.Keccak256([]byte("MintCandy DRC20 issuance v1"))
	b = append(b, common.LeftPadBytes(treasury.Bytes(), 32)...)
	return append(b, common.BigToHash(fee).Bytes()...)
}

func (r *customCollateralRegistry) issuanceCalldata(ctx sdk.Context, data []byte) []byte {
	if _, active := r.issuanceTreasury(ctx); active && len(data) >= 4+96 {
		return data[:len(data)-96]
	}
	return data
}

func symbolKey(symbol string) []byte { return append([]byte{issuanceSymbolPrefix}, []byte(symbol)...) }

func (r *customCollateralRegistry) preflightIssuanceSymbols(ctx sdk.Context) error {
	if !testIssuanceActive(ctx.ChainID()) {
		return nil
	}
	disposable, _ := ctx.CacheContext()
	return r.indexIssuanceSymbols(disposable)
}

func (r *customCollateralRegistry) indexIssuanceSymbols(ctx sdk.Context) error {
	store := ctx.KVStore(r.store)
	if store.Has(symbolKey("")) {
		return nil
	}
	manifest, err := r.manifest(ctx)
	if err != nil {
		return err
	}
	// Read every legacy record before writing an index. Repeated references to one
	// proxy are valid. Two distinct proxies claiming one ticker block activation.
	type entry struct {
		symbol string
		token  common.Address
	}
	var entries []entry
	seen := map[string]common.Address{}
	add := func(token common.Address) error {
		word := r.evm.GetState(ctx, token, customSlotOffset(customERC20Slot, 4))
		n := int(word[31]) / 2
		if word[31]%2 != 0 || n < 3 || n > 10 {
			return fmt.Errorf("invalid historical DRC20 ticker on %s", token)
		}
		symbol := string(word[:n])
		for _, c := range []byte(symbol) {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z') {
				return fmt.Errorf("noncanonical historical ticker %q on %s", symbol, token)
			}
		}
		if reservedPublicTokenSymbol(symbol) {
			return fmt.Errorf("reserved historical ticker %q on %s", symbol, token)
		}
		if old, ok := seen[symbol]; ok {
			if old != token {
				return fmt.Errorf("historical ticker collision %q: %s and %s", symbol, old, token)
			}
			return nil
		}
		seen[symbol] = token
		entries = append(entries, entry{symbol, token})
		return nil
	}
	for pos := 1; pos < len(manifest); pos += customCollateralEntrySize {
		if err := add(common.BytesToAddress(manifest[pos : pos+20])); err != nil {
			return err
		}
	}
	for _, prefix := range []byte{backedTokenMembershipPrefix, reservelessMembershipPrefix} {
		it := store.Iterator([]byte{prefix}, []byte{prefix + 1})
		for ; it.Valid(); it.Next() {
			key := it.Key()
			if len(key) != 21 {
				it.Close()
				return errors.New("invalid historical DRC20 membership")
			}
			if err := add(common.BytesToAddress(key[1:])); err != nil {
				it.Close()
				return err
			}
		}
		if err := it.Close(); err != nil {
			return err
		}
	}
	for _, e := range entries {
		store.Set(symbolKey(e.symbol), e.token.Bytes())
	}
	store.Set(symbolKey(""), []byte{1})
	return nil
}

// Called inside the successful EVM transaction's existing SDK cache, before insertion of membership.
func (r *customCollateralRegistry) settleIssuance(ctx sdk.Context, sender, token common.Address, symbol string, data []byte, legacy *big.Int) error {
	destination, active := r.issuanceTreasury(ctx)
	if !active {
		return nil
	}
	fee, err := issuanceFee(len(symbol))
	if err != nil {
		return err
	}
	if !bytes.HasSuffix(data, issuanceApproval(fee, destination)) {
		return errors.New("missing signed DRC20 issuance tariff approval")
	}
	if err := r.indexIssuanceSymbols(ctx); err != nil {
		return err
	}
	store := ctx.KVStore(r.store)
	key := symbolKey(symbol)
	if store.Has(key) {
		return errors.New("DRC20 ticker already registered")
	}
	treasury := sdk.AccAddress(destination.Bytes())
	payer := sdk.AccAddress(sender.Bytes())
	burn := new(big.Int).Quo(fee, big.NewInt(5))
	share := new(big.Int).Sub(fee, burn)
	coins := func(n *big.Int) sdk.Coins {
		return sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntFromBigInt(n)))
	}
	// The pinned backed factory has just credited this exact legacy component to zero.
	// Route that credit as part of the treasury share, never as an additional fee.
	if legacy.Sign() > 0 {
		if err := r.bank.SendCoins(ctx, sdk.AccAddress((common.Address{}).Bytes()), treasury, coins(legacy)); err != nil {
			return err
		}
	}
	if err := r.bank.SendCoins(ctx, payer, treasury, coins(new(big.Int).Sub(share, legacy))); err != nil {
		return err
	}
	if err := r.bank.SendCoinsFromAccountToModule(ctx, payer, evmtypes.ModuleName, coins(burn)); err != nil {
		return err
	}
	if err := r.bank.BurnCoins(ctx, evmtypes.ModuleName, coins(burn)); err != nil {
		return err
	}
	store.Set(key, token.Bytes())
	ctx.EventManager().EmitEvent(sdk.NewEvent("mintcandy_drc20_issuance", sdk.NewAttribute("symbol", symbol), sdk.NewAttribute("token", token.Hex()), sdk.NewAttribute("fee", fee.String()), sdk.NewAttribute("treasury_amount", share.String()), sdk.NewAttribute("burn", burn.String())))
	return nil
}

func (app *App) handleIssuanceTariff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		feeAPIError(w, 405, "GET required")
		return
	}
	ctx, err := app.feeAPIContext()
	if err != nil {
		feeAPIError(w, 503, "latest committed state unavailable")
		return
	}
	if app.withdrawals == nil || app.withdrawals.customCollateral == nil {
		feeAPIError(w, 404, "issuance tariff inactive")
		return
	}
	destination, active := app.withdrawals.customCollateral.issuanceTreasury(ctx)
	if !active {
		feeAPIError(w, 404, "issuance tariff inactive")
		return
	}
	rows := make([]map[string]any, 0, 8)
	for length := 3; length <= 10; length++ {
		fee, _ := issuanceFee(length)
		rows = append(rows, map[string]any{"length": length, "fee_atoms": fee.String(), "backed_factory_component_atoms": backedSymbolCommission(length).String(), "approval_suffix": hexutil.Encode(issuanceApproval(fee, destination))})
	}
	feeAPIJSON(w, 200, map[string]any{"version": 1, "chain_id": ctx.ChainID(), "denom": identity.BaseDenom, "height": ctx.BlockHeight(), "token_center": app.withdrawals.profile.TokenCenter.Hex(), "treasury": destination.Hex(), "treasury_percent": 80, "burn_percent": 20, "tariffs": rows})
}
