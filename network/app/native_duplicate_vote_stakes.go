package app

import (
	"errors"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

// Reconcile all of one validator's custody under the exact typed Master entry.
// The caller owns the outer cache, jail, and durable evidence completion.
func (c *withdrawalController) reconcileNativeDuplicateVoteStakes(ctx sdk.Context, master settlement.MasterPenaltyProof,
	supply func(sdk.Context) sdkmath.Int,
) error {
	validator := master.Validator.Bytes()
	active, frozen, err := c.ordinaryDowntimePositions(ctx, validator)
	if err != nil {
		return err
	}
	for _, row := range active {
		if row.Kind != validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT {
			continue
		}
		plan, err := c.keeper.PlanActiveNFTHistoricalPenalty(ctx, row, master.InfractionHeight)
		if err != nil {
			return err
		}
		err = c.executor.ApplyActiveHistoricalNFTPenalty(ctx, row, master, plan.Eligible, supply, func(next sdk.Context, proof settlement.ActiveNFTPenaltyProof) error {
			penalty, err := c.activeNFTPenaltyProjection(next, row, proof)
			if err != nil {
				return err
			}
			return c.keeper.ApplyGatewayActiveHistoricalNFTPenalty(next, c.nft, penalty, master.InfractionHeight, proof.Eligible)
		})
		if err != nil {
			return err
		}
	}
	for _, row := range active {
		if row.Kind != validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN {
			continue
		}
		if !row.RawNftBackedAmount.IsZero() {
			current, err := c.keeper.GetEVMStakeBinding(ctx, row.Kind, row.Gateway, row.StakeID)
			if errors.Is(err, validatorkeeper.ErrNotFound) && row.RawAmount.Equal(row.RawNftBackedAmount) {
				continue
			}
			if err != nil {
				return err
			}
			row = current
		}
		plan, err := c.keeper.PlanActiveDELHistoricalPenalty(ctx, row, master.InfractionHeight)
		if err != nil {
			return err
		}
		err = c.executor.ApplyActiveHistoricalDELPenalty(ctx, row, master, plan.Eligible, supply, func(next sdk.Context, proof settlement.ActiveDELPenaltyProof) error {
			before := validatorkeeper.GatewayDELStake{
				Gateway: c.profile.Gateway, WrappedDEL: c.profile.WrappedDEL, Validator: master.Validator,
				Beneficiary: common.BytesToAddress(row.Beneficiary), StakeID: common.BytesToHash(row.StakeID),
				Amount: row.RawAmount.Sub(row.RawNftBackedAmount), NFTBacked: row.RawNftBackedAmount,
				PenaltyCursor: master.BeforeCursor, HoldStartTime: proof.Before.NativeHoldStartTime,
				HoldDeadline: common.BytesToHash(proof.Before.HoldTimestamp).Big().Int64(),
			}
			return c.keeper.ApplyGatewayActiveHistoricalDELPenalty(next, before, master.InfractionHeight,
				proof.After.RawAmount.Sub(row.RawNftBackedAmount), master.AfterCursor)
		})
		if err != nil {
			return err
		}
	}
	for _, row := range frozen {
		if err := c.reconcileNativeDuplicateVoteClaim(ctx, row, master, supply); err != nil {
			return err
		}
	}
	return c.keeper.RequireActiveStakePenaltyCatchup(ctx, validator, master.AfterCursor[:])
}

func (c *withdrawalController) reconcileNativeDuplicateVoteClaim(ctx sdk.Context, row validatortypes.EVMFrozenBinding,
	master settlement.MasterPenaltyProof, supply func(sdk.Context) sdkmath.Int,
) error {
	if row.SourceKind == validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT {
		plan, err := c.keeper.PlanFrozenNFTHistoricalPenalty(ctx, row, master.InfractionHeight)
		if err != nil {
			return err
		}
		return c.executor.ApplyFrozenHistoricalNFTPenalty(ctx, row, master, plan.Eligible, supply, func(next sdk.Context, proof settlement.FrozenNFTPenaltyProof) error {
			return c.keeper.ApplyGatewayFrozenHistoricalNFTPenalty(next, c.nft, row, master.InfractionHeight,
				proof.BeforeCursor, proof.AfterCursor, proof.RemainingReserve, proof.Eligible)
		})
	}
	plan, err := c.keeper.PlanFrozenDELHistoricalPenalty(ctx, row, master.InfractionHeight)
	if err != nil {
		return err
	}
	return c.executor.ApplyFrozenHistoricalDELPenalty(ctx, row, master, plan.Eligible, supply, func(next sdk.Context, proof settlement.FrozenDELPenaltyProof) error {
		var hold *validatorkeeper.GatewayFrozenDELHold
		if row.NativeTarget.StakeType == validatortypes.StakeType_CoinHold {
			hold = &validatorkeeper.GatewayFrozenDELHold{
				WrappedDEL: c.profile.WrappedDEL, HoldDeadline: proof.HoldDeadline, HoldStartTime: proof.HoldStartTime,
			}
		}
		return c.keeper.ApplyGatewayFrozenHistoricalDELPenalty(next, row, hold, master.InfractionHeight,
			proof.BeforeCursor, proof.AfterCursor, proof.Remaining)
	})
}
