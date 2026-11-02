//go:build experimental_ibc

package app

import (
	dsctest "bitbucket.org/decimalteam/go-smart-node/testutil/dsc"
	"encoding/json"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	transfer "github.com/cosmos/ibc-go/v6/modules/apps/transfer/types"
	"github.com/stretchr/testify/require"
	abci "github.com/tendermint/tendermint/abci/types"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	tmtypes "github.com/tendermint/tendermint/types"
	"os"
	"path/filepath"
	"testing"
)

func TestExperimentalICS20DSCGenesis(t *testing.T) {
	const id = "decimal_900001-1"
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "config"), 0700))
	g := tmtypes.GenesisDoc{ChainID: id, GenesisTime: reliabilityTime, ConsensusParams: tmtypes.DefaultConsensusParams(), AppState: json.RawMessage(`{}`)}
	require.NoError(t, g.SaveAs(filepath.Join(home, "config", "genesis.json")))
	dsc, _ := reliabilityOpen(t, home)
	vals := dsctest.NewValidatorSet(t, 1)
	acc, balance := dsctest.NewAcc(t)
	gs, err := dsctest.GenesisStateWithValSet(dsc.AppCodec(), NewDefaultGenesisState(dsc.AppCodec()), vals, []authtypes.GenesisAccount{acc}, balance)
	require.NoError(t, err)
	require.NotEmpty(t, gs[transfer.ModuleName])
	b, err := json.Marshal(gs)
	require.NoError(t, err)
	dsc.InitChain(abci.RequestInitChain{ChainId: id, ConsensusParams: dsctest.DefaultConsensusParams, AppStateBytes: b})
	dsc.Commit()
	header := tmproto.Header{ChainID: id, Height: 2, Time: reliabilityTime, ProposerAddress: vals.Validators[0].Address}
	dsc.BeginBlock(abci.RequestBeginBlock{Header: header})
	ctx := dsc.BaseApp.NewContext(false, header)
	require.True(t, dsc.TransferKeeper.IsBound(ctx, transfer.PortID))
	require.NotNil(t, dsc.AccountKeeper.GetModuleAccount(ctx, transfer.ModuleName))
	require.NotNil(t, dsc.GetKey(transfer.StoreKey))
	require.True(t, dsc.TransferKeeper.GetParams(ctx).SendEnabled)
	_, ok := dsc.IBCKeeper.Router.GetRoute(transfer.ModuleName)
	require.True(t, ok)
	require.Panics(t, func() { dsc.InitChainer(ctx, abci.RequestInitChain{ChainId: "decimal_75-1"}) })
}

func TestExperimentalICS20RefusesMainnet(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "config"), 0700))
	g := tmtypes.GenesisDoc{ChainID: "decimal_75-1", GenesisTime: reliabilityTime, ConsensusParams: tmtypes.DefaultConsensusParams(), AppState: json.RawMessage(`{}`)}
	require.NoError(t, g.SaveAs(filepath.Join(home, "config", "genesis.json")))
	require.Panics(t, func() { reliabilityOpen(t, home) })
}
