package ibctesting

import (
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/ibc-go/v11/modules/core/keeper"
	"github.com/stretchr/testify/require"
	"math/rand"
	"testing"
	"time"
)

// TestingApp deliberately has no concrete app or default genesis factory.
type TestingApp interface {
	servertypes.ABCI
	GetBaseApp() *baseapp.BaseApp
	GetIBCKeeper() *keeper.Keeper
	GetTxConfig() client.TxConfig
	AppCodec() codec.Codec
	LastCommitID() storetypes.CommitID
	LastBlockHeight() int64
}

// Derived from ibc-go v11.2.0 testing/simapp/test_helpers.go (Apache-2.0).
// Deliver an encoded, signed native SDK transaction through actual FinalizeBlock.
// The caller's TxConfig supplies its explicit fee before signing.
func signAndDeliver(tb testing.TB, cfg client.TxConfig, app *baseapp.BaseApp, msgs []sdk.Msg, chainID string, nums, seqs []uint64, _ bool, blockTime time.Time, nextVals []byte, keys ...cryptotypes.PrivKey) (*abci.ResponseFinalizeBlock, error) {
	tb.Helper()
	tx, err := simtestutil.GenSignedMockTx(rand.New(rand.NewSource(time.Now().UnixNano())), cfg, msgs, sdk.Coins{}, simtestutil.DefaultGenTxGas, chainID, nums, seqs, keys...)
	require.NoError(tb, err)
	encoded, err := cfg.TxEncoder()(tx)
	require.NoError(tb, err)
	return app.FinalizeBlock(&abci.RequestFinalizeBlock{Height: app.LastBlockHeight() + 1, Time: blockTime, NextValidatorsHash: nextVals, Txs: [][]byte{encoded}})
}
