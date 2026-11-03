package keeper

import (
	"math/big"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	multistore "cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestZeroBackedIsolatedChildSurvivesRealIAVLLevelDBRestart(t *testing.T) {
	_, _, genesis, owners := isolatedLineageFixture(t)
	token := genesis.Collections[1].Tokens[0]
	token.Reserve = sdk.NewCoin("del", sdkmath.ZeroInt())
	position := &genesis.IsolatedPositions[0]
	position.PositionID = common.BigToHash(big.NewInt(9)).Bytes()
	position.ParentPositionID = common.BigToHash(big.NewInt(8)).Bytes()
	position.Reserve.Amount = make([]byte, 32)
	genesis.ExternalBindings[2].TokenID = append([]byte(nil), position.PositionID...)
	genesis.IsolatedVaults[0].NextPositionID = common.BigToHash(big.NewInt(10)).Bytes()
	directory := t.TempDir()
	open := func() (sdk.Context, multistore.CommitMultiStore, dbm.DB, *StateKeeper) {
		database, err := dbm.NewDB("nft-isolated", dbm.GoLevelDBBackend, directory)
		require.NoError(t, err)
		cms := multistore.NewCommitMultiStore(database, log.NewNopLogger(), metrics.NewNoOpMetrics())
		key := storetypes.NewKVStoreKey("nft-isolated")
		cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, database)
		require.NoError(t, cms.LoadLatestVersion())
		keeper, err := NewStateKeeperWithDomain(runtime.NewKVStoreService(key), bindingChain, 777)
		require.NoError(t, err)
		ctx := sdk.NewContext(cms, cmtproto.Header{ChainID: bindingChain, Height: 10, Time: time.Unix(2000, 0).UTC()}, false, log.NewNopLogger())
		return ctx, cms, database, keeper
	}
	ctx, cms, database, k := open()
	require.NoError(t, k.InitGenesis(ctx, genesis))
	require.NoError(t, k.TransferSubTokens(ctx, token.ID, []uint32{1}, owners[1], owners[2]))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	beforeBytes, err := proto.Marshal(before)
	require.NoError(t, err)
	cms.Commit()
	require.NoError(t, database.Close())
	ctx, cms, database, k = open()
	defer func() { require.NoError(t, database.Close()) }()
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	afterBytes, err := proto.Marshal(after)
	require.NoError(t, err)
	require.Equal(t, beforeBytes, afterBytes)
	loaded, err := k.GetToken(ctx, token.ID)
	require.NoError(t, err)
	require.True(t, loaded.IsolatedReceipt)
	require.False(t, loaded.ReserveLess)
	require.True(t, loaded.Reserve.IsZero())
	lineage, err := k.GetIsolatedLineage(ctx, position.Vault, position.PositionID)
	require.NoError(t, err)
	require.Equal(t, position.ParentPositionID, lineage.ParentPositionID)
	require.Equal(t, position.Reserve, lineage.Reserve)
	sub, err := k.GetSubToken(ctx, token.ID, 1)
	require.NoError(t, err)
	require.Equal(t, owners[2], sub.Owner)
	cms.Commit()
}
