package keeper

import (
	"bytes"
	"errors"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const (
	maxPairDelegations     = 256
	maxPairDelegationBytes = 2 << 20
)

var ErrDelegationQueryLimit = errors.New("native delegation pair exceeds the complete query response limit")

// GetPairDelegations reads one exact framed owner/operator prefix. This query
// has no pagination in its existing schema, so it refuses oversized pairs.
func (k *StateKeeper) GetPairDelegations(ctx sdk.Context, owner, operator string) (rows []types.Delegation, err error) {
	prefix := composite(delegationPrefix, owner, operator)
	iterator, err := k.service.OpenKVStore(ctx).Iterator(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, iterator.Close()) }()
	var totalBytes int
	for ; iterator.Valid(); iterator.Next() {
		if err := iterator.Error(); err != nil {
			return nil, err
		}
		raw := iterator.Value()
		if len(raw) > MaxRecordBytes || len(rows) == maxPairDelegations || totalBytes+len(raw)+4 > maxPairDelegationBytes {
			return nil, ErrDelegationQueryLimit
		}
		var row types.Delegation
		if err := proto.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		if row.Delegator != owner || row.Validator != operator || !bytes.Equal(iterator.Key(), delegationKey(row)) {
			return nil, errors.New("native delegation key mismatch")
		}
		if err := row.Stake.Validate(); err != nil {
			return nil, err
		}
		// The one-byte repeated-field tag and size varint use at most four
		// bytes per row at MaxRecordBytes.
		totalBytes += proto.Size(&row) + 4
		if totalBytes > maxPairDelegationBytes {
			return nil, ErrDelegationQueryLimit
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// GetUndelegation reads one bounded queue record for the exact native pair.
func (k *StateKeeper) GetUndelegation(ctx sdk.Context, owner, operator string) (types.Undelegation, error) {
	var row types.Undelegation
	if err := k.read(ctx, composite(undelegationPrefix, owner, operator), &row); err != nil {
		return types.Undelegation{}, err
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return types.Undelegation{}, err
	}
	if row.Delegator != owner || row.Validator != operator || len(row.Entries) == 0 || uint64(len(row.Entries)) > uint64(params.MaxEntries) {
		return types.Undelegation{}, errors.New("invalid native undelegation pair")
	}
	for _, entry := range row.Entries {
		if entry.CreationHeight < 0 || entry.CompletionTime.IsZero() {
			return types.Undelegation{}, errors.New("invalid native undelegation entry timing")
		}
		if err := entry.Stake.Validate(); err != nil {
			return types.Undelegation{}, err
		}
	}
	return row, nil
}
