package app

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"mime"
	"net/http"
	"time"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/mux"
	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
	"github.com/maxwell2010/go-smart-node/network/fees/envelope"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const DRC20FeeQuoteRequestBytes int64 = 8 * 1024

type feeQuoteRequest struct {
	Token               string `json:"token"`
	ExactInput          string `json:"exact_input"`
	MinimumNativeOutput string `json:"min_native_output"`
	NativeFeeBudget     string `json:"native_fee_budget"`
	Deadline            string `json:"deadline"`
	Payer               string `json:"payer,omitempty"`
}
type parsedFeeQuote struct {
	token, payer                     common.Address
	hasPayer                         bool
	input, minimum, budget, deadline *big.Int
}

func feeAPIAddress(s string) (common.Address, error) {
	if len(s) != 42 || s[:2] != "0x" {
		return common.Address{}, errors.New("address must contain 0x and exactly 40 hex digits")
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return common.Address{}, errors.New("address is not valid hex")
	}
	a := common.BytesToAddress(b)
	if a == (common.Address{}) {
		return a, errors.New("zero address is not permitted")
	}
	return a, nil
}
func feeAPIInteger(s string) (*big.Int, error) {
	if len(s) == 0 || len(s) > 78 || (len(s) > 1 && s[0] == '0') {
		return nil, errors.New("amount must be a canonical uint256 decimal string")
	}
	for _, ch := range []byte(s) {
		if ch < '0' || ch > '9' {
			return nil, errors.New("amount must contain decimal digits only")
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() <= 0 || n.BitLen() > 256 {
		return nil, errors.New("amount is outside positive uint256 bounds")
	}
	return n, nil
}

// Quotes are not signed transport. JSON whitespace and field order are free,
// but duplicate/unknown fields and noncanonical numeric strings are rejected.
func parseFeeQuoteRequest(raw []byte) (parsedFeeQuote, error) {
	var parsed parsedFeeQuote
	if len(raw) == 0 || int64(len(raw)) > DRC20FeeQuoteRequestBytes {
		return parsed, errors.New("invalid quote request size")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return parsed, errors.New("quote request must be a JSON object")
	}
	seen := map[string]bool{}
	allowed := map[string]bool{"token": true, "exact_input": true, "min_native_output": true, "native_fee_budget": true, "deadline": true, "payer": true}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return parsed, err
		}
		name, ok := key.(string)
		if !ok || seen[name] || !allowed[name] {
			return parsed, errors.New("duplicate or unknown quote request field")
		}
		seen[name] = true
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return parsed, err
		}
		if len(value) == 0 || value[0] != '"' {
			return parsed, errors.New("quote request values must be strings")
		}
	}
	if _, err = d.Token(); err != nil {
		return parsed, err
	}
	if d.Decode(new(any)) != io.EOF {
		return parsed, errors.New("trailing quote request data")
	}
	var wire feeQuoteRequest
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&wire); err != nil {
		return parsed, err
	}
	parsed.token, err = feeAPIAddress(wire.Token)
	if err != nil {
		return parsed, err
	}
	parsed.input, err = feeAPIInteger(wire.ExactInput)
	if err != nil {
		return parsed, err
	}
	parsed.minimum, err = feeAPIInteger(wire.MinimumNativeOutput)
	if err != nil {
		return parsed, err
	}
	parsed.budget, err = feeAPIInteger(wire.NativeFeeBudget)
	if err != nil {
		return parsed, err
	}
	if parsed.minimum.Cmp(parsed.budget) < 0 {
		return parsed, errors.New("min_native_output must cover native_fee_budget")
	}
	parsed.deadline, err = feeAPIInteger(wire.Deadline)
	if err != nil || !parsed.deadline.IsInt64() {
		return parsed, errors.New("deadline must be a positive Unix-seconds decimal string within int64")
	}
	if wire.Payer != "" {
		parsed.payer, err = feeAPIAddress(wire.Payer)
		if err != nil {
			return parsed, err
		}
		parsed.hasPayer = true
	}
	return parsed, nil
}

func feeAPIJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func feeAPIError(w http.ResponseWriter, status int, message string) {
	feeAPIJSON(w, status, map[string]string{"error": message})
}
func feeAPIRecover(w http.ResponseWriter) {
	if p := recover(); p != nil {
		if _, ok := p.(storetypes.ErrorOutOfGas); ok {
			feeAPIError(w, http.StatusServiceUnavailable, "fee quote query gas budget exhausted")
		} else {
			panic(p)
		}
	}
}
func (app *App) feeAPIContext() (sdk.Context, error) {
	if app == nil || app.BaseApp == nil {
		return sdk.Context{}, errors.New("application is unavailable")
	}
	return app.CreateQueryContext(0, false)
}

func (app *App) feePolicyJSON(ctx sdk.Context, payer common.Address, hasPayer bool) (map[string]any, error) {
	native := app.withdrawals != nil && app.withdrawals.customCollateral != nil && app.usesNativeConsensus(ctx)
	result := map[string]any{
		"enabled": native, "cosmos_chain_id": app.chainID, "evm_chain_id": new(big.Int).SetUint64(app.evmChainID).String(),
		"verifying_module": DRC20FeeModuleAddress().Hex(), "domain_name": authorization.DomainName, "domain_version": authorization.DomainVersion,
		"domain_salt": crypto.Keccak256Hash([]byte(app.chainID)).Hex(), "snapshot_height": ctx.BlockHeight(), "snapshot_time": ctx.BlockTime().UTC().Format(time.RFC3339Nano),
		"max_conversion_gas": DRC20FeeMaxConversionGas, "settlement_gas": DRC20FeeSettlementGas, "max_quote_request_bytes": DRC20FeeQuoteRequestBytes,
		"max_envelope_bytes": envelope.MaxEnvelopeBytes, "native_fee_budget_formula": "(ethereum_gas_limit + conversion_gas_limit) * ethereum_fee_cap",
		"refund_asset": "native " + identity.BaseDenom, "transport": "mintcandy-drc20-fee-v1", "broadcast_method": "Comet broadcast_tx_sync or Cosmos /cosmos/tx/v1beta1/txs",
		"submits_transactions": false,
	}
	if hasPayer {
		n, err := app.DRC20FeeNonce(ctx, payer)
		if err != nil {
			return nil, err
		}
		result["payer"] = payer.Hex()
		result["fee_nonce"] = n.String()
	}
	return result, nil
}

func (app *App) registerDRC20FeeRoutes(router *mux.Router) {
	if router == nil {
		return
	}
	if identity.ProfileChainID == identity.TestnetChainID || identity.ProfileChainID == identity.MainnetChainID {
		router.HandleFunc("/mintcandy/issuance/v1/tariff", app.handleIssuanceTariff)
	}
	router.HandleFunc("/mintcandy/fees/v1/quote", app.handleDRC20FeeQuote)
	router.HandleFunc("/mintcandy/fees/v1/policy", app.handleDRC20FeePolicy)
}
func (app *App) handleDRC20FeePolicy(w http.ResponseWriter, r *http.Request) {
	defer feeAPIRecover(w)
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		feeAPIError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	var payer common.Address
	hasPayer := false
	for name, values := range r.URL.Query() {
		if name != "payer" || len(values) != 1 {
			feeAPIError(w, http.StatusBadRequest, "only a single optional payer parameter is supported")
			return
		}
		var err error
		payer, err = feeAPIAddress(values[0])
		if err != nil {
			feeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		hasPayer = true
	}
	ctx, err := app.feeAPIContext()
	if err != nil {
		feeAPIError(w, http.StatusServiceUnavailable, "latest committed state is unavailable")
		return
	}
	policy, err := app.feePolicyJSON(ctx, payer, hasPayer)
	if err != nil {
		feeAPIError(w, http.StatusServiceUnavailable, "fee nonce state is unavailable")
		return
	}
	feeAPIJSON(w, http.StatusOK, policy)
}
func (app *App) handleDRC20FeeQuote(w http.ResponseWriter, r *http.Request) {
	defer feeAPIRecover(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		feeAPIError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if r.URL.RawQuery != "" {
		feeAPIError(w, http.StatusBadRequest, "quote parameters belong in the JSON body")
		return
	}
	if content := r.Header.Get("Content-Type"); content != "" {
		media, _, err := mime.ParseMediaType(content)
		if err != nil || media != "application/json" {
			feeAPIError(w, http.StatusUnsupportedMediaType, "application/json required")
			return
		}
	}
	if r.Body == nil {
		feeAPIError(w, http.StatusBadRequest, "JSON quote body required")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, DRC20FeeQuoteRequestBytes))
	if err != nil {
		feeAPIError(w, http.StatusRequestEntityTooLarge, "quote request exceeds 8 KiB")
		return
	}
	request, err := parseFeeQuoteRequest(raw)
	if err != nil {
		feeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, err := app.feeAPIContext()
	if err != nil {
		feeAPIError(w, http.StatusServiceUnavailable, "latest committed state is unavailable")
		return
	}
	quote, err := app.QuoteDRC20Fee(ctx, request.token, request.input, request.minimum, request.deadline, request.budget)
	if err != nil {
		feeAPIError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	result, err := app.feePolicyJSON(ctx, request.payer, request.hasPayer)
	if err != nil {
		feeAPIError(w, http.StatusServiceUnavailable, "fee nonce state is unavailable")
		return
	}
	result["token"], result["symbol"] = quote.Token.Hex(), quote.Symbol
	result["exact_input"], result["native_output"] = quote.ExactInput.String(), quote.NativeOutput.String()
	result["min_native_output"], result["native_fee_budget"] = quote.MinimumNativeOutput.String(), quote.NativeFeeBudget.String()
	result["reserve"], result["minimum_reserve"] = quote.Reserve.String(), quote.MinimumReserve.String()
	result["supply"], result["minimum_supply"] = quote.Supply.String(), quote.MinimumSupply.String()
	result["quote_sdk_gas_used"], result["deadline"] = quote.SDKGasUsed, request.deadline.String()
	result["liquidity_reserved"] = false
	feeAPIJSON(w, http.StatusOK, result)
}
