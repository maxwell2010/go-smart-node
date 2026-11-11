package keeper

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
)

func gatewayIsolationFixture(t *testing.T) (sdk.Context, *StateKeeper, *types.GenesisState, GatewayIsolation, []string) {
	t.Helper()
	ctx, k, g, owners := isolatedLineageFixture(t)
	g.IsolatedPositions = nil
	g.IsolatedVaults[0].NextPositionID = stateIsolationID(1).Bytes()
	g.Collections[1].Tokens, g.Collections[1].Supply = nil, 0
	g.ExternalBindings = g.ExternalBindings[:2]
	origin := g.Collections[0].Tokens[1]
	origin.Minted, origin.Burnt = 7, 3
	origin.SubTokens = types.SubTokens{
		{ID: 2, Owner: owners[0]}, {ID: 3, Owner: owners[1]},
		{ID: 5, Owner: owners[0]}, {ID: 7, Owner: owners[0]},
	}
	g.ExternalBindings[1].NextSubTokenID = 8
	owner, err := account(owners[0])
	require.NoError(t, err)
	gateway, err := account(owners[2])
	require.NoError(t, err)
	proof := GatewayIsolation{
		Vault: common.BytesToAddress(g.IsolatedVaults[0].Vault), Origin: common.BytesToAddress(g.ExternalBindings[1].Contract),
		Owner: common.BytesToAddress(owner), Gateway: common.BytesToAddress(gateway),
		PositionID: stateIsolationID(1), OriginTokenID: common.BytesToHash(g.ExternalBindings[1].TokenID), NextPositionID: stateIsolationID(2),
		Quantity: 2, OriginSupplyAfter: 2, Reserve: origin.Reserve.Amount.MulRaw(2), OriginReserveAfter: origin.Reserve.Amount.MulRaw(2),
	}
	return ctx, k, g, proof, owners
}

func stateIsolationID(id byte) common.Hash {
	return common.BytesToHash([]byte{id})
}

func TestGatewayIsolationArchivesOnlySelectedOwnerUnits(t *testing.T) {
	ctx, k, g, proof, owners := gatewayIsolationFixture(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	lineage, err := k.AdmitGatewayIsolation(ctx, proof)
	require.NoError(t, err)
	require.Equal(t, []uint32{2, 5}, lineage.OriginSubTokenIDs)
	require.Equal(t, proof.OriginTokenID.Bytes(), lineage.OriginTokenID)
	require.Greater(t, proof.OriginTokenID.Big().BitLen(), 64)
	require.Equal(t, proof.Reserve.BigInt(), common.BytesToHash(lineage.Reserve.Amount).Big())
	read, err := k.GetIsolatedLineage(ctx, proof.Vault[:], proof.PositionID[:])
	require.NoError(t, err)
	require.Equal(t, lineage, read)
	origin, err := k.GetToken(ctx, lineage.OriginNativeTokenID)
	require.NoError(t, err)
	require.EqualValues(t, 7, origin.Minted)
	require.EqualValues(t, 5, origin.Burnt)
	require.Equal(t, g.Collections[0].Tokens[1].Reserve, origin.Reserve)
	binding, err := k.GetExternalNFTBinding(ctx, origin.ID)
	require.NoError(t, err)
	require.EqualValues(t, 8, binding.NextSubTokenID)
	for _, id := range []uint32{2, 5} {
		_, err = k.GetSubToken(ctx, origin.ID, id)
		require.ErrorIs(t, err, ErrNotFound)
	}
	sibling, err := k.GetSubToken(ctx, origin.ID, 3)
	require.NoError(t, err)
	require.Equal(t, owners[1], sibling.Owner)
	remaining, err := k.GetSubToken(ctx, origin.ID, 7)
	require.NoError(t, err)
	require.Equal(t, owners[0], remaining.Owner)
	receipt, err := k.ResolveExternalNFT(ctx, proof.Vault[:], proof.PositionID[:])
	require.NoError(t, err)
	require.Equal(t, lineage.ReceiptNativeTokenID, receipt.NativeTokenID)
	require.Equal(t, types.NftType_NFT721, receipt.NFTType)
	receiptToken, err := k.GetToken(ctx, receipt.NativeTokenID)
	require.NoError(t, err)
	require.Equal(t, origin.URI, receiptToken.URI)
	require.Equal(t, proof.Reserve, receiptToken.Reserve.Amount)
	require.True(t, receiptToken.IsolatedReceipt)
	require.False(t, receiptToken.AllowMint)
	receiptUnit, err := k.GetSubToken(ctx, receipt.NativeTokenID, lineage.ReceiptSubTokenID)
	require.NoError(t, err)
	require.Equal(t, owners[2], receiptUnit.Owner)
	vault, err := k.GetIsolatedVaultState(ctx, proof.Vault[:])
	require.NoError(t, err)
	require.Equal(t, proof.NextPositionID.Bytes(), vault.NextPositionID)
	_, err = k.AdmitGatewayIsolation(ctx, proof)
	require.ErrorContains(t, err, "native vault cursor")
	require.Len(t, ctx.EventManager().Events(), 1)
	require.Equal(t, "decimal_nft_isolated", ctx.EventManager().Events()[0].Type)
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err, "all new NFT indexes and counters must agree")

	nextOwner, err := account(owners[1])
	require.NoError(t, err)
	proof.Owner = common.BytesToAddress(nextOwner)
	proof.PositionID, proof.NextPositionID = proof.NextPositionID, stateIsolationID(3)
	proof.Quantity, proof.OriginSupplyAfter = 1, 1
	proof.Reserve, proof.OriginReserveAfter = origin.Reserve.Amount, origin.Reserve.Amount
	second, err := k.AdmitGatewayIsolation(ctx, proof)
	require.NoError(t, err)
	require.Equal(t, []uint32{3}, second.OriginSubTokenIDs)
	require.NotEqual(t, lineage.ReceiptNativeTokenID, second.ReceiptNativeTokenID)
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
}

func TestGatewayIsolationRejectsInconsistentProofAndPreservesState(t *testing.T) {
	ctx, k, g, proof, owners := gatewayIsolationFixture(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	for _, tc := range []struct {
		change func(*GatewayIsolation)
		err    string
	}{
		{func(p *GatewayIsolation) { p.NextPositionID = stateIsolationID(3) }, "advance the vault cursor"},
		{func(p *GatewayIsolation) { p.Quantity = types.MaxIsolatedOriginUnits + 1 }, "identity, quantity or DEL reserve"},
		{func(p *GatewayIsolation) { p.Reserve = p.Reserve.AddRaw(1) }, "uniform DEL reserve or supply"},
		{func(p *GatewayIsolation) { p.OriginReserveAfter = p.OriginReserveAfter.AddRaw(1) }, "uniform DEL reserve or supply"},
		{func(p *GatewayIsolation) { p.OriginSupplyAfter++ }, "uniform DEL reserve or supply"},
	} {
		invalid := proof
		tc.change(&invalid)
		_, err := k.AdmitGatewayIsolation(ctx, invalid)
		require.ErrorContains(t, err, tc.err)
	}
	other, err := account(owners[1])
	require.NoError(t, err)
	invalidOwner := proof
	invalidOwner.Owner = common.BytesToAddress(other)
	_, err = k.AdmitGatewayIsolation(ctx, invalidOwner)
	require.ErrorContains(t, err, "insufficient native origin units")
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Empty(t, ctx.EventManager().Events())

	badCtx, bad, badGenesis, badProof, _ := gatewayIsolationFixture(t)
	origin := badGenesis.Collections[0].Tokens[1]
	mismatch := sdk.NewCoin(origin.Reserve.Denom, sdkmath.ZeroInt())
	origin.SubTokens[2].Reserve = &mismatch
	require.NoError(t, bad.InitGenesis(badCtx, badGenesis))
	_, err = bad.AdmitGatewayIsolation(badCtx, badProof)
	require.ErrorContains(t, err, "uniform native DEL reserve on each origin unit")
}

func TestGatewayIsolationLateFailureAndOuterRollback(t *testing.T) {
	ctx, k, g, proof, _ := gatewayIsolationFixture(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	broken, err := NewStateKeeperWithDomain(failIsolatedIndexService{k.service}, bindingChain, 777)
	require.NoError(t, err)
	_, err = broken.AdmitGatewayIsolation(ctx, proof)
	require.ErrorContains(t, err, "isolated origin index write failed")
	unchanged, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, unchanged)
	require.Empty(t, ctx.EventManager().Events())
	child, _ := ctx.CacheContext()
	_, err = k.AdmitGatewayIsolation(child, proof)
	require.NoError(t, err)
	unchanged, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, unchanged)
	require.Empty(t, ctx.EventManager().Events())
	_, err = k.AdmitGatewayIsolation(ctx, proof)
	require.NoError(t, err)
}

func TestGatewayIsolationBoundedMaximumQuantity(t *testing.T) {
	ctx, k, g, proof, owners := gatewayIsolationFixture(t)
	origin := g.Collections[0].Tokens[1]
	origin.Minted, origin.Burnt = types.MaxIsolatedOriginUnits, 0
	origin.SubTokens = make(types.SubTokens, origin.Minted)
	for i := range origin.SubTokens {
		origin.SubTokens[i] = &types.SubToken{ID: uint32(i) + 1, Owner: owners[0]}
	}
	g.ExternalBindings[1].NextSubTokenID = uint64(origin.Minted) + 1
	proof.Quantity, proof.OriginSupplyAfter = types.MaxIsolatedOriginUnits, 0
	proof.Reserve, proof.OriginReserveAfter = origin.Reserve.Amount.MulRaw(int64(proof.Quantity)), sdkmath.ZeroInt()
	require.NoError(t, k.InitGenesis(ctx, g))
	ctx = ctx.WithGasMeter(storetypes.NewGasMeter(24_000_000))
	lineage, err := k.AdmitGatewayIsolation(ctx, proof)
	require.NoError(t, err)
	require.Len(t, lineage.OriginSubTokenIDs, int(proof.Quantity))
	require.EqualValues(t, 1, lineage.OriginSubTokenIDs[0])
	require.Equal(t, proof.Quantity, lineage.OriginSubTokenIDs[proof.Quantity-1])
	t.Logf("%d-unit NFT keeper admission: %d SDK gas", proof.Quantity, ctx.GasMeter().GasConsumed())
	metadata, err := k.GetToken(ctx, lineage.OriginNativeTokenID)
	require.NoError(t, err)
	require.Equal(t, metadata.Minted, metadata.Burnt)
	require.Equal(t, origin.Reserve, metadata.Reserve)
}
