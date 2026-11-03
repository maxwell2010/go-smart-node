package app

import (
	"bytes"
	"encoding/json"
	"testing"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	nativevalidatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func TestNativeGenesisAllocationRequiresExplicitBuilder(t *testing.T) {
	cfg := MakeEncodingConfig(990001)
	genesis := DefaultGenesis(cfg.Codec)
	require.NoError(t, ValidateNativeGenesisAllocation(cfg.Codec, genesis))
	owner, err := address.NewBech32Codec("d0").BytesToString(bytes.Repeat([]byte{3}, 20))
	require.NoError(t, err)
	var bank banktypes.GenesisState
	cfg.Codec.MustUnmarshalJSON(genesis["bank"], &bank)
	bank.Balances = []banktypes.Balance{{Address: owner, Coins: sdk.NewCoins(sdk.NewInt64Coin("del", 123))}}
	bank.Supply = nil
	genesis["bank"] = cfg.Codec.MustMarshalJSON(&bank)
	before := bytes.Clone(genesis["coin"])
	require.ErrorContains(t, ValidateNativeGenesisAllocation(cfg.Codec, genesis), "explicitly run")
	require.True(t, bytes.Equal(before, genesis["coin"]), "startup validation must never silently repair allocation")
	require.NoError(t, ReconcileNativeGenesisAllocation(cfg.Codec, genesis))
	require.NoError(t, ValidateNativeGenesisAllocation(cfg.Codec, genesis))
	var coin cointypes.GenesisState
	cfg.Codec.MustUnmarshalJSON(genesis["coin"], &coin)
	require.Equal(t, sdkmath.NewInt(123), coin.Params.BaseVolume)
	bank.Supply = sdk.NewCoins(sdk.NewInt64Coin("del", 124))
	genesis["bank"] = cfg.Codec.MustMarshalJSON(&bank)
	before = bytes.Clone(genesis["coin"])
	require.Error(t, ReconcileNativeGenesisAllocation(cfg.Codec, genesis))
	require.True(t, bytes.Equal(before, genesis["coin"]))
}

func TestNativeValidatorGenesisRequiresExplicitModuleAndDEL(t *testing.T) {
	cfg := MakeEncodingConfig(990001)
	genesis := DefaultGenesis(cfg.Codec)
	before := bytes.Clone(genesis[nativevalidatortypes.ModuleName])
	delete(genesis, nativevalidatortypes.ModuleName)
	require.ErrorContains(t, ValidateNativeGenesisAllocation(cfg.Codec, genesis), "native validator genesis must be explicit")
	var g nativevalidatortypes.GenesisState
	cfg.Codec.MustUnmarshalJSON(before, &g)
	g.Params.BaseDenom = "other"
	genesis[nativevalidatortypes.ModuleName] = cfg.Codec.MustMarshalJSON(&g)
	require.ErrorContains(t, ValidateNativeGenesisAllocation(cfg.Codec, genesis), "native validator base denomination must be del")
	genesis[nativevalidatortypes.ModuleName] = before
	require.NoError(t, ValidateNativeGenesisAllocation(cfg.Codec, genesis))
}

func assertNativeValidatorBindingDomainAdmission(t *testing.T, a *App) {
	t.Helper()
	const chainID = "mintcandy_990001-1"
	expected, ok := a.NativeValidatorKeeper.ConfiguredEVMSourceDomain()
	require.True(t, ok)
	require.Equal(t, nativevalidatortypes.EVMSourceDomain{ChainID: chainID, EVMChainID: 990001}, expected)

	genesis := a.DefaultGenesis()
	require.NoError(t, a.validateNativeBindingGenesis(genesis), "fresh defaults have no exported domain")
	var g nativevalidatortypes.GenesisState
	a.appCodec.MustUnmarshalJSON(genesis[nativevalidatortypes.ModuleName], &g)
	g.BindingDomain = &expected
	genesis[nativevalidatortypes.ModuleName] = a.appCodec.MustMarshalJSON(&g)
	require.NoError(t, a.validateNativeBindingGenesis(genesis))

	ctx := a.NewUncachedContext(false, cmtproto.Header{ChainID: chainID})
	for _, wrong := range []nativevalidatortypes.EVMSourceDomain{
		{ChainID: "mintcandy_990001-2", EVMChainID: 990001},
		{ChainID: chainID, EVMChainID: 990002},
	} {
		g.BindingDomain = &wrong
		genesis[nativevalidatortypes.ModuleName] = a.appCodec.MustMarshalJSON(&g)
		data, err := json.Marshal(genesis)
		require.NoError(t, err)
		_, err = a.InitChainer(ctx, &abci.RequestInitChain{ChainId: chainID, AppStateBytes: data})
		require.ErrorContains(t, err, "binding domain differs")
		_, err = a.NativeCoinKeeper.GetParams(ctx)
		require.Error(t, err, "domain rejection must precede all module writes")
	}

	g.BindingDomain = &expected
	g.BindingRevision = 1
	genesis[nativevalidatortypes.ModuleName] = a.appCodec.MustMarshalJSON(&g)
	require.NoError(t, g.Validate(), "a valid keeper export still requires application bootstrap admission")
	require.ErrorContains(t, ValidateNativeGenesisAllocation(a.appCodec, genesis), "authenticated financial bootstrap")

	child, _ := ctx.CacheContext()
	g.BindingRevision = 0
	require.NoError(t, a.NativeValidatorKeeper.InitGenesis(child, &g))
	actual, err := a.NativeValidatorKeeper.GetEVMSourceDomain(child)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	exported, err := a.NativeValidatorKeeper.ExportGenesis(child)
	require.NoError(t, err)
	require.Equal(t, &expected, exported.BindingDomain)
	_, err = a.NativeValidatorKeeper.GetEVMSourceDomain(ctx)
	require.Error(t, err, "discarded child must not persist the domain")
}
