package keeper

import (
	"bytes"
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// ApplyGatewayFrozenNFTPenalty projects one verified Vault loss. The frozen
// claim keeps its original receipt and units even when its DEL reserve is zero.
func (k *StateKeeper) ApplyGatewayFrozenNFTPenalty(ctx sdk.Context, nft *nftkeeper.StateKeeper, before types.EVMFrozenBinding,
	infractionHeight int64, oldCursor, newCursor common.Hash, remaining sdkmath.Int,
) error {
	if err := before.Validate(); err != nil {
		return err
	}
	var zero common.Hash
	if nft == nil || !before.Active || before.FreezeType != 1 || before.SourceKind != gatewayNFTBinding ||
		before.NativeTarget.StakeType != types.StakeType_NFT || before.ReserveAsset.ReserveKind != nfttypes.IsolatedReserveDEL ||
		before.ReserveAsset.NativeDenom != identity.BaseDenom || before.NFTPosition.Mode != types.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT ||
		before.NFTPosition.Origin.Standard != 1155 || common.BytesToHash(before.NFTPosition.ParentPositionID) != zero ||
		!before.RawAmount.Equal(sdkmath.OneInt()) || before.CreatedHeight <= 0 || before.UpdatedHeight > ctx.BlockHeight() ||
		infractionHeight <= 0 || infractionHeight > ctx.BlockHeight() || remaining.IsNil() || remaining.IsNegative() ||
		remaining.GT(before.ReportedReserveAmount) || new(big.Int).Add(oldCursor.Big(), big.NewInt(1)).Cmp(newCursor.Big()) != 0 {
		return errors.New("invalid ordinary frozen NFT penalty transition")
	}
	p := before.NFTPosition
	if crypto.Keccak256Hash([]byte("NFT"), p.Vault, p.PositionID, before.Beneficiary, before.SourceValidator, zero[:]) != common.BytesToHash(before.SourceStakeID) {
		return errors.New("frozen NFT penalty requires the exact ordinary Gateway stake ID")
	}
	child, commit := ctx.CacheContext()
	current, err := recordFromFrozen(&before)
	if err != nil {
		return err
	}
	actual, err := k.readBindingRow(child, current.key)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual.data, current.data) {
		return errors.New("frozen NFT penalty binding prestate changed")
	}
	state := k.service.OpenKVStore(child)
	cursorKey := frozenDELPenaltyCursorKey(before.Gateway, before.FrozenIndex)
	rawCursor, err := state.Get(cursorKey)
	if err != nil {
		return err
	}
	if (rawCursor != nil && len(rawCursor) != common.HashLength) || common.BytesToHash(rawCursor) != oldCursor {
		return errors.New("frozen NFT penalty cursor prestate changed")
	}
	key, pending, i, err := k.matchWithdrawalEntry(child, actual)
	if err != nil {
		return err
	}
	entry := &pending.Entries[i]
	if entry.CreationHeight != before.CreatedHeight || !entry.CompletionTime.After(ctx.BlockTime()) || len(entry.Stake.Holds) != 0 ||
		entry.Stake.Stake.Denom != identity.BaseDenom || !entry.Stake.Stake.Amount.Equal(before.ReportedReserveAmount) {
		return errors.New("frozen NFT penalty requires the exact unmatured NFT queue entry and reserve")
	}
	loss := before.ReportedReserveAmount.Sub(remaining)
	if entry.CreationHeight < infractionHeight && !loss.IsZero() {
		return errors.New("frozen NFT claim predates the infraction")
	}
	lineage, err := nft.GetIsolatedLineage(child, p.Vault, p.PositionID)
	if err != nil {
		return err
	}
	if !bytes.Equal(lineage.ParentPositionID, p.ParentPositionID) || !bytes.Equal(lineage.OriginContract, p.Origin.Contract) ||
		!bytes.Equal(lineage.OriginTokenID, p.Origin.TokenID) || lineage.OriginNativeTokenID != p.Origin.NativeTokenID ||
		!types.SameNativeUnitIDs(lineage.OriginSubTokenIDs, p.Origin.NativeSubTokenIDs) ||
		lineage.ReceiptNativeTokenID != p.Custody.NativeTokenID || lineage.ReceiptSubTokenID != p.Custody.NativeSubTokenIDs[0] ||
		lineage.Reserve.Kind != nfttypes.IsolatedReserveDEL || common.BytesToHash(lineage.Reserve.Amount) != common.BigToHash(before.ReportedReserveAmount.BigInt()) {
		return errors.New("frozen NFT penalty lineage differs from the native claim")
	}
	if err := nft.ApplyGatewayIsolatedDELPenalty(child, common.BytesToAddress(before.Gateway), lineage, remaining); err != nil {
		return err
	}
	after := before
	after.ReportedReserveAmount, after.UpdatedHeight = remaining, ctx.BlockHeight()
	if loss.IsPositive() {
		entry.Stake.Stake = sdk.NewCoin(identity.BaseDenom, remaining)
		after.NativeEntryHash, err = types.NativeQueueEntryHash(1, pending.Delegator, pending.Validator, "", entry)
		if err != nil {
			return err
		}
		pair, err := encodeWithdrawalPair(pending)
		if err != nil {
			return err
		}
		if err := state.Set(key, pair); err != nil {
			return err
		}
	}
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{{Frozen: &after}})
	if err != nil {
		return err
	}
	if err := k.ApplyBindingPlan(child, plan); err != nil {
		return err
	}
	if err := state.Set(cursorKey, newCursor[:]); err != nil {
		return err
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_frozen_nft_penalty",
		sdk.NewAttribute("frozen_index", common.BytesToHash(before.FrozenIndex).Hex()),
		sdk.NewAttribute("penalty_cursor", newCursor.Hex()), sdk.NewAttribute("amount", loss.String())))
	commit()
	return nil
}
