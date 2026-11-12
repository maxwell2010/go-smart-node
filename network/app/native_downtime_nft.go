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

func (c *withdrawalController) applyActiveNFTDowntime(ctx sdk.Context, row validatortypes.EVMStakeBinding, master settlement.MasterPenaltyProof,
	percent uint16, supply func(sdk.Context) sdkmath.Int,
) error {
	if common.BytesToHash(row.PenaltyCursor) != master.BeforeCursor {
		return errors.New("isolated NFT stake differs from the prior Master penalty cursor")
	}
	return c.executor.ApplyActiveNFTPenalty(ctx, row, percent, supply, func(next sdk.Context, proof settlement.ActiveNFTPenaltyProof) error {
		if proof.BeforeCursor != master.BeforeCursor || proof.AfterCursor != master.AfterCursor {
			return errors.New("isolated NFT stake differs from the announced Master penalty")
		}
		penalty, err := c.activeNFTPenaltyProjection(next, row, proof)
		if err != nil {
			return err
		}
		return c.keeper.ApplyGatewayNFTPenalty(next, c.nft, penalty)
	})
}

func (c *withdrawalController) activeNFTPenaltyProjection(ctx sdk.Context, row validatortypes.EVMStakeBinding,
	proof settlement.ActiveNFTPenaltyProof,
) (validatorkeeper.GatewayNFTPenalty, error) {
	lineage, err := c.nft.GetIsolatedLineage(ctx, row.TokenContract, row.TokenID)
	if err != nil {
		return validatorkeeper.GatewayNFTPenalty{}, err
	}
	companion := c.delStakePosition(proof.Companion)
	companion.PenaltyCursor = proof.CompanionCursor
	return validatorkeeper.GatewayNFTPenalty{
		Before: validatorkeeper.GatewayNFTPosition{
			Companion: companion, StakeID: common.BytesToHash(row.StakeID), Lineage: lineage,
		},
		BeforeCursor: proof.BeforeCursor, AfterCursor: proof.AfterCursor, RemainingReserve: proof.RemainingReserve,
	}, nil
}

func (c *withdrawalController) applyFrozenNFTDowntime(ctx sdk.Context, row validatortypes.EVMFrozenBinding, master settlement.MasterPenaltyProof,
	infractionHeight int64, percent uint16, supply func(sdk.Context) sdkmath.Int,
) error {
	return c.executor.ApplyFrozenNFTPenalty(ctx, row, infractionHeight, percent, supply, func(next sdk.Context, proof settlement.FrozenNFTPenaltyProof) error {
		if proof.BeforeCursor != master.BeforeCursor || proof.AfterCursor != master.AfterCursor {
			return errors.New("isolated NFT claim differs from the announced Master penalty")
		}
		return c.keeper.ApplyGatewayFrozenNFTPenalty(next, c.nft, row, infractionHeight, proof.BeforeCursor, proof.AfterCursor, proof.RemainingReserve)
	})
}
