package governance

import (
	"context"
	"errors"

	coreaddress "cosmossdk.io/core/address"
	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func (a *Adapter) ValidatorAddressCodec() coreaddress.Codec {
	return address.NewBech32Codec(identity.Bech32PrefixValAddr)
}

// TotalBondedTokens is the SDK53 gov API name. This returns native voting
// weight units, never monetary supply, escrow, minted tokens or SDK shares.
func (a *Adapter) TotalBondedTokens(ctx context.Context) (sdkmath.Int, error) {
	if e := a.guard(ctx); e != nil {
		return sdkmath.Int{}, e
	}
	return sdkmath.NewIntFromBigInt(a.total.BigInt()), nil
}

func (a *Adapter) IterateBondedValidatorsByPower(ctx context.Context, fn func(int64, stakingtypes.ValidatorI) bool) error {
	if fn == nil {
		return errors.New("validator voting callback is missing")
	}
	if e := a.guard(ctx); e != nil {
		return e
	}
	for i, v := range a.validators {
		pk, e := v.native.ConsensusPubKey()
		if e != nil {
			return e
		}
		key, e := codectypes.NewAnyWithValue(pk)
		if e != nil {
			return e
		}
		// These ephemeral SDK fields are exact governance ratio operands. They
		// are not persisted, minted, redeemable or passed to financial precompiles.
		view := stakingtypes.Validator{OperatorAddress: v.native.OperatorAddress, ConsensusPubkey: key, Jailed: v.native.Jailed, Status: stakingtypes.Bonded, Tokens: sdkmath.NewIntFromBigInt(v.tokens.BigInt()), DelegatorShares: sdkmath.LegacyNewDecFromInt(v.ownership)}
		if fn(int64(i), view) {
			break
		}
	}
	return nil
}

// IterateDelegations groups every coin/NFT/hold-containing native position for
// the same owner/validator into one exact voting-weight ratio operand. SDK's
// default tally can then deduct that owner's override from validator influence.
func (a *Adapter) IterateDelegations(ctx context.Context, delegator sdk.AccAddress, fn func(int64, stakingtypes.DelegationI) bool) error {
	if fn == nil {
		return errors.New("delegation voting callback is missing")
	}
	if len(delegator) != 20 {
		return errors.New("voting delegator must contain 20 bytes")
	}
	if e := a.guard(ctx); e != nil {
		return e
	}
	owner, e := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(delegator)
	if e != nil {
		return e
	}
	for i, d := range a.delegations[owner] {
		view := stakingtypes.Delegation{DelegatorAddress: d.delegator, ValidatorAddress: d.validator, Shares: sdkmath.LegacyNewDecFromInt(d.ownership)}
		if fn(int64(i), view) {
			break
		}
	}
	return nil
}
