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

// Two ordinary holders keep legacy floor-per-position claims. Global target
// acknowledgement must not burn collateral before an individual loss realizes.
func checkSignedLazyTinyUnitSettlement(t *testing.T, reverse bool, keys []*ecdsa.PrivateKey, roles map[string]common.Address, artifacts map[string]contracts.Artifact, wrappedAddress common.Address, nativeSupply func() *big.Int, nativeBalance func(common.Address) *big.Int, send func(*ecdsa.PrivateKey, *common.Address, *big.Int, []byte, bool) *evmtypes.MsgEthereumTxResponse) {
	t.Helper()
	roles, artifacts = cloneRoleMap(roles), cloneArtifactMap(artifacts)
	wrapped, err := contracts.Load("MintCandyWrappedDEL")
	require.NoError(t, err)
	artifacts["wdel"] = wrapped
	roles["wdel"] = wrappedAddress
	var last *evmtypes.MsgEthereumTxResponse
	call := func(user int, role, method string, value *big.Int, args ...interface{}) []interface{} {
		target, artifact := roles[role], artifacts[role]
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		last = send(keys[user], &target, value, data, true)
		result, err := artifact.ABI.Unpack(method, last.Ret)
		require.NoError(t, err)
		return result
	}
	integer := func(result []interface{}, field string) *big.Int {
		if field == "" {
			return result[0].(*big.Int)
		}
		return reflect.ValueOf(result[0]).FieldByName(field).Interface().(*big.Int)
	}
	seed := byte(0x71)
	if reverse {
		seed++
	}
	validator := common.BytesToAddress(crypto.Keccak256([]byte{seed}, []byte("ordinary-lazy-rounding"))[12:])
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validator.Bytes())
	require.NoError(t, err)
	meta, err := json.Marshal(map[string]interface{}{"operator_address": operator, "reward_address": validator.Hex(), "consensus_pubkey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32)), "description": map[string]string{"moniker": "lazy-rounding"}, "commission": "10"})
	require.NoError(t, err)
	type validatorMeta struct {
		Validator common.Address
		Meta      string
	}
	call(1, "master-validator", "addValidators", new(big.Int), []validatorMeta{{validator, string(meta)}})
	call(1, "delegation", "setFreezeTime", new(big.Int), new(big.Int), new(big.Int))
	initialSupply := new(big.Int).Set(nativeSupply())
	initialWrapped := new(big.Int).Set(integer(call(1, "wdel", "totalSupply", new(big.Int)), ""))
	require.Zero(t, integer(call(1, "wdel", "balanceOf", new(big.Int), roles["delegation"]), "").Sign(), "each isolated business case starts with empty staking escrow")
	for _, user := range []int{0, 2} {
		call(user, "delegation", "delegateDEL", big.NewInt(3), validator)
	}
	call(1, "master-validator", "addPenalty", new(big.Int), validator, uint16(50))
	type validatorToken struct {
		Validator, Token common.Address
		TokenId          *big.Int
	}
	targets := []validatorToken{{validator, roles["wdel"], new(big.Int)}}
	call(1, "delegation", "applyPenaltiesToValidator", new(big.Int), targets)
	require.Equal(t, initialSupply, nativeSupply(), "global notification must not pre-burn aggregate floor")
	require.Equal(t, big.NewInt(6), integer(call(1, "wdel", "balanceOf", new(big.Int), roles["delegation"]), ""))
	order := []int{0, 2}
	if reverse {
		order = []int{2, 0}
	}
	indices := make([]*big.Int, 0, 2)
	event := artifacts["delegation"].ABI.Events["WithdrawRequest"]
	for i, user := range order {
		call(user, "delegation", "withdraw", new(big.Int), validator, roles["wdel"], big.NewInt(2))
		var index *big.Int
		for _, entry := range last.Logs {
			if common.HexToAddress(entry.Address) == roles["delegation"] && len(entry.Topics) > 0 && common.HexToHash(entry.Topics[0]) == event.ID {
				values, err := event.Inputs.NonIndexed().Unpack(entry.Data)
				require.NoError(t, err)
				index = values[1].(*big.Int)
			}
		}
		require.NotNil(t, index)
		indices = append(indices, index)
		require.Equal(t, new(big.Int).Sub(initialSupply, big.NewInt(int64(i+1))), nativeSupply(), "burn exact realized one-unit loss")
	}
	require.Equal(t, big.NewInt(4), integer(call(1, "wdel", "balanceOf", new(big.Int), roles["delegation"]), ""))
	require.Zero(t, integer(call(1, "delegation", "getValidatorReserve", new(big.Int), validator, roles["wdel"]), "Reserve").Sign(), "both claims are now frozen, not bonded")
	// Repeating target acknowledgement is rejected and cannot burn again.
	target := roles["delegation"]
	data, err := artifacts["delegation"].ABI.Pack("applyPenaltiesToValidator", targets)
	require.NoError(t, err)
	send(keys[1], &target, new(big.Int), data, false)
	require.Equal(t, new(big.Int).Sub(initialSupply, big.NewInt(2)), nativeSupply())
	for i, user := range order {
		owner := crypto.PubkeyToAddress(keys[user].PublicKey)
		before := new(big.Int).Set(nativeBalance(owner))
		call(1, "delegation", "complete", new(big.Int), []*big.Int{indices[i]})
		require.Equal(t, new(big.Int).Add(before, big.NewInt(2)), nativeBalance(owner), "separate completion caller pays fees; beneficiary receives exact two-unit claim")
	}
	require.Zero(t, integer(call(1, "wdel", "balanceOf", new(big.Int), roles["delegation"]), "").Sign())
	require.Zero(t, initialWrapped.Cmp(integer(call(1, "wdel", "totalSupply", new(big.Int)), "")))
	require.Equal(t, new(big.Int).Sub(initialSupply, big.NewInt(2)), nativeSupply())
	t.Logf("reverse=%v: two3-unit claims at50%% paid2+2, native supply burn2, final escrow0", reverse)
}
