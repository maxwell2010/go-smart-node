package types_test

import (
	"bytes"
	sdkmath "cosmossdk.io/math"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func completeStoredCoin(t *testing.T) types.Coin {
	t.Helper()
	creator, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(bytes.Repeat([]byte{1}, 20))
	require.NoError(t, err)
	return types.Coin{Denom: "sco", Title: "Some coin", Creator: creator, CRR: 50,
		Volume: sdkmath.NewInt(1000), Reserve: sdkmath.NewInt(100), LimitVolume: sdkmath.NewInt(10000), MinVolume: sdkmath.ZeroInt()}
}

func completeCoinGenesis(t *testing.T) *types.GenesisState {
	t.Helper()
	coin := completeStoredCoin(t)
	params := types.DefaultParams()
	params.BaseVolume = coin.Reserve
	return types.NewGenesisState(params, []types.Coin{coin}, nil)
}

func TestGenesisState_Validate(t *testing.T) {
	for _, tc := range []struct {
		description  string
		genesisState *types.GenesisState
		valid        bool
	}{
		{
			description:  "default is valid",
			genesisState: types.DefaultGenesisState(),
			valid:        true,
		},
		{
			description:  "valid genesis state",
			genesisState: completeCoinGenesis(t),
			valid:        true,
		},
	} {
		t.Run(tc.description, func(t *testing.T) {
			err := tc.genesisState.Validate()
			if tc.valid {
				require.NoError(t, err, tc.description)
			} else {
				require.Error(t, err, tc.description)
			}
		})
	}
}
