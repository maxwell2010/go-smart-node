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

func TestSignTestnetAtomicConvert(t *testing.T) {
	dir := os.Getenv("MCND_CONVERT_DIR")
	if dir == "" {
		t.Skip("isolated tMSP only")
	}
	requirePrivateMainnetFixture(t)
	require.Equal(t, identity.TestnetChainID, identity.ProfileChainID)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(dir) && info.IsDir() && info.Mode().Perm()&0077 == 0)
	address := func(key string) common.Address {
		raw := os.Getenv("MCND_CONVERT_" + key)
		require.True(t, common.IsHexAddress(raw))
		parsed := common.HexToAddress(raw)
		require.NotEqual(t, common.Address{}, parsed)
		return parsed
	}
	input, output, capped, center, recipient := address("IN"), address("OUT"), address("CAPPED"), address("CENTER"), address("RECEIVER")
	founder, err := crypto.LoadECDSA(os.Getenv("MCND_TESTNET_TARIFF_FOUNDER_FILE"))
	require.NoError(t, err)
	creator := crypto.PubkeyToAddress(founder.PublicKey)
	nonce, err := strconv.ParseUint(os.Getenv("MCND_CONVERT_NONCE"), 10, 64)
	require.NoError(t, err)
	erc, err := contracts.CompiledABI("DRC20")
	require.NoError(t, err)
	factory, err := contracts.CompiledABI("NewNetworkTokenCenter")
	require.NoError(t, err)
	method := ""
	for _, m := range factory.Methods {
		if m.Sig == "convert(address,address,uint256,uint256,address)" {
			method = m.Name
		}
	}
	require.NotEmpty(t, method)
	units := func(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), big.NewInt(1_000_000_000_000_000_000)) }
	var rows []map[string]any
	sign := func(label, action string, target, assetIn, assetOut, receiver common.Address, data []byte, value, inputAmount, minOut *big.Int, want bool) {
		t.Helper()
		tx, err := ethtypes.SignTx(ethtypes.NewTx(&ethtypes.LegacyTx{To: &target, Nonce: nonce, Value: value, Gas: 8_000_000, GasPrice: big.NewInt(2_000_000_000), Data: data}), ethtypes.NewEIP155Signer(big.NewInt(identity.TestnetEVMChainID)), founder)
		require.NoError(t, err)
		raw, err := tx.MarshalBinary()
		require.NoError(t, err)
		rows = append(rows, map[string]any{"label": label, "action": action, "success": want, "target": target.Hex(), "creator": creator.Hex(), "receiver": receiver.Hex(), "input": assetIn.Hex(), "output": assetOut.Hex(), "value": hexutil.EncodeBig(value), "amount": inputAmount.String(), "minimum": minOut.String(), "nonce": nonce, "raw": hexutil.Encode(raw), "hash": tx.Hash().Hex(), "data": hexutil.Encode(data)})
		nonce++
	}
	buy := func(label string, target common.Address, value int64) {
		in, min := units(value), units(10*value)
		data, err := erc.Pack("buy", min, creator)
		require.NoError(t, err)
		sign(label, "buy", target, target, target, creator, data, in, in, min, true)
	}
	convert := func(label string, src, dst common.Address, in, min int64, success bool) {
		amount, minimum := units(in), units(min)
		data, err := factory.Pack(method, src, dst, amount, minimum, recipient)
		require.NoError(t, err)
		sign(label, "convert", center, src, dst, recipient, data, new(big.Int), amount, minimum, success)
	}
	buy("fund-input", input, 2)
	convert("missing-allowance", input, output, 5, 5, false)
	amount := units(50)
	approval, err := erc.Pack("approve", center, amount)
	require.NoError(t, err)
	sign("approve", "approve", input, input, output, center, approval, new(big.Int), amount, new(big.Int), true)
	convert("convert", input, output, 5, 5, true)
	convert("min-out", input, output, 5, 6, false)
	convert("input-reserve-floor", input, output, 20, 0, false)
	convert("output-cap", input, capped, 1, 0, false)
	convert("same-token", input, input, 1, 0, false)
	convert("unregistered-out", input, recipient, 1, 0, false)
	convert("unregistered-in", recipient, output, 1, 0, false)
	// Output has 15 tokens after the first conversion; seed it to 995.
	buy("seed-output-near-cap", output, 98)
	convert("exact-cap", input, output, 5, 5, true)
	convert("above-cap", input, output, 1, 0, false)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "conversions.json"), encoded, 0600))
}
