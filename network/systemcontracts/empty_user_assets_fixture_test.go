package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
)

// System receipt custody exists even when users have not created any NFT.
// The factory and vault are actual fresh contracts; no user collection has
// been deployed then removed from native metadata to manufacture emptiness.
func addEmptyNFTGenesis(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, genesis map[string]json.RawMessage) {
	t.Helper()
	nft, err := e.app.NativeNFTKeeper.ExportGenesis(e.ctx())
	require.NoError(t, err)
	require.Empty(t, nft.Collections)
	require.Empty(t, nft.ExternalBindings)
	require.Empty(t, nft.IsolatedVaults)
	require.Empty(t, nft.IsolatedPositions)
	domain, ok := e.app.NativeValidatorKeeper.ConfiguredEVMSourceDomain()
	require.True(t, ok)
	creator := sdk.AccAddress(e.govOwner.Bytes()).String()
	nft.Collections = []nfttypes.Collection{{Creator: creator, Denom: signedNFTNativeVaultDenom,
		TypeNft: nfttypes.NftType_NFT721, AddressDRC: profile.PositionVault.Hex()}}
	nft.IsolatedVaults = []nfttypes.IsolatedVaultState{{ChainID: domain.ChainID, EVMChainID: domain.EVMChainID,
		Vault: profile.PositionVault.Bytes(), NextPositionID: common.BigToHash(big.NewInt(1)).Bytes(),
		ReceiptCreator: creator, ReceiptDenom: signedNFTNativeVaultDenom}}
	require.NoError(t, nft.Validate())
	genesis[nfttypes.ModuleName] = e.app.AppCodec().MustMarshalJSON(nft)
}
