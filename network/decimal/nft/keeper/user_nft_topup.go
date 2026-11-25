package keeper

import (
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func (k *StateKeeper) BurnReservelessUnits(ctx sdk.Context, nativeID string, owner sdk.AccAddress, quantity uint32) ([]uint32, error) {
	token, err := k.GetToken(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	if !token.ReserveLess || !token.Reserve.IsZero() || token.Reserve.Denom != identity.BaseDenom {
		return nil, errors.New("invalid reserveless NFT retirement")
	}
	return k.retireUserNFTUnits(ctx, token, owner, quantity)
}

// IncreaseUserNFTReserve changes the default for active units only. Archived
// isolated positions retain their own reserve snapshot. Refuse custodial units
// and legacy individually priced units whose stake or payout would diverge.
func (k *StateKeeper) IncreaseUserNFTReserve(ctx sdk.Context, nativeID string, previous, unit sdk.Coin, gateway, vault sdk.AccAddress) error {
	token, err := k.GetToken(ctx, nativeID)
	if err != nil {
		return err
	}
	if !token.Reserve.IsEqual(previous) || token.ReserveLess || token.IsolatedReceipt || token.Minted > 1024 || token.Minted == token.Burnt ||
		unit.Denom != identity.BaseDenom || token.Reserve.Denom != unit.Denom || !unit.Amount.GT(token.Reserve.Amount) {
		return errors.New("invalid user NFT reserve increase")
	}
	var active uint32
	for id := uint32(1); id <= token.Minted; id++ {
		sub, err := k.getIndexedSubToken(ctx, token, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		owner, err := account(sub.Owner)
		if err != nil {
			return err
		}
		if sub.Reserve != nil || owner.Equals(gateway) || owner.Equals(vault) {
			return errors.New("NFT top-up requires uniform wallet origin units")
		}
		active++
	}
	if active != token.Minted-token.Burnt {
		return errors.New("active NFT supply mismatch")
	}
	token.Reserve = unit
	return k.put(ctx, types.GetTokenKey(token.ID), &token)
}
