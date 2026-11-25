//go:build mintcandy_testnet

package app

import (
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/ethereum/go-ethereum/common"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestIssuanceHistoricalSymbols(t *testing.T) {
	app := testApp(t, identity.TestnetChainID, identity.TestnetEVMChainID)
	ctx, _ := app.NewUncachedContext(false, cmtproto.Header{ChainID: identity.TestnetChainID, Height: 2}).CacheContext()
	registry := &customCollateralRegistry{evm: app.EVMKeeper, store: app.keys[validatortypes.StoreKey]}
	store := ctx.KVStore(registry.store)
	store.Set([]byte{customCollateralManifestKey}, []byte{customCollateralManifestVersion})
	a, b := common.HexToAddress("0x1111"), common.HexToAddress("0x2222")
	store.Set(backedTokenMembershipKey(a), []byte{1})
	store.Set(reservelessMembershipKey(b), []byte{1})
	symbol := func(addr common.Address, s string) {
		var word common.Hash
		copy(word[:], s)
		word[31] = byte(2 * len(s))
		registry.evm.SetState(ctx, addr, customSlotOffset(customERC20Slot, 4), word[:])
	}
	symbol(a, "abc")
	symbol(b, "abc")
	require.ErrorContains(t, registry.indexIssuanceSymbols(ctx), "historical ticker collision")
	require.False(t, store.Has(symbolKey("")))
	require.False(t, store.Has(symbolKey("abc")))
	symbol(b, "wmsp")
	require.ErrorContains(t, registry.indexIssuanceSymbols(ctx), "reserved historical")
	symbol(b, "xyz")
	require.NoError(t, registry.preflightIssuanceSymbols(ctx))
	require.False(t, store.Has(symbolKey("")))
	require.NoError(t, registry.indexIssuanceSymbols(ctx))
	require.Equal(t, a.Bytes(), store.Get(symbolKey("abc")))
	require.Equal(t, b.Bytes(), store.Get(symbolKey("xyz")))
	require.NoError(t, registry.indexIssuanceSymbols(ctx))
}
