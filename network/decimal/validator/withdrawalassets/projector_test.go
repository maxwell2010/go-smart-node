package withdrawalassets

import (
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nftreceipts "github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func positionFixture() (validatortypes.EVMFrozenBinding, nfttypes.IsolatedPositionLineage, nfttypes.IsolatedVaultState, nftview.PositionSnapshot) {
	gateway := common.HexToAddress("0x10")
	beneficiary := common.HexToAddress("0x20")
	vault := common.HexToAddress("0x30")
	origin := common.HexToAddress("0x40")
	originID := common.BigToHash(new(big.Int).Lsh(big.NewInt(1), 200))
	positionID := common.BigToHash(new(big.Int).Lsh(big.NewInt(1), 180))
	nextID := common.BigToHash(new(big.Int).Add(positionID.Big(), big.NewInt(1)))
	parentID := common.HexToHash("0x99")
	amount := common.BigToHash(big.NewInt(13))
	position := &validatortypes.NFTPositionIdentity{
		Mode:            validatortypes.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT,
		Origin:          &validatortypes.EVMNFTReference{Contract: origin.Bytes(), TokenID: originID.Bytes(), Standard: 1155, NativeTokenID: "origin", NativeSubTokenIDs: []uint32{2, 3}},
		Custody:         &validatortypes.EVMNFTReference{Contract: vault.Bytes(), TokenID: positionID.Bytes(), Standard: 721, NativeTokenID: "receipt", NativeSubTokenIDs: []uint32{7}},
		OriginQuantity:  sdkmath.NewInt(2),
		ReceiptQuantity: sdkmath.OneInt(),
		Vault:           vault.Bytes(), PositionID: positionID.Bytes(), ParentPositionID: parentID.Bytes(),
	}
	row := validatortypes.EVMFrozenBinding{
		Gateway: gateway.Bytes(), FrozenIndex: make([]byte, 32), SourceKind: validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT,
		SourceStakeID: make([]byte, 32), SourceValidator: common.HexToAddress("0x50").Bytes(), Beneficiary: beneficiary.Bytes(),
		FreezeType: 1, CompletionTimestamp: common.HexToHash("0x1000").Bytes(), RawAmount: sdkmath.OneInt(),
		ReportedReserveAmount: sdkmath.NewIntFromBigInt(amount.Big()), ReserveAsset: &validatortypes.EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom},
		NFTPosition: position, NativeTarget: &validatortypes.NativeStakeTarget{StakeType: validatortypes.StakeType_NFT, ID: "receipt", SubTokenIDs: []uint32{7}},
		NativeEntryHash: common.HexToHash("0x88").Bytes(), Active: true, CreatedHeight: 10, UpdatedHeight: 10,
	}
	lineage := nfttypes.IsolatedPositionLineage{
		Vault: vault.Bytes(), PositionID: positionID.Bytes(), ParentPositionID: parentID.Bytes(),
		ReceiptNativeTokenID: "receipt", ReceiptSubTokenID: 7,
		OriginContract: origin.Bytes(), OriginTokenID: originID.Bytes(), OriginNativeTokenID: "origin", OriginSubTokenIDs: []uint32{2, 3},
		Reserve: nfttypes.IsolatedReserveSnapshot{Kind: nfttypes.IsolatedReserveDEL, Token: make([]byte, 20), Amount: amount.Bytes()},
	}
	actual := nftview.PositionSnapshot{
		Asset: nftreceipts.AssetKey{Contract: vault, TokenID: positionID}, Origin: nftreceipts.AssetKey{Contract: origin, TokenID: originID},
		OriginQuantity: 2, ParentPositionID: parentID, Owner: beneficiary, Status: nftview.PositionActive,
		ReserveKind: nfttypes.IsolatedReserveDEL, ReserveAmount: amount, NextPositionID: nextID,
	}
	return row, lineage, nfttypes.IsolatedVaultState{Vault: vault.Bytes(), NextPositionID: nextID.Bytes()}, actual
}

func TestWithdrawalMovementUsesExactPersistedUnitAndRecipient(t *testing.T) {
	row, _, _, actual := positionFixture()
	require.NoError(t, row.Validate())
	move := nftreceipts.Movement{
		Transfer:      nftreceipts.Transfer{Asset: actual.Asset, NFTType: nfttypes.NftType_NFT721, From: common.BytesToAddress(row.Gateway), To: actual.Owner, Quantity: 1},
		NativeTokenID: "receipt", SubTokenIDs: []uint32{7},
	}
	require.NoError(t, requireExactMovement(row, []nftreceipts.Movement{move}))
	require.Error(t, requireExactMovement(row, nil))
	require.Error(t, requireExactMovement(row, []nftreceipts.Movement{move, move}))
	for _, test := range []struct {
		name   string
		change func(*nftreceipts.Movement)
	}{
		{"wrong recipient", func(m *nftreceipts.Movement) { m.Transfer.To = common.HexToAddress("0x41") }},
		{"wrong escrow", func(m *nftreceipts.Movement) { m.Transfer.From = common.HexToAddress("0x42") }},
		{"wrong contract", func(m *nftreceipts.Movement) { m.Transfer.Asset.Contract = common.HexToAddress("0x43") }},
		{"truncated ID", func(m *nftreceipts.Movement) { m.Transfer.Asset.TokenID = common.Hash{} }},
		{"wrong quantity", func(m *nftreceipts.Movement) { m.Transfer.Quantity = 2 }},
		{"wrong unit", func(m *nftreceipts.Movement) { m.SubTokenIDs = []uint32{8} }},
		{"origin instead of receipt", func(m *nftreceipts.Movement) { m.NativeTokenID = "origin" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := move
			test.change(&candidate)
			require.Error(t, requireExactMovement(row, []nftreceipts.Movement{candidate}))
		})
	}
}

func TestWithdrawalBackingRemainsExactForUnchangedOrZeroReserve(t *testing.T) {
	row, lineage, vault, actual := positionFixture()
	require.NoError(t, requireExactBacking(row, lineage, vault, actual, common.BytesToAddress(row.Beneficiary)))
	require.True(t, matchesNativeReserve(sdk.NewCoin(identity.BaseDenom, row.ReportedReserveAmount), row))
	require.False(t, matchesNativeReserve(sdk.NewInt64Coin(identity.BaseDenom, 12), row))
	require.False(t, matchesNativeReserve(sdk.NewCoin("other", row.ReportedReserveAmount), row))
	for _, test := range []struct {
		name   string
		change func(*nftview.PositionSnapshot)
	}{
		{"closed receipt", func(p *nftview.PositionSnapshot) { p.Status = nftview.PositionClosed }},
		{"different owner", func(p *nftview.PositionSnapshot) { p.Owner = common.HexToAddress("0x41") }},
		{"different origin", func(p *nftview.PositionSnapshot) { p.Origin.TokenID = common.Hash{} }},
		{"different origin quantity", func(p *nftview.PositionSnapshot) { p.OriginQuantity++ }},
		{"different parent", func(p *nftview.PositionSnapshot) { p.ParentPositionID = common.Hash{} }},
		{"different high water mark", func(p *nftview.PositionSnapshot) { p.NextPositionID = common.HexToHash("0x2") }},
		{"different reserve amount", func(p *nftview.PositionSnapshot) { p.ReserveAmount = common.HexToHash("0x10") }},
		{"different reserve currency", func(p *nftview.PositionSnapshot) {
			p.ReserveKind = nfttypes.IsolatedReserveERC20
			p.ReserveToken = common.HexToAddress("0x42")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := actual
			test.change(&candidate)
			require.Error(t, requireExactBacking(row, lineage, vault, candidate, common.BytesToAddress(row.Beneficiary)))
		})
	}
	lineage.OriginSubTokenIDs = []uint32{2, 4}
	require.Error(t, requireExactBacking(row, lineage, vault, actual, common.BytesToAddress(row.Beneficiary)))
	lineage.OriginSubTokenIDs = []uint32{2, 3}
	lineage.Reserve.Amount, actual.ReserveAmount = make([]byte, 32), common.Hash{}
	row.ReportedReserveAmount = sdkmath.ZeroInt()
	require.NoError(t, requireExactBacking(row, lineage, vault, actual, common.BytesToAddress(row.Beneficiary)))
	require.True(t, matchesNativeReserve(sdk.NewInt64Coin(identity.BaseDenom, 0), row))

	// An unchanged native zero is not evidence for an undeclared currency.
	actual.ReserveKind = 0
	require.Error(t, requireExactBacking(row, lineage, vault, actual, common.BytesToAddress(row.Beneficiary)))
}

func TestGenesisNFTBackingRequiresGatewayCustody(t *testing.T) {
	row, lineage, vault, actual := positionFixture()
	escrow := common.BytesToAddress(row.Gateway)
	require.Error(t, requireExactBacking(row, lineage, vault, actual, escrow))
	actual.Owner = escrow
	require.NoError(t, requireExactBacking(row, lineage, vault, actual, escrow))
}

func TestWithdrawalProjectionRejectsStaleFrozenPrestate(t *testing.T) {
	row, _, _, _ := positionFixture()
	require.NoError(t, requireSameFrozenRow(&row, &row))
	modified := row
	modified.UpdatedHeight++
	require.Error(t, requireSameFrozenRow(&row, &modified))
	modified = row
	modified.Active = false
	require.Error(t, requireSameFrozenRow(&row, &modified))
	require.NoError(t, nftreceipts.EntryPointWithdrawalSettlement.Validate())
	require.Error(t, nftreceipts.EntryPointIBC.Validate())
}
