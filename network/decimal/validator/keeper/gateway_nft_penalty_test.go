package keeper

import (
	"testing"
	"time"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func gatewayNFTPenaltyFixture(t *testing.T) (sdk.Context, store.KVStoreService, *StateKeeper, *nftkeeper.StateKeeper, GatewayNFTPosition, GatewayNFTPosition) {
	t.Helper()
	nftKey, validatorKey := storetypes.NewKVStoreKey("nft-penalty"), storetypes.NewKVStoreKey("validator-penalty")
	ctx := testutil.DefaultContextWithKeys(map[string]*storetypes.KVStoreKey{"nft": nftKey, "validator": validatorKey}, nil, nil).
		WithChainID(bindingTestChain).WithBlockHeight(10).WithBlockTime(time.Unix(2000, 0).UTC())
	service := runtime.NewKVStoreService(validatorKey)
	k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
	require.NoError(t, err)
	nft, err := nftkeeper.NewStateKeeperWithDomain(runtime.NewKVStoreService(nftKey), bindingTestChain, 990001)
	require.NoError(t, err)
	require.NoError(t, k.InitGenesis(ctx, bindingGenesisState(t, false)))
	companion := gatewayDELFixture()
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, companion, companion.Amount))
	companion.NFTBacked = sdkmath.NewInt(5)
	first := gatewayNFTFixture(t, companion, 1, 5, []uint32{1})
	companion.NFTBacked = sdkmath.NewInt(12)
	second := gatewayNFTFixture(t, companion, 2, 7, []uint32{2})
	require.NoError(t, k.ApplyGatewayNFTDelegation(ctx, first))
	require.NoError(t, k.ApplyGatewayNFTDelegation(ctx, second))

	creator := addr(t, identity.Bech32PrefixAccAddr, 9)
	gateway := addr(t, identity.Bech32PrefixAccAddr, 3)
	origin := first.Lineage
	nextPosition := common.BytesToHash([]byte{3})
	nextPosition[5] = 1
	originToken := &nfttypes.Token{Creator: creator, Denom: "origins", ID: origin.OriginNativeTokenID, URI: "ipfs://origin",
		Reserve: sdk.NewInt64Coin(identity.BaseDenom, 7), Minted: 2, Burnt: 2}
	receipts := nfttypes.Collection{Creator: creator, Denom: "positions", Supply: 2, TypeNft: nfttypes.NftType_NFT721,
		AddressDRC: common.BytesToAddress(origin.Vault).Hex()}
	g := nfttypes.DefaultGenesisState()
	g.ExternalBindings = []nfttypes.ExternalNFTBinding{{ChainID: bindingTestChain, EVMChainID: 990001,
		Contract: origin.OriginContract, TokenID: origin.OriginTokenID, NativeTokenID: origin.OriginNativeTokenID,
		NFTType: nfttypes.NftType_NFT1155, NextSubTokenID: 3}}
	g.IsolatedVaults = []nfttypes.IsolatedVaultState{{ChainID: bindingTestChain, EVMChainID: 990001,
		Vault: origin.Vault, NextPositionID: nextPosition.Bytes(), ReceiptCreator: creator, ReceiptDenom: receipts.Denom}}
	for _, p := range []nfttypes.IsolatedPositionLineage{first.Lineage, second.Lineage} {
		reserve := sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntFromBigInt(common.BytesToHash(p.Reserve.Amount).Big()))
		unit := &nfttypes.SubToken{ID: 1, Owner: gateway}
		if p.ReceiptNativeTokenID == first.Lineage.ReceiptNativeTokenID {
			unit.Reserve = &reserve
		}
		receipts.Tokens = append(receipts.Tokens, &nfttypes.Token{Creator: creator, Denom: receipts.Denom, ID: p.ReceiptNativeTokenID,
			URI: "ipfs://receipt", Reserve: reserve, Minted: 1, IsolatedReceipt: true, SubTokens: nfttypes.SubTokens{unit}})
		g.ExternalBindings = append(g.ExternalBindings, nfttypes.ExternalNFTBinding{ChainID: bindingTestChain, EVMChainID: 990001,
			Contract: p.Vault, TokenID: p.PositionID, NativeTokenID: p.ReceiptNativeTokenID, NFTType: nfttypes.NftType_NFT721, NextSubTokenID: 2})
		g.IsolatedPositions = append(g.IsolatedPositions, p)
	}
	g.Collections = []nfttypes.Collection{{Creator: creator, Denom: originToken.Denom, Supply: 1, TypeNft: nfttypes.NftType_NFT1155,
		AddressDRC: common.BytesToAddress(origin.OriginContract).Hex(), Tokens: nfttypes.Tokens{originToken}}, receipts}
	require.NoError(t, nft.InitGenesis(ctx, g))
	first.Companion = second.Companion
	return ctx, service, k, nft, first, second
}

func TestGatewayNFTPenaltyProjectsExactReserveAndPreservesCustody(t *testing.T) {
	ctx, service, k, nft, first, sibling := gatewayNFTPenaltyFixture(t)
	companion := first.Companion
	owner, operator, err := companion.nativePair()
	require.NoError(t, err)
	companion.Amount = companion.Amount.SubRaw(2)
	frozenID := common.Hash{}
	deadline := time.Unix(2010, 0).UTC()
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, companion, frozenID, sdkmath.NewInt(2), deadline))
	first.Companion = companion
	ctx = ctx.WithBlockHeight(11)
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	nftBefore, err := nft.ExportGenesis(ctx)
	require.NoError(t, err)
	events := len(ctx.EventManager().Events())
	penalty := GatewayNFTPenalty{Before: first, AfterCursor: common.BytesToHash([]byte{1}), RemainingReserve: sdkmath.NewInt(2)}
	badCursor := penalty
	badCursor.AfterCursor = common.BytesToHash([]byte{2})
	require.ErrorContains(t, k.ApplyGatewayNFTPenalty(ctx, nft, badCursor), "advance exactly once")
	increase := penalty
	increase.RemainingReserve = sdkmath.NewInt(6)
	require.ErrorContains(t, k.ApplyGatewayNFTPenalty(ctx, nft, increase), "exceeds the position or companion reserve")

	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: bindingRevisionKey}, bindingTestChain, 990001)
	require.NoError(t, err)
	require.ErrorContains(t, broken.ApplyGatewayNFTPenalty(ctx, nft, penalty), "injected write failure")
	afterFailure, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	assertGenesisBytes(t, before, afterFailure)
	nftAfterFailure, err := nft.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, nftBefore, nftAfterFailure)
	require.Len(t, ctx.EventManager().Events(), events)

	require.NoError(t, k.ApplyGatewayNFTPenalty(ctx, nft, penalty))
	require.Error(t, k.ApplyGatewayNFTPenalty(ctx, nft, penalty), "old proof cannot burn twice")
	assertActiveDELReserve(t, ctx, k, operator, 19)
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before.EVMFrozenBindings, after.EVMFrozenBindings)
	require.Equal(t, before.Undelegations, after.Undelegations)
	coin, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, companion.Gateway[:], companion.StakeID[:])
	require.NoError(t, err)
	require.True(t, sdkmath.NewInt(19).Equal(coin.RawAmount))
	require.True(t, sdkmath.NewInt(9).Equal(coin.RawNftBackedAmount))
	require.Equal(t, companion.PenaltyCursor.Bytes(), coin.PenaltyCursor)
	direct, err := k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.True(t, sdkmath.NewInt(10).Equal(direct.Stake.Stake.Amount))

	updatedLineage, err := nft.GetIsolatedLineage(ctx, first.Lineage.Vault, first.Lineage.PositionID)
	require.NoError(t, err)
	first.Lineage.Reserve.Amount = stateWord(2)
	require.True(t, first.Lineage.Equal(&updatedLineage))
	siblingLineage, err := nft.GetIsolatedLineage(ctx, sibling.Lineage.Vault, sibling.Lineage.PositionID)
	require.NoError(t, err)
	require.True(t, sibling.Lineage.Equal(&siblingLineage))
	for _, p := range []GatewayNFTPosition{first, sibling} {
		row, err := k.GetEVMStakeBinding(ctx, gatewayNFTBinding, companion.Gateway[:], p.StakeID[:])
		require.NoError(t, err)
		require.True(t, row.RawAmount.Equal(sdkmath.OneInt()))
		require.Equal(t, []uint32{1}, row.NativeTarget.SubTokenIDs)
		require.Equal(t, p.Lineage.Reserve.Amount, common.BigToHash(row.ReportedReserveAmount.BigInt()).Bytes())
	}

	first.Companion.NFTBacked = sdkmath.NewInt(9)
	zero := GatewayNFTPenalty{Before: first, BeforeCursor: penalty.AfterCursor,
		AfterCursor: common.BytesToHash([]byte{2}), RemainingReserve: sdkmath.ZeroInt()}
	require.NoError(t, k.ApplyGatewayNFTPenalty(ctx, nft, zero))
	row, err := k.GetEVMStakeBinding(ctx, gatewayNFTBinding, companion.Gateway[:], first.StakeID[:])
	require.NoError(t, err)
	require.True(t, row.Active)
	require.Empty(t, row.CoinCompanionStakeID)
	require.True(t, row.ReportedReserveAmount.IsZero())
	token, err := nft.GetToken(ctx, first.Lineage.ReceiptNativeTokenID)
	require.NoError(t, err)
	require.True(t, token.IsolatedReceipt)
	require.False(t, token.ReserveLess)
	require.True(t, token.Reserve.IsZero())
	unit, err := nft.GetSubToken(ctx, token.ID, 1)
	require.NoError(t, err)
	require.Equal(t, addr(t, identity.Bech32PrefixAccAddr, 3), unit.Owner)
	require.NotNil(t, unit.Reserve)
	require.True(t, unit.Reserve.IsZero())

	zero.Before.Lineage.Reserve.Amount = stateWord(0)
	zero.Before.Companion.NFTBacked = sdkmath.NewInt(7)
	zero.BeforeCursor, zero.AfterCursor = zero.AfterCursor, common.BytesToHash([]byte{3})
	require.NoError(t, k.ApplyGatewayNFTPenalty(ctx, nft, zero))
	row, err = k.GetEVMStakeBinding(ctx, gatewayNFTBinding, companion.Gateway[:], first.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, zero.AfterCursor.Bytes(), row.PenaltyCursor)
	assertActiveDELReserve(t, ctx, k, operator, 17)

	// The last logical NFT amount can fall to zero even with no direct stake.
	companion.Amount, companion.NFTBacked = sdkmath.ZeroInt(), sdkmath.NewInt(7)
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, companion, common.BytesToHash([]byte{1}), sdkmath.NewInt(10), deadline))
	sibling.Companion = companion
	require.NoError(t, k.ApplyGatewayNFTPenalty(ctx, nft, GatewayNFTPenalty{Before: sibling,
		AfterCursor: common.BytesToHash([]byte{1}), RemainingReserve: sdkmath.ZeroInt()}))
	_, err = k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, companion.Gateway[:], companion.StakeID[:])
	require.ErrorIs(t, err, ErrNotFound)
	assertActiveDELReserve(t, ctx, k, operator, 0)
	after, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, after.Delegations, 2)
	require.Len(t, after.EVMFrozenBindings, 2)
	require.Len(t, after.Undelegations, 1)
	require.Len(t, after.Undelegations[0].Entries, 2)
	_, err = nft.ExportGenesis(ctx)
	require.NoError(t, err)
}
