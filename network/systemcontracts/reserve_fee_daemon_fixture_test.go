//go:build appstate

package systemcontracts_test

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const reserveFeeDaemonFixtureEnv = "MCND_DRC20_FEE_DAEMON_FIXTURE"

// This is an opt-in exporter for an already-generated PRIVATE native historical
// + custom-token seed. It does not start a node, submit a transaction, or write
// a payer private key. The two disposable signatures are bound to chain 990001.
func TestExportDRC20FeeDaemonFixture(t *testing.T) {
	dir := os.Getenv(reserveFeeDaemonFixtureEnv)
	if dir == "" {
		t.Skip("private DRC20 daemon fixture directory is not configured")
	}
	info, err := os.Lstat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir() && info.Mode().Perm()&0077 == 0)
	doc, err := cmttypes.GenesisDocFromFile(filepath.Join(dir, "genesis.json"))
	require.NoError(t, err)
	require.Equal(t, "mintcandy_990001-1", doc.ChainID)
	require.Equal(t, int64(1), doc.InitialHeight)
	var expected nativeHistoricalExpectations
	readNativeHistoricalJSON(t, filepath.Join(dir, "expectations.json"), &expected)
	require.Len(t, expected.Validators, 3)
	var state ownedapp.GenesisState
	require.NoError(t, json.Unmarshal(doc.AppState, &state))
	var profile ownedapp.WithdrawalProfile
	require.NoError(t, json.Unmarshal(state[ownedapp.WithdrawalGenesisKey], &profile))
	require.True(t, profile.EnableNativeConsensus && profile.EnableDELDelegation)
	require.False(t, profile.EnableNativeFeeRewards)
	seedDRC20FeePayer(t, state, expected.Actor)
	codec := ownedapp.MakeEncodingConfig(990001).Codec
	var coins cointypes.GenesisState
	require.NoError(t, codec.UnmarshalJSON(state[cointypes.ModuleName], &coins))
	require.Len(t, coins.Coins, 1)
	coin := coins.Coins[0]
	token := common.HexToAddress(coin.DRC20Contract)
	var bank banktypes.GenesisState
	require.NoError(t, codec.UnmarshalJSON(state[banktypes.ModuleName], &bank))
	key, payer := reserveFeePayer(t)
	for _, row := range bank.Balances {
		if row.Address == sdk.AccAddress(payer[:]).String() {
			require.True(t, row.Coins.AmountOf(identity.BaseDenom).IsZero(), "gasless payer starts with no native DEL")
		}
	}
	doc.ConsensusParams.Block.MaxGas = 24_000_000
	doc.GenesisTime = time.Now().UTC().Truncate(time.Second).Add(15 * time.Second)
	doc.AppState, err = json.Marshal(state)
	require.NoError(t, err)
	genesis, err := cmtjson.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "genesis.json"), genesis, 0600))
	abi, err := contracts.CompiledABI("DRC20")
	require.NoError(t, err)
	deadline := uint64(time.Now().Add(time.Hour).Unix())
	input := sdkmath.NewIntWithDecimal(1, 18).BigInt()
	const gas, conversionGas = uint64(1_000_000), uint64(5_000_000)
	price := big.NewInt(1_000_000_000)
	budget := new(big.Int).Mul(new(big.Int).SetUint64(gas+conversionGas), price)
	policy := authorization.Policy{CosmosChainID: doc.ChainID, EVMChainID: big.NewInt(990001), VerifyingModule: ownedapp.DRC20FeeModuleAddress(),
		MaxConversionGasLimit: ownedapp.DRC20FeeMaxConversionGas, ConversionNativeFee: new(big.Int).Mul(new(big.Int).SetUint64(conversionGas), price)}
	operations := make([]map[string]any, 0, 2)
	for index, name := range []string{"approve", "revert"} {
		var data []byte
		if index == 0 {
			// The native profile explicitly admits canonical approvals to its
			// configured converter. This operation needs no native user value.
			data, err = abi.Pack("approve", profile.CurveConverter, big.NewInt(1))
		} else {
			// Even BEFORE either sale, the payer has only 3 tokens. Transfer 4
			// therefore executes an actual ERC20 insufficient-balance REVERT;
			// it is neither an unsupported-success hook error nor ante failure.
			data, err = abi.Pack("transfer", expected.Actor, sdkmath.NewIntWithDecimal(4, 18).BigInt())
		}
		require.NoError(t, err)
		raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: uint64(index), To: &token, Value: new(big.Int), Gas: gas, GasPrice: price, Data: data})
		signed, err := ethtypes.SignTx(raw, ethtypes.NewEIP155Signer(big.NewInt(990001)), key)
		require.NoError(t, err)
		ethBytes, err := signed.MarshalBinary()
		require.NoError(t, err)
		a := authorization.Authorization{TxHash: signed.Hash(), Payer: payer, Token: token, ExactInput: input, MinimumNativeOutput: budget,
			NativeFeeBudget: budget, ConversionGasLimit: conversionGas, Deadline: deadline, FeeNonce: big.NewInt(int64(index))}
		digest, err := authorization.SigningDigest(policy, a)
		require.NoError(t, err)
		signature, err := crypto.Sign(digest[:], key)
		require.NoError(t, err)
		writeHex := func(suffix string, contents []byte) string {
			path := name + suffix
			require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte("0x"+hex.EncodeToString(contents)+"\n"), 0600))
			return path
		}
		operations = append(operations, map[string]any{"name": name, "raw_file": writeHex(".ethereum.hex", ethBytes),
			"signature_file": writeHex(".fee-signature.hex", signature), "ethereum_hash": signed.Hash().Hex(), "digest": digest.Hex(),
			"fee_nonce": index, "status": 1 - index})
	}
	pack := func(method string, args ...any) string {
		data, err := abi.Pack(method, args...)
		require.NoError(t, err)
		return "0x" + hex.EncodeToString(data)
	}
	manifest := map[string]any{"schema": 1, "chain_id": doc.ChainID, "evm_id": 990001, "payer": payer.Hex(), "payer_native": sdk.AccAddress(payer[:]).String(),
		"fee_collector": common.BytesToAddress(authtypes.NewModuleAddress(authtypes.FeeCollectorName)).Hex(), "fee_collector_native": authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
		"token": token.Hex(), "token_native": sdk.AccAddress(token[:]).String(), "actor": expected.Actor.Hex(), "actor_native": sdk.AccAddress(expected.Actor[:]).String(),
		"converter": profile.CurveConverter.Hex(), "native_supply": bank.Supply.AmountOf(identity.BaseDenom).String(), "token_supply": coin.Volume.String(),
		"token_reserve": coin.Reserve.String(), "payer_tokens": sdkmath.NewIntWithDecimal(3, 18).String(), "exact_input": input.String(), "min_native_output": budget.String(),
		"native_fee_budget": budget.String(), "conversion_gas": conversionGas, "ethereum_gas": gas, "gas_price": price.String(), "deadline": deadline,
		"verifying_module": policy.VerifyingModule.Hex(), "payer_balance_data": pack("balanceOf", payer), "actor_balance_data": pack("balanceOf", expected.Actor),
		"supply_data": pack("totalSupply"), "allowance_data": pack("allowance", payer, profile.CurveConverter), "operations": operations}
	writeNativeHistoricalJSON(t, filepath.Join(dir, "reserve-fee-daemon.json"), manifest)
	t.Log("private reserve-fee daemon fixture exported; no payer private key written")
}
