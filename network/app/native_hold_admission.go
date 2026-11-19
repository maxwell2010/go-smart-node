package app

import (
	"errors"

	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// Hold work is bounded for the affected owner/operator pair, independently of
// unrelated delegators. Native hold buckets share that pair's delegation row.
func (c *withdrawalController) nativePairHoldLots(ctx sdk.Context, validator, beneficiary common.Address) (int, error) {
	operator, err := c.keeper.ValidatorAddressCodec().BytesToString(validator[:])
	if err != nil {
		return 0, err
	}
	owner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(beneficiary[:])
	if err != nil {
		return 0, err
	}
	delegation, err := c.keeper.GetDelegation(ctx, owner, operator, validatortypes.StakeType_CoinHold, identity.BaseDenom)
	if err != nil && !errors.Is(err, validatorkeeper.ErrNotFound) {
		return 0, err
	}
	count := len(delegation.Stake.Holds)
	pending, err := c.keeper.GetUndelegation(ctx, owner, operator)
	if err != nil && !errors.Is(err, validatorkeeper.ErrNotFound) {
		return 0, err
	}
	for _, entry := range pending.Entries {
		if entry.Stake.Type == validatortypes.StakeType_CoinHold {
			count += len(entry.Stake.Holds)
		}
	}
	return count, nil
}
