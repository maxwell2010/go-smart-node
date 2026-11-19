//go:build appstate

package systemcontracts_test

import (
	"crypto/ecdsa"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
	"github.com/maxwell2010/go-smart-node/network/fees/envelope"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

var feeBalanceMapSlot = common.HexToHash("0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace00")

func reserveFeePayer(t *testing.T) (*ecdsa.PrivateKey, common.Address) {
	t.Helper()
	k, err := registryFixtureKey(5, true)
	require.NoError(t, err)
	key, err := crypto.ToECDSA(k.Bytes())
	require.NoError(t, err)
	return key, crypto.PubkeyToAddress(key.PublicKey)
}

// Reassign existing ERC20 balances in the disposable genesis. No native mint,
// supply increase, or post-genesis backdoor is used to fund the gasless actor.
func seedDRC20FeePayer(t *testing.T, state ownedapp.GenesisState, actor common.Address) {
	t.Helper()
	cdc := ownedapp.MakeEncodingConfig(990001).Codec
	var coins cointypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(state[cointypes.ModuleName], &coins))
	require.Len(t, coins.Coins, 1)
	var evm evmtypes.GenesisState
	// The EVM genesis contains protobuf enums (e.g. AccessType); standard
	// encoding/json cannot read their canonical string representation.
	require.NoError(t, cdc.UnmarshalJSON(state[evmtypes.ModuleName], &evm))
	_, payer := reserveFeePayer(t)
	token := common.HexToAddress(coins.Coins[0].DRC20Contract)
	fromSlot := crypto.Keccak256Hash(common.BytesToHash(actor.Bytes()).Bytes(), feeBalanceMapSlot[:])
	toSlot := crypto.Keccak256Hash(common.BytesToHash(payer.Bytes()).Bytes(), feeBalanceMapSlot[:])
	amount := sdkmath.NewIntWithDecimal(3, 18).BigInt()
	found := false
	for i := range evm.Accounts {
		if common.HexToAddress(evm.Accounts[i].Address) != token {
			continue
		}
		for j := range evm.Accounts[i].Storage {
			s := &evm.Accounts[i].Storage[j]
			if common.HexToHash(s.Key) == fromSlot {
				n := common.HexToHash(s.Value).Big()
				require.GreaterOrEqual(t, n.Cmp(amount), 0)
				s.Value = common.BigToHash(new(big.Int).Sub(n, amount)).Hex()
				found = true
			}
			require.NotEqual(t, toSlot, common.HexToHash(s.Key), "gasless actor is fresh")
		}
		evm.Accounts[i].Storage = append(evm.Accounts[i].Storage, evmtypes.State{Key: toSlot.Hex(), Value: common.BigToHash(amount).Hex()})
	}
	require.True(t, found)
	var err error
	state[evmtypes.ModuleName], err = cdc.MarshalJSON(&evm)
	require.NoError(t, err)
}

type drc20FeeCheckpoint struct {
	Payer         common.Address
	FirstEnvelope []byte
	Nonce         string
}

func assertDRC20FeeRestart(t *testing.T, l nativeHistoricalLifecycle, dir string) {
	t.Helper()
	var checkpoint drc20FeeCheckpoint
	readNativeHistoricalJSON(t, filepath.Join(dir, "drc20-fees.json"), &checkpoint)
	n, err := l.app.DRC20FeeNonce(l.snapshot(), checkpoint.Payer)
	require.NoError(t, err)
	require.Equal(t, checkpoint.Nonce, n.String())
	before := l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(checkpoint.Payer.Bytes()), identity.BaseDenom)
	r, err := l.app.CheckTx(&abci.RequestCheckTx{Tx: checkpoint.FirstEnvelope})
	require.NoError(t, err)
	require.NotZero(t, r.Code)
	require.Equal(t, before, l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(checkpoint.Payer.Bytes()), identity.BaseDenom))
	n, err = l.app.DRC20FeeNonce(l.snapshot(), checkpoint.Payer)
	require.NoError(t, err)
	require.Equal(t, checkpoint.Nonce, n.String())
}

func assertNativeDRC20FeePayments(t *testing.T, l nativeHistoricalLifecycle, free common.Address, dir string) {
	t.Helper()
	key, payer := reserveFeePayer(t)
	account := sdk.AccAddress(payer.Bytes())
	var state ownedapp.GenesisState
	require.NoError(t, json.Unmarshal(l.doc.AppState, &state))
	var coins cointypes.GenesisState
	require.NoError(t, l.app.AppCodec().UnmarshalJSON(state[cointypes.ModuleName], &coins))
	token := common.HexToAddress(coins.Coins[0].DRC20Contract)
	unit := func(n int64) *big.Int { return sdkmath.NewIntWithDecimal(n, 18).BigInt() }
	freeABI, err := contracts.CompiledABI("DRC20Reserveless")
	require.NoError(t, err)
	// Give the new account two existing reserve-free units for its actual user
	// operations, using an ordinary native-funded signed transfer from owner.
	data, err := freeABI.Pack("transfer", payer, big.NewInt(2))
	require.NoError(t, err)
	ownerTx := l.signedEthereumCall(free, sdkmath.ZeroInt(), data)
	block, response := l.finalizeSignedEthereum(ownerTx)
	require.Empty(t, response.VmError)
	l.commit(block)
	require.True(t, l.app.BankKeeper.GetBalance(l.snapshot(), account, identity.BaseDenom).IsZero())
	price := big.NewInt(1_000_000_000)
	build := func(method string, args []interface{}, change func(*authorization.Authorization), badSignature bool, gas uint64) []byte {
		t.Helper()
		payload, err := freeABI.Pack(method, args...)
		require.NoError(t, err)
		raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: l.app.EVMKeeper.GetNonce(l.snapshot(), payer), To: &free, Value: new(big.Int), Gas: gas, GasPrice: price, Data: payload})
		signed, err := ethtypes.SignTx(raw, ethtypes.NewEIP155Signer(big.NewInt(990001)), key)
		require.NoError(t, err)
		msg := &evmtypes.MsgEthereumTx{}
		require.NoError(t, msg.FromSignedEthereumTx(signed, ethtypes.NewEIP155Signer(big.NewInt(990001))))
		inner, err := msg.BuildTx(l.app.TxConfig().NewTxBuilder(), identity.BaseDenom)
		require.NoError(t, err)
		encoded, err := l.app.TxConfig().TxEncoder()(inner)
		require.NoError(t, err)
		policy, err := l.app.DRC20FeePolicy(signed, 5_000_000)
		require.NoError(t, err)
		nonce, err := l.app.DRC20FeeNonce(l.snapshot(), payer)
		require.NoError(t, err)
		budget := new(big.Int).Add(new(big.Int).Mul(new(big.Int).SetUint64(gas), price), policy.ConversionNativeFee)
		a := authorization.Authorization{TxHash: signed.Hash(), Payer: payer, Token: token, ExactInput: unit(1), MinimumNativeOutput: budget, NativeFeeBudget: budget, ConversionGasLimit: 5_000_000, Deadline: uint64(l.snapshot().BlockTime().Unix() + 300), FeeNonce: nonce}
		if change != nil {
			change(&a)
		}
		digest, err := authorization.SigningDigest(policy, a)
		require.NoError(t, err)
		sig, err := crypto.Sign(digest[:], key)
		require.NoError(t, err)
		if badSignature {
			sig[0] ^= 1
		}
		outer, err := envelope.Marshal(encoded, a, sig)
		require.NoError(t, err)
		decoded, err := l.app.TxConfig().TxDecoder()(outer)
		require.NoError(t, err, "application client codec must retain composite fee transport")
		_, ok := decoded.(*envelope.Tx)
		require.True(t, ok)
		roundTrip, err := l.app.TxConfig().TxEncoder()(decoded)
		require.NoError(t, err)
		require.Equal(t, outer, roundTrip)
		return outer
	}
	// Invalid ante input must neither spend tokens nor persist a fee nonce,
	// including failure of ordinary Ethereum ante AFTER conversion succeeds.
	for _, name := range []string{"bad-signature", "wrong-fee-nonce", "expired", "slippage", "minimum-supply", "bad-inner-gas"} {
		t.Run("drc20-fee-reject/"+name, func(t *testing.T) {
			var change func(*authorization.Authorization)
			gas := uint64(1_000_000)
			switch name {
			case "wrong-fee-nonce":
				change = func(a *authorization.Authorization) { a.FeeNonce.Add(a.FeeNonce, big.NewInt(1)) }
			case "expired":
				change = func(a *authorization.Authorization) { a.Deadline = uint64(l.snapshot().BlockTime().Unix() - 1) }
			case "slippage":
				change = func(a *authorization.Authorization) { a.MinimumNativeOutput = unit(2) }
			case "minimum-supply":
				change = func(a *authorization.Authorization) { a.ExactInput = unit(6) }
			case "bad-inner-gas":
				gas = 100
			}
			outer := build("transfer", []interface{}{l.expect.Actor, big.NewInt(1)}, change, name == "bad-signature", gas)
			before := l.app.BankKeeper.GetBalance(l.snapshot(), account, identity.BaseDenom)
			reserve := l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(token.Bytes()), identity.BaseDenom)
			nativeSupply := l.supply()
			ethNonce := l.app.EVMKeeper.GetNonce(l.snapshot(), payer)
			feeNonce, err := l.app.DRC20FeeNonce(l.snapshot(), payer)
			require.NoError(t, err)
			block := l.finalize([][]byte{outer}, nil)
			require.Len(t, block.TxResults, 1)
			require.NotZero(t, block.TxResults[0].Code, block.TxResults[0].Log)
			l.commit(block)
			require.Equal(t, before, l.app.BankKeeper.GetBalance(l.snapshot(), account, identity.BaseDenom))
			require.Equal(t, reserve, l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(token.Bytes()), identity.BaseDenom))
			require.Equal(t, nativeSupply, l.supply())
			require.Equal(t, ethNonce, l.app.EVMKeeper.GetNonce(l.snapshot(), payer))
			n, err := l.app.DRC20FeeNonce(l.snapshot(), payer)
			require.NoError(t, err)
			require.Equal(t, feeNonce.String(), n.String())
		})
	}
	var first []byte
	for i := 0; i < 3; i++ {
		amount := int64(1)
		if i == 1 {
			amount = 3
		}
		outer := build("transfer", []interface{}{l.expect.Actor, big.NewInt(amount)}, nil, false, 1_000_000)
		if i == 0 {
			first = append([]byte(nil), outer...)
			check, err := l.app.CheckTx(&abci.RequestCheckTx{Tx: outer})
			require.NoError(t, err)
			require.Zero(t, check.Code, check.Log)
			n, err := l.app.DRC20FeeNonce(l.snapshot(), payer)
			require.NoError(t, err)
			require.Zero(t, n.Sign())
			require.True(t, l.app.BankKeeper.GetBalance(l.snapshot(), account, identity.BaseDenom).IsZero(), "CheckTx cannot persist conversion")
		}
		before := l.app.BankKeeper.GetBalance(l.snapshot(), account, identity.BaseDenom).Amount
		beforeSupply := l.supply()
		reserve := l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(token.Bytes()), identity.BaseDenom).Amount
		balanceKey := crypto.Keccak256Hash(common.BytesToHash(payer.Bytes()).Bytes(), feeBalanceMapSlot[:])
		beforeFree := l.app.EVMKeeper.GetState(l.snapshot(), free, balanceKey).Big()
		block := l.finalize([][]byte{outer}, nil)
		require.Len(t, block.TxResults, 1)
		r := block.TxResults[0]
		require.Zero(t, r.Code, r.Log)
		var messages sdk.TxMsgData
		require.NoError(t, l.app.AppCodec().Unmarshal(r.Data, &messages))
		require.Len(t, messages.MsgResponses, 1)
		var response evmtypes.MsgEthereumTxResponse
		require.NoError(t, response.Unmarshal(messages.MsgResponses[0].Value))
		if i == 1 {
			require.NotEmpty(t, response.VmError)
			require.Empty(t, response.Logs)
		} else {
			require.Empty(t, response.VmError)
		}
		var conversionFee, conversionGas *big.Int
		for _, event := range r.Events {
			if event.Type != "mintcandy_drc20_fee" {
				continue
			}
			for _, a := range event.Attributes {
				if a.Key == "conversion_fee" {
					conversionFee, _ = new(big.Int).SetString(a.Value, 10)
				}
				if a.Key == "conversion_gas" {
					conversionGas, _ = new(big.Int).SetString(a.Value, 10)
				}
			}
		}
		require.NotNil(t, conversionFee)
		require.NotNil(t, conversionGas)
		require.Positive(t, conversionFee.Sign())
		require.Greater(t, r.GasUsed, int64(response.GasUsed))
		require.Equal(t, new(big.Int).SetInt64(r.GasUsed).String(), new(big.Int).Add(new(big.Int).SetUint64(response.GasUsed), conversionGas).String())
		l.commit(block)
		fee := new(big.Int).Add(new(big.Int).Mul(new(big.Int).SetUint64(response.GasUsed), price), conversionFee)
		want := before.Add(sdkmath.NewIntFromBigInt(unit(1))).Sub(sdkmath.NewIntFromBigInt(fee))
		require.Equal(t, want.String(), l.app.BankKeeper.GetBalance(l.snapshot(), account, identity.BaseDenom).Amount.String(), "unused gas/sale surplus remain native DEL")
		require.Equal(t, reserve.Sub(sdkmath.NewIntFromBigInt(unit(1))).String(), l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(token.Bytes()), identity.BaseDenom).Amount.String())
		require.Equal(t, beforeSupply, l.supply())
		n, err := l.app.DRC20FeeNonce(l.snapshot(), payer)
		require.NoError(t, err)
		require.Equal(t, int64(i+1), n.Int64())
		afterFree := l.app.EVMKeeper.GetState(l.snapshot(), free, balanceKey).Big()
		if i == 1 {
			require.Equal(t, beforeFree.String(), afterFree.String())
		} else {
			require.Equal(t, new(big.Int).Sub(beforeFree, big.NewInt(1)).String(), afterFree.String())
		}
	}
	encoded, err := json.Marshal(drc20FeeCheckpoint{payer, first, "3"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "drc20-fees.json"), encoded, 0600))
}
