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
	"os/exec"
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
	var expected struct {
		Stores map[string]string
		Tokens map[string]string
	}
	if file := os.Getenv("MINTCANDY_AUDIT_REOPEN"); file != "" {
		encoded, err := os.ReadFile(file)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(encoded, &expected))
		require.Equal(t, []byte{1}, ctx.KVStore(vk).Get(symbolKey("")))
		require.Equal(t, expected.Tokens, index(ctx))
		require.Equal(t, expected.Stores, state(a, ctx))
		require.NoError(t, a.Close())
		return
	}
	require.Empty(t, index(ctx))
	reg := customCollateralRegistry{evm: a.EVMKeeper, store: vk}
	require.False(t, ctx.KVStore(vk).Has(symbolKey("")))
	before := state(a, ctx)
	cache, _ := ctx.CacheContext()
	require.NoError(t, reg.indexIssuanceSymbols(cache))
	require.False(t, ctx.KVStore(vk).Has(symbolKey("")))
	cache, write := ctx.CacheContext()
	require.NoError(t, reg.indexIssuanceSymbols(cache))
	write()
	tokens := index(ctx)
	require.Contains(t, tokens, "test")
	require.Contains(t, tokens, "cusdt")
	require.Equal(t, before, state(a, ctx))
	require.NoError(t, reg.indexIssuanceSymbols(ctx))
	require.Equal(t, tokens, index(ctx))
	post := a.CommitMultiStore().Commit()
	require.Equal(t, pre.Version+1, post.Version)
	require.NoError(t, a.Close())
	// A fresh process exercises the actual on-disk reopen without sharing
	// the process-global EVM chain configuration with the first instance.
	expected.Stores, expected.Tokens = before, tokens
	check := func(name string, value any) string {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, encoded, 0600))
		return path
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	child := exec.Command(executable, "-test.run=^TestOfflineMainnetTickerIndex$", "-test.v")
	child.Env = append(os.Environ(), "MINTCANDY_AUDIT_DB="+dst,
		"MINTCANDY_AUDIT_CHECKPOINTS="+check("post.json", map[int64]string{post.Version: hex.EncodeToString(post.Hash)}),
		"MINTCANDY_AUDIT_REOPEN="+check("expected.json", expected))
	childOut, err := child.CombinedOutput()
	require.NoError(t, err, "%s", childOut)
	result := map[string]interface{}{"height": pre.Version, "inputAppHash": fmt.Sprintf("%X", pre.Hash), "offlineMigratedAppHash": fmt.Sprintf("%X", post.Hash), "tickers": tokens, "unchangedStoresOutsideIndex": len(before)}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	_, err = io.WriteString(os.Stdout, "TICKER_COPY_AUDIT "+string(encoded)+"\n")
	require.NoError(t, err)
}
