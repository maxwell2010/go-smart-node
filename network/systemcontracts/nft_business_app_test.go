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

// These ordinary fresh-network cases exercise pending exit and ERC1155 reserve
// units with signed public methods. No state or runtime is patched by the test.
func checkSignedNFTBusinessCase(t *testing.T, multi bool, exitMode string, keys []*ecdsa.PrivateKey, roles map[string]common.Address, artifacts map[string]contracts.Artifact, wrapped common.Address, nativeSupply func() *big.Int, send func(*ecdsa.PrivateKey, *common.Address, *big.Int, []byte, bool) *evmtypes.MsgEthereumTxResponse) {
	t.Helper()
	roles, artifacts = cloneRoleMap(roles), cloneArtifactMap(artifacts)
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
	field := func(values []interface{}, name string) *big.Int {
		return reflect.ValueOf(values[0]).FieldByName(name).Interface().(*big.Int)
	}
	seed := byte(0x81)
	if exitMode == "transfer" {
		seed += 2
	}
	if exitMode == "forced" {
		seed += 4
	}
	if exitMode == "penalty" {
		seed += 6
	}
	if exitMode == "rounding" {
		seed += 8
	}
	if multi {
		seed++
	}
	validator := common.BytesToAddress(crypto.Keccak256([]byte{seed}, []byte("ordinary-nft-business-case"))[12:])
	owner := crypto.PubkeyToAddress(keys[0].PublicKey)
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validator.Bytes())
	require.NoError(t, err)
	meta, err := json.Marshal(map[string]interface{}{"operator_address": operator, "reward_address": validator.Hex(), "consensus_pubkey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32)), "description": map[string]string{"moniker": "nft-business-case"}, "commission": "10"})
	require.NoError(t, err)
	type validatorMeta struct {
		Validator common.Address
		Meta      string
	}
	call(1, "master-validator", "addValidators", new(big.Int), []validatorMeta{{validator, string(meta)}})
	type collection struct {
		Creator                   common.Address
		Symbol, Name, ContractURI string
		Refundable, Burnable      bool
	}
	create, name := "createDRC721", "NewNetworkDRC721"
	if multi {
		create, name = "createDRC1155", "NewNetworkDRC1155"
	}
	nft := call(0, "nft-center", create, new(big.Int), collection{owner, "BUSINESS", "NFT business case", "ipfs://business", true, true})[0].(common.Address)
	asset, err := contracts.Load(name)
	require.NoError(t, err)
	roles["business-nft"], artifacts["business-nft"] = nft, asset
	unit := big.NewInt(1_000_000_000_000_000_000)
	amount := func(n int64) *big.Int { return new(big.Int).Mul(unit, big.NewInt(n)) }
	quantity, initialPool, percentage := big.NewInt(2), amount(20), uint16(10)
	expectedPool, expectedClaim, expectedLoss := amount(18), amount(18), amount(2)
	if exitMode == "rounding" {
		quantity, initialPool, percentage = big.NewInt(3), new(big.Int).Add(amount(12), big.NewInt(1)), uint16(50)
		expectedPool, expectedClaim, expectedLoss = new(big.Int).Add(amount(6), big.NewInt(1)), amount(6), amount(6)
	}
	var tokenID *big.Int
	if multi {
		tokenID = new(big.Int).Lsh(big.NewInt(1), 200)
		call(0, "business-nft", "mintByDEL", initialPool, owner, tokenID, quantity, "ipfs://units")
		call(0, "business-nft", "setApprovalForAll", new(big.Int), roles["delegation"], true)
		call(0, "delegation", "delegateNFT1155", new(big.Int), validator, nft, tokenID, quantity)
	} else {
		tokenID = call(0, "business-nft", "mintByDEL", amount(20), owner, "ipfs://one")[0].(*big.Int)
		call(0, "business-nft", "approve", new(big.Int), roles["delegation"], tokenID)
		call(0, "delegation", "delegateNFT", new(big.Int), validator, nft, tokenID)
	}
	nftID := call(0, "delegation", "getNFTStakeId", new(big.Int), nft, tokenID, owner, validator, new(big.Int))[0].([32]byte)
	call(1, "master-validator", "addPenalty", new(big.Int), validator, percentage)
	beforeSupply := new(big.Int).Set(nativeSupply())
	if multi && (exitMode == "penalty" || exitMode == "rounding") {
		call(0, "delegation", "applyPenaltiesToNFTStakes", new(big.Int), [][32]byte{nftID})
		actual := field(call(1, "business-nft", "getReserve", new(big.Int), tokenID), "Amount")
		recorded := field(call(1, "delegation", "getNFTStakeById", new(big.Int), nftID), "ReserveAmount")
		t.Logf("ERC1155 quantity=%s: actualReserve=%s recordedStakeReserve=%s actualNativeBurn=%s", quantity, actual, recorded, new(big.Int).Sub(beforeSupply, nativeSupply()))
		require.Equal(t, expectedPool, actual)
		require.Equal(t, expectedLoss, new(big.Int).Sub(beforeSupply, nativeSupply()))
		require.Equal(t, expectedClaim, recorded, "record reserve loss in wei, never NFT quantity")
		require.Equal(t, expectedClaim, field(call(1, "delegation", "getStake", new(big.Int), validator, owner, wrapped), "Amount"))
		require.Equal(t, expectedClaim, field(call(1, "delegation", "getValidatorReserve", new(big.Int), validator, wrapped), "Reserve"))
		if exitMode == "rounding" {
			require.Equal(t, big.NewInt(1), new(big.Int).Sub(actual, recorded), "whole-ID pool remainder remains unassigned")
		}
	} else {
		destination := common.BytesToAddress(crypto.Keccak256([]byte{seed}, []byte("ordinary-nft-destination"))[12:])
		transferMethod, withdrawMethod, forcedMethod := "transferNFT", "withdrawNFT", "forcedNFTWithdrawal"
		if multi {
			transferMethod, withdrawMethod, forcedMethod = "transferNFT1155", "withdrawNFT1155", "forcedNFT1155Withdrawal"
		}
		if exitMode == "transfer" {
			targetOperator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(destination.Bytes())
			require.NoError(t, err)
			targetMeta, err := json.Marshal(map[string]interface{}{"operator_address": targetOperator, "reward_address": destination.Hex(), "consensus_pubkey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed + 16}, 32)), "description": map[string]string{"moniker": "nft-destination"}, "commission": "10"})
			require.NoError(t, err)
			call(1, "master-validator", "addValidators", new(big.Int), []validatorMeta{{destination, string(targetMeta)}})
			call(0, "delegation", transferMethod, new(big.Int), validator, nft, tokenID, destination)
		} else if exitMode == "forced" {
			call(1, "delegation", forcedMethod, new(big.Int), validator, owner, nft, tokenID, new(big.Int))
		} else {
			call(0, "delegation", withdrawMethod, new(big.Int), validator, nft, tokenID)
		}
		if exitMode != "forced" {
			event := artifacts["delegation"].ABI.Events["NFTUndelegated"]
			var index *big.Int
			for _, entry := range last.Logs {
				if common.HexToAddress(entry.Address) == roles["delegation"] && len(entry.Topics) > 0 && common.HexToHash(entry.Topics[0]) == event.ID {
					values, err := event.Inputs.NonIndexed().Unpack(entry.Data)
					require.NoError(t, err)
					index = values[0].(*big.Int)
				}
			}
			require.NotNil(t, index)
			call(1, "delegation", "complete", new(big.Int), []*big.Int{index})
		}
		expectedOwner := owner
		if exitMode == "transfer" {
			expectedOwner = roles["delegation"]
			targetStake := call(1, "delegation", "getNFTStake", new(big.Int), nft, tokenID, owner, destination, new(big.Int))
			require.Equal(t, amount(18), field(targetStake, "ReserveAmount"))
			require.Equal(t, amount(18), field(call(1, "delegation", "getValidatorReserve", new(big.Int), destination, wrapped), "Reserve"))
		}
		if multi {
			require.Equal(t, quantity, call(1, "business-nft", "balanceOf", new(big.Int), expectedOwner, tokenID)[0].(*big.Int))
		} else {
			require.Equal(t, expectedOwner, call(1, "business-nft", "ownerOf", new(big.Int), tokenID)[0])
		}
		actual := field(call(1, "business-nft", "getReserve", new(big.Int), tokenID), "Amount")
		t.Logf("pending NFT penalty then ordinary %s: returnedReserve=%s nativeBurn=%s", exitMode, actual, new(big.Int).Sub(beforeSupply, nativeSupply()))
		require.Equal(t, amount(18), actual, "pending NFT loss must be realized before detaching its coin companion")
		require.Equal(t, amount(2), new(big.Int).Sub(beforeSupply, nativeSupply()))
		require.Zero(t, field(call(1, "delegation", "getValidatorReserve", new(big.Int), validator, wrapped), "Reserve").Sign(), "source raw reserve must be debited on every exit")
		require.Zero(t, field(call(1, "delegation", "getStake", new(big.Int), validator, owner, wrapped), "Amount").Sign())
		target := roles["delegation"]
		data, err := artifacts["delegation"].ABI.Pack("applyPenaltiesToNFTStakes", [][32]byte{nftID})
		require.NoError(t, err)
		send(keys[0], &target, new(big.Int), data, false)
		require.Equal(t, amount(2), new(big.Int).Sub(beforeSupply, nativeSupply()), "inactive source cannot burn twice")
	}
}
