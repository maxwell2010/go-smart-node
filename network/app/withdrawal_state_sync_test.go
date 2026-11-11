package app

import (
	"bytes"
	"encoding/json"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store/snapshots"
	snapshottypes "cosmossdk.io/store/snapshots/types"
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/ethereum/go-ethereum/common"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func newWithdrawalSnapshotTestApp(t *testing.T) *App {
	t.Helper()
	snapshotStore, err := snapshots.NewStore(dbm.NewMemDB(), t.TempDir())
	require.NoError(t, err)
	base := baseapp.NewBaseApp(t.Name(), log.NewNopLogger(), dbm.NewMemDB(), nil,
		baseapp.SetSnapshot(snapshotStore, snapshottypes.NewSnapshotOptions(0, 2)))
	keys := map[string]*storetypes.KVStoreKey{
		validatortypes.StoreKey: storetypes.NewKVStoreKey(validatortypes.StoreKey),
	}
	base.MountKVStores(keys)
	require.NoError(t, base.LoadLatestVersion())
	t.Cleanup(func() { require.NoError(t, base.Close()) })
	return &App{BaseApp: base, keys: keys}
}

func TestWithdrawalProfileStateSync(t *testing.T) {
	profile := WithdrawalProfile{
		Registry: common.HexToAddress("0x01"), Gateway: common.HexToAddress("0x02"),
		Library: common.HexToAddress("0x03"), NFTCenter: common.HexToAddress("0x04"),
		PositionVault: common.HexToAddress("0x05"), MasterValidator: common.HexToAddress("0x06"),
		WrappedDEL: common.HexToAddress("0x07"), SettlementOnly: true, EnableDELDelegation: true,
	}
	canonical, err := json.Marshal(profile)
	require.NoError(t, err)
	different := profile
	different.EnableDELDelegation = false
	mismatch, err := json.Marshal(different)
	require.NoError(t, err)

	for _, tc := range []struct {
		name        string
		prepared    bool
		installed   *WithdrawalProfile
		restored    []byte
		badHash     bool
		missingHash bool
		accept      bool
	}{
		{name: "financial genesis profile", prepared: true, installed: &profile, restored: canonical, accept: true},
		{name: "changed profile", prepared: true, installed: &profile, restored: mismatch},
		{name: "missing profile", prepared: true, installed: &profile},
		{name: "different encoding", prepared: true, installed: &profile, restored: append(bytes.Clone(canonical), '\n')},
		{name: "bad app hash", prepared: true, installed: &profile, restored: canonical, badHash: true},
		{name: "missing app hash", prepared: true, installed: &profile, restored: canonical, missingHash: true},
		{name: "ordinary genesis", prepared: true, accept: true},
		{name: "unexpected financial profile with ordinary genesis", prepared: true, restored: canonical},
		{name: "ordinary without preparation", accept: true},
		{name: "financial without preparation", restored: canonical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := newWithdrawalSnapshotTestApp(t)
			sourceStore := source.CommitMultiStore().GetKVStore(source.keys[validatortypes.StoreKey])
			sourceStore.Set([]byte("checkpoint"), []byte("present"))
			if tc.restored != nil {
				sourceStore.Set([]byte{WithdrawalProfileKey}, tc.restored)
			}
			commit := source.CommitMultiStore().Commit()
			_, err := source.SnapshotManager().Create(uint64(commit.Version))
			require.NoError(t, err)
			list, err := source.ListSnapshots(&abci.RequestListSnapshots{})
			require.NoError(t, err)
			require.Len(t, list.Snapshots, 1)
			snapshot := list.Snapshots[0]

			target := newWithdrawalSnapshotTestApp(t)
			target.stateSync.prepared = tc.prepared
			if tc.installed != nil {
				target.withdrawals = &withdrawalController{profile: *tc.installed}
			}
			installed := target.withdrawals
			appHash := bytes.Clone(commit.Hash)
			if tc.badHash {
				appHash[0] ^= 1
			}
			if tc.missingHash || !tc.prepared {
				appHash = nil
			}
			offer, err := target.OfferSnapshot(&abci.RequestOfferSnapshot{Snapshot: snapshot, AppHash: appHash})
			require.NoError(t, err)
			if tc.missingHash {
				require.Equal(t, abci.ResponseOfferSnapshot_ABORT, offer.Result)
				require.Zero(t, target.LastBlockHeight())
				return
			}
			require.Equal(t, abci.ResponseOfferSnapshot_ACCEPT, offer.Result)

			retry, err := target.ApplySnapshotChunk(&abci.RequestApplySnapshotChunk{Index: 0, Chunk: []byte("wrong chunk")})
			require.NoError(t, err)
			require.Equal(t, abci.ResponseApplySnapshotChunk_RETRY, retry.Result)
			require.Equal(t, []uint32{0}, retry.RefetchChunks)
			for index := uint32(0); index < snapshot.Chunks; index++ {
				chunk, err := source.LoadSnapshotChunk(&abci.RequestLoadSnapshotChunk{
					Height: snapshot.Height, Format: snapshot.Format, Chunk: index,
				})
				require.NoError(t, err)
				applied, err := target.ApplySnapshotChunk(&abci.RequestApplySnapshotChunk{Index: index, Chunk: chunk.Chunk})
				require.NoError(t, err)
				if index+1 == snapshot.Chunks && !tc.accept {
					require.Equal(t, abci.ResponseApplySnapshotChunk_ABORT, applied.Result)
				} else {
					require.Equal(t, abci.ResponseApplySnapshotChunk_ACCEPT, applied.Result)
				}
			}
			require.True(t, target.withdrawals == installed, "snapshot must never install or replace hooks")
			if tc.accept {
				require.Equal(t, commit, target.LastCommitID())
			}
		})
	}
}
