package referenceapp

import (
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	log "cosmossdk.io/log"
	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	"github.com/stretchr/testify/require"
)

type fixtureOptions map[string]any

func (o fixtureOptions) Get(key string) any { return o[key] }

// The independent counterparty must charge actual native fees, unlike the
// upstream protocol-only SimApp fixture from which its wiring was derived.
func TestReferenceSignedNativeFee(t *testing.T) {
	const chainID = "ibc-reference-1"
	app := NewApp(log.NewNopLogger(), dbm.NewMemDB(), nil, true, fixtureOptions{flags.FlagHome: t.TempDir()}, baseapp.SetChainID(chainID))
	t.Cleanup(func() { require.NoError(t, app.Close()) })
	key := secp256k1.GenPrivKey()
	account := authtypes.NewBaseAccount(key.PubKey().Address().Bytes(), key.PubKey(), 0, 0)
	validator, _ := cmttypes.RandValidator(false, 1)
	vals := cmttypes.NewValidatorSet([]*cmttypes.Validator{validator})
	const initial int64 = 1000000000
	genesis, err := simtestutil.GenesisStateWithValSet(app.AppCodec(), app.DefaultGenesis(), vals, []authtypes.GenesisAccount{account}, banktypes.Balance{Address: account.GetAddress().String(), Coins: sdk.NewCoins(sdk.NewInt64Coin(sdk.DefaultBondDenom, initial))})
	require.NoError(t, err)
	// Disable fixture inflation so the exact fee-collector delta has no minted subsidy.
	var mintGenesis minttypes.GenesisState
	app.AppCodec().MustUnmarshalJSON(genesis[minttypes.ModuleName], &mintGenesis)
	mintGenesis.Minter.Inflation = math.LegacyZeroDec()
	mintGenesis.Minter.AnnualProvisions = math.LegacyZeroDec()
	mintGenesis.Params.InflationMin = math.LegacyZeroDec()
	mintGenesis.Params.InflationMax = math.LegacyZeroDec()
	genesis[minttypes.ModuleName] = app.AppCodec().MustMarshalJSON(&mintGenesis)
	data, err := json.Marshal(genesis)
	require.NoError(t, err)
	now := time.Unix(1900000000, 0).UTC()
	_, err = app.InitChain(&abci.RequestInitChain{ChainId: chainID, Time: now, AppStateBytes: data, ConsensusParams: simtestutil.DefaultConsensusParams})
	require.NoError(t, err)
	recipient := sdk.AccAddress(secp256k1.GenPrivKey().PubKey().Address())
	amount := sdk.NewInt64Coin(sdk.DefaultBondDenom, 100)
	fee := sdk.NewInt64Coin(sdk.DefaultBondDenom, 1000)
	msg := banktypes.NewMsgSend(account.GetAddress(), recipient, sdk.NewCoins(amount))
	tx, err := simtestutil.GenSignedMockTx(rand.New(rand.NewSource(1)), app.GetTxConfig(), []sdk.Msg{msg}, sdk.NewCoins(fee), 200000, chainID, []uint64{0}, []uint64{0}, key)
	require.NoError(t, err)
	encoded, err := app.GetTxConfig().TxEncoder()(tx)
	require.NoError(t, err)
	block, err := app.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: now, NextValidatorsHash: vals.Hash(), Txs: [][]byte{encoded}})
	require.NoError(t, err)
	require.Len(t, block.TxResults, 1)
	require.Zero(t, block.TxResults[0].Code, block.TxResults[0].Log)
	require.Positive(t, block.TxResults[0].GasUsed)
	ctx := app.GetBaseApp().NewContextLegacy(false, cmtproto.Header{ChainID: chainID, Height: 1, Time: now})
	require.Equal(t, math.NewInt(initial).Sub(amount.Amount).Sub(fee.Amount), app.BankKeeper.GetBalance(ctx, account.GetAddress(), sdk.DefaultBondDenom).Amount)
	require.Equal(t, amount.Amount, app.BankKeeper.GetBalance(ctx, recipient, sdk.DefaultBondDenom).Amount)
	require.Equal(t, fee.Amount, app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(authtypes.FeeCollectorName), sdk.DefaultBondDenom).Amount)
	require.Equal(t, uint64(1), app.AccountKeeper.GetAccount(ctx, account.GetAddress()).GetSequence())
}
