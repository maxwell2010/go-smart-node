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

// This ordinary two-owner business case records the still-unfixed global
// rounding discrepancy. It does not choose a new claim-rounding policy.
func checkSignedTinyUnitRounding(t *testing.T, keys []*ecdsa.PrivateKey, roles map[string]common.Address, artifacts map[string]contracts.Artifact, send func(*ecdsa.PrivateKey, *common.Address, *big.Int, []byte, bool) *evmtypes.MsgEthereumTxResponse) {
	t.Helper()
	roles, artifacts = cloneRoleMap(roles), cloneArtifactMap(artifacts)
	call := func(user int, role, method string, value *big.Int, args ...interface{}) []interface{} {
		target := roles[role]
		artifact := artifacts[role]
		input, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		response := send(keys[user], &target, value, input, true)
		result, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		return result
	}
	owner := crypto.PubkeyToAddress(keys[0].PublicKey)
	second := crypto.PubkeyToAddress(keys[2].PublicKey)
	validator := crypto.PubkeyToAddress(keys[4].PublicKey)
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validator.Bytes())
	require.NoError(t, err)
	meta, err := json.Marshal(map[string]interface{}{"operator_address": operator, "reward_address": validator.Hex(), "consensus_pubkey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x65}, 32)), "description": map[string]string{"moniker": "rounding-validator"}, "commission": "10"})
	require.NoError(t, err)
	type validatorMeta struct {
		Validator common.Address
		Meta      string
	}
	call(1, "master-validator", "addValidators", new(big.Int), []validatorMeta{{validator, string(meta)}})
	unit := big.NewInt(1_000_000_000_000_000_000)
	type coinMeta struct {
		InitialMint, MinTotalSupply, MaxTotalSupply *big.Int
		Creator                                     common.Address
		Crr                                         uint8
		Identity, Symbol, Name                      string
	}
	coin := coinMeta{new(big.Int).Mul(unit, big.NewInt(100)), new(big.Int).Set(unit), new(big.Int).Mul(unit, big.NewInt(1000)), owner, 100, "rounding-fixture", "roundcoinx", "Rounding reserve coin"}
	commission := call(0, "token-center", "getCommissionSymbol", new(big.Int), coin.Symbol)[0].(*big.Int)
	funding := new(big.Int).Add(new(big.Int).Mul(unit, big.NewInt(100)), commission)
	call(0, "token-center", "createToken", funding, coin)
	token := call(0, "token-center", "tokens", new(big.Int), coin.Symbol)[0].(common.Address)
	tokenArtifact, err := contracts.Load("DRC20")
	require.NoError(t, err)
	roles["round-token"], artifacts["round-token"] = token, tokenArtifact
	initialSupply := new(big.Int).Set(call(0, "round-token", "totalSupply", new(big.Int))[0].(*big.Int))
	call(0, "round-token", "transfer", new(big.Int), second, big.NewInt(3))
	for _, user := range []int{0, 2} {
		call(user, "round-token", "approve", new(big.Int), roles["delegation"], big.NewInt(3))
		call(user, "delegation", "delegate", new(big.Int), validator, token, big.NewInt(3))
	}
	var ids [][32]byte
	for _, holder := range []common.Address{owner, second} {
		ids = append(ids, call(0, "delegation", "getStakeId", new(big.Int), validator, holder, token)[0].([32]byte))
	}
	call(1, "master-validator", "addPenalty", new(big.Int), validator, uint16(50))
	call(0, "delegation", "applyPenaltiesToStakes", new(big.Int), ids)
	type validatorToken struct {
		Validator, Token common.Address
		TokenId          *big.Int
	}
	call(0, "delegation", "applyPenaltiesToValidator", new(big.Int), []validatorToken{{validator, token, new(big.Int)}})
	claims := new(big.Int)
	for _, holder := range []common.Address{owner, second} {
		record := call(0, "delegation", "getStake", new(big.Int), validator, holder, token)[0]
		amount := reflect.ValueOf(record).FieldByName("Amount").Interface().(*big.Int)
		require.Equal(t, big.NewInt(2), amount)
		claims.Add(claims, amount)
	}
	escrow := call(0, "round-token", "balanceOf", new(big.Int), roles["delegation"])[0].(*big.Int)
	finalSupply := call(0, "round-token", "totalSupply", new(big.Int))[0].(*big.Int)
	burned := new(big.Int).Sub(initialSupply, finalSupply)
	t.Logf("two3-unit positions at50%%: survivingClaims=%s escrow=%s actualERC20SupplyBurn=%s realizedIndividualLoss=2", claims, escrow, burned)
	require.Equal(t, big.NewInt(4), claims)
	require.Equal(t, big.NewInt(3), escrow)
	require.Equal(t, big.NewInt(3), burned)
	require.True(t, claims.Cmp(escrow) > 0, "negative control: aggregate floor burns more than the sum of individual losses")
}
