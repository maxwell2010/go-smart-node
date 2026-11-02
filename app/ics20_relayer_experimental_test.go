//go:build experimental_ibc

package app

import (
	cmdcfg "bitbucket.org/decimalteam/go-smart-node/cmd/config"
	validator "bitbucket.org/decimalteam/go-smart-node/x/validator/types"
	"context"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/json"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	transfer "github.com/cosmos/ibc-go/v6/modules/apps/transfer/types"
	ethencoding "github.com/decimalteam/ethermint/encoding"
	"github.com/stretchr/testify/require"
	tmtypes "github.com/tendermint/tendermint/types"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Uses a separately source-built, pinned official Cosmos relayer binary.
// Only disposable local fixtures are configured; no public endpoint is accepted.
func runExternalIBCRelayer(t *testing.T, root, dest string) {
	binary := os.Getenv("DSC_IBC_RELAYER_BINARY")
	info, err := buildinfo.ReadFile(binary)
	require.NoError(t, err)
	require.Equal(t, "github.com/cosmos/relayer/v2", info.Main.Path)
	require.Equal(t, "v2.6.0", info.Main.Version)
	home := filepath.Join(root, "relayer")
	run := func(secret bool, args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		args = append(args, "--home", home)
		cmd := exec.CommandContext(ctx, binary, args...)
		hideTestWindow(cmd)
		out, err := cmd.CombinedOutput()
		if secret {
			redacted := string(out)
			for _, arg := range args {
				if strings.Count(arg, " ") >= 11 {
					redacted = strings.ReplaceAll(redacted, arg, "[ephemeral mnemonic redacted]")
				}
			}
			require.NoError(t, err, "relayer private-key import: %s", redacted)
		} else {
			if len(out) > 5000 {
				out = out[len(out)-5000:]
			}
			require.NoError(t, err, "relayer %s: %s", args[0], string(out))
		}
		return string(out)
	}
	run(false, "config", "init", "--memo", "-")
	addresses := map[string]string{}
	for i, id := range []string{"decimal_900001-1", "decimal_900002-1"} {
		cfg := map[string]any{"type": "cosmos", "value": map[string]any{"key": "relayer", "chain-id": id, "rpc-addr": fmt.Sprintf("http://127.0.0.1:%d", 26757+i*100), "account-prefix": cmdcfg.Bech32Prefix, "keyring-backend": "test", "gas-adjustment": 1.5, "gas-prices": "0del", "min-gas-amount": 1000000, "max-gas-amount": 10000000, "timeout": "10s", "output-format": "json", "sign-mode": "direct", "extra-codecs": []string{"ethermint"}, "coin-type": 60, "signing-algorithm": "eth_secp256k1", "broadcast-mode": "single"}}
		data, err := json.Marshal(cfg)
		require.NoError(t, err)
		file := filepath.Join(root, id+"-relayer.json")
		require.NoError(t, os.WriteFile(file, data, 0600))
		run(false, "chains", "add", "--file", file, id)
		keyData, err := os.ReadFile(filepath.Join(dest, id, "relayer-test-key.json"))
		require.NoError(t, err)
		var key struct {
			Mnemonic string `json:"mnemonic"`
			Address  string `json:"address"`
		}
		require.NoError(t, json.Unmarshal(keyData, &key))
		require.NotEmpty(t, key.Mnemonic)
		addresses[id] = key.Address
		run(true, "keys", "restore", id, "relayer", key.Mnemonic, "--coin-type", "60")
		addr := run(false, "keys", "show", id, "relayer")
		require.True(t, strings.Contains(addr, key.Address), "relayer derived a different funded fixture address")
	}
	allBalances := func(id string) sdk.Coins {
		out := run(false, "query", "balance", id, "relayer", "--output", "json", "--ibc-denoms")
		var values map[string]string
		require.NoError(t, json.Unmarshal([]byte(out), &values))
		coins, err := sdk.ParseCoinsNormalized(values["balance"])
		require.NoError(t, err)
		return coins
	}
	balance := func(id string) sdk.Int { return allBalances(id).AmountOf(cmdcfg.BaseDenom) }
	run(false, "paths", "new", "decimal_900001-1", "decimal_900002-1", "isolated-ics20")
	// The external relayer cannot query Decimal's custom validator params.
	// Derive both exact unbonding periods from fresh controlled genesis, never guess.
	codec := ethencoding.MakeConfig(ModuleBasics).Codec
	ids := []string{"decimal_900001-1", "decimal_900002-1"}
	for i, src := range ids {
		dst := ids[1-i]
		genesis, err := tmtypes.GenesisDocFromFile(filepath.Join(dest, dst, "config", "genesis.json"))
		require.NoError(t, err)
		var state GenesisState
		require.NoError(t, json.Unmarshal(genesis.AppState, &state))
		var validators validator.GenesisState
		codec.MustUnmarshalJSON(state[validator.ModuleName], &validators)
		unbonding := validators.Params.UndelegationTime
		require.Positive(t, unbonding)
		before := balance(src)
		run(false, "tx", "client", src, dst, "isolated-ics20", "--client-unbonding-period", unbonding.String(), "--client-tp", (unbonding / 2).String())
		time.Sleep(3 * time.Second)
		run(false, "query", "client", src, "07-tendermint-0")
		require.True(t, balance(src).LT(before), "zero declared fee must still charge native Decimal commission")
	}
	beforeLink := map[string]sdk.Int{ids[0]: balance(ids[0]), ids[1]: balance(ids[1])}
	run(false, "tx", "link", "isolated-ics20", "--src-port", "transfer", "--dst-port", "transfer", "--order", "unordered", "--version", "ics20-1")
	for _, id := range ids {
		require.True(t, balance(id).LT(beforeLink[id]), "handshake must debit native commission on both chains")
	}
	t.Log("Official external relayer completed isolated client/connection/channel handshakes")
	src, dst := ids[0], ids[1]
	beforeSend := balance(src)
	run(false, "query", "channel", src, "channel-0", "transfer")
	run(false, "tx", "transfer", src, dst, "100del", addresses[dst], "channel-0")
	time.Sleep(3 * time.Second)
	require.Equal(t, sdk.NewInt(100), externalBankBalance(t, 26757, transfer.GetEscrowAddress(transfer.PortID, "channel-0").String(), cmdcfg.BaseDenom).Amount)
	require.True(t, beforeSend.Sub(balance(src)).GT(sdk.NewInt(100)), "send must escrow amount and charge nonzero computed fee")
	run(false, "tx", "flush", "isolated-ics20", "channel-0")
	time.Sleep(3 * time.Second)
	voucher := transfer.GetTransferCoin(transfer.PortID, "channel-0", cmdcfg.BaseDenom, sdk.NewInt(100))
	require.Equal(t, sdk.NewInt(100), allBalances(dst).AmountOf(voucher.Denom))
	t.Log("External relayer delivered native transfer into exactly 100 voucher units")
	beforeReturn := balance(dst)
	run(false, "tx", "transfer", dst, src, "100"+voucher.Denom, addresses[src], "channel-0")
	time.Sleep(3 * time.Second)
	require.True(t, balance(dst).LT(beforeReturn), "return send must charge nonzero native fee")
	run(false, "tx", "flush", "isolated-ics20", "channel-0")
	time.Sleep(3 * time.Second)
	require.True(t, allBalances(dst).AmountOf(voucher.Denom).IsZero())
	require.True(t, externalBankBalance(t, 26757, transfer.GetEscrowAddress(transfer.PortID, "channel-0").String(), cmdcfg.BaseDenom).IsZero())
	assertExternalTxGas(t, 26757)
	assertExternalTxGas(t, 26857)
	t.Log("External relayer completed voucher return and native release")
}

func externalBankBalance(t *testing.T, port int, address, denom string) sdk.Coin {
	req := banktypes.QueryBalanceRequest{Address: address, Denom: denom}
	data, err := req.Marshal()
	require.NoError(t, err)
	values := url.Values{"path": {`"/cosmos.bank.v1beta1.Query/Balance"`}, "data": {"0x" + fmt.Sprintf("%x", data)}, "prove": {"false"}}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/abci_query?%s", port, values.Encode()))
	require.NoError(t, err)
	defer resp.Body.Close()
	var result struct {
		Result struct {
			Response struct {
				Code  uint32 `json:"code"`
				Value string `json:"value"`
			} `json:"response"`
		} `json:"result"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Zero(t, result.Result.Response.Code)
	raw, err := base64.StdEncoding.DecodeString(result.Result.Response.Value)
	require.NoError(t, err)
	var balance banktypes.QueryBalanceResponse
	require.NoError(t, balance.Unmarshal(raw))
	require.NotNil(t, balance.Balance)
	return *balance.Balance
}

func assertExternalTxGas(t *testing.T, port int) {
	values := url.Values{"query": {`"tx.height > 0"`}, "per_page": {"100"}}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/tx_search?%s", port, values.Encode()))
	require.NoError(t, err)
	defer resp.Body.Close()
	var result struct {
		Result struct {
			Total string `json:"total_count"`
			Txs   []struct {
				Result struct {
					Code      uint32 `json:"code"`
					GasWanted string `json:"gas_wanted"`
					GasUsed   string `json:"gas_used"`
				} `json:"tx_result"`
			} `json:"txs"`
		} `json:"result"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.NotEmpty(t, result.Result.Txs)
	total, err := strconv.Atoi(result.Result.Total)
	require.NoError(t, err)
	require.Equal(t, total, len(result.Result.Txs))
	for _, tx := range result.Result.Txs {
		require.Zero(t, tx.Result.Code)
		wanted, err := strconv.ParseUint(tx.Result.GasWanted, 10, 64)
		require.NoError(t, err)
		used, err := strconv.ParseUint(tx.Result.GasUsed, 10, 64)
		require.NoError(t, err)
		require.Positive(t, wanted)
		require.Less(t, wanted, uint64(100000000))
		require.Positive(t, used)
	}
	t.Logf("Verified %d committed local relayer transactions on port %d with bounded positive gas", total, port)
}
