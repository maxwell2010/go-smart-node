package keeper

import (
	"bytes"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// GetFrozenWithdrawalCreationBlock returns the authenticated request block for
// an active native withdrawal. The binding and matching native queue entry
// attest its creation height. Provenance records its canonical header time.
func (k *StateKeeper) GetFrozenWithdrawalCreationBlock(ctx sdk.Context, expected types.EVMFrozenBinding) (CommittedEvidenceBlock, error) {
	var creation CommittedEvidenceBlock
	entry, err := k.verifyFrozenWithdrawalProvenance(ctx, expected)
	if err != nil {
		return creation, err
	}
	if expected.SourceKind == gatewayNFTBinding {
		provenance, err := k.readGatewayFrozenNFTProvenance(ctx, expected)
		if err != nil {
			return creation, err
		}
		creation = provenance.creation
		if creation.Time.IsZero() {
			return CommittedEvidenceBlock{}, ErrNotFound
		}
	} else {
		_, createdTime, err := k.GetFrozenDELProvenance(ctx, expected)
		if err != nil {
			return creation, err
		}
		creation = CommittedEvidenceBlock{Height: entry.CreationHeight, Time: createdTime}
	}
	if creation.Time.Unix() > entry.CompletionTime.Unix() {
		return CommittedEvidenceBlock{}, errors.New("withdrawal creation time exceeds its native queue deadline")
	}
	if ctx.BlockHeight() == creation.Height && !creation.Time.Equal(ctx.BlockTime()) {
		return CommittedEvidenceBlock{}, errors.New("withdrawal creation time differs from the canonical block")
	}
	return creation, nil
}

func (k *StateKeeper) verifyFrozenWithdrawalProvenance(ctx sdk.Context, expected types.EVMFrozenBinding) (types.UndelegationEntry, error) {
	var empty types.UndelegationEntry
	if err := k.checkBindingDomain(ctx); err != nil {
		return empty, err
	}
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil {
		return empty, err
	}
	if !enabled {
		return empty, ErrNotFound
	}
	preimage, err := recordFromFrozen(&expected)
	if err != nil {
		return empty, err
	}
	if !expected.Active || expected.FreezeType != 1 || expected.CreatedHeight <= 0 ||
		expected.UpdatedHeight > ctx.BlockHeight() || expected.ReserveAsset.NativeDenom != identity.BaseDenom {
		return empty, errors.New("stake provenance requires an active DEL-backed withdrawal")
	}
	isNFT := expected.SourceKind == gatewayNFTBinding
	if isNFT && (expected.NativeTarget.StakeType != types.StakeType_NFT ||
		expected.ReserveAsset.ReserveKind != nfttypes.IsolatedReserveDEL ||
		expected.NFTPosition.Mode != types.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT) {
		return empty, errors.New("NFT stake provenance requires an active isolated DEL withdrawal")
	}
	current, err := k.readBindingRow(ctx, preimage.key)
	if err != nil {
		return empty, err
	}
	if !bytes.Equal(current.data, preimage.data) {
		return empty, errors.New("frozen binding differs from the expected provenance preimage")
	}
	_, pending, i, err := k.matchWithdrawalEntry(ctx, current)
	if err != nil {
		return empty, err
	}
	entry := pending.Entries[i]
	if entry.CreationHeight != expected.CreatedHeight {
		return empty, errors.New("native undelegation differs from the frozen provenance creation height")
	}
	if isNFT && (len(entry.Stake.Holds) != 0 || !entry.Stake.Stake.Amount.Equal(expected.ReportedReserveAmount)) {
		return empty, errors.New("native undelegation differs from the frozen NFT stake provenance")
	}
	return entry, nil
}
