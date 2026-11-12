package keeper

import (
	"bytes"
	"errors"
	"math/big"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const gatewayNFTBinding = types.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT

// GatewayNFTPosition describes one authenticated ordinary isolated NFT stake.
// Companion records the coin slot after the EVM transition. Its direct amount
// is unchanged and its logical NFT backing moves by the lineage reserve.
type GatewayNFTPosition struct {
	Companion GatewayDELStake
	StakeID   common.Hash
	Lineage   nfttypes.IsolatedPositionLineage
}

func (k *StateKeeper) gatewayNFTProjection(ctx sdk.Context, position GatewayNFTPosition) (types.Delegation, types.EVMStakeBinding, error) {
	if position.Companion.PenaltyCursor != (common.Hash{}) {
		_, _, err := position.Companion.nativePair()
		return types.Delegation{}, types.EVMStakeBinding{}, err
	}
	delegation, row, err := k.gatewayNFTState(ctx, position, common.Hash{})
	if err != nil {
		return delegation, row, err
	}
	if !row.ReportedReserveAmount.IsPositive() {
		return types.Delegation{}, types.EVMStakeBinding{}, errors.New("invalid Gateway NFT stake or unsupported isolated position")
	}
	return delegation, row, nil
}

func (k *StateKeeper) gatewayNFTState(ctx sdk.Context, position GatewayNFTPosition, cursor common.Hash) (types.Delegation, types.EVMStakeBinding, error) {
	var delegation types.Delegation
	var row types.EVMStakeBinding
	companion, lineage := position.Companion, position.Lineage
	owner, operator, err := companion.nativePairIdentity()
	if err != nil {
		return delegation, row, err
	}
	if err := lineage.Validate(); err != nil {
		return delegation, row, err
	}
	reserve := sdkmath.NewIntFromBigInt(new(big.Int).SetBytes(lineage.Reserve.Amount))
	var zero common.Hash
	vault, origin := common.BytesToAddress(lineage.Vault), common.BytesToAddress(lineage.OriginContract)
	if companion.HoldDeadline != 0 || !bytes.Equal(lineage.ParentPositionID, zero[:]) ||
		lineage.Reserve.Kind != nfttypes.IsolatedReserveDEL ||
		lineage.ReceiptSubTokenID != 1 || vault == companion.Gateway || vault == companion.WrappedDEL ||
		origin == companion.Gateway || origin == companion.WrappedDEL ||
		position.StakeID != crypto.Keccak256Hash([]byte("NFT"), lineage.Vault, lineage.PositionID, companion.Beneficiary[:], companion.Validator[:], zero[:]) {
		return delegation, row, errors.New("invalid Gateway NFT stake or unsupported isolated position")
	}
	domain, ok := k.ConfiguredEVMSourceDomain()
	if !ok {
		return delegation, row, ErrBindingDomain
	}
	nativeID, err := nfttypes.FreshExternalTokenID(domain.ChainID, domain.EVMChainID, lineage.Vault, lineage.PositionID)
	if err != nil {
		return delegation, row, err
	}
	if nativeID != lineage.ReceiptNativeTokenID {
		return delegation, row, errors.New("Gateway NFT receipt differs from its native identity")
	}

	units := []uint32{lineage.ReceiptSubTokenID}
	delegation = types.Delegation{
		Delegator: owner, Validator: operator, Stake: types.NewStakeNFT(nativeID, units, sdk.NewCoin(identity.BaseDenom, reserve)),
	}
	row = types.EVMStakeBinding{
		Kind: gatewayNFTBinding, Gateway: companion.Gateway.Bytes(), StakeID: position.StakeID.Bytes(),
		Validator: companion.Validator.Bytes(), Beneficiary: companion.Beneficiary.Bytes(),
		TokenContract: lineage.Vault, TokenID: lineage.PositionID, HoldTimestamp: zero.Bytes(), PenaltyCursor: cursor.Bytes(),
		RawAmount: sdkmath.OneInt(), RawNftBackedAmount: sdkmath.ZeroInt(), ReportedReserveAmount: reserve,
		ReserveAsset: &types.EVMReserveAsset{ReserveKind: nfttypes.IsolatedReserveDEL, Token: lineage.Reserve.Token, NativeDenom: identity.BaseDenom},
		NFTPosition: &types.NFTPositionIdentity{
			Mode: types.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT, Vault: lineage.Vault,
			PositionID: lineage.PositionID, ParentPositionID: lineage.ParentPositionID,
			Origin: &types.EVMNFTReference{Contract: lineage.OriginContract, TokenID: lineage.OriginTokenID,
				NativeTokenID: lineage.OriginNativeTokenID, NativeSubTokenIDs: lineage.OriginSubTokenIDs, Standard: 1155},
			Custody: &types.EVMNFTReference{Contract: lineage.Vault, TokenID: lineage.PositionID,
				NativeTokenID: nativeID, NativeSubTokenIDs: units, Standard: 721},
			OriginQuantity: sdkmath.NewInt(int64(len(lineage.OriginSubTokenIDs))), ReceiptQuantity: sdkmath.OneInt(),
		},
		NativeTarget: &types.NativeStakeTarget{StakeType: types.StakeType_NFT, ID: nativeID, SubTokenIDs: units},
		Active:       true, CreatedHeight: ctx.BlockHeight(), UpdatedHeight: ctx.BlockHeight(),
	}
	if reserve.IsPositive() {
		row.CoinCompanionStakeID = companion.StakeID.Bytes()
	}
	return delegation, row, nil
}

func gatewayNFTCompanionMutation(ctx sdk.Context, companion GatewayDELStake, previous *types.EVMStakeBinding) (BindingMutation, error) {
	created := ctx.BlockHeight()
	if previous != nil {
		created = previous.CreatedHeight
	}
	row, err := companion.binding(companion.Amount, created, ctx.BlockHeight())
	if err != nil {
		return BindingMutation{}, err
	}
	if row.RawAmount.IsZero() {
		return BindingMutation{Remove: &BindingReference{Kind: row.Kind, Gateway: row.Gateway, ID: row.StakeID}}, nil
	}
	return BindingMutation{Stake: &row}, nil
}

// ApplyGatewayNFTDelegation adds one isolated ERC721 receipt without changing
// the owner's direct DEL stake. The caller admits the NFT lineage in the same
// outer transaction cache.
func (k *StateKeeper) ApplyGatewayNFTDelegation(ctx sdk.Context, position GatewayNFTPosition) error {
	delegation, row, err := k.gatewayNFTProjection(ctx, position)
	if err != nil {
		return err
	}
	companion := position.Companion
	backed := companion.nftBackedAmount()
	if backed.LT(row.ReportedReserveAmount) {
		return errors.New("Gateway NFT deposit exceeds the companion backing")
	}
	prior := companion
	prior.NFTBacked = backed.Sub(row.ReportedReserveAmount)
	child, commit := ctx.CacheContext()
	oldCompanion, _, err := k.checkGatewayDELPrestate(child, prior, prior.Amount, delegation.Delegator, delegation.Validator)
	if err != nil {
		return err
	}
	if _, err := k.GetEVMStakeBinding(child, row.Kind, row.Gateway, row.StakeID); err == nil {
		return errors.New("Gateway NFT stake already has a native binding")
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	coin, err := gatewayNFTCompanionMutation(child, companion, oldCompanion)
	if err != nil {
		return err
	}
	plan, err := k.PrepareBindingPlan(child, []BindingMutation{coin, {Stake: &row}})
	if err != nil {
		return err
	}
	if err := k.ApplyDelegationBatch(child, []DelegationChange{{Next: &delegation}}); err != nil {
		return err
	}
	if err := k.ApplyBindingPlan(child, plan); err != nil {
		return err
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_nft_delegated",
		sdk.NewAttribute("stake_id", position.StakeID.Hex()), sdk.NewAttribute("position_id", common.BytesToHash(row.TokenID).Hex()),
		sdk.NewAttribute("reserve", row.ReportedReserveAmount.String())))
	commit()
	return nil
}

// ApplyGatewayNFTWithdrawal replaces one active stake with a frozen claim on
// the same receipt. The asset remains in Gateway custody until completion.
func (k *StateKeeper) ApplyGatewayNFTWithdrawal(ctx sdk.Context, position GatewayNFTPosition, index common.Hash, deadline time.Time) error {
	return k.applyGatewayNFTWithdrawal(ctx, position, index, deadline, common.Hash{}, false)
}

// ApplyGatewayPenalizedNFTWithdrawal queues an authenticated ordinary NFT
// receipt after its exact native and EVM penalty cursor has been reconciled.
// Zero remaining reserve changes neither the receipt quantity nor custody.
func (k *StateKeeper) ApplyGatewayPenalizedNFTWithdrawal(ctx sdk.Context, position GatewayNFTPosition, index common.Hash,
	deadline time.Time, cursor common.Hash, companionAcknowledged bool,
) error {
	if cursor == (common.Hash{}) || position.Companion.PenaltyCursor != cursor ||
		(companionAcknowledged && (position.Companion.Amount.IsNil() || !position.Companion.Amount.IsZero())) {
		return errors.New("invalid realized NFT withdrawal penalty cursor or companion acknowledgement")
	}
	return k.applyGatewayNFTWithdrawal(ctx, position, index, deadline, cursor, companionAcknowledged)
}

func (k *StateKeeper) applyGatewayNFTWithdrawal(ctx sdk.Context, position GatewayNFTPosition, index common.Hash,
	deadline time.Time, cursor common.Hash, companionAcknowledged bool,
) error {
	if index == common.MaxHash || deadline.Unix() <= 0 || deadline.Nanosecond() != 0 || deadline.Unix() <= ctx.BlockTime().Unix() {
		return errors.New("invalid Gateway NFT withdrawal index or deadline")
	}
	delegation, expected, err := k.gatewayNFTState(ctx, position, cursor)
	if err != nil {
		return err
	}
	if cursor == (common.Hash{}) && (!expected.ReportedReserveAmount.IsPositive() || position.Companion.PenaltyCursor != (common.Hash{})) {
		return errors.New("unpenalized NFT withdrawal requires a positive reserve and zero companion cursor")
	}
	companion := position.Companion
	prior := companion
	backed := new(big.Int).Add(companion.nftBackedAmount().BigInt(), expected.ReportedReserveAmount.BigInt())
	if backed.BitLen() > 256 {
		return errors.New("Gateway NFT withdrawal prestate exceeds 256 bits")
	}
	prior.NFTBacked = sdkmath.NewIntFromBigInt(backed)
	owner, operator := delegation.Delegator, delegation.Validator
	child, commit := ctx.CacheContext()
	if companionAcknowledged && prior.NFTBacked.IsPositive() {
		previous, err := k.GetEVMStakeBinding(child, gatewayDELCoinBinding, prior.Gateway[:], prior.StakeID[:])
		if err != nil {
			return err
		}
		prior.PenaltyCursor = common.BytesToHash(previous.PenaltyCursor)
		if bytes.Compare(prior.PenaltyCursor[:], cursor[:]) >= 0 {
			return errors.New("NFT-only companion acknowledgement must advance the native cursor")
		}
	}
	oldCompanion, _, err := k.checkGatewayDELPrestate(child, prior, prior.Amount, owner, operator)
	if err != nil {
		return err
	}
	active, err := k.GetEVMStakeBinding(child, expected.Kind, expected.Gateway, expected.StakeID)
	if err != nil {
		return err
	}
	expected.CreatedHeight, expected.UpdatedHeight = active.CreatedHeight, active.UpdatedHeight
	actualBytes, err := types.CanonicalBindingBytes(&active)
	if err != nil {
		return err
	}
	expectedBytes, err := types.CanonicalBindingBytes(&expected)
	if err != nil {
		return err
	}
	if active.UpdatedHeight > ctx.BlockHeight() || !bytes.Equal(actualBytes, expectedBytes) {
		return errors.New("Gateway NFT withdrawal differs from the active native binding")
	}
	previous, err := k.GetDelegation(child, owner, operator, types.StakeType_NFT, delegation.Stake.ID)
	if err != nil {
		return err
	}
	if !previous.Stake.Equal(delegation.Stake) {
		return errors.New("Gateway NFT withdrawal differs from the native delegation")
	}
	if _, err := k.GetEVMFrozenBinding(child, active.Gateway, index[:]); err == nil {
		return errors.New("Gateway frozen ID already has a native binding")
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	entry := types.UndelegationEntry{CreationHeight: ctx.BlockHeight(), CompletionTime: deadline.UTC(), Stake: delegation.Stake}
	entryHash, err := types.NativeQueueEntryHash(1, owner, operator, "", &entry)
	if err != nil {
		return err
	}
	frozen := types.EVMFrozenBinding{
		Gateway: active.Gateway, FrozenIndex: index.Bytes(), SourceKind: active.Kind, SourceStakeID: active.StakeID,
		SourceValidator: active.Validator, Beneficiary: active.Beneficiary, FreezeType: 1,
		CompletionTimestamp: common.BigToHash(big.NewInt(deadline.Unix())).Bytes(), RawAmount: active.RawAmount,
		ReportedReserveAmount: active.ReportedReserveAmount, ReserveAsset: active.ReserveAsset,
		NFTPosition: active.NFTPosition, NativeTarget: active.NativeTarget, NativeEntryHash: entryHash,
		Active: true, CreatedHeight: ctx.BlockHeight(), UpdatedHeight: ctx.BlockHeight(),
	}
	remove := BindingReference{Kind: active.Kind, Gateway: active.Gateway, ID: active.StakeID}
	mutations := []BindingMutation{{Remove: &remove}, {Frozen: &frozen}}
	if expected.ReportedReserveAmount.IsPositive() || (companionAcknowledged && oldCompanion != nil) {
		coin, err := gatewayNFTCompanionMutation(child, companion, oldCompanion)
		if err != nil {
			return err
		}
		mutations = append(mutations, coin)
	}
	plan, err := k.PrepareBindingPlan(child, mutations)
	if err != nil {
		return err
	}
	if err := k.ApplyDelegationBatch(child, []DelegationChange{{Expected: &previous}}); err != nil {
		return err
	}
	if err := k.ApplyBindingPlan(child, plan); err != nil {
		return err
	}
	if err := k.appendGatewayUndelegation(child, owner, operator, entry); err != nil {
		return err
	}
	if cursor != (common.Hash{}) {
		if err := k.service.OpenKVStore(child).Set(frozenDELPenaltyCursorKey(frozen.Gateway, frozen.FrozenIndex), cursor[:]); err != nil {
			return err
		}
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_nft_withdrawal_requested",
		sdk.NewAttribute("stake_id", position.StakeID.Hex()), sdk.NewAttribute("position_id", common.BytesToHash(active.TokenID).Hex()),
		sdk.NewAttribute("frozen_index", index.Hex()), sdk.NewAttribute("reserve", active.ReportedReserveAmount.String())))
	commit()
	return nil
}
