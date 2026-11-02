// Package adapter exposes native validator metadata to the narrow EVM reader.
// It is not an SDK staking ledger or a financial precompile adapter.
package adapter

import (
	"bytes"
	"context"
	"errors"
	"math/big"

	coreaddress "cosmossdk.io/core/address"
	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	vmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/cosmos/gogoproto/proto"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	nativetypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// EVM has exactly the four read-only methods required by Cosmos EVM. Native
// state is private; financial setters and SDK governance iterators are absent.
type EVM struct {
	native    *nativekeeper.StateKeeper
	reduction sdkmath.Int
}

var _ vmtypes.StakingKeeper = (*EVM)(nil)

// NewEVM requires an explicit power container scale, independent of mutable SDK
// global power-reduction defaults. Tokens in returned SDK structs are ONLY
// native integer power multiplied by this scale, never bank supply or shares.
func NewEVM(native *nativekeeper.StateKeeper, powerReduction sdkmath.Int) (*EVM, error) {
	if native == nil {
		return nil, errors.New("native validator keeper is missing")
	}
	if powerReduction.IsNil() || !powerReduction.IsPositive() {
		return nil, errors.New("power container scale must be initialized and positive")
	}
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	maximum.Quo(maximum, big.NewInt(cmttypes.MaxTotalVotingPower))
	if powerReduction.BigInt().Cmp(maximum) > 0 {
		return nil, errors.New("power container scale can overflow native consensus power")
	}
	return &EVM{native: native, reduction: sdkmath.NewIntFromBigInt(powerReduction.BigInt())}, nil
}

func (a *EVM) ValidatorAddressCodec() coreaddress.Codec {
	return address.NewBech32Codec(identity.Bech32PrefixValAddr)
}
func (a *EVM) BondDenom(ctx context.Context) (string, error) {
	if a == nil || a.native == nil {
		return "", errors.New("native validator adapter is not initialized")
	}
	denom, e := a.native.BondDenom(ctx)
	if e != nil {
		return "", e
	}
	if denom != identity.BaseDenom {
		return "", errors.New("EVM native adapter requires del bond denomination")
	}
	return denom, nil
}

func (a *EVM) project(v nativetypes.Validator, full bool) (stakingtypes.Validator, error) {
	if full {
		if e := v.Validate(); e != nil {
			return stakingtypes.Validator{}, e
		}
	}
	operator, e := a.ValidatorAddressCodec().StringToBytes(v.OperatorAddress)
	if e != nil {
		return stakingtypes.Validator{}, e
	}
	if len(operator) != 20 {
		return stakingtypes.Validator{}, errors.New("native operator address must contain 20 bytes")
	}
	if v.Stake < 0 || v.Stake > cmttypes.MaxTotalVotingPower {
		return stakingtypes.Validator{}, errors.New("native power exceeds consensus bounds")
	}
	var status stakingtypes.BondStatus
	switch v.Status {
	case nativetypes.BondStatus_Unbonded:
		status = stakingtypes.Unbonded
	case nativetypes.BondStatus_Unbonding:
		status = stakingtypes.Unbonding
	case nativetypes.BondStatus_Bonded:
		status = stakingtypes.Bonded
	default:
		return stakingtypes.Validator{}, errors.New("invalid native bond status")
	}
	key, e := v.ConsensusPubKey()
	if e != nil {
		return stakingtypes.Validator{}, e
	}
	anyKey, e := codectypes.NewAnyWithValue(key)
	if e != nil {
		return stakingtypes.Validator{}, e
	}
	// The constructor bounds the multiplication for every allowed native power.
	tokens := sdkmath.NewInt(v.Stake).Mul(a.reduction)
	result := stakingtypes.Validator{OperatorAddress: v.OperatorAddress, ConsensusPubkey: anyKey, Jailed: v.Jailed, Status: status, Tokens: tokens,
		Description:     stakingtypes.Description{Moniker: v.Description.Moniker, Identity: v.Description.Identity, Website: v.Description.Website, SecurityContact: v.Description.SecurityContact, Details: v.Description.Details},
		UnbondingHeight: v.UnbondingHeight, UnbondingTime: v.UnbondingTime}
	// DelegatorShares, commission policy and minimum self-delegation are not
	// supplied: native multi-asset accounting has no equivalent SDK share ledger.
	return result, nil
}

func (a *EVM) GetValidatorByConsAddr(ctx context.Context, consensus sdk.ConsAddress) (stakingtypes.Validator, error) {
	if _, e := a.BondDenom(ctx); e != nil {
		return stakingtypes.Validator{}, e
	}
	v, e := a.native.GetValidatorByConsAddr(ctx, consensus)
	if e != nil {
		return stakingtypes.Validator{}, e
	}
	return a.project(v, true)
}

func (a *EVM) GetHistoricalInfo(ctx context.Context, height int64) (stakingtypes.HistoricalInfo, error) {
	if _, e := a.BondDenom(ctx); e != nil {
		return stakingtypes.HistoricalInfo{}, e
	}
	h, e := a.native.GetHistoricalInfo(ctx, height)
	if e != nil {
		return stakingtypes.HistoricalInfo{}, e
	}
	// A historical effective set must not be replaced with current dynamic rows.
	// Copy the header independently through protobuf, including all hash bytes.
	data, e := proto.Marshal(&h.Header)
	if e != nil {
		return stakingtypes.HistoricalInfo{}, e
	}
	var header cmtproto.Header
	if e = proto.Unmarshal(data, &header); e != nil {
		return stakingtypes.HistoricalInfo{}, e
	}
	result := stakingtypes.HistoricalInfo{Header: header}
	var consensus []*cmttypes.Validator
	for _, v := range h.Valset {
		projected, e := a.project(v, false)
		if e != nil {
			return stakingtypes.HistoricalInfo{}, e
		}
		if projected.ConsensusPower(a.reduction) != v.ConsensusPower() {
			return stakingtypes.HistoricalInfo{}, errors.New("SDK metadata power projection changed native power")
		}
		pk, e := v.ConsensusPubKey()
		if e != nil {
			return stakingtypes.HistoricalInfo{}, e
		}
		cmtKey, e := cryptocodec.ToCmtPubKeyInterface(pk)
		if e != nil {
			return stakingtypes.HistoricalInfo{}, e
		}
		consensus = append(consensus, cmttypes.NewValidator(cmtKey, projected.ConsensusPower(a.reduction)))
		result.Valset = append(result.Valset, projected)
	}
	if len(consensus) == 0 || !bytes.Equal(cmttypes.NewValidatorSet(consensus).Hash(), header.ValidatorsHash) {
		return stakingtypes.HistoricalInfo{}, errors.New("SDK metadata projection does not match historical Comet hash")
	}
	return result, nil
}
