package keeper

import (
	"bytes"
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// ApplyGatewayIsolatedDELPenalty compares and updates the live backed receipt.
// The caller authenticates the actual vault loss and burn and commits this
// together with the active staking binding and companion update.
func (k *StateKeeper) ApplyGatewayIsolatedDELPenalty(ctx sdk.Context, gateway common.Address, expected types.IsolatedPositionLineage, remaining sdkmath.Int) error {
	if gateway == (common.Address{}) || remaining.IsNil() || remaining.IsNegative() || expected.Reserve.Kind != types.IsolatedReserveDEL {
		return errors.New("invalid isolated NFT penalty identity or remaining DEL reserve")
	}
	child, write := ctx.CacheContext()
	lineage, err := k.GetIsolatedLineage(child, expected.Vault, expected.PositionID)
	if err != nil {
		return err
	}
	if !lineage.Equal(&expected) {
		return errors.New("isolated NFT penalty differs from the current native lineage")
	}
	before := new(big.Int).SetBytes(lineage.Reserve.Amount)
	if remaining.BigInt().Cmp(before) > 0 {
		return errors.New("isolated NFT penalty cannot increase reserve")
	}
	token, err := k.GetToken(child, lineage.ReceiptNativeTokenID)
	if err != nil {
		return err
	}
	unit, err := k.getIndexedSubToken(child, token, lineage.ReceiptSubTokenID)
	if err != nil {
		return err
	}
	owner, err := account(unit.Owner)
	if err != nil {
		return err
	}
	if !bytes.Equal(owner, gateway[:]) {
		return errors.New("isolated NFT penalty requires the live receipt in Gateway custody")
	}
	if token.Reserve.Denom != identity.BaseDenom || token.Reserve.Amount.BigInt().Cmp(before) != 0 ||
		(unit.Reserve != nil && (unit.Reserve.Denom != identity.BaseDenom || unit.Reserve.Amount.BigInt().Cmp(before) != 0)) {
		return errors.New("isolated NFT penalty differs from the current native receipt reserve")
	}
	if remaining.BigInt().Cmp(before) == 0 {
		return nil
	}
	token.Reserve = sdk.NewCoin(identity.BaseDenom, remaining)
	if err := token.Validate(); err != nil {
		return err
	}
	lineage.Reserve.Amount = common.BigToHash(remaining.BigInt()).Bytes()
	if err := k.put(child, types.GetTokenKey(token.ID), &token); err != nil {
		return err
	}
	if unit.Reserve != nil {
		reserve := token.Reserve
		unit.Reserve = &reserve
		if err := k.put(child, types.GetSubTokenKey(token.ID, unit.ID), &unit); err != nil {
			return err
		}
	}
	if err := k.put(child, types.GetIsolatedPositionKey(lineage.Vault, lineage.PositionID), &lineage); err != nil {
		return err
	}
	write()
	return nil
}
