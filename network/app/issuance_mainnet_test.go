//go:build mintcandy_mainnet

package app

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"
	"testing"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/ethereum/go-ethereum/common"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestMainnetDRC20IssuanceTariff(t *testing.T) {
	a := testApp(t, identity.MainnetChainID, identity.MainnetEVMChainID)
	ctx, _ := a.NewUncachedContext(false, cmtproto.Header{ChainID: identity.MainnetChainID, Height: 15}).CacheContext()
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize)
	payer := common.HexToAddress("0x0000000000000000000000000000000000011234")
	treasury := common.HexToAddress(issuanceTreasuryMain)
	testTreasury := common.HexToAddress(issuanceTreasuryTest)
	asCoins := func(n *big.Int) sdk.Coins {
		return sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntFromBigInt(n)))
	}
	initial := sdkmath.NewIntWithDecimal(1_000_000, 18)
	a.AccountKeeper.InitGenesis(ctx, *authtypes.NewGenesisState(authtypes.DefaultParams(), authtypes.GenesisAccounts{authtypes.NewBaseAccountWithAddress(sdk.AccAddress(payer.Bytes()))}))
	var bank banktypes.GenesisState
	a.appCodec.MustUnmarshalJSON(a.DefaultGenesis()[banktypes.ModuleName], &bank)
	bank.Balances = []banktypes.Balance{{Address: sdk.AccAddress(payer.Bytes()).String(), Coins: asCoins(initial.BigInt())}}
	a.BankKeeper.InitGenesis(ctx, &bank)
	r := &customCollateralRegistry{store: a.keys[validatortypes.StoreKey], evm: a.EVMKeeper, bank: a.BankKeeper}
	a.withdrawals = &withdrawalController{customCollateral: r}
	store := ctx.KVStore(r.store)
	store.Set([]byte{customCollateralManifestKey}, []byte{customCollateralManifestVersion})
	legacyData := bytes.Repeat([]byte{0x11}, 164)
	gas := ctx.GasMeter().GasConsumed()
	require.Equal(t, legacyData, r.issuanceCalldata(ctx, legacyData))
	require.Equal(t, gas, ctx.GasMeter().GasConsumed())
	token := common.HexToAddress("0xdd21")
	require.NoError(t, r.settleIssuance(ctx, payer, token, "abc", legacyData, new(big.Int)))
	// Two historical members collide; failed activation may not persist even a partial index.
	old1, old2 := common.HexToAddress("0xaa1"), common.HexToAddress("0xaa2")
	setSymbol := func(token common.Address, s string) {
		var word common.Hash
		copy(word[:], s)
		word[31] = byte(2 * len(s))
		r.evm.SetState(ctx, token, customSlotOffset(customERC20Slot, 4), word[:])
	}
	store.Set(backedTokenMembershipKey(old1), []byte{1})
	store.Set(reservelessMembershipKey(old2), []byte{1})
	setSymbol(old1, "old")
	setSymbol(old2, "old")
	require.ErrorContains(t, a.activateDRC20IssuanceTariff(ctx), "collision")
	require.False(t, store.Has(symbolKey("old")))
	_, active := r.issuanceTreasury(ctx)
	require.False(t, active)
	setSymbol(old2, "oldtwo")
	require.NoError(t, a.activateDRC20IssuanceTariff(ctx))
	require.ErrorContains(t, a.activateDRC20IssuanceTariff(ctx), "already initialized")
	require.Equal(t, old1.Bytes(), store.Get(symbolKey("old")))
	require.Equal(t, old2.Bytes(), store.Get(symbolKey("oldtwo")))
	beneficiary, active := r.issuanceTreasury(ctx)
	require.True(t, active)
	require.Equal(t, treasury, beneficiary)
	amounts := []int64{100_000, 10_000, 1_000, 100, 100, 100, 100, 10}
	for i, units := range amounts {
		length := i + 3
		for _, backed := range []bool{false, true} {
			t.Run(fmt.Sprintf("length%d/backed%t", length, backed), func(t *testing.T) {
				child, _ := ctx.CacheContext()
				fee := sdkmath.NewIntWithDecimal(units, 18)
				legacy := new(big.Int)
				if backed {
					legacy = backedSymbolCommission(length)
					require.NoError(t, r.bank.SendCoins(child, payer.Bytes(), (common.Address{}).Bytes(), asCoins(legacy)))
				}
				symbol := strings.Repeat("a", length)
				data := append(bytes.Clone(legacyData), issuanceApproval(fee.BigInt(), treasury)...)
				require.Equal(t, legacyData, r.issuanceCalldata(child, data))
				require.ErrorContains(t, r.settleIssuance(child, payer, token, symbol, issuanceApproval(fee.BigInt(), testTreasury), legacy), "approval")
				require.NoError(t, r.settleIssuance(child, payer, token, symbol, data, legacy))
				balance := func(addr common.Address) sdkmath.Int {
					return r.bank.GetBalance(child, addr.Bytes(), identity.BaseDenom).Amount
				}
				burn := fee.QuoRaw(5)
				require.Equal(t, fee.Sub(burn), balance(treasury))
				require.Equal(t, initial.Sub(fee), balance(payer))
				require.True(t, balance(common.Address{}).IsZero())
				require.True(t, balance(testTreasury).IsZero())
				require.Equal(t, initial.Sub(burn), r.bank.GetSupply(child, identity.BaseDenom).Amount)
				require.ErrorContains(t, r.settleIssuance(child, payer, token, symbol, data, legacy), "already registered")
				require.Equal(t, initial.Sub(fee), balance(payer))
			})
		}
	}
}
