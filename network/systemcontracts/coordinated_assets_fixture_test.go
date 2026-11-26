//go:build mintcandy_testnet

package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

func TestSignCoordinatedPoolAction(t *testing.T) {
	out := os.Getenv("MCND_POOL_OUTPUT")
	if out == "" {
		t.Skip("requires isolated tMSP fixture")
	}
	requirePrivateMainnetFixture(t)
	require.Equal(t, identity.TestnetChainID, identity.ProfileChainID)
	var input struct {
		Action                  string
		Factory, Wrapper, Token string
		Nonce                   uint64
	}
	require.NoError(t, json.Unmarshal([]byte(os.Getenv("MCND_POOL_ACTION")), &input))
	key, err := crypto.LoadECDSA(os.Getenv("MCND_TESTNET_TARIFF_FOUNDER_FILE"))
	require.NoError(t, err)
	sender := crypto.PubkeyToAddress(key.PublicKey)
	require.Equal(t, common.HexToAddress("0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf"), sender)
	h := crypto.Keccak256Hash([]byte("mintcandy/liquidity-pool/wmsp-cusdt/v1:"), []byte(identity.TestnetChainID))
	pool := common.BytesToAddress(h.Bytes())
	wrapper, token := common.HexToAddress(input.Wrapper), common.HexToAddress(input.Token)
	amount := func(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), big.NewInt(1e18)) }
	word := func(n *big.Int) []byte { return common.LeftPadBytes(n.Bytes(), 32) }
	address := func(a common.Address) []byte { return common.LeftPadBytes(a.Bytes(), 32) }
	call := func(method string, words ...[]byte) []byte {
		data := crypto.Keccak256([]byte(method))[:4:4]
		for _, w := range words {
			data = append(data, w...)
		}
		return data
	}
	deadline := word(big.NewInt(4102444800))
	one := word(big.NewInt(1))
	to, value := pool, new(big.Int)
	var data []byte
	switch input.Action {
	case "create":
		to = common.HexToAddress(input.Factory)
		factory, err := contracts.CompiledABI("NewNetworkTokenCenter")
		require.NoError(t, err)
		data, err = factory.Pack("createTokenReserveless", "CUSDT", "cusdt", true, true, amount(5000), amount(10000000), "")
		require.NoError(t, err)
	case "initialize":
		data = call("initialize(address,address)", address(wrapper), address(token))
	case "wrap":
		to, value, data = wrapper, amount(50001), call("deposit()")
	case "approve-wrapper", "approve-token":
		to = wrapper
		n := int64(50001)
		if input.Action == "approve-token" {
			to, n = token, 5000
		}
		data = call("approve(address,uint256)", address(pool), word(amount(n)))
	case "deposit":
		data = call("addLiquidity(uint112,uint112,uint112,uint256)", word(amount(50000)), word(amount(5000)), one, deadline)
	case "swap":
		data = call("swapExactInput(bool,uint112,uint112,uint256)", one, word(amount(1)), one, deadline)
	case "withdraw":
		data = call("removeLiquidity(uint112,uint112,uint112,uint256)", word(amount(1)), one, one, deadline)
	default:
		t.Fatalf("unknown action %q", input.Action)
	}

	tx, err := ethtypes.SignTx(ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: input.Nonce, To: &to, Data: data, Value: value, Gas: 16_000_000, GasPrice: big.NewInt(2_000_000_000)}), ethtypes.NewEIP155Signer(big.NewInt(identity.TestnetEVMChainID)), key)
	require.NoError(t, err)
	b, err := tx.MarshalBinary()
	require.NoError(t, err)
	payload, err := json.Marshal(map[string]string{"raw": hexutil.Encode(b), "hash": tx.Hash().Hex(), "target": to.Hex(), "creator": sender.Hex(), "pool": pool.Hex()})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(out, payload, 0600))
}
