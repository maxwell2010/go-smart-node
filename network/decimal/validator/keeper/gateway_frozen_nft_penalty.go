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
	return k.applyGatewayFrozenNFTPenalty(ctx, nft, before, infractionHeight, oldCursor, newCursor, remaining, frozenPenaltyRegular, false)
}

// ApplyGatewayFrozenHistoricalNFTPenalty projects the exact historical loss
// on a still-custodied NFT. Nominal maturity only controls withdrawal, so a
// mature claim that still has its native queue entry remains eligible.
func (k *StateKeeper) ApplyGatewayFrozenHistoricalNFTPenalty(ctx sdk.Context, nft *nftkeeper.StateKeeper, before types.EVMFrozenBinding,
	infractionHeight int64, oldCursor, newCursor common.Hash, remaining sdkmath.Int, eligible bool,
) error {
	return k.applyGatewayFrozenNFTPenalty(ctx, nft, before, infractionHeight, oldCursor, newCursor, remaining, frozenPenaltyHistorical, eligible)
}

// ApplyGatewayCustodiedNFTCheckpoint acknowledges one verified ordinary
// penalty on a mature claim created before the infraction. Its NFT and
// reserve stay in custody until the claim can be withdrawn.
func (k *StateKeeper) ApplyGatewayCustodiedNFTCheckpoint(ctx sdk.Context, nft *nftkeeper.StateKeeper, before types.EVMFrozenBinding,
	infractionHeight int64, oldCursor, newCursor common.Hash,
) error {
	return k.applyGatewayFrozenNFTPenalty(ctx, nft, before, infractionHeight, oldCursor, newCursor, before.ReportedReserveAmount, frozenPenaltyCustodiedCheckpoint, false)
}

func (k *StateKeeper) applyGatewayFrozenNFTPenalty(ctx sdk.Context, nft *nftkeeper.StateKeeper, before types.EVMFrozenBinding,
	infractionHeight int64, oldCursor, newCursor common.Hash, remaining sdkmath.Int, mode frozenPenaltyMode, eligible bool,
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
	exactQueueEntry := entry.CreationHeight == before.CreatedHeight && len(entry.Stake.Holds) == 0 &&
		entry.Stake.Stake.Denom == identity.BaseDenom && entry.Stake.Stake.Amount.Equal(before.ReportedReserveAmount)
	if mode == frozenPenaltyRegular && (!exactQueueEntry || !entry.CompletionTime.After(ctx.BlockTime())) {
		return errors.New("frozen NFT penalty requires the exact unmatured NFT queue entry and reserve")
	}
	if !exactQueueEntry {
		return errors.New("frozen NFT historical penalty requires the exact NFT queue entry and reserve")
	}
	if mode == frozenPenaltyCustodiedCheckpoint {
		if err := checkMatureCustodiedCheckpoint(entry, infractionHeight, ctx.BlockTime()); err != nil {
			return err
		}
	}
	loss := before.ReportedReserveAmount.Sub(remaining)
	if entry.CreationHeight < infractionHeight && !loss.IsZero() {
		return errors.New("frozen NFT claim predates the infraction")
	}
	if mode != frozenPenaltyRegular {
		if err := k.checkBindingDomain(child); err != nil {
			return err
		}
		if err := k.requireNFTHistoricalProvenance(child); err != nil {
			return err
		}
		provenance, err := k.readGatewayFrozenNFTProvenance(child, before)
		if err != nil {
			return err
		}
		plan, err := planFrozenNFTHistoricalPenalty(before, provenance.stakeEntryHeight, infractionHeight)
		if err != nil {
			return err
		}
		if plan.Eligible != eligible || !plan.Remaining.Equal(remaining) {
			return errors.New("frozen NFT penalty differs from the historical exposure")
		}
	}
	// The binding already contains the exact archived unit and reserve
	// identity. Derive the expected lineage from it so the NFT keeper reads
	// and validates those units once, even for a 1024-unit receipt.
	lineage := nfttypes.IsolatedPositionLineage{
		Vault: p.Vault, PositionID: p.PositionID, ParentPositionID: p.ParentPositionID,
		OriginContract: p.Origin.Contract, OriginTokenID: p.Origin.TokenID,
		OriginNativeTokenID: p.Origin.NativeTokenID, OriginSubTokenIDs: p.Origin.NativeSubTokenIDs,
		ReceiptNativeTokenID: p.Custody.NativeTokenID, ReceiptSubTokenID: p.Custody.NativeSubTokenIDs[0],
		Reserve: nfttypes.IsolatedReserveSnapshot{Kind: before.ReserveAsset.ReserveKind, Token: before.ReserveAsset.Token,
			Amount: common.BigToHash(before.ReportedReserveAmount.BigInt()).Bytes()},
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
