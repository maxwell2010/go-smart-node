package keeper

import (
	"bytes"
	"errors"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// NFTHistoricalPenaltyPlan records the exact surviving reserve and the loss
// of one NFT position. An exposed position can have zero loss after rounding.
type NFTHistoricalPenaltyPlan struct {
	Before, Loss, Remaining sdkmath.Int
	Eligible                bool
}

// PlanActiveNFTHistoricalPenalty uses the immutable entry height of an
// ordinary isolated receipt in a network initialized with fresh provenance.
func (k *StateKeeper) PlanActiveNFTHistoricalPenalty(ctx sdk.Context, expected types.EVMStakeBinding, infractionHeight int64) (NFTHistoricalPenaltyPlan, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return NFTHistoricalPenaltyPlan{}, err
	}
	preimage, err := recordFromStake(&expected)
	if err != nil {
		return NFTHistoricalPenaltyPlan{}, err
	}
	plan, err := k.planActiveNFTHistoricalPenalty(ctx, expected, infractionHeight)
	if err != nil {
		return NFTHistoricalPenaltyPlan{}, err
	}
	current, err := k.readBindingRow(ctx, preimage.key)
	if err != nil {
		return NFTHistoricalPenaltyPlan{}, err
	}
	if !bytes.Equal(current.data, preimage.data) {
		return NFTHistoricalPenaltyPlan{}, errors.New("active NFT binding differs from the expected historical penalty preimage")
	}
	if err := k.checkBindingIndexes(ctx, current); err != nil {
		return NFTHistoricalPenaltyPlan{}, err
	}
	owner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(expected.Beneficiary)
	if err != nil {
		return NFTHistoricalPenaltyPlan{}, err
	}
	operator, err := k.ValidatorAddressCodec().BytesToString(expected.Validator)
	if err != nil {
		return NFTHistoricalPenaltyPlan{}, err
	}
	target := expected.NativeTarget
	delegation, err := k.GetDelegation(ctx, owner, operator, target.StakeType, target.ID)
	if err != nil {
		return NFTHistoricalPenaltyPlan{}, err
	}
	stake := types.NewStakeNFT(target.ID, target.SubTokenIDs, sdk.NewCoin(identity.BaseDenom, expected.ReportedReserveAmount))
	if !delegation.Stake.Equal(stake) {
		return NFTHistoricalPenaltyPlan{}, errors.New("native delegation differs from the active NFT historical penalty reserve or receipt")
	}
	return plan, nil
}

// The caller validates the binding and reconciles the exact native row and
// delegation. Keeping those reads outside the common calculation also lets
// the mutation path reuse the prestate it already checked.
func (k *StateKeeper) planActiveNFTHistoricalPenalty(ctx sdk.Context, expected types.EVMStakeBinding, infractionHeight int64) (NFTHistoricalPenaltyPlan, error) {
	if infractionHeight <= 0 || infractionHeight > ctx.BlockHeight() {
		return NFTHistoricalPenaltyPlan{}, errors.New("invalid NFT infraction height")
	}
	if err := k.requireNFTHistoricalProvenance(ctx); err != nil {
		return NFTHistoricalPenaltyPlan{}, err
	}
	if !expected.Active || expected.Kind != gatewayNFTBinding || expected.CreatedHeight <= 0 || expected.UpdatedHeight > ctx.BlockHeight() ||
		common.BytesToHash(expected.HoldTimestamp) != (common.Hash{}) || expected.NativeTarget.StakeType != types.StakeType_NFT ||
		!ordinaryGatewayNFTPosition(expected.NFTPosition, expected.ReserveAsset, expected.Beneficiary, expected.Validator, expected.StakeID) {
		return NFTHistoricalPenaltyPlan{}, errors.New("NFT historical penalty requires an ordinary active Gateway receipt")
	}
	return planNFTHistoricalPenalty(expected.ReportedReserveAmount, expected.CreatedHeight < infractionHeight), nil
}

// PlanFrozenNFTHistoricalPenalty uses both original entry height and native
// queue height. A withdrawal at the infraction height still had bonded stake
// in the state that signed that block.
func (k *StateKeeper) PlanFrozenNFTHistoricalPenalty(ctx sdk.Context, expected types.EVMFrozenBinding, infractionHeight int64) (NFTHistoricalPenaltyPlan, error) {
	if infractionHeight <= 0 || infractionHeight > ctx.BlockHeight() {
		return NFTHistoricalPenaltyPlan{}, errors.New("invalid NFT infraction height")
	}
	entryHeight, err := k.GatewayFrozenNFTStakeEntryHeight(ctx, expected)
	if err != nil {
		return NFTHistoricalPenaltyPlan{}, err
	}
	return planFrozenNFTHistoricalPenalty(expected, entryHeight, infractionHeight)
}

func planFrozenNFTHistoricalPenalty(expected types.EVMFrozenBinding, entryHeight, infractionHeight int64) (NFTHistoricalPenaltyPlan, error) {
	if !ordinaryGatewayNFTPosition(expected.NFTPosition, expected.ReserveAsset, expected.Beneficiary, expected.SourceValidator, expected.SourceStakeID) {
		return NFTHistoricalPenaltyPlan{}, errors.New("NFT historical penalty requires an ordinary frozen Gateway receipt")
	}
	eligible := entryHeight < infractionHeight && infractionHeight <= expected.CreatedHeight
	return planNFTHistoricalPenalty(expected.ReportedReserveAmount, eligible), nil
}

func (k *StateKeeper) requireNFTHistoricalProvenance(ctx sdk.Context) error {
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrNotFound
	}
	return nil
}

func ordinaryGatewayNFTPosition(position *types.NFTPositionIdentity, asset *types.EVMReserveAsset, beneficiary, validator, stakeID []byte) bool {
	var zero common.Hash
	return position.Mode == types.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT && position.Origin.Standard == 1155 &&
		position.Custody.NativeSubTokenIDs[0] == 1 && common.BytesToHash(position.ParentPositionID) == zero &&
		asset.ReserveKind == nfttypes.IsolatedReserveDEL && asset.NativeDenom == identity.BaseDenom &&
		crypto.Keccak256Hash([]byte("NFT"), position.Vault, position.PositionID, beneficiary, validator, zero[:]) == common.BytesToHash(stakeID)
}

func planNFTHistoricalPenalty(reserve sdkmath.Int, eligible bool) NFTHistoricalPenaltyPlan {
	loss := sdkmath.ZeroInt()
	if eligible {
		loss = reserve.QuoRaw(20) // 5%, rounded down in DEL atoms.
	}
	return NFTHistoricalPenaltyPlan{Before: reserve, Eligible: eligible, Loss: loss, Remaining: reserve.Sub(loss)}
}
