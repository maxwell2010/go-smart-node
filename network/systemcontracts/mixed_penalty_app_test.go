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

func checkSignedMixedPenalty(t *testing.T, user int, nftFirst bool, owned bool, keys []*ecdsa.PrivateKey, roles map[string]common.Address, artifacts map[string]contracts.Artifact, wrapped common.Address, registryAddress common.Address, registryArtifact contracts.Artifact, nativeSupply func() *big.Int, send func(*ecdsa.PrivateKey, *common.Address, *big.Int, []byte, bool) *evmtypes.MsgEthereumTxResponse) {
	t.Helper()
	// The fresh network uses the unified gateway for both coin and NFT flows.
	// Canonical NFT reserve authorization resolves the delegation-nft role.
	input, err := registryArtifact.ABI.Pack("getContractAddress", "delegation-nft")
	require.NoError(t, err)
	roleResponse := send(keys[user], &registryAddress, new(big.Int), input, true)
	roleValues, err := registryArtifact.ABI.Unpack("getContractAddress", roleResponse.Ret)
	require.NoError(t, err)
	require.Equal(t, roles["delegation"], roleValues[0], "canonical NFT reserve auth must resolve the unified gateway")
	validator := crypto.PubkeyToAddress(keys[user].PublicKey)
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validator.Bytes())
	require.NoError(t, err)
	meta, err := json.Marshal(map[string]interface{}{"operator_address": operator, "reward_address": validator.Hex(), "consensus_pubkey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{byte(0x55 + user)}, 32)), "description": map[string]string{"moniker": "mixed-penalty-validator"}, "commission": "10"})
	require.NoError(t, err)
	unit := big.NewInt(1_000_000_000_000_000_000)
	amount := func(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), unit) }
	call := func(key int, role, method string, value *big.Int, args ...interface{}) []interface{} {
		target := roles[role]
		artifact := artifacts[role]
		input, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		response := send(keys[key], &target, value, input, true)
		result, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		return result
	}
	call(1, "master-validator", "addValidatorDEL", amount(40), validator, string(meta))
	type collection struct {
		Creator                   common.Address
		Symbol, Name, ContractURI string
		Refundable, Burnable      bool
	}
	result := call(user, "nft-center", "createDRC721", new(big.Int), collection{validator, "PENALTY", "Penalty Reserve NFT", "ipfs://penalty-collection", true, true})
	require.Len(t, result, 1)
	nft := result[0].(common.Address)
	nftArtifact, err := contracts.Load("DRC721")
	require.NoError(t, err)
	roles = cloneRoleMap(roles)
	artifacts = cloneArtifactMap(artifacts)
	roles["penalty-nft"] = nft
	artifacts["penalty-nft"] = nftArtifact
	result = call(user, "penalty-nft", "mintByDEL", amount(60), validator, "ipfs://penalty-token")
	tokenID := result[0].(*big.Int)
	call(user, "penalty-nft", "approve", new(big.Int), roles["delegation"], tokenID)
	call(user, "delegation", "delegateNFT", new(big.Int), validator, nft, tokenID)
	integer := func(result []interface{}, field string) *big.Int {
		if field == "" {
			return result[0].(*big.Int)
		}
		return reflect.ValueOf(result[0]).FieldByName(field).Interface().(*big.Int)
	}
	stakeID := call(user, "delegation", "getStakeId", new(big.Int), validator, validator, wrapped)[0].([32]byte)
	nftID := call(user, "delegation", "getNFTStakeId", new(big.Int), nft, tokenID, validator, validator, new(big.Int))[0].([32]byte)
	read := func(label string) (total, available, nftBacking, reserve, nftReserve *big.Int) {
		total = integer(call(user, "delegation", "getStake", new(big.Int), validator, validator, wrapped), "Amount")
		available = integer(call(user, "delegation", "getAvailableCoinAmount", new(big.Int), validator, validator, wrapped, new(big.Int)), "")
		nftBacking = integer(call(user, "delegation", "getNFTBackedAmount", new(big.Int), validator, validator, wrapped, new(big.Int)), "")
		reserve = integer(call(user, "delegation", "getValidatorReserve", new(big.Int), validator, wrapped), "Reserve")
		nftReserve = integer(call(user, "penalty-nft", "getReserve", new(big.Int), tokenID), "Amount")
		t.Logf("%s: coinStake=%s directAvailable=%s NFTbacked=%s validatorReserve=%s NFTactualReserve=%s", label, total, available, nftBacking, reserve, nftReserve)
		return
	}
	total, available, backing, reserve, nftReserve := read("before penalty")
	require.Equal(t, amount(100), total)
	require.Equal(t, amount(40), available)
	require.Equal(t, amount(60), backing)
	require.Equal(t, amount(100), reserve)
	require.Equal(t, amount(60), nftReserve)
	call(1, "master-validator", "addPenalty", new(big.Int), validator, uint16(10))
	applyNFT := func() {
		beforeSupply := new(big.Int).Set(nativeSupply())
		call(user, "delegation", "applyPenaltiesToNFTStakes", new(big.Int), [][32]byte{nftID})
		afterSupply := nativeSupply()
		t.Logf("canonical NFT native supply delta=%s", new(big.Int).Sub(afterSupply, beforeSupply))
		require.Equal(t, beforeSupply, afterSupply, "canonical NFT reserve sink is not a protocol bank-supply burn; this remains a separate gate")
	}
	if nftFirst {
		applyNFT()
		read("after NFT penalty first")
		call(user, "delegation", "applyPenaltiesToStakes", new(big.Int), [][32]byte{stakeID})
	} else {
		call(user, "delegation", "applyPenaltiesToStakes", new(big.Int), [][32]byte{stakeID})
		read("after coin penalty first")
		applyNFT()
	}
	total, available, backing, reserve, nftReserve = read("after coin and NFT penalties")
	// Independently exercise aggregate settlement. Canonical unified code sends
	// WDEL to address0, which the fresh standard wrapper rejects.
	type validatorToken struct {
		Validator, Token common.Address
		TokenId          *big.Int
	}
	input, err = artifacts["delegation"].ABI.Pack("applyPenaltiesToValidator", []validatorToken{{validator, wrapped, new(big.Int)}})
	require.NoError(t, err)
	target := roles["delegation"]
	failed := send(keys[user], &target, new(big.Int), input, false)
	wrapper, err := contracts.Load("MintCandyWrappedDEL")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(failed.Ret), 4)
	var selector [4]byte
	copy(selector[:], failed.Ret[:4])
	reason, err := wrapper.ABI.ErrorByID(selector)
	require.NoError(t, err)
	t.Logf("canonical aggregate settlement rejected by fresh wrapper: %s raw=%x", reason.Name, failed.Ret)
	require.Equal(t, "ERC20InvalidReceiver", reason.Name)
	// A single10% penalty should retain36 direct units and54 NFT reserve units.
	// The unified total and validator reserve must both reconcile to90 units.
	require.Equal(t, amount(54), nftReserve, "NFT actual reserve after10%")
	require.Equal(t, amount(54), backing, "NFT backing after10%")
	if owned {
		require.Equal(t, amount(36), available, "direct collateral must lose10%, not absorb NFT penalty twice")
		require.Equal(t, amount(90), total, "one penalty must not reduce NFT-backed collateral twice")
	} else {
		expectedDirect := amount(30)
		if nftFirst {
			expectedDirect.Mul(expectedDirect, big.NewInt(102))
			expectedDirect.Div(expectedDirect, big.NewInt(100))
		}
		require.Equal(t, expectedDirect, available, "canonical negative control must preserve the observed ordering-dependent defect")
		require.Equal(t, new(big.Int).Add(expectedDirect, amount(54)), total)
	}
	// Aggregate reserve is a lazy gross record until its separate settlement
	// entrypoint succeeds. Compare the effective10% amount, not gross100.
	expectedEffective := new(big.Int).Sub(reserve, new(big.Int).Div(new(big.Int).Set(reserve), big.NewInt(10)))
	if owned {
		require.Equal(t, total, expectedEffective, "effective aggregate must reconcile to unified stake")
	} else {
		require.NotEqual(t, total, expectedEffective, "canonical negative control must fail the conservation predicate")
	}
}

func cloneRoleMap(in map[string]common.Address) map[string]common.Address {
	out := map[string]common.Address{}
	for key, value := range in {
		out[key] = value
	}
	return out
}
func cloneArtifactMap(in map[string]contracts.Artifact) map[string]contracts.Artifact {
	out := map[string]contracts.Artifact{}
	for key, value := range in {
		out[key] = value
	}
	return out
}
