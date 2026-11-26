package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// Only the loopback rehearsal invokes this signer. All signatures stay in its private temporary directory.
func TestSignNativeFeeRehearsal(t *testing.T) {
	dir := os.Getenv("MCND_PROFILE_FEE_FIXTURE")
	if dir == "" {
		t.Skip("requires the disposable profile rehearsal")
	}
	requirePrivateMainnetFixture(t)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir() && info.Mode().Perm()&0077 == 0)
	doc, err := cmttypes.GenesisDocFromFile(os.Getenv("MCND_PROFILE_FEE_GENESIS"))
	require.NoError(t, err)
	require.Equal(t, identity.ProfileChainID, doc.ChainID)
	chain := int64(identity.MainnetEVMChainID)
	if doc.ChainID == identity.TestnetChainID {
		chain = identity.TestnetEVMChainID
	} else {
		require.Equal(t, identity.MainnetChainID, doc.ChainID)
	}
	var modules struct {
		Withdrawal ownedapp.WithdrawalProfile `json:"withdrawal_settlement"`
	}
	require.NoError(t, json.Unmarshal(doc.AppState, &modules))
	center, converter := modules.Withdrawal.TokenCenter, modules.Withdrawal.CurveConverter
	require.NotEqual(t, common.Address{}, center)
	require.NotEqual(t, common.Address{}, converter)
	spec, ok := nativeLaunchSpecForMode("4-empty-sponsored")
	require.True(t, ok)
	key, err := crypto.ToECDSA(nativeLaunchAccountKey(1, spec).Bytes())
	require.NoError(t, err)
	payer := crypto.PubkeyToAddress(key.PublicKey)
	price := big.NewInt(2_000_000_000)
	signer := ethtypes.NewEIP155Signer(big.NewInt(chain))
	sign := func(nonce, gas uint64, to common.Address, value *big.Int, data []byte) (*ethtypes.Transaction, string) {
		t.Helper()
		tx, err := ethtypes.SignTx(ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce, To: &to, Value: value,
			Gas: gas, GasPrice: price, Data: data}), signer, key)
		require.NoError(t, err)
		raw, err := tx.MarshalBinary()
		require.NoError(t, err)
		return tx, hexutil.Encode(raw)
	}
	write := func(name string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".json"), data, 0600))
	}
	unit := func(n int64) *big.Int { return sdkmath.NewIntWithDecimal(n, 18).BigInt() }
	tokenHex := os.Getenv("MCND_PROFILE_FEE_TOKEN")
	if tokenHex == "" {
		factory, err := contracts.CompiledABI("NewNetworkTokenCenter")
		require.NoError(t, err)
		meta := struct {
			InitialMint, MinTotalSupply, MaxTotalSupply *big.Int
			Creator                                     common.Address
			Crr                                         uint8
			Identity, Symbol, Name                      string
		}{unit(10), unit(1), unit(1000), payer, 100, "profile-fee-test", "feesmoke", "PROFILE FEE TOKEN"}
		if doc.ChainID == identity.TestnetChainID {
			meta.Symbol = "feesmockxx"
		}
		data, err := factory.Pack("createToken", meta)
		require.NoError(t, err)
		if doc.ChainID == identity.TestnetChainID {
			data = append(data, testTariffApproval(unit(10), "0x00000000000000000000000000000000Cafe9900")...)
		}
		tx, raw := sign(0, 16_000_000, center, new(big.Int).Add(unit(10), big.NewInt(250_000_000_000_000_000)), data)
		write("create", map[string]string{"creator": payer.Hex(), "target": center.Hex(), "hash": tx.Hash().Hex(),
			"raw": raw, "event": factory.Events["TokenDeployed"].ID.Hex()})
		return
	}
	require.True(t, common.IsHexAddress(tokenHex))
	token := common.HexToAddress(tokenHex)
	abi, err := contracts.CompiledABI("DRC20")
	require.NoError(t, err)
	const gas, conversion = uint64(1_000_000), uint64(5_000_000)
	budget := new(big.Int).Mul(price, new(big.Int).SetUint64(gas+conversion))
	deadline := uint64(time.Now().Add(10 * time.Minute).Unix())
	policy := authorization.Policy{CosmosChainID: doc.ChainID, EVMChainID: big.NewInt(chain),
		VerifyingModule: ownedapp.DRC20FeeModuleAddress(), MaxConversionGasLimit: ownedapp.DRC20FeeMaxConversionGas,
		ConversionNativeFee: new(big.Int).Mul(price, new(big.Int).SetUint64(conversion))}
	var operations []map[string]any
	for index, name := range []string{"approve", "revert"} {
		method, recipient, amount := "approve", converter, big.NewInt(1)
		if name == "revert" {
			method, amount = "transfer", unit(100)
		}
		data, err := abi.Pack(method, recipient, amount)
		require.NoError(t, err)
		tx, raw := sign(uint64(index+1), gas, token, new(big.Int), data)
		a := authorization.Authorization{TxHash: tx.Hash(), Payer: payer, Token: token, ExactInput: unit(1),
			MinimumNativeOutput: budget, NativeFeeBudget: budget, ConversionGasLimit: conversion, Deadline: deadline, FeeNonce: big.NewInt(int64(index))}
		digest, err := authorization.SigningDigest(policy, a)
		require.NoError(t, err)
		sig, err := crypto.Sign(digest[:], key)
		require.NoError(t, err)
		txFile, sigFile := name+".ethereum.hex", name+".signature.hex"
		require.NoError(t, os.WriteFile(filepath.Join(dir, txFile), []byte(raw), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, sigFile), []byte(hexutil.Encode(sig)), 0600))
		operations = append(operations, map[string]any{"name": name, "raw_file": txFile, "signature_file": sigFile,
			"fee_nonce": index, "ethereum_hash": tx.Hash().Hex(), "digest": digest.Hex()})
	}
	write("fee", map[string]any{"chain_id": doc.ChainID, "evm_id": chain, "token": token.Hex(), "payer": payer.Hex(),
		"exact_input": unit(1).String(), "min_native_output": budget.String(), "native_fee_budget": budget.String(),
		"conversion_gas": conversion, "deadline": deadline, "operations": operations})
}
