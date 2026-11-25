package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

func TestSignTestnetTrades(t *testing.T) {
	dir := os.Getenv("MCND_TRADE_FIXTURE")
	if dir == "" {
		t.Skip("isolated reserve market only")
	}
	requirePrivateMainnetFixture(t)
	require.Equal(t, identity.TestnetChainID, identity.ProfileChainID)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(dir) && info.IsDir() && info.Mode().Perm()&0077 == 0)
	target := common.HexToAddress(os.Getenv("MCND_TRADE_TOKEN"))
	require.NotEqual(t, common.Address{}, target)
	abi, err := contracts.CompiledABI("DRC20")
	require.NoError(t, err)
	founder, err := crypto.LoadECDSA(os.Getenv("MCND_TESTNET_TARIFF_FOUNDER_FILE"))
	require.NoError(t, err)
	spec, ok := nativeLaunchSpecForMode("4-empty-sponsored")
	require.True(t, ok)
	other, err := crypto.ToECDSA(nativeLaunchAccountKey(5, spec).Bytes())
	require.NoError(t, err)
	a, b := crypto.PubkeyToAddress(founder.PublicKey), crypto.PubkeyToAddress(other.PublicKey)
	n1, err := strconv.ParseUint(os.Getenv("MCND_TRADE_NONCE_A"), 10, 64)
	require.NoError(t, err)
	n2, err := strconv.ParseUint(os.Getenv("MCND_TRADE_NONCE_B"), 10, 64)
	require.NoError(t, err)
	tenth := func(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), big.NewInt(100000000000000000)) }
	rows := []map[string]any{}
	add := func(label, side string, second bool, receiver common.Address, input, minOut int64, success bool) {
		key, n := founder, n1
		if second {
			key, n = other, n2
			n2++
		} else {
			n1++
		}
		value := new(big.Int)
		var data []byte
		if side == "buy" {
			value = tenth(input)
			data, err = abi.Pack(side, tenth(minOut), receiver)
		} else {
			data, err = abi.Pack(side, tenth(input), tenth(minOut), receiver)
		}
		require.NoError(t, err)
		tx, err := ethtypes.SignTx(ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: n, To: &target, Gas: 3_000_000, GasPrice: big.NewInt(2_000_000_000), Value: value, Data: data}), ethtypes.NewEIP155Signer(big.NewInt(identity.TestnetEVMChainID)), key)
		require.NoError(t, err)
		raw, err := tx.MarshalBinary()
		require.NoError(t, err)
		rows = append(rows, map[string]any{"label": label, "side": side, "input": tenth(input).String(), "min_out": tenth(minOut).String(), "success": success, "nonce": n, "creator": crypto.PubkeyToAddress(key.PublicKey).Hex(), "recipient": receiver.Hex(), "target": target.Hex(), "hash": tx.Hash().Hex(), "raw": hexutil.Encode(raw)})
	}
	// At CRR=100 this fixture starts at 10 tokens per native unit, with supply=10, reserve=1 and cap=1000.
	add("buy", "buy", true, a, 10, 100, true)
	add("sell", "sell", false, b, 50, 5, true)
	add("sell-reserve-floor", "sell", false, a, 60, 0, false)
	add("sell-supply-floor", "sell", false, a, 149, 0, false)
	add("buy-min-out", "buy", false, a, 10, 101, false)
	add("sell-min-out", "sell", false, b, 10, 2, false)
	add("buy-above-cap", "buy", false, a, 986, 0, false)
	add("approach-cap", "buy", false, a, 980, 9800, true)
	add("race-a", "buy", false, a, 5, 50, true)
	add("race-b", "buy", true, b, 5, 50, true)
	out, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "trades.json"), out, 0600))
}
