package compat_test

import (
	"bytes"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"cosmossdk.io/x/tx/signing"
	"github.com/cosmos/gogoproto/proto"
	coin "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/compat"
	nft "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestNativeProtoSigningUsesModernAddressCodec(t *testing.T) {
	addresses := address.NewBech32Codec(identity.Bech32PrefixAccAddr)
	registry, err := codectypes.NewInterfaceRegistryWithOptions(codectypes.InterfaceRegistryOptions{
		ProtoFiles: proto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec:          addresses,
			ValidatorAddressCodec: address.NewBech32Codec(identity.Bech32PrefixValAddr),
		},
	})
	require.NoError(t, err)
	coin.RegisterInterfaces(registry)
	nft.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	senderBytes := bytes.Repeat([]byte{0x23}, 20)
	sender, err := addresses.BytesToString(senderBytes)
	require.NoError(t, err)
	amount := compat.EtherToWei(sdkmath.NewInt(1000))
	tests := []struct {
		url string
		msg proto.Message
	}{
		{"/decimal.coin.v1.MsgCreateCoin", &coin.MsgCreateCoin{Sender: sender, Denom: "abc", Title: "Coin", CRR: 50, InitialVolume: amount, InitialReserve: amount, LimitVolume: amount, MinVolume: sdkmath.ZeroInt()}},
		{"/decimal.coin.v1.MsgSendCoin", &coin.MsgSendCoin{Sender: sender, Recipient: sender, Coin: sdk.NewCoin("del", sdkmath.OneInt())}},
		{"/decimal.nft.v1.MsgMintToken", &nft.MsgMintToken{Sender: sender, Recipient: sender, Denom: "collection", TokenID: "token", TokenURI: "metadata", Quantity: 1, Reserve: sdk.NewCoin("del", amount)}},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			wrapped, err := codectypes.NewAnyWithValue(tt.msg)
			require.NoError(t, err)
			require.Equal(t, tt.url, wrapped.TypeUrl, "wire type URLs must not change with the Go module path")
			signers, _, err := cdc.GetMsgAnySigners(wrapped)
			require.NoError(t, err, "modern descriptor-based signing must resolve the native sender")
			require.Equal(t, [][]byte{senderBytes}, signers)
		})
	}
	invalid, err := codectypes.NewAnyWithValue(&coin.MsgSendCoin{Sender: "invalid", Coin: sdk.NewCoin("del", sdkmath.OneInt())})
	require.NoError(t, err)
	_, _, err = cdc.GetMsgAnySigners(invalid)
	require.Error(t, err, "bad sender must return a decoding error rather than invoke a panicking legacy signer")
}
