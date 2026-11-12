package keeper

import (
	"bytes"
	"errors"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// ReadActiveDELVotingState returns the canonical active ownership projection
// for the bound DEL ledger. It does not read pending queues, frozen records,
// completion history or consensus history. Binding revisions can change with
// those records and are deliberately omitted from the active fingerprint.
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
	validators, err := k.GetValidators(ctx)
	if err != nil {
		return nil, err
	}
	state := k.service.OpenKVStore(ctx)
	snapshot := &types.GenesisState{BindingDomain: &domain, Params: params, Validators: validators}
	counts := make(map[string]uint32, len(validators))
	err = k.scan(ctx, delegationPrefix, func(key, data []byte) error {
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
	})
	if err != nil {
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
	err = k.scan(ctx, stakeBindingPrefix, func(key, data []byte) error {
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
	})
	if err != nil {
		return nil, err
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	return snapshot, nil
}
