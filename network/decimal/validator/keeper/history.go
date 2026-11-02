package keeper

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"

	cmttypes "github.com/cometbft/cometbft/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const MaxHistoricalBytes = 2 * 1024 * 1024

func historyKey(height int64) []byte {
	key := make([]byte, 9)
	key[0] = historyPrefix
	binary.BigEndian.PutUint64(key[1:], uint64(height))
	return key
}

// ConsensusSet projects actual native power directly to Comet validators.
// It creates neither SDK token balances nor delegation shares.
func ConsensusSet(vals []types.Validator) (*cmttypes.ValidatorSet, error) {
	var result []*cmttypes.Validator
	seen := map[string]bool{}
	var total int64
	for _, v := range vals {
		if v.Status != types.BondStatus_Bonded || v.Stake <= 0 || v.Stake > cmttypes.MaxTotalVotingPower-total {
			return nil, errors.New("invalid effective native consensus power")
		}
		pk, e := v.ConsensusPubKey()
		if e != nil {
			return nil, e
		}
		consensus, e := cryptocodec.ToCmtPubKeyInterface(pk)
		if e != nil {
			return nil, e
		}
		if seen[string(pk.Address())] {
			return nil, errors.New("duplicate effective consensus key")
		}
		seen[string(pk.Address())] = true
		total += v.Stake
		result = append(result, cmttypes.NewValidator(consensus, v.Stake))
	}
	if len(result) == 0 {
		return nil, errors.New("effective native consensus set is empty")
	}
	return cmttypes.NewValidatorSet(result), nil
}

func validateHistory(h types.HistoricalInfo) error {
	if e := h.ValidateBasic(); e != nil {
		return e
	}
	set, e := ConsensusSet(h.Valset)
	if e != nil {
		return e
	}
	if !bytes.Equal(h.Header.ValidatorsHash, set.Hash()) {
		return errors.New("historical native power does not match canonical header validators hash")
	}
	return nil
}

// SetHistoricalInfo takes the effective Comet set for this header explicitly.
// EndBlock changes take effect with Comet's validator-update delay; deriving
// this set blindly from current native records would be incorrect.
func (k *StateKeeper) SetHistoricalInfo(ctx sdk.Context, h types.HistoricalInfo) error {
	if e := validateHistory(h); e != nil {
		return e
	}
	p, e := k.GetParams(ctx)
	if e != nil {
		return e
	}
	if p.HistoricalEntries == 0 {
		return errors.New("native historical retention is disabled")
	}
	data, e := proto.Marshal(&h)
	if e != nil {
		return e
	}
	if len(data) > MaxHistoricalBytes {
		return errors.New("historical native record exceeds size limit")
	}
	child, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	key := historyKey(h.Header.Height)
	old, e := state.Get(key)
	if e != nil {
		return e
	}
	if old != nil && !bytes.Equal(old, data) {
		return errors.New("historical native record is immutable")
	}
	// Reject out-of-order new records so a stale header cannot prune live history.
	var remove [][]byte
	if e = k.scan(child, historyPrefix, func(existing, _ []byte) error {
		if len(existing) != 9 {
			return errors.New("malformed historical native key")
		}
		height := int64(binary.BigEndian.Uint64(existing[1:]))
		if height <= 0 {
			return errors.New("invalid historical height")
		}
		if height > h.Header.Height && old == nil {
			return errors.New("historical native insertion is out of order")
		}
		if h.Header.Height >= int64(p.HistoricalEntries) && height <= h.Header.Height-int64(p.HistoricalEntries) {
			remove = append(remove, existing)
		}
		return nil
	}); e != nil {
		return e
	}
	if e = state.Set(key, data); e != nil {
		return e
	}
	for _, stale := range remove {
		if e = state.Delete(stale); e != nil {
			return e
		}
	}
	commit()
	return nil
}

func (k *StateKeeper) GetHistoricalInfo(ctx context.Context, height int64) (types.HistoricalInfo, error) {
	if height <= 0 {
		return types.HistoricalInfo{}, errors.New("historical height must be positive")
	}
	data, e := k.service.OpenKVStore(sdk.UnwrapSDKContext(ctx)).Get(historyKey(height))
	if e != nil {
		return types.HistoricalInfo{}, e
	}
	if data == nil {
		return types.HistoricalInfo{}, ErrNotFound
	}
	if len(data) > MaxHistoricalBytes {
		return types.HistoricalInfo{}, errors.New("historical native record exceeds size limit")
	}
	var h types.HistoricalInfo
	if e = proto.Unmarshal(data, &h); e != nil {
		return h, e
	}
	if h.Header.Height != height {
		return types.HistoricalInfo{}, errors.New("historical native key mismatch")
	}
	if e = validateHistory(h); e != nil {
		return types.HistoricalInfo{}, e
	}
	return h, nil
}
