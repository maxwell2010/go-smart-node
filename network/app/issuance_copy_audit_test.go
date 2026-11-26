//go:build mintcandy_mainnet && mintcandy_drc20_issuance

package app

import (
	"bytes"
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
	upgradetypes "cosmossdk.io/x/upgrade/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/cosmos/gogoproto/jsonpb"
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
	const historicalHeight int64 = 37995
	const proposedID uint64 = 990990
	const historicalAppHash = "0E4105EE5A66F8062919EB91CA85165DDB91BB9FC06F2C1BD51DAD5EE6962793"
	const canonicalAppHash = "24CC83BBE8443F38E201A9D30868F93D9022C1BC258A1DA6F28ADB31A7006AAD"
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
	if os.Getenv("MINTCANDY_AUDIT_REOPEN") == "" {
		require.Equal(t, historicalHeight, pre.Version)
		require.Equal(t, historicalAppHash, fmt.Sprintf("%X", pre.Hash))
	} else {
		require.Equal(t, historicalHeight+1, pre.Version)
		require.Equal(t, os.Getenv("MINTCANDY_AUDIT_POST_HASH"), hex.EncodeToString(pre.Hash))
	}
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
				if name == validatortypes.StoreKey && (bytes.HasPrefix(k, []byte{issuanceSymbolPrefix}) || bytes.Equal(k, []byte{issuanceActivationKey})) {
					continue
				}
				if name == govtypes.StoreKey && bytes.Equal(k, binary.BigEndian.AppendUint64([]byte{0}, proposedID)) {
					continue
				}
				doneKey := append(binary.BigEndian.AppendUint64([]byte{upgradetypes.DoneByte}, uint64(historicalHeight+1)), []byte(drc20IssuancePlan)...)
				if name == upgradetypes.StoreKey && (bytes.Equal(k, doneKey) || bytes.Equal(k, []byte{upgradetypes.ProtocolVersionByte})) {
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
		require.Equal(t, []byte{1}, ctx.KVStore(vk).Get([]byte{issuanceActivationKey}))
		require.Equal(t, expected.Tokens, index(ctx))
		require.Equal(t, expected.Stores, state(a, ctx))
		require.NoError(t, a.Close())
		return
	}
	require.Empty(t, index(ctx))
	block, err := os.ReadFile("testdata/mainnet-issuance-37995/block-37996.json")
	require.NoError(t, err)
	var next abci.RequestFinalizeBlock
	require.NoError(t, jsonpb.UnmarshalString(string(block), &next))
	require.Equal(t, historicalHeight+1, next.Height)
	replay := func() {
		res, err := a.FinalizeBlock(&next)
		require.NoError(t, err)
		_, err = a.Commit()
		require.NoError(t, err)
		require.Equal(t, next.Height, a.LastBlockHeight())
		require.Equal(t, res.AppHash, a.LastCommitID().Hash)
	}

	controlFile := filepath.Join(dir, "control-stores.json")
	if control := os.Getenv("MINTCANDY_AUDIT_CONTROL_OUTPUT"); control != "" {
		replay()
		require.Equal(t, strings.ToLower(canonicalAppHash), hex.EncodeToString(a.LastCommitID().Hash))
		digests, err := json.Marshal(state(a, a.NewUncachedContext(false, cmtproto.Header{ChainID: identity.MainnetChainID, Height: next.Height})))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(control, digests, 0600))
		require.NoError(t, a.Close())
		return
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	control := exec.Command(executable, "-test.run=^TestOfflineMainnetTickerIndex$", "-test.v")
	control.Env = append(os.Environ(), "MINTCANDY_AUDIT_CONTROL_OUTPUT="+controlFile)
	controlOut, err := control.CombinedOutput()
	require.NoError(t, err, "%s", controlOut)
	var controlStores map[string]string
	data, err := os.ReadFile(controlFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &controlStores))
	require.False(t, ctx.KVStore(vk).Has(symbolKey("")))
	require.False(t, ctx.KVStore(vk).Has([]byte{issuanceActivationKey}))
	exists, err := a.GovKeeper.Proposals.Has(ctx, proposedID)
	require.NoError(t, err)
	require.False(t, exists)
	beforeProtocol := ctx.KVStore(a.keys[upgradetypes.StoreKey]).Get([]byte{upgradetypes.ProtocolVersionByte})
	var protocol uint64
	if len(beforeProtocol) > 0 {
		require.Len(t, beforeProtocol, 8)
		protocol = binary.BigEndian.Uint64(beforeProtocol)
	}
	// Synthesize only the completed vote and scheduled plan in this disposable copy.
	// FinalizeBlock then exercises the regular x/upgrade PreBlock and keeper handler.
	manifest := nativeUpgradeManifest{Name: drc20IssuancePlan, Height: next.Height}
	encodedManifest, err := json.Marshal(manifest)
	require.NoError(t, err)
	proposal := govv1.Proposal{Id: proposedID, Status: govv1.StatusPassed, FinalTallyResult: &govv1.TallyResult{YesCount: "40000000000000000000000", NoCount: "0", NoWithVetoCount: "0", AbstainCount: "0"}, Metadata: nativeUpgradeSchema + ":" + string(encodedManifest)}
	require.NoError(t, a.GovKeeper.Proposals.Set(ctx, proposedID, proposal))
	require.NoError(t, a.scheduleNativeUpgrade(ctx, proposal, manifest))
	plan, err := a.UpgradeKeeper.GetUpgradePlan(ctx)
	require.NoError(t, err)
	require.Equal(t, manifest.Height, plan.Height)
	replay()
	post := a.LastCommitID()
	after := a.NewUncachedContext(false, cmtproto.Header{ChainID: identity.MainnetChainID, Height: next.Height})
	require.Equal(t, []byte{1}, after.KVStore(vk).Get([]byte{issuanceActivationKey}))
	require.Equal(t, []byte{1}, after.KVStore(vk).Get(symbolKey("")))
	done, err := a.UpgradeKeeper.GetDoneHeight(after, drc20IssuancePlan)
	require.NoError(t, err)
	require.Equal(t, next.Height, done)
	require.Equal(t, binary.BigEndian.AppendUint64(nil, protocol+1), after.KVStore(a.keys[upgradetypes.StoreKey]).Get([]byte{upgradetypes.ProtocolVersionByte}))
	got, err := a.GovKeeper.Proposals.Get(after, proposedID)
	require.NoError(t, err)
	require.Equal(t, proposal.Metadata, got.Metadata)
	require.Equal(t, proposal.Status, got.Status)
	tokens := index(after)
	require.Equal(t, map[string]string{"test": "0xdB6E89eB27131c6b0FC226f9A4E241c1A84f7628", "cusdt": "0x16c1e12e5F0f338E9521082Fd671559845406B96", "cdel": "0x92Fe7b1A7558278634154E3972C87c1355A43c27"}, tokens)
	require.Equal(t, controlStores, state(a, after))
	require.NoError(t, a.Close())
	// A fresh process exercises the actual on-disk reopen without sharing
	// the process-global EVM chain configuration with the first instance.
	expected.Stores, expected.Tokens = controlStores, tokens
	check := func(name string, value any) string {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, encoded, 0600))
		return path
	}
	child := exec.Command(executable, "-test.run=^TestOfflineMainnetTickerIndex$", "-test.v")
	child.Env = append(os.Environ(), "MINTCANDY_AUDIT_DB="+dst,
		"MINTCANDY_AUDIT_POST_HASH="+hex.EncodeToString(post.Hash),
		"MINTCANDY_AUDIT_REOPEN="+check("expected.json", expected))
	childOut, err := child.CombinedOutput()
	require.NoError(t, err, "%s", childOut)
	// Restore the pre-upgrade backup into a new process after closing the
	// experimental database, then replay the original canonical block.
	restore := exec.Command(executable, "-test.run=^TestOfflineMainnetTickerIndex$", "-test.v")
	restoredFile := filepath.Join(dir, "restored-stores.json")
	restore.Env = append(os.Environ(), "MINTCANDY_AUDIT_CONTROL_OUTPUT="+restoredFile)
	restoreOut, err := restore.CombinedOutput()
	require.NoError(t, err, "%s", restoreOut)
	restored, err := os.ReadFile(restoredFile)
	require.NoError(t, err)
	require.JSONEq(t, string(data), string(restored))
	result := map[string]interface{}{"height": pre.Version, "inputAppHash": fmt.Sprintf("%X", pre.Hash), "offlineMigratedAppHash": fmt.Sprintf("%X", post.Hash), "canonicalControlAppHash": canonicalAppHash, "replayedHeight": next.Height, "tickers": tokens, "storesComparedOutsideExpectedChanges": len(controlStores), "approval": "synthetic passed vote on offline clone", "scheduledPlan": drc20IssuancePlan, "appliedHeight": done, "restoredBackupCanonicalReplay": true}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	_, err = io.WriteString(os.Stdout, "TICKER_COPY_AUDIT "+string(encoded)+"\n")
	require.NoError(t, err)
}
