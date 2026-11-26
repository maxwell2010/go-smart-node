//go:build mintcandy_mainnet && mintcandy_coordinated_assets

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

	coreheader "cosmossdk.io/core/header"
	"cosmossdk.io/log"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	srvflags "github.com/cosmos/evm/server/flags"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/cosmos/gogoproto/jsonpb"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
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
	const forkHeight int64 = 62000
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
		require.Equal(t, historicalHeight+2, pre.Version)
		require.Equal(t, os.Getenv("MINTCANDY_AUDIT_POST_HASH"), hex.EncodeToString(pre.Hash))
	}
	header := cmtproto.Header{ChainID: identity.MainnetChainID, Height: pre.Version}
	pin := mainnetDRC20PoolPin()
	code, err := contracts.LiquidityPoolRuntime(pin.Initializer)
	require.NoError(t, err)
	runtimeHash := crypto.Keccak256Hash(code)
	doneKey := append(binary.BigEndian.AppendUint64([]byte{upgradetypes.DoneByte}, 60000), []byte(nativeSDKMigrationPlan)...)
	// Digest every record outside the new index, including all bank and EVM
	// data. Length prefixes distinguish arbitrary binary keys and values.
	state := func(a *App, ctx sdk.Context) map[string]string {
		out := map[string]string{}
		poolAccount := a.AccountKeeper.GetAccount(ctx, sdk.AccAddress(pin.Address.Bytes()))
		num, err := a.AccountKeeper.AccountNumber.Peek(ctx)
		require.NoError(t, err)
		if poolAccount != nil {
			num = poolAccount.GetAccountNumber()
		}
		skip := map[string][][]byte{
			authtypes.StoreKey:      {append([]byte{1}, pin.Address.Bytes()...), {2}, binary.BigEndian.AppendUint64([]byte("accountNumber"), num)},
			evmtypes.StoreKey:       {append(bytes.Clone(evmtypes.KeyPrefixCodeHash), pin.Address.Bytes()...), append(bytes.Clone(evmtypes.KeyPrefixCode), runtimeHash.Bytes()...)},
			validatortypes.StoreKey: {{userNFTRefundActivationKey}, {issuanceActivationKey}, {nativeLiquidityPoolUpgradeKey}, {coordinatedAssetsMarker}},
			upgradetypes.StoreKey:   {doneKey},
		}
		for name, key := range a.keys {
			it := ctx.KVStore(key).Iterator(nil, nil)
			h := sha256.New()
			for ; it.Valid(); it.Next() {
				k := it.Key()
				if name == validatortypes.StoreKey && bytes.HasPrefix(k, []byte{issuanceSymbolPrefix}) {
					continue
				}
				excluded := false
				for _, allowed := range skip[name] {
					if bytes.Equal(k, allowed) {
						excluded = true
						break
					}
				}
				if excluded {
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
		pool, err := a.withdrawals.nativeLiquidityPoolAt(ctx)
		require.NoError(t, err)
		require.NotNil(t, pool)
		require.NoError(t, pool.verifySnapshot(ctx, true))
		require.Equal(t, pin, pool.pin)
		require.Equal(t, []byte{1}, ctx.KVStore(vk).Get([]byte{coordinatedAssetsMarker}))
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
	// First replay the unmodified historical block on the candidate as a control.
	replay()
	require.Equal(t, strings.ToLower(canonicalAppHash), hex.EncodeToString(a.LastCommitID().Hash))
	ctx = a.NewUncachedContext(false, cmtproto.Header{ChainID: identity.MainnetChainID, Height: next.Height, Time: next.Time})
	virtual := func(h int64) sdk.Context {
		return ctx.WithBlockHeight(h).WithExecMode(sdk.ExecModeFinalize).WithHeaderInfo(coreheader.Info{ChainID: identity.MainnetChainID, Height: h, Time: next.Time})
	}
	missing, _ := virtual(forkHeight).CacheContext()
	require.Error(t, a.applyCoordinatedAssets(missing))
	require.False(t, ctx.KVStore(vk).Has([]byte{coordinatedAssetsMarker}))
	// The real H60000 completion is still in the future in this fixture.
	// Model its receipts, without adding a proposal or changing user balances.
	ctx.KVStore(a.keys[upgradetypes.StoreKey]).Set(doneKey, []byte{1})
	require.ErrorContains(t, a.applyCoordinatedAssets(virtual(forkHeight)), "completed height 60000")
	ctx.KVStore(vk).Set([]byte{userNFTRefundActivationKey}, []byte{1})
	require.NoError(t, a.applyCoordinatedAssets(virtual(forkHeight-1)))
	require.False(t, ctx.KVStore(vk).Has([]byte{coordinatedAssetsMarker}))
	require.Error(t, a.applyCoordinatedAssets(virtual(forkHeight+1)))
	occupied, _ := virtual(forkHeight).CacheContext()
	a.EVMKeeper.SetCodeHash(occupied, pin.Address.Bytes(), crypto.Keccak256([]byte("occupied fixture")))
	require.ErrorContains(t, a.applyCoordinatedAssets(occupied), "pool")
	require.False(t, occupied.KVStore(vk).Has([]byte{issuanceActivationKey}))
	require.False(t, occupied.KVStore(vk).Has(symbolKey("")))
	require.False(t, occupied.KVStore(vk).Has([]byte{coordinatedAssetsMarker}))
	require.NoError(t, a.applyCoordinatedAssets(virtual(forkHeight)))
	require.ErrorContains(t, a.applyCoordinatedAssets(virtual(forkHeight)), "already applied")
	require.NoError(t, a.applyCoordinatedAssets(virtual(forkHeight+1)))
	// Persist the isolated experiment as a new IAVL version, without forging
	// a mainnet block or representing its AppHash as a canonical block hash.
	post := a.CommitMultiStore().Commit()
	require.Equal(t, historicalHeight+2, post.Version)
	after := a.NewUncachedContext(false, cmtproto.Header{ChainID: identity.MainnetChainID, Height: post.Version})
	require.Equal(t, []byte{1}, after.KVStore(vk).Get([]byte{coordinatedAssetsMarker}))
	pool, err := a.withdrawals.nativeLiquidityPoolAt(after)
	require.NoError(t, err)
	require.NotNil(t, pool)
	require.Equal(t, pin, pool.pin)
	require.NoError(t, pool.verifySnapshot(after, true))
	require.Equal(t, runtimeHash, a.EVMKeeper.GetCodeHash(after, pin.Address))
	require.Equal(t, uint64(1), a.AccountKeeper.GetAccount(after, sdk.AccAddress(pin.Address.Bytes())).GetSequence())
	cusdtSupply := a.EVMKeeper.GetState(after, pin.CUSDT, customSlotOffset(customERC20Slot, 2)).Big().String()
	require.Equal(t, "5000000000000000000000", cusdtSupply)
	poolBalanceKey := crypto.Keccak256Hash(customAddressWord(pin.Address).Bytes(), customERC20Slot.Bytes())
	for _, token := range []common.Address{pin.CUSDT, pool.wrapper} {
		require.Equal(t, common.Hash{}, a.EVMKeeper.GetState(after, token, poolBalanceKey))
	}
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
	result := map[string]interface{}{"height": pre.Version, "inputAppHash": fmt.Sprintf("%X", pre.Hash), "offlineMigratedAppHash": fmt.Sprintf("%X", post.Hash), "canonicalControlAppHash": canonicalAppHash, "replayedHeight": next.Height, "tickers": tokens, "storesComparedOutsideExpectedChanges": len(controlStores), "virtualForkHeight": forkHeight, "syntheticPriorUpgradeHeight": 60000, "physicalCommitVersion": post.Version, "pool": pin.Address.Hex(), "cusdt": pin.CUSDT.Hex(), "issuer": pin.Initializer.Hex(), "cusdtSupply": cusdtSupply, "poolLP": "0", "custodyCUSDT": "0", "custodyWMSP": "0", "runtimeHash": runtimeHash.Hex(), "approval": "none; coordinated fork method in virtual context on historical state", "restoredBackupCanonicalReplay": true}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	_, err = io.WriteString(os.Stdout, "TICKER_COPY_AUDIT "+string(encoded)+"\n")
	require.NoError(t, err)
}
