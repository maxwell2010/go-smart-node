package keeper

import (
	"bytes"
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// GatewayNFTPenalty describes one verified active isolated DEL-backed NFT
// penalty. The NFT cursor is independent of the companion coin cursor.
type GatewayNFTPenalty struct {
	Before                    GatewayNFTPosition
	BeforeCursor, AfterCursor common.Hash
	RemainingReserve          sdkmath.Int
}

// ApplyGatewayNFTPenalty projects an authenticated single EVM penalty and
// native supply burn into both keepers. The caller owns EVM verification and
// commits the projection in the same outer cache. This method does not burn
// funds, change effective Comet power or touch queued withdrawals.
func (k *StateKeeper) ApplyGatewayNFTPenalty(ctx sdk.Context, nft *nftkeeper.StateKeeper, penalty GatewayNFTPenalty) error {
	return k.applyGatewayNFTPenalty(ctx, nft, penalty, 0, nil)
}

// ApplyGatewayActiveHistoricalNFTPenalty projects the exact historical loss
// from one active isolated NFT in the same cache as its verified EVM debit.
func (k *StateKeeper) ApplyGatewayActiveHistoricalNFTPenalty(ctx sdk.Context, nft *nftkeeper.StateKeeper, penalty GatewayNFTPenalty,
	infractionHeight int64, eligible bool,
) error {
	return k.applyGatewayNFTPenalty(ctx, nft, penalty, infractionHeight, &eligible)
}

func (k *StateKeeper) applyGatewayNFTPenalty(ctx sdk.Context, nft *nftkeeper.StateKeeper, penalty GatewayNFTPenalty,
	infractionHeight int64, historicalEligibility *bool,
) error {
	if nft == nil || ctx.BlockHeight() <= 0 || penalty.RemainingReserve.IsNil() || penalty.RemainingReserve.IsNegative() {
		return errors.New("invalid active isolated NFT penalty projection")
	}
	nextCursor := new(big.Int).Add(penalty.BeforeCursor.Big(), big.NewInt(1))
	if nextCursor.Cmp(penalty.AfterCursor.Big()) != 0 {
		return errors.New("isolated NFT penalty cursor must advance exactly once")
	}
	child, write := ctx.CacheContext()
	delegation, expected, err := k.gatewayNFTState(child, penalty.Before, penalty.BeforeCursor)
	if err != nil {
		return err
	}
	before := expected.ReportedReserveAmount
	companion := penalty.Before.Companion
	backed := companion.nftBackedAmount()
	if penalty.RemainingReserve.GT(before) || backed.LT(before) {
		return errors.New("isolated NFT penalty exceeds the position or companion reserve")
	}
	owner, operator := delegation.Delegator, delegation.Validator
	oldCompanion, _, err := k.checkGatewayDELPrestate(child, companion, companion.Amount, owner, operator)
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
		return errors.New("isolated NFT penalty differs from the active native binding")
	}
	previous, err := k.GetDelegation(child, owner, operator, types.StakeType_NFT, delegation.Stake.ID)
	if err != nil {
		return err
	}
	if !previous.Stake.Equal(delegation.Stake) {
		return errors.New("isolated NFT penalty differs from the active native delegation")
	}
	if historicalEligibility != nil {
		plan, err := k.planActiveNFTHistoricalPenalty(child, active, infractionHeight)
		if err != nil {
			return err
		}
		if plan.Eligible != *historicalEligibility || !plan.Remaining.Equal(penalty.RemainingReserve) {
			return errors.New("active NFT penalty differs from the historical exposure")
		}
	}
	reserve, err := k.GetActiveDELReserve(child, operator)
	if err != nil {
		return err
	}
	if reserve.LT(before) {
		return errors.New("active DEL reserve is below the penalized isolated NFT position")
	}
	loss := before.Sub(penalty.RemainingReserve)
	active.ReportedReserveAmount = penalty.RemainingReserve
	active.PenaltyCursor = penalty.AfterCursor.Bytes()
	active.UpdatedHeight = ctx.BlockHeight()
	if penalty.RemainingReserve.IsZero() {
		// An active zero-reserve receipt still owns its exact NFT units. It
		// no longer contributes to the separate logical DEL companion.
		active.CoinCompanionStakeID = nil
	}
	mutations := []BindingMutation{{Stake: &active}}
	if loss.IsPositive() {
		companion.NFTBacked = backed.Sub(loss)
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
	if err := nft.ApplyGatewayIsolatedDELPenalty(child, companion.Gateway, penalty.Before.Lineage, penalty.RemainingReserve); err != nil {
		return err
	}
	if loss.IsPositive() {
		next := previous
		next.Stake.Stake = sdk.NewCoin(identity.BaseDenom, penalty.RemainingReserve)
		if err := k.ApplyDelegationBatch(child, []DelegationChange{{Expected: &previous, Next: &next}}); err != nil {
			return err
		}
	}
	if err := k.ApplyBindingPlan(child, plan); err != nil {
		return err
	}
	remaining, err := k.GetActiveDELReserve(child, operator)
	if err != nil {
		return err
	}
	if !remaining.Equal(reserve.Sub(loss)) {
		return errors.New("native active DEL reserve did not decrease by the isolated NFT penalty loss")
	}
	child.EventManager().EmitEvent(sdk.NewEvent("decimal_nft_penalty_applied",
		sdk.NewAttribute("stake_id", penalty.Before.StakeID.Hex()),
		sdk.NewAttribute("position_id", common.BytesToHash(active.TokenID).Hex()),
		sdk.NewAttribute("previous_cursor", penalty.BeforeCursor.Hex()),
		sdk.NewAttribute("penalty_cursor", penalty.AfterCursor.Hex()),
		sdk.NewAttribute("amount", loss.String())))
	write()
	return nil
}
