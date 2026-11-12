package settlement

import (
	"errors"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// VerifySignedPenalizedNFTWithdrawal requires an already reconciled native
// stake and its exact EVM cursor. A zero-reserve receipt still owns one NFT.
func (e *Executor) VerifySignedPenalizedNFTWithdrawal(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt,
	before validatortypes.EVMStakeBinding,
) (NFTWithdrawal, error) {
	if err := before.Validate(); err != nil {
		return NFTWithdrawal{}, err
	}
	if !before.Active || before.Kind != validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT ||
		before.NativeTarget.StakeType != validatortypes.StakeType_NFT || common.BytesToHash(before.HoldTimestamp) != (common.Hash{}) ||
		before.NFTPosition.Mode != validatortypes.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT ||
		before.ReserveAsset.ReserveKind != nfttypes.IsolatedReserveDEL || before.ReserveAsset.NativeDenom != identity.BaseDenom ||
		common.BytesToHash(before.PenaltyCursor) == (common.Hash{}) || before.UpdatedHeight > ctx.BlockHeight() {
		return NFTWithdrawal{}, errors.New("penalized NFT withdrawal requires a fully reconciled ordinary DEL-backed receipt")
	}
	return e.verifySignedNFTWithdrawal(ctx, msg, receipt, &before)
}

func (e *Executor) checkPenalizedNFTWithdrawalPosition(before validatortypes.EVMStakeBinding, p evmview.PositionSnapshot,
	validator, beneficiary common.Address, companionID common.Hash,
) error {
	origin := before.NFTPosition.Origin
	if common.BytesToAddress(before.Gateway) != e.roles.Gateway ||
		common.BytesToHash(before.StakeID) != e.ordinaryNFTStakeID(p.Asset.TokenID, validator, beneficiary) ||
		common.BytesToAddress(before.Validator) != validator || common.BytesToAddress(before.Beneficiary) != beneficiary ||
		common.BytesToAddress(before.TokenContract) != p.Asset.Contract || common.BytesToHash(before.TokenID) != p.Asset.TokenID ||
		origin.Standard != 1155 || p.Origin.Contract != common.BytesToAddress(origin.Contract) || p.Origin.TokenID != common.BytesToHash(origin.TokenID) ||
		uint32(len(origin.NativeSubTokenIDs)) != p.OriginQuantity || p.ParentPositionID != (common.Hash{}) ||
		common.BytesToHash(before.NFTPosition.ParentPositionID) != p.ParentPositionID ||
		common.BigToHash(before.ReportedReserveAmount.BigInt()) != p.ReserveAmount ||
		(before.ReportedReserveAmount.IsPositive() && common.BytesToHash(before.CoinCompanionStakeID) != companionID) ||
		(before.ReportedReserveAmount.IsZero() && len(before.CoinCompanionStakeID) != 0) {
		return errors.New("penalized NFT withdrawal differs from the bound custody, reserve or companion")
	}
	return nil
}

func (e *Executor) verifyNFTWithdrawalPenaltyAcknowledgements(ctx sdk.Context, logs []*ethtypes.Log, validator common.Address,
	companion DELTransition, post []byte, withdrawn *big.Int, nftCursor, coinCursor common.Hash,
) (bool, error) {
	master, err := e.read(ctx, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, validator)
	if err != nil {
		return false, err
	}
	status := common.BytesToHash(master[:common.HashLength])
	if (status != smallWord(1) && status != smallWord(2)) || common.BytesToHash(master[2*common.HashLength:]) != nftCursor || coinCursor != nftCursor {
		return false, errors.New("penalized NFT withdrawal requires both cursors at the exact Master penalty count")
	}
	reserve, err := e.read(ctx, e.roles.Gateway, e.gateway, "getValidatorReserve", 2*common.HashLength, validator, e.roles.WrappedDEL)
	if err != nil {
		return false, err
	}
	if common.BytesToHash(reserve[:common.HashLength]) != nftCursor {
		return false, errors.New("penalized NFT withdrawal requires the validator reserve at the exact Master penalty count")
	}
	// An ordinary NFT withdrawal may acknowledge the aggregate cursor with
	// zero loss. Verify the final prefix log before considering the coin slot.
	if len(logs) != 0 {
		ack := e.gateway.Events["PenaltyAppliedToValidator"]
		last := logs[len(logs)-1]
		if len(last.Topics) != 0 && last.Topics[0] == ack.ID {
			index := common.BigToHash(new(big.Int).Sub(nftCursor.Big(), big.NewInt(1)))
			if !matchesNFTEvent(last, e.roles.Gateway, []common.Hash{ack.ID, common.BytesToHash(validator[:])},
				common.BytesToHash(e.roles.WrappedDEL[:]), smallWord(tokenDEL), common.Hash{}, index, common.Hash{}) {
				return false, errors.New("invalid validator reserve penalty acknowledgement during NFT withdrawal")
			}
			logs = logs[:len(logs)-1]
		}
	}
	if len(logs) == 0 {
		return false, nil
	}
	// A slot with no direct DEL can advance only its cursor. The pinned
	// contract emits a single event for empty slots, or an unchanged gross
	// balance and cursor for a slot with NFT backing. Require the exact logs.
	priorGross := new(big.Int).Add(companion.NFTBacked.BigInt(), withdrawn)
	ack := e.gateway.Events["PenaltyAppliedToStake"]
	if !companion.Remaining.IsZero() || priorGross.BitLen() > 256 ||
		!matchesNFTEvent(logs[len(logs)-1], e.roles.Gateway, []common.Hash{ack.ID, companion.StakeID}, nftCursor) {
		return false, errors.New("signed NFT withdrawal cannot realize a coin penalty")
	}
	if priorGross.Sign() == 0 && len(logs) == 1 {
		return true, nil
	}
	if priorGross.Sign() == 0 || len(logs) != 3 {
		return false, errors.New("invalid NFT-only companion penalty acknowledgement")
	}
	previous := append([]byte(nil), post...)
	priorGross.FillBytes(previous[3*common.HashLength : 4*common.HashLength])
	if err := e.checkDELStakeLogs(logs[:2], companion.StakeID, previous, new(big.Int), false); err != nil {
		return false, err
	}
	return true, nil
}
