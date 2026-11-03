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
const historyProgressKey byte = 0x42

// These derived cursors are persisted with the history mutations. They are not
// a new genesis field. A legacy development store without the cursor needs one
// reverse seek to its newest key, not an iteration over its retained histories.
func (k *StateKeeper) historyProgress(ctx sdk.Context) (newest, prunedThrough int64, err error) {
	state := k.service.OpenKVStore(ctx)
	data, err := state.Get([]byte{historyProgressKey})
	if err != nil {
		return 0, 0, err
	}
	if data != nil {
		if len(data) != 17 || data[0] != 1 {
			return 0, 0, errors.New("malformed historical progress metadata")
		}
		newest = int64(binary.BigEndian.Uint64(data[1:9]))
		prunedThrough = int64(binary.BigEndian.Uint64(data[9:17]))
		if newest <= 0 || prunedThrough < 0 || prunedThrough >= newest {
			return 0, 0, errors.New("invalid historical progress heights")
		}
		latest, e := state.Get(historyKey(newest))
		if e != nil {
			return 0, 0, e
		}
		if latest == nil {
			return 0, 0, errors.New("historical progress references missing newest row")
		}
		return newest, prunedThrough, nil
	}
	it, err := state.ReverseIterator([]byte{historyPrefix}, []byte{historyPrefix + 1})
	if err != nil {
		return 0, 0, err
	}
	if it.Valid() {
		err = it.Error()
		if err == nil {
			key := it.Key()
			if len(key) != 9 || key[0] != historyPrefix {
				err = errors.New("malformed historical native key")
			} else {
				newest = int64(binary.BigEndian.Uint64(key[1:]))
				if newest <= 0 {
					err = errors.New("invalid historical height")
				}
			}
		}
	}
	closeErr := it.Close()
	if err != nil {
		return 0, 0, err
	}
	return newest, 0, closeErr
}

func (k *StateKeeper) setHistoryProgress(ctx sdk.Context, newest, prunedThrough int64) error {
	data := make([]byte, 17)
	data[0] = 1
	binary.BigEndian.PutUint64(data[1:9], uint64(newest))
	binary.BigEndian.PutUint64(data[9:17], uint64(prunedThrough))
	return k.service.OpenKVStore(ctx).Set([]byte{historyProgressKey}, data)
}

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
	newest, prunedThrough, e := k.historyProgress(child)
	if e != nil {
		return e
	}
	if old == nil && h.Header.Height < newest {
		return errors.New("historical native insertion is out of order")
	}
	if h.Header.Height > newest {
		newest = h.Header.Height
	}
	// Seek only the stale range. Ordinary sequential blocks visit at most one
	// stale row; gaps or lower retention delete every newly stale row without
	// silently delaying retention or introducing a new work-count policy.
	var remove [][]byte
	cutoff := h.Header.Height - int64(p.HistoricalEntries)
	if cutoff > prunedThrough {
		it, err := state.Iterator([]byte{historyPrefix}, historyKey(cutoff+1))
		if err != nil {
			return err
		}
		for ; it.Valid(); it.Next() {
			if e = it.Error(); e != nil {
				break
			}
			key := it.Key()
			if len(key) != 9 || key[0] != historyPrefix || int64(binary.BigEndian.Uint64(key[1:])) <= 0 {
				e = errors.New("malformed historical native key")
				break
			}
			remove = append(remove, bytes.Clone(key))
		}
		closeErr := it.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		prunedThrough = cutoff
	}
	if e = state.Set(key, data); e != nil {
		return e
	}
	for _, stale := range remove {
		if e = state.Delete(stale); e != nil {
			return e
		}
	}
	if e = k.setHistoryProgress(child, newest, prunedThrough); e != nil {
		return e
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
