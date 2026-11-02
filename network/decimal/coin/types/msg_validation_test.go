package types

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"
)

func TestNativeMessageValidationRejectsMissingAndNegativeAmounts(t *testing.T) {
	sender, err := bech32.ConvertAndEncode("d0", make([]byte, 20))
	require.NoError(t, err)
	recipient, err := bech32.ConvertAndEncode("d0", []byte("other-address-bytes!"))
	require.NoError(t, err)
	positive := sdk.Coin{Denom: "abc", Amount: sdkmath.OneInt()}
	missing := sdk.Coin{Denom: "def"}
	negative := sdk.Coin{Denom: "def", Amount: sdkmath.NewInt(-1)}
	tests := []interface{ ValidateBasic() error }{
		(*MsgCreateCoin)(nil), (*MsgUpdateCoin)(nil), (*MsgSendCoin)(nil), (*MsgMultiSendCoin)(nil),
		(*MsgBuyCoin)(nil), (*MsgSellCoin)(nil), (*MsgSellAllCoin)(nil), (*MsgBurnCoin)(nil), (*MsgRedeemCheck)(nil),
		&MsgCreateCoin{Sender: sender, Denom: "abc", Title: "Coin", CRR: 50},
		&MsgUpdateCoin{Sender: sender, Denom: "abc"},
		&MsgSendCoin{Sender: sender, Recipient: recipient, Coin: missing},
		&MsgMultiSendCoin{Sender: sender, Sends: []MultiSendEntry{{Recipient: recipient, Coin: missing}}},
		&MsgBuyCoin{Sender: sender, CoinToBuy: positive, MaxCoinToSell: missing},
		&MsgSellCoin{Sender: sender, CoinToSell: positive, MinCoinToBuy: missing},
		&MsgSellCoin{Sender: sender, CoinToSell: positive, MinCoinToBuy: negative},
		&MsgSellAllCoin{Sender: sender, CoinDenomToSell: "abc", MinCoinToBuy: missing},
		&MsgBurnCoin{Sender: sender, Coin: missing},
		&MsgRedeemCheck{Sender: sender},
	}
	for _, msg := range tests {
		require.NotPanics(t, func() { require.Error(t, msg.ValidateBasic(), "%T", msg) })
	}
	// Explicit zero minimum output remains valid for a regular sell quotation.
	require.NoError(t, (&MsgSellCoin{Sender: sender, CoinToSell: positive, MinCoinToBuy: sdk.NewCoin("def", sdkmath.ZeroInt())}).ValidateBasic())
	require.NoError(t, (&MsgSendCoin{Sender: sender, Recipient: recipient, Coin: positive}).ValidateBasic(), "native d0 validation must not need a process-global SDK prefix")
	require.Equal(t, TypeMsgBurnCoin, (&MsgBurnCoin{}).Type())
}
