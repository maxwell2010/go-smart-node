package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

func testTariffApproval(fee *big.Int) []byte {
	raw := crypto.Keccak256([]byte("MintCandy DRC20 issuance v1"))
	raw = append(raw, common.LeftPadBytes(common.HexToAddress("0x00000000000000000000000000000000Cafe9900").Bytes(), 32)...)
	return append(raw, common.BigToHash(fee).Bytes()...)
}

func TestSignTestnetIssuance(t *testing.T) {
	dir := os.Getenv("MCND_TARIFF_FIXTURE")
	if dir == "" {
		t.Skip("loopback tMSP rehearsal only")
	}
	requirePrivateMainnetFixture(t)
	require.Equal(t, identity.TestnetChainID, identity.ProfileChainID)
	require.True(t, filepath.IsAbs(dir))
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir() && info.Mode().Perm()&0077 == 0)
	factory, err := contracts.CompiledABI("NewNetworkTokenCenter")
	require.NoError(t, err)
	target := common.HexToAddress(os.Getenv("MCND_TARIFF_FACTORY"))
	require.NotEqual(t, common.Address{}, target)
	spec, ok := nativeLaunchSpecForMode("4-empty-sponsored")
	require.True(t, ok)
	atoms := func(n int64) *big.Int { return sdkmath.NewIntWithDecimal(n, 18).BigInt() }
	fees := []int64{100000, 10000, 1000, 100, 100, 100, 100, 10}
	legacyQuarters := []int64{10000, 1000, 100, 10, 1, 1, 1, 1}
	nonces := map[int]uint64{}
	var fixtures []map[string]any
	add := func(label, symbol string, backed, success bool, account int, signedFee int64) {
		key, err := crypto.ToECDSA(nativeLaunchAccountKey(account, spec).Bytes())
		require.NoError(t, err)
		creator := crypto.PubkeyToAddress(key.PublicKey)
		n := nonces[account]
		nonces[account]++
		name := "TOKEN " + strings.ToUpper(label)
		var data []byte
		value := new(big.Int)
		if backed {
			meta := struct {
				InitialMint, MinTotalSupply, MaxTotalSupply *big.Int
				Creator                                     common.Address
				Crr                                         uint8
				Identity, Symbol, Name                      string
			}{atoms(10), atoms(1), atoms(1000), creator, 100, "", symbol, name}
			data, err = factory.Pack("createToken", meta)
			value.Add(atoms(1), new(big.Int).Quo(atoms(legacyQuarters[len(symbol)-3]), big.NewInt(4)))
		} else {
			data, err = factory.Pack("createTokenReserveless", name, symbol, true, true, atoms(10), atoms(1000), "")
		}
		require.NoError(t, err)
		if signedFee >= 0 {
			data = append(data, testTariffApproval(atoms(signedFee))...)
		}
		tx, err := ethtypes.SignTx(ethtypes.NewTx(&ethtypes.LegacyTx{To: &target, Nonce: n, Value: value, Gas: 16_000_000, GasPrice: big.NewInt(2_000_000_000), Data: data}), ethtypes.NewEIP155Signer(big.NewInt(identity.TestnetEVMChainID)), key)
		require.NoError(t, err)
		raw, err := tx.MarshalBinary()
		require.NoError(t, err)
		fixtures = append(fixtures, map[string]any{"label": label, "symbol": symbol, "creator": creator.Hex(), "target": target.Hex(), "nonce": n, "hash": tx.Hash().Hex(), "raw": hexutil.Encode(raw), "backed": backed, "success": success, "fee": atoms(fees[len(symbol)-3]).String()})
	}
	for i, fee := range fees {
		for _, family := range []string{"b", "r"} {
			symbol := family + strings.Repeat("a", i+2)
			add(family+" "+strings.ToUpper(symbol), symbol, family == "b", true, 3, fee)
		}
	}
	add("backed duplicate", "baa", true, false, 3, 100000)
	add("free duplicate", "raa", false, false, 3, 100000)
	add("free on backed", "baa", false, false, 3, 100000)
	add("backed on free", "raa", true, false, 3, 100000)
	add("no approval", "rollback", false, false, 3, -1)
	add("wrong price", "rollback", true, false, 3, 99)
	add("reserved", "tmsp", false, false, 3, 10000)
	add("insufficient", "low", false, false, 5, 100000)
	add("retry", "rollback", true, true, 3, 100)
	add("race backed", "raceaaaaaa", true, true, 3, 10)
	add("race free", "raceaaaaaa", false, true, 5, 10)
	out, err := json.Marshal(fixtures)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "issues.json"), out, 0600))
}
