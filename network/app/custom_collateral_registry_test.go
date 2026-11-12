package app

import (
	"context"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

type reserveBalanceFixture struct {
	bankkeeper.Keeper
	total, locked int64
}

func (f reserveBalanceFixture) GetBalance(_ context.Context, _ sdk.AccAddress, denom string) sdk.Coin {
	return sdk.NewInt64Coin(denom, f.total)
}

func (f reserveBalanceFixture) LockedCoins(_ context.Context, _ sdk.AccAddress) sdk.Coins {
	return sdk.NewCoins(sdk.NewInt64Coin(identity.BaseDenom, f.locked))
}

func TestCustomDRC20ReserveMustBeFullySpendable(t *testing.T) {
	token := common.BytesToAddress([]byte{1})
	r := customCollateralRegistry{bank: reserveBalanceFixture{total: 10, locked: 1}}
	_, err := r.spendableReserve(sdk.Context{}, token)
	require.ErrorContains(t, err, "approved DRC20 DEL reserve must be fully spendable")

	r.bank = reserveBalanceFixture{total: 10}
	amount, err := r.spendableReserve(sdk.Context{}, token)
	require.NoError(t, err)
	require.True(t, sdk.NewInt64Coin(identity.BaseDenom, 10).Amount.Equal(amount))
}
