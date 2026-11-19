//go:build appstate

package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type publicBackedMeta struct {
	InitialMint, MinTotalSupply, MaxTotalSupply *big.Int
	Creator                                     common.Address
	Crr                                         uint8
	Identity, Symbol, Name                      string
}

func assertNativeBackedTokenLifecycle(t *testing.T, l nativeHistoricalLifecycle, profile ownedapp.WithdrawalProfile, dir string) {
	t.Helper()
	center, err := contracts.CompiledABI("NewNetworkTokenCenter")
	require.NoError(t, err)
	tokenABI, err := contracts.CompiledABI("DRC20")
	require.NoError(t, err)
	key, err := registryFixtureKey(3, true)
	require.NoError(t, err)
	private, err := crypto.ToECDSA(key.Bytes())
	require.NoError(t, err)
	creator := crypto.PubkeyToAddress(private.PublicKey)
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	send := func(target common.Address, value *big.Int, data []byte, success bool) uint64 {
		t.Helper()
		before := l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(creator.Bytes()), identity.BaseDenom).Amount.BigInt()
		tx := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: l.app.EVMKeeper.GetNonce(l.snapshot(), creator), To: &target, Value: value, Gas: 24_000_000, GasPrice: big.NewInt(1_000_000_000_000), Data: data})
		signed, err := ethtypes.SignTx(tx, ethtypes.NewEIP155Signer(big.NewInt(990001)), private)
		require.NoError(t, err)
		block, result := l.finalizeSignedEthereum(signed)
		if success {
			require.Empty(t, result.VmError)
		} else {
			require.NotEmpty(t, result.VmError)
			require.Empty(t, result.Logs)
		}
		l.commit(block)
		charged := new(big.Int).Mul(new(big.Int).SetUint64(result.GasUsed), big.NewInt(1_000_000_000_000))
		if success {
			charged.Add(charged, value)
		}
		// A fee-reward profile can pay previously committed fees in BeginBlock
		// before this transaction. Account for that independent native credit.
		for _, event := range block.Events {
			if event.Type != "decimal_native_fee_reward" {
				continue
			}
			var recipient, amount string
			for _, attribute := range event.Attributes {
				switch attribute.Key {
				case "recipient":
					recipient = attribute.Value
				case "amount":
					amount = attribute.Value
				}
			}
			if recipient == sdk.AccAddress(creator.Bytes()).String() {
				paid, err := sdk.ParseCoinNormalized(amount)
				require.NoError(t, err)
				require.Equal(t, identity.BaseDenom, paid.Denom)
				before.Add(before, paid.Amount.BigInt())
			}
		}
		after := l.app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(creator.Bytes()), identity.BaseDenom).Amount.BigInt()
		require.Zero(t, new(big.Int).Sub(before, after).Cmp(charged))
		return result.GasUsed
	}
	supply := l.supply().String()
	metadata := publicBackedMeta{InitialMint: new(big.Int).Mul(unit, big.NewInt(10)), MinTotalSupply: new(big.Int).Set(unit), MaxTotalSupply: new(big.Int).Mul(unit, big.NewInt(1000)), Creator: creator, Crr: 50, Identity: nativeAvatarMetadata, Symbol: "publicbx", Name: "Public backed avatar token"}
	payment := new(big.Int).Add(new(big.Int).Mul(unit, big.NewInt(10)), big.NewInt(250_000_000_000_000_000))
	data, err := center.Pack("createToken", metadata)
	require.NoError(t, err)
	send(profile.TokenCenter, payment, data, true)
	coin, err := l.app.NativeCoinKeeper.GetCoin(l.snapshot(), metadata.Symbol)
	require.NoError(t, err)
	token := common.HexToAddress(coin.DRC20Contract)
	require.NotEqual(t, common.Address{}, token)
	require.Equal(t, metadata.Identity, coin.Identity)
	require.Equal(t, "publicbx", coin.Denom)
	require.Equal(t, new(big.Int).Mul(unit, big.NewInt(10)).String(), coin.Reserve.String())
	require.Equal(t, metadata.InitialMint.String(), coin.Volume.String())
	data, err = tokenABI.Pack("transfer", common.HexToAddress(l.expect.Validators[0].EVM), unit)
	require.NoError(t, err)
	send(token, new(big.Int), data, true)
	bad := metadata
	bad.Symbol = "badbackx"
	bad.Identity = strings.Repeat("x", 257)
	data, err = center.Pack("createToken", bad)
	require.NoError(t, err)
	send(profile.TokenCenter, payment, data, false)
	_, err = l.app.NativeCoinKeeper.GetCoin(l.snapshot(), bad.Symbol)
	require.Error(t, err)
	require.Equal(t, supply, l.supply().String(), "backed creation and rejected metadata must preserve native supply")

	// The expected tariff comes from the released contract's documented
	// schedule. Check actual ABCI debits, reserve projection and escrow.
	quarter := new(big.Int).Quo(unit, big.NewInt(4))
	zeroAccount := sdk.AccAddress((common.Address{}).Bytes())
	sinkBalance := func() *big.Int {
		return l.app.BankKeeper.GetBalance(l.snapshot(), zeroAccount, identity.BaseDenom).Amount.BigInt()
	}
	for i, quarters := range []int64{10_000, 1_000, 100, 10, 1, 1, 1, 1} {
		length := i + 3
		meta := metadata
		meta.Symbol = strings.Repeat("q", length)
		commission := new(big.Int).Mul(quarter, big.NewInt(quarters))
		minPayment := new(big.Int).Add(commission, unit)
		data, err := center.Pack("createToken", meta)
		require.NoError(t, err)
		previousSink := sinkBalance()
		if length == 10 {
			send(profile.TokenCenter, new(big.Int).Sub(minPayment, big.NewInt(1)), data, false)
			_, err := l.app.NativeCoinKeeper.GetCoin(l.snapshot(), meta.Symbol)
			require.Error(t, err)
			require.Equal(t, previousSink.String(), sinkBalance().String(), "a rejected payment must leave the tariff balance intact")
		}
		gas := send(profile.TokenCenter, minPayment, data, true)
		stored, err := l.app.NativeCoinKeeper.GetCoin(l.snapshot(), meta.Symbol)
		require.NoError(t, err, "ticker length %d", length)
		require.Equal(t, unit.String(), stored.Reserve.String(), "minimum native reserve at ticker length %d", length)
		account := sdk.AccAddress(common.HexToAddress(stored.DRC20Contract).Bytes())
		require.Equal(t, unit.String(), l.app.BankKeeper.GetBalance(l.snapshot(), account, identity.BaseDenom).Amount.String())
		require.Equal(t, commission.String(), new(big.Int).Sub(sinkBalance(), previousSink).String())
		require.Equal(t, supply, l.supply().String(), "the fee transfer must preserve bank supply")
		t.Logf("ticker tariff length=%d fee_atoms=%s reserve_atoms=%s gas_used=%d", length, commission, stored.Reserve, gas)
	}
	encoded, err := json.Marshal(token)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "public-backed.json"), encoded, 0600))
}

func assertNativeBackedTokenRestart(t *testing.T, l nativeHistoricalLifecycle, dir string) {
	t.Helper()
	var token common.Address
	data, err := os.ReadFile(filepath.Join(dir, "public-backed.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &token))
	coin, err := l.app.NativeCoinKeeper.GetCoinByDRC(l.snapshot(), token.Hex())
	require.NoError(t, err)
	require.Equal(t, nativeAvatarMetadata, coin.Identity)
}
