package compat_test

import (
	"bytes"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/compat"
	nft "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
)

func TestParameterFactoryDoesNotCloneFinancialDefaults(t *testing.T) {
	key := storetypes.NewKVStoreKey("financial-prototype")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient"))
	owner, err := address.NewBech32Codec("d0").BytesToString(bytes.Repeat([]byte{1}, 20))
	require.NoError(t, err)
	prototype := &nft.Token{Creator: owner, Denom: "collection", ID: "token", URI: "ipfs://original", Reserve: sdk.NewCoin("del", sdkmath.NewIntWithDecimal(1, 18)), Minted: 1}
	// A populated nested SDK Coin contains custom math fields that reflective
	// gogoproto cloning cannot safely merge. Only the prototype's type is needed.
	var params compat.ParamsStore[*nft.Token]
	require.NotPanics(t, func() {
		params, err = compat.NewParamsStore(runtime.NewKVStoreService(key), []byte{1}, prototype, func(token *nft.Token) error { return token.Validate() })
	})
	require.NoError(t, err)
	_, err = params.Get(ctx)
	require.ErrorIs(t, err, compat.ErrMissingParams, "prototype values must never initialize storage")
	require.NoError(t, params.Set(ctx, prototype))
	prototype.URI = "mutated prototype"
	prototype.Reserve = sdk.NewCoin("other", sdkmath.OneInt())
	stored, err := params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, "ipfs://original", stored.URI)
	require.Equal(t, "del", stored.Reserve.Denom)
	require.Equal(t, sdkmath.NewIntWithDecimal(1, 18), stored.Reserve.Amount)
	stored.URI = "mutated result"
	again, err := params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, "ipfs://original", again.URI)
}
