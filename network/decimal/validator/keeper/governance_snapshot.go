package keeper

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// ReadActiveDELVotingState returns the canonical active ownership projection
// for the bound DEL ledger. In a live-roster profile only positions of
// registered operators without permanent exclusions carry voting weight.
// Pending queues, frozen records, and historical records are outside the
// projection, so their changes do not affect the active fingerprint.
func (k *StateKeeper) ReadActiveDELVotingState(ctx sdk.Context) (*types.GenesisState, error) {
	domain, err := k.GetEVMSourceDomain(ctx)
	if err != nil {
		return nil, err
	}
	if err = k.checkDelegationIndex(ctx); err != nil {
		return nil, err
	}
	indexed, err := k.hasActiveDELReserveIndex(ctx)
	if err != nil {
		return nil, err
	}
	if !indexed {
		return nil, ErrActiveDELReserveIndexUnavailable
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	if params.BaseDenom != identity.BaseDenom {
		return nil, errors.New("native voting requires the active DEL ledger")
	}
	operators, err := k.NativeLiveValidatorOperators(ctx)
	useRoster := !errors.Is(err, ErrNotFound)
	var validators []types.Validator
	if useRoster {
		if err != nil {
			return nil, err
		}
		for _, operator := range operators {
			validator, err := k.GetValidator(ctx, operator)
			if err != nil {
				return nil, err
			}
			validators = append(validators, validator)
		}
	} else {
		validators, err = k.GetValidators(ctx)
		if err != nil {
			return nil, err
		}
	}
	state := k.service.OpenKVStore(ctx)
	snapshot := &types.GenesisState{BindingDomain: &domain, Params: params, Validators: validators}
	counts := make(map[string]uint32, len(validators))
	readDelegation := func(operator string, key, data []byte) error {
		if len(data) > MaxRecordBytes {
			return errors.New("native voting delegation exceeds the record size limit")
		}
		var row types.Delegation
		if err := proto.Unmarshal(data, &row); err != nil {
			return err
		}
		canonical, err := proto.Marshal(&row)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, canonical) || !bytes.Equal(key, delegationKey(row)) {
			return errors.New("native voting delegation key or encoding mismatch")
		}
		if operator != "" && row.Validator != operator {
			return errors.New("native voting delegation reverse index operator mismatch")
		}
		if err := row.Stake.Validate(); err != nil {
			return err
		}
		if row.Stake.Stake.Denom != identity.BaseDenom {
			return errors.New("native voting requires DEL-backed positions")
		}
		index, err := state.Get(validatorDelegationKey(row))
		if err != nil {
			return err
		}
		if !bytes.Equal(index, key) {
			return errors.New("native voting delegation reverse index mismatch")
		}
		if counts[row.Validator] >= params.MaxDelegations {
			return errors.New("native voting delegation count exceeds the configured validator limit")
		}
		counts[row.Validator]++
		snapshot.Delegations = append(snapshot.Delegations, row)
		return nil
	}
	if useRoster {
		for _, operator := range operators {
			prefix := composite(validatorDelegationPrefix, operator)
			err = k.scanPrefix(ctx, prefix, func(index, key []byte) error {
				if len(key) == 0 || key[0] != delegationPrefix || !bytes.HasPrefix(index, prefix) || !bytes.Equal(index[len(prefix):], key) {
					return errors.New("native voting delegation reverse index key mismatch")
				}
				data, err := state.Get(key)
				if err != nil {
					return err
				}
				return readDelegation(operator, key, data)
			})
			if err != nil {
				return nil, err
			}
		}
		slices.SortFunc(snapshot.Delegations, func(a, b types.Delegation) int {
			return bytes.Compare(delegationKey(a), delegationKey(b))
		})
	} else if err := k.scan(ctx, delegationPrefix, func(key, data []byte) error {
		return readDelegation("", key, data)
	}); err != nil {
		return nil, err
	}
	totals, err := activeDELTotals(validators, snapshot.Delegations)
	if err != nil {
		return nil, err
	}
	for _, validator := range validators {
		operator := validator.OperatorAddress
		count, err := k.delegationCount(ctx, operator)
		if err != nil {
			return nil, err
		}
		reserve, err := k.readActiveDELReserve(ctx, operator)
		if err != nil {
			return nil, err
		}
		if count != counts[operator] || reserve.Cmp(totals[operator]) != 0 {
			return nil, fmt.Errorf("native voting positions differ from indexed DEL ownership for %s", operator)
		}
	}
	if useRoster {
		codec := k.ValidatorAddressCodec()
		for _, operator := range operators {
			address, err := codec.StringToBytes(operator)
			if err != nil {
				return nil, err
			}
			var cursor []byte
			for {
				page, next, err := k.GetActiveStakePenaltyPage(ctx, address, cursor, types.MaxBindingPageSize)
				if err != nil {
					return nil, err
				}
				snapshot.EVMStakeBindings = append(snapshot.EVMStakeBindings, page...)
				if next == nil {
					break
				}
				cursor = next
			}
		}
		slices.SortFunc(snapshot.EVMStakeBindings, func(a, b types.EVMStakeBinding) int {
			return cmp.Or(cmp.Compare(a.Kind, b.Kind), bytes.Compare(a.Gateway, b.Gateway), bytes.Compare(a.StakeID, b.StakeID))
		})
	} else if err := k.scan(ctx, stakeBindingPrefix, func(key, data []byte) error {
		row, err := decodeBindingRecord(key, data)
		if err != nil {
			return err
		}
		if row.active() {
			if err := k.checkBindingIndexes(ctx, row); err != nil {
				return err
			}
			snapshot.EVMStakeBindings = append(snapshot.EVMStakeBindings, *row.stake)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	return snapshot, nil
}
