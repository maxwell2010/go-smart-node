package keeper

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestUserNFTWalletBurnAndTopup(t *testing.T) {
	ctx, k, genesis, owners := externalFixture(t)
	require.NoError(t, k.InitGenesis(ctx, genesis))
	owner, err := account(owners[0])
	require.NoError(t, err)
	stranger, err := account(owners[1])
	require.NoError(t, err)
	vault := sdk.AccAddress(common.HexToAddress("0x1234").Bytes())
	for i, free := range []bool{false, true} {
		target := common.BytesToAddress([]byte{byte(i+10)})
		col := types.Collection{Creator: owners[0], Denom: []string{"funded", "free"}[i], TypeNft: types.NftType_NFT1155, AddressDRC: target.Hex(), ReserveLess: free, ExternalName: "NFT", ExternalSymbol: "NFT"}
		require.NoError(t, k.AdmitUserCollection(ctx, col))
		id := common.HexToHash("0x15")
		native, err := types.FreshExternalTokenID(bindingChain, 777, target[:], id[:])
		require.NoError(t, err)
		reserve := sdk.NewInt64Coin(identity.BaseDenom, 4)
		if free {
			reserve = sdk.NewInt64Coin(identity.BaseDenom, 0)
		}
		require.NoError(t, k.AdmitUserMint(ctx, target, id, "ipfs://asset", owners[0], 2, reserve))
		next := sdk.NewInt64Coin(identity.BaseDenom, 6)
		if free {
			_, err = k.BurnReservelessUnits(ctx, native, stranger, 1)
			require.Error(t, err)
			require.Error(t, k.IncreaseUserNFTReserve(ctx, native, reserve, next, vault, vault))
			ids, err := k.BurnReservelessUnits(ctx, native, owner, 2)
			require.NoError(t, err)
			require.Equal(t, []uint32{1, 2}, ids)
			_, err = k.BurnReservelessUnits(ctx, native, owner, 1)
			require.Error(t, err)
			count, err := k.GetActiveSubTokenCount(ctx, native, owners[0])
			require.NoError(t, err)
			require.Zero(t, count)
		} else {
			_, err = k.BurnReservelessUnits(ctx, native, owner, 1)
			require.Error(t, err)
			require.Error(t, k.IncreaseUserNFTReserve(ctx, native, reserve, next, owner, vault))
			require.NoError(t, k.IncreaseUserNFTReserve(ctx, native, reserve, next, vault, vault))
			require.Error(t, k.IncreaseUserNFTReserve(ctx, native, reserve, next, vault, vault))
			tok, err := k.GetToken(ctx, native)
			require.NoError(t, err)
			require.Equal(t, next, tok.Reserve)
			require.Equal(t, uint32(2), tok.Minted-tok.Burnt)
		}
	}
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
}
