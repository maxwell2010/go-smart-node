package app

import (
	"math/big"
	"strings"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func TestPublicTokenNameAlphabetAndLimits(t *testing.T) {
	for _, name := range []string{"A", "STAR TOKEN 2", "RESERVE-FREE", strings.Repeat("X", 64)} {
		require.True(t, validPublicTokenName(name, 64), name)
	}
	for _, name := range []string{"", "  ", "Star token", "STAR TОKEN", "ТОКЕН", "A\n", " A", "A  B", "123", "MINTCANDY", "MSP", "DEL", "WMSP", "MINTCANDY WRAPPED MSP", strings.Repeat("X", 65)} {
		require.False(t, validPublicTokenName(name, 64), name)
	}
	require.True(t, reservedPublicTokenSymbol("wmsp"))
}

func checkPublicTokenNamesBackfillLegacyStorage(t *testing.T, app *App) {
	t.Helper()
	ctx, _ := app.NewUncachedContext(false, cmtproto.Header{ChainID: app.chainID, Height: 2}).CacheContext()
	registry := &customCollateralRegistry{evm: app.EVMKeeper, store: app.keys[validatortypes.StoreKey]}
	store := ctx.KVStore(registry.store)
	store.Set([]byte{customCollateralManifestKey}, []byte{customCollateralManifestVersion})
	nameSlot := customSlotOffset(customERC20Slot, 3)
	oldFree := common.HexToAddress("0x1001")
	oldBacked := common.HexToAddress("0x1002")
	store.Set(reservelessMembershipKey(oldFree), []byte{1})
	store.Set(backedTokenMembershipKey(oldBacked), []byte{1})
	short := "  Example   asset  "
	var word common.Hash
	copy(word[:], short)
	word[31] = byte(2 * len(short))
	app.EVMKeeper.SetState(ctx, oldFree, nameSlot, word[:])
	long := "Earlier Token Name Longer Than One Storage Word"
	length := common.BigToHash(big.NewInt(int64(2*len(long) + 1)))
	app.EVMKeeper.SetState(ctx, oldBacked, nameSlot, length[:])
	base := crypto.Keccak256Hash(nameSlot[:])
	for i := 0; i < len(long); i += common.HashLength {
		var part common.Hash
		copy(part[:], long[i:min(i+common.HashLength, len(long))])
		app.EVMKeeper.SetState(ctx, oldBacked, customSlotOffset(base, int64(i/common.HashLength)), part[:])
	}
	child, _ := ctx.CacheContext()
	require.ErrorContains(t, registry.reservePublicTokenName(child, "EXAMPLE ASSET"), "already registered")
	require.False(t, store.Has(publicTokenNameKey("")), "failed attempts must roll back the backfill")
	require.NoError(t, registry.reservePublicTokenName(ctx, "NEW ASSET"))
	require.ErrorContains(t, registry.reservePublicTokenName(ctx, strings.ToUpper(long)), "already registered")
	require.ErrorContains(t, registry.reservePublicTokenName(ctx, "NEW ASSET"), "already registered")
	require.Equal(t, word, app.EVMKeeper.GetState(ctx, oldFree, nameSlot))
	require.Equal(t, length, app.EVMKeeper.GetState(ctx, oldBacked, nameSlot))
}
