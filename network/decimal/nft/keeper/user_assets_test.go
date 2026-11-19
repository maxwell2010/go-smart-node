package keeper

import (
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUserNFTAdmissionAtomicIndicesAndMintBounds(t *testing.T) {
	ctx, k, genesis, owners := externalFixture(t)
	require.NoError(t, k.InitGenesis(ctx, genesis))
	target := common.HexToAddress("0x9999999999999999999999999999999999999999")
	collection := types.Collection{Creator: owners[0], Denom: "new_user", TypeNft: types.NftType_NFT1155, AddressDRC: target.Hex(), ReserveLess: true, ExternalName: "User NFT", ExternalSymbol: "USER"}
	child, _ := ctx.CacheContext()
	require.NoError(t, k.AdmitUserCollection(child, collection))
	_, err := k.GetCollectionByDRC(ctx, target.Hex())
	require.Error(t, err, "discarded callback must not publish native collection")
	require.NoError(t, k.AdmitUserCollection(ctx, collection))
	require.Error(t, k.AdmitUserCollection(ctx, collection))
	id := common.HexToHash("0x123")
	reserve := sdk.NewCoin("del", sdkmath.ZeroInt())
	require.NoError(t, k.AdmitUserMint(ctx, target, id, "ipfs://user", owners[1], 3, reserve))
	nativeID, err := types.FreshExternalTokenID(bindingChain, 777, target[:], id[:])
	require.NoError(t, err)
	count, err := k.GetActiveSubTokenCount(ctx, nativeID, owners[1])
	require.NoError(t, err)
	require.Equal(t, uint32(3), count)
	require.Error(t, k.AdmitUserMint(ctx, target, id, "ipfs://changed", owners[1], 2, reserve))
	token, err := k.GetToken(ctx, nativeID)
	require.NoError(t, err)
	require.Equal(t, uint32(3), token.Minted)
	require.NoError(t, k.AdmitUserMint(ctx, target, id, "ipfs://user", owners[2], 2, reserve))
	binding, err := k.GetExternalNFTBinding(ctx, nativeID)
	require.NoError(t, err)
	require.Equal(t, uint64(6), binding.NextSubTokenID)
	ids, _, err := k.ListTokenIDsByURI(ctx, "ipfs://user", "", 10)
	require.NoError(t, err)
	require.Contains(t, ids, nativeID)
	require.Error(t, k.AdmitUserMint(ctx, target, id, "ipfs://user", owners[1], 129, reserve))
}
