package app

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
)

func TestEmptyUserAssetGenesisRetainsSystemVault(t *testing.T) {
	p := WithdrawalProfile{PositionVault: common.HexToAddress("0x1234")}
	g := nfttypes.GenesisState{Collections: []nfttypes.Collection{{AddressDRC: p.PositionVault.Hex(), TypeNft: nfttypes.NftType_NFT721}}, IsolatedVaults: []nfttypes.IsolatedVaultState{{Vault: p.PositionVault.Bytes(), NextPositionID: common.BigToHash(big.NewInt(1)).Bytes()}}}
	origins, err := freshNFTGenesisOrigins(p, &g)
	require.NoError(t, err)
	require.Empty(t, origins)
	g.IsolatedVaults[0].NextPositionID = common.BigToHash(big.NewInt(2)).Bytes()
	_, err = freshNFTGenesisOrigins(p, &g)
	require.ErrorContains(t, err, "advanced cursor")
	g.IsolatedVaults = nil
	_, err = freshNFTGenesisOrigins(p, &g)
	require.Error(t, err)
}

func TestEmptyCustomCollateralGenesisAndManifest(t *testing.T) {
	g := cointypes.GenesisState{}
	require.NoError(t, validateCustomCollateralGenesis(g))
	manifest, err := customCollateralManifest(nil)
	require.NoError(t, err)
	require.Equal(t, []byte{customCollateralManifestVersion}, manifest)
	g.Coins = make([]cointypes.Coin, maxCustomCollateralCoins+1)
	require.Error(t, validateCustomCollateralGenesis(g))
}

func TestBackedCreationUsesPinnedFactoryCommissions(t *testing.T) {
	expected := map[int]string{3: "2500000000000000000000", 4: "250000000000000000000", 5: "25000000000000000000", 6: "2500000000000000000", 7: "250000000000000000", 8: "250000000000000000", 9: "250000000000000000", 10: "250000000000000000"}
	for size, want := range expected {
		require.Equal(t, want, backedSymbolCommission(size).String())
	}
}
