package systemcontracts_test

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"reflect"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec/address"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// Measurement only: a fungible ID shares one reserve across both owners. These
// ordinary exits/redemptions do not impose a new collateral allocation policy.
func checkSignedShared1155(t *testing.T, reverse, settleFirst bool, keys []*ecdsa.PrivateKey, roles map[string]common.Address, artifacts map[string]contracts.Artifact, wrapped common.Address, nativeSupply func() *big.Int, nativeBalance func(common.Address) *big.Int, expectRefund func(*big.Int), price *big.Int, send func(*ecdsa.PrivateKey, *common.Address, *big.Int, []byte, bool) *evmtypes.MsgEthereumTxResponse) {
	t.Helper()
	roles, artifacts = cloneRoleMap(roles), cloneArtifactMap(artifacts)
	var last *evmtypes.MsgEthereumTxResponse
	call := func(user int, role, method string, value *big.Int, args ...interface{}) []interface{} {
		target, artifact := roles[role], artifacts[role]
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		last = send(keys[user], &target, value, data, true)
		out, err := artifact.ABI.Unpack(method, last.Ret)
		require.NoError(t, err)
		return out
	}
	field := func(out []interface{}, name string) *big.Int {
		return reflect.ValueOf(out[0]).FieldByName(name).Interface().(*big.Int)
	}
	seed := byte(0xe0)
	if reverse {
		seed += 4
	}
	if settleFirst {
		seed += 8
	}
	unit := big.NewInt(1_000_000_000_000_000_000)
	amount := func(n int64) *big.Int { return new(big.Int).Mul(unit, big.NewInt(n)) }
	users := []int{0, 2}
	owners := []common.Address{crypto.PubkeyToAddress(keys[0].PublicKey), crypto.PubkeyToAddress(keys[2].PublicKey)}
	validators := make([]common.Address, 2)
	type validatorMeta struct {
		Validator common.Address
		Meta      string
	}
	for i := range validators {
		validators[i] = common.BytesToAddress(crypto.Keccak256([]byte{seed + byte(i)}, []byte("shared-1155-validator"))[12:])
		operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validators[i].Bytes())
		require.NoError(t, err)
		meta, err := json.Marshal(map[string]interface{}{"operator_address": operator, "reward_address": validators[i].Hex(), "consensus_pubkey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed + byte(i)}, 32)), "description": map[string]string{"moniker": "shared-pool"}, "commission": "10"})
		require.NoError(t, err)
		call(1, "master-validator", "addValidators", new(big.Int), []validatorMeta{{validators[i], string(meta)}})
	}
	type collection struct {
		Creator                   common.Address
		Symbol, Name, ContractURI string
		Refundable, Burnable      bool
	}
	nft := call(0, "nft-center", "createDRC1155", new(big.Int), collection{owners[0], "SHARED", "Shared ID reserve", "ipfs://shared", true, true})[0].(common.Address)
	artifact, err := contracts.Load("NewNetworkDRC1155")
	require.NoError(t, err)
	roles["shared-nft"], artifacts["shared-nft"] = nft, artifact
	methodKey := func(sig string) string {
		for key, method := range artifact.ABI.Methods {
			if method.Sig == sig {
				return key
			}
		}
		t.Fatalf("missing exact ABI signature %s", sig)
		return ""
	}
	rateMethod, supplyMethod := methodKey("rate(uint256)"), methodKey("totalSupply(uint256)")
	tokenID := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 220), big.NewInt(int64(seed)))
	call(0, "shared-nft", "mintByDEL", amount(20), owners[0], tokenID, big.NewInt(2), "ipfs://shared-token")
	call(0, "shared-nft", "safeTransferFrom", new(big.Int), owners[0], owners[1], tokenID, big.NewInt(1), []byte{})
	ids := make([][32]byte, 2)
	for i, user := range users {
		call(user, "shared-nft", "setApprovalForAll", new(big.Int), roles["delegation"], true)
		call(user, "delegation", "delegateNFT1155", new(big.Int), validators[i], nft, tokenID, big.NewInt(1))
		ids[i] = call(1, "delegation", "getNFTStakeId", new(big.Int), nft, tokenID, owners[i], validators[i], new(big.Int))[0].([32]byte)
	}
	call(1, "delegation", "setFreezeTime", new(big.Int), new(big.Int), new(big.Int))
	beforeSupply := new(big.Int).Set(nativeSupply())
	call(1, "master-validator", "addPenalty", new(big.Int), validators[0], uint16(10))
	if settleFirst {
		call(0, "delegation", "applyPenaltiesToNFTStakes", new(big.Int), [][32]byte{ids[0]})
		a := field(call(1, "delegation", "getNFTStakeById", new(big.Int), ids[0]), "ReserveAmount")
		b := field(call(1, "delegation", "getNFTStakeById", new(big.Int), ids[1]), "ReserveAmount")
		rate := call(1, "shared-nft", rateMethod, new(big.Int), tokenID)[0].(*big.Int)
		t.Logf("pre-exit A-only realized penalty: pool=%s rate=%s recordedA=%s recordedB=%s", field(call(1, "shared-nft", "getReserve", new(big.Int), tokenID), "Amount"), rate, a, b)
		require.Equal(t, amount(9), a)
		require.Equal(t, amount(10), b)
		require.Equal(t, new(big.Int).Div(amount(19), big.NewInt(2)), rate)
	}
	order := []int{0, 1}
	if reverse {
		order = []int{1, 0}
	}
	payouts := []*big.Int{new(big.Int), new(big.Int)}
	for _, i := range order {
		call(users[i], "delegation", "withdrawNFT1155", new(big.Int), validators[i], nft, tokenID)
		event := artifacts["delegation"].ABI.Events["NFTUndelegated"]
		var index *big.Int
		for _, entry := range last.Logs {
			if common.HexToAddress(entry.Address) == roles["delegation"] && len(entry.Topics) > 0 && common.HexToHash(entry.Topics[0]) == event.ID {
				out, err := event.Inputs.NonIndexed().Unpack(entry.Data)
				require.NoError(t, err)
				index = out[0].(*big.Int)
			}
		}
		require.NotNil(t, index)
		call(1, "delegation", "complete", new(big.Int), []*big.Int{index})
		refund := new(big.Int).Set(call(1, "shared-nft", rateMethod, new(big.Int), tokenID)[0].(*big.Int))
		before := new(big.Int).Set(nativeBalance(owners[i]))
		expectRefund(refund)
		call(users[i], "shared-nft", "burn", new(big.Int), tokenID, big.NewInt(1))
		fee := new(big.Int).Mul(new(big.Int).SetUint64(last.GasUsed), price)
		payouts[i].Sub(nativeBalance(owners[i]), before)
		payouts[i].Add(payouts[i], fee)
		require.Equal(t, refund, payouts[i], "actual SDK refund after separate fee accounting")
	}
	if reverse && !settleFirst {
		require.Equal(t, amount(9), payouts[0])
		require.Equal(t, amount(10), payouts[1])
	} else {
		half := new(big.Int).Div(amount(19), big.NewInt(2))
		require.Equal(t, half, payouts[0])
		require.Equal(t, half, payouts[1])
	}
	require.Equal(t, amount(1), new(big.Int).Sub(beforeSupply, nativeSupply()))
	require.Equal(t, amount(19), new(big.Int).Add(payouts[0], payouts[1]))
	require.Zero(t, field(call(1, "shared-nft", "getReserve", new(big.Int), tokenID), "Amount").Sign())
	require.Zero(t, call(1, "shared-nft", supplyMethod, new(big.Int), tokenID)[0].(*big.Int).Sign())
	require.Zero(t, nativeBalance(nft).Sign())
	for _, validator := range validators {
		require.Zero(t, field(call(1, "delegation", "getValidatorReserve", new(big.Int), validator, wrapped), "Reserve").Sign())
	}
	t.Logf("shared1155 reverse=%v settleFirst=%v: actualPaidA=%s actualPaidB=%s nativeBurn=1DEL pool=0", reverse, settleFirst, payouts[0], payouts[1])
}
