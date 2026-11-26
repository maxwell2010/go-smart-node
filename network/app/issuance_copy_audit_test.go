//go:build mintcandy_mainnet

package app

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cosmossdk.io/log"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/ethereum/go-ethereum/common"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

// The input is an offline copy of application.db only. LevelDB is opened on a
// second, disposable copy. No consensus service or transaction signer is used.
func TestOfflineMainnetTickerIndex(t *testing.T) {
	source := os.Getenv("MINTCANDY_AUDIT_DB")
	if source == "" {
		t.Skip("requires a verified offline application.db")
	}
	var checkpoints map[int64]string
	raw, err := os.ReadFile(os.Getenv("MINTCANDY_AUDIT_CHECKPOINTS"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &checkpoints))
	dir := t.TempDir()
	dst := filepath.Join(dir, "application.db")
	require.NoError(t, os.Mkdir(dst, 0700))
	files, err := os.ReadDir(source)
	require.NoError(t, err)
	for _, file := range files {
		require.True(t, file.Type().IsRegular())
		data, err := os.ReadFile(filepath.Join(source, file.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dst, file.Name()), data, 0600))
	}
	open := func() *App {
		resetEVMConfigurationForTest()
		db, err := dbm.NewDB("application", dbm.GoLevelDBBackend, dir)
		require.NoError(t, err)
		return NewApp(log.NewNopLogger(), db, true, options{
			flags.FlagHome: dir, flags.FlagChainID: identity.MainnetChainID, srvflags.EVMChainID: uint64(9905401),
		})
	}
	a := open()
	pre := a.LastCommitID()
	require.NotEmpty(t, checkpoints[pre.Version], "no independent header for %d (%X)", pre.Version, pre.Hash)
	require.Equal(t, strings.ToLower(checkpoints[pre.Version]), hex.EncodeToString(pre.Hash))
	header := cmtproto.Header{ChainID: identity.MainnetChainID, Height: pre.Version}
	// Digest every record outside the new index, including all bank and EVM
	// data. Length prefixes distinguish arbitrary binary keys and values.
	state := func(a *App, ctx sdk.Context) map[string]string {
		out := map[string]string{}
		for name, key := range a.keys {
			it := ctx.KVStore(key).Iterator(nil, nil)
			h := sha256.New()
			for ; it.Valid(); it.Next() {
				k := it.Key()
				if name == validatortypes.StoreKey && len(k) > 0 && k[0] == issuanceSymbolPrefix {
					continue
				}
				for _, b := range [][]byte{k, it.Value()} {
					require.NoError(t, binary.Write(h, binary.BigEndian, uint64(len(b))))
					_, err := h.Write(b)
					require.NoError(t, err)
				}
			}
			require.NoError(t, it.Close())
			out[name] = hex.EncodeToString(h.Sum(nil))
		}
		return out
	}
	ctx := a.NewUncachedContext(false, header)
	vk := a.keys[validatortypes.StoreKey]
	reg := customCollateralRegistry{evm: a.EVMKeeper, store: vk}
	require.False(t, ctx.KVStore(vk).Has(symbolKey("")))
	before := state(a, ctx)
	cache, _ := ctx.CacheContext()
	require.NoError(t, reg.indexIssuanceSymbols(cache))
	require.False(t, ctx.KVStore(vk).Has(symbolKey("")))
	cache, write := ctx.CacheContext()
	require.NoError(t, reg.indexIssuanceSymbols(cache))
	write()
	index := func(ctx sdk.Context) map[string]string {
		it := ctx.KVStore(vk).Iterator([]byte{issuanceSymbolPrefix}, []byte{issuanceSymbolPrefix + 1})
		defer it.Close()
		out := map[string]string{}
		for ; it.Valid(); it.Next() {
			if len(it.Key()) > 1 {
				require.Len(t, it.Value(), 20)
				out[string(it.Key()[1:])] = common.BytesToAddress(it.Value()).Hex()
			}
		}
		return out
	}
	tokens := index(ctx)
	require.Contains(t, tokens, "test")
	require.Contains(t, tokens, "cusdt")
	require.Equal(t, before, state(a, ctx))
	require.NoError(t, reg.indexIssuanceSymbols(ctx))
	require.Equal(t, tokens, index(ctx))
	post := a.CommitMultiStore().Commit()
	require.Equal(t, pre.Version+1, post.Version)
	require.NoError(t, a.Close())
	a = open()
	defer a.Close()
	vk = a.keys[validatortypes.StoreKey]
	ctx = a.NewUncachedContext(false, cmtproto.Header{ChainID: identity.MainnetChainID, Height: post.Version})
	require.Equal(t, post, a.LastCommitID())
	require.Equal(t, []byte{1}, ctx.KVStore(vk).Get(symbolKey("")))
	require.Equal(t, tokens, index(ctx))
	require.Equal(t, before, state(a, ctx))
	result := map[string]interface{}{"height": pre.Version, "inputAppHash": fmt.Sprintf("%X", pre.Hash), "offlineMigratedAppHash": fmt.Sprintf("%X", post.Hash), "tickers": tokens, "unchangedStoresOutsideIndex": len(before)}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	_, err = io.WriteString(os.Stdout, "TICKER_COPY_AUDIT "+string(encoded)+"\n")
	require.NoError(t, err)
}
