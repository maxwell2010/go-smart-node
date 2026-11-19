package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
)

const quoteRequestFixture = `{"token":"0x2000000000000000000000000000000000000001","exact_input":"1000000000000000000","min_native_output":"100","native_fee_budget":"100","deadline":"1800000300"}`

func TestDRC20FeeHTTPInputBoundsBeforeStateAccess(t *testing.T) {
	// A nil app deliberately makes state access unavailable. Invalid HTTP inputs
	// must still get their precise error without accessing stores or any RPC.
	var app *App
	router := mux.NewRouter()
	app.registerDRC20FeeRoutes(router)
	for _, test := range []struct {
		name, method, path, body, content string
		status                            int
	}{
		{"quote-method", http.MethodGet, "/mintcandy/fees/v1/quote", "", "", 405},
		{"policy-method", http.MethodPost, "/mintcandy/fees/v1/policy", "", "", 405},
		{"oversized", http.MethodPost, "/mintcandy/fees/v1/quote", strings.Repeat("x", int(DRC20FeeQuoteRequestBytes)+1), "application/json", 413},
		{"unknown", http.MethodPost, "/mintcandy/fees/v1/quote", strings.Replace(quoteRequestFixture, `"token"`, `"unknown"`, 1), "application/json", 400},
		{"case-alias", http.MethodPost, "/mintcandy/fees/v1/quote", strings.Replace(quoteRequestFixture, `"token"`, `"Token"`, 1), "application/json", 400},
		{"duplicate", http.MethodPost, "/mintcandy/fees/v1/quote", strings.Replace(quoteRequestFixture, `"deadline":"1800000300"`, `"deadline":"1800000300","deadline":"1800000301"`, 1), "application/json", 400},
		{"numeric-json", http.MethodPost, "/mintcandy/fees/v1/quote", strings.Replace(quoteRequestFixture, `"native_fee_budget":"100"`, `"native_fee_budget":100`, 1), "application/json", 400},
		{"null", http.MethodPost, "/mintcandy/fees/v1/quote", strings.Replace(quoteRequestFixture, `"native_fee_budget":"100"`, `"native_fee_budget":null`, 1), "application/json", 400},
		{"trailing", http.MethodPost, "/mintcandy/fees/v1/quote", quoteRequestFixture + "{}", "application/json", 400},
		{"content", http.MethodPost, "/mintcandy/fees/v1/quote", quoteRequestFixture, "text/plain", 415},
		{"query", http.MethodPost, "/mintcandy/fees/v1/quote?token=other", quoteRequestFixture, "application/json", 400},
		{"duplicate-payer", http.MethodGet, "/mintcandy/fees/v1/policy?payer=a&payer=b", "", "", 400},
		{"policy-unknown", http.MethodGet, "/mintcandy/fees/v1/policy?secret=value", "", "", 400},
		{"policy-malformed-payer", http.MethodGet, "/mintcandy/fees/v1/policy?payer=0x01", "", "", 400},
		{"valid-unavailable", http.MethodPost, "/mintcandy/fees/v1/quote", quoteRequestFixture, "application/json; charset=utf-8", 503},
		{"policy-unavailable", http.MethodGet, "/mintcandy/fees/v1/policy", "", "", 503},
		{"exact-bound", http.MethodPost, "/mintcandy/fees/v1/quote", quoteRequestFixture + strings.Repeat(" ", int(DRC20FeeQuoteRequestBytes)-len(quoteRequestFixture)), "application/json", 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.content != "" {
				r.Header.Set("Content-Type", test.content)
			}
			w := httptest.NewRecorder()
			require.NotPanics(t, func() { router.ServeHTTP(w, r) })
			require.Equal(t, test.status, w.Code, w.Body.String())
			require.Equal(t, "application/json", w.Header().Get("Content-Type"))
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.NotEmpty(t, body["error"])
		})
	}
}

func TestDRC20FeeHTTPScalarValidation(t *testing.T) {
	parsed, err := parseFeeQuoteRequest([]byte(quoteRequestFixture))
	require.NoError(t, err)
	require.Equal(t, "1000000000000000000", parsed.input.String())
	require.False(t, parsed.hasPayer)
	withPayer := strings.Replace(quoteRequestFixture, `"deadline":"1800000300"`, `"deadline":"1800000300","payer":"0x40900a48273644768c09183e00e43528c17a29f6"`, 1)
	parsed, err = parseFeeQuoteRequest([]byte(withPayer))
	require.NoError(t, err)
	require.True(t, parsed.hasPayer)
	for _, value := range []string{"0", "-1", "01", "1e3", " 10", strings.Repeat("9", 79)} {
		bad := strings.Replace(quoteRequestFixture, `"native_fee_budget":"100"`, `"native_fee_budget":"`+value+`"`, 1)
		_, err := parseFeeQuoteRequest([]byte(bad))
		require.Error(t, err, value)
	}
	for _, bad := range []string{
		strings.Replace(quoteRequestFixture, `"min_native_output":"100"`, `"min_native_output":"99"`, 1),
		strings.Replace(quoteRequestFixture, `"deadline":"1800000300"`, `"deadline":"9223372036854775808"`, 1),
		strings.Replace(quoteRequestFixture, "0x2000000000000000000000000000000000000001", "0x0000000000000000000000000000000000000000", 1),
	} {
		_, err := parseFeeQuoteRequest([]byte(bad))
		require.Error(t, err)
	}
}
