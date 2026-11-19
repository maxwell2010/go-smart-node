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
	send := func(target common.Address, value *big.Int, data []byte, success bool) []byte {
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
		return result.Ret
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
