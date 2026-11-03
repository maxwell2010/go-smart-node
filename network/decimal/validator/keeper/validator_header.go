package keeper

import (
	"bytes"
	storetypes "cosmossdk.io/store/types"
	"errors"
	"sort"

	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const validatorHeaderMasterKey byte = 0x57
const validatorHeaderPrefix byte = 0x58
const validatorHeaderOperatorPrefix byte = 0x59
const validatorHeaderEligiblePrefix byte = 0x5a

// The initialized empty marker stores no permission. The first authenticated
// registration binds one master; changing that source requires explicit migration.
func (k *StateKeeper) validatorHeaderMaster(ctx sdk.Context) ([]byte, bool, error) {
	data, err := k.service.OpenKVStore(ctx).Get([]byte{validatorHeaderMasterKey})
	if err != nil {
		return nil, false, err
	}
	if bytes.Equal(data, []byte{0}) {
		return nil, false, nil
	}
	if len(data) != 21 || data[0] != 1 || bytes.Equal(data[1:], make([]byte, 20)) {
		return nil, false, errors.New("missing or corrupt validator source-master marker")
	}
	return bytes.Clone(data[1:]), true, nil
}

func validatorHeaderKey(master, validator []byte) ([]byte, error) {
	key, err := types.ValidatorHeaderKey(master, validator)
	return append([]byte{validatorHeaderPrefix}, key...), err
}
func validatorHeaderOperatorKey(operator string) ([]byte, error) {
	raw, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).StringToBytes(operator)
	if err != nil {
		return nil, err
	}
	if len(raw) != 20 || bytes.Equal(raw, make([]byte, 20)) {
		return nil, errors.New("invalid native source-header operator")
	}
	return append([]byte{validatorHeaderOperatorPrefix}, raw...), nil
}
func validatorHeaderEligibleKey(h *types.EVMValidatorHeader) []byte {
	return append(append([]byte{validatorHeaderEligiblePrefix}, h.Master...), h.Validator...)
}
func decodeValidatorHeader(key, data []byte) (types.EVMValidatorHeader, error) {
	var h types.EVMValidatorHeader
	if len(key) != 41 || key[0] != validatorHeaderPrefix || len(data) == 0 || len(data) > types.MaxValidatorHeaderBytes {
		return h, errors.New("invalid validator lifecycle header record")
	}
	if err := proto.Unmarshal(data, &h); err != nil {
		return h, err
	}
	canonical, err := types.CanonicalValidatorHeaderBytes(&h)
	if err != nil {
		return h, err
	}
	expected, err := validatorHeaderKey(h.Master, h.Validator)
	if err != nil {
		return h, err
	}
	if !bytes.Equal(key, expected) || !bytes.Equal(data, canonical) {
		return h, errors.New("noncanonical validator lifecycle header or source-key mismatch")
	}
	return h, nil
}
func (k *StateKeeper) readValidatorHeader(ctx sdk.Context, key []byte) (types.EVMValidatorHeader, error) {
	data, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return types.EVMValidatorHeader{}, err
	}
	if data == nil {
		return types.EVMValidatorHeader{}, ErrNotFound
	}
	h, err := decodeValidatorHeader(key, data)
	if err != nil {
		return h, err
	}
	master, bound, err := k.validatorHeaderMaster(ctx)
	if err != nil {
		return h, err
	}
	if !bound || !bytes.Equal(master, h.Master) {
		return types.EVMValidatorHeader{}, errors.New("validator header disagrees with fixed authoritative MasterValidator")
	}
	return h, nil
}
func (k *StateKeeper) checkValidatorHeaderIndexes(ctx sdk.Context, h *types.EVMValidatorHeader) error {
	key, err := validatorHeaderKey(h.Master, h.Validator)
	if err != nil {
		return err
	}
	operatorKey, err := validatorHeaderOperatorKey(h.NativeOperator)
	if err != nil {
		return err
	}
	state := k.service.OpenKVStore(ctx)
	reverse, err := state.Get(operatorKey)
	if err != nil {
		return err
	}
	if !bytes.Equal(reverse, key) {
		return errors.New("native/source validator header reverse index mismatch")
	}
	eligible, err := state.Get(validatorHeaderEligibleKey(h))
	if err != nil {
		return err
	}
	if h.SourceEligible() {
		if !bytes.Equal(eligible, key) {
			return errors.New("source-eligible validator header index mismatch")
		}
	} else if eligible != nil {
		return errors.New("ineligible source header owns an eligibility index")
	}
	// Metadata and its unique consensus identity must exist independently of the
	// source predicate. No header can invent an eligible funded native validator.
	_, err = k.GetValidator(ctx, h.NativeOperator)
	return err
}
func (k *StateKeeper) GetEVMValidatorHeader(ctx sdk.Context, master, validator []byte) (types.EVMValidatorHeader, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return types.EVMValidatorHeader{}, err
	}
	key, err := validatorHeaderKey(master, validator)
	if err != nil {
		return types.EVMValidatorHeader{}, err
	}
	h, err := k.readValidatorHeader(ctx, key)
	if err != nil {
		return h, err
	}
	if err = k.checkValidatorHeaderIndexes(ctx, &h); err != nil {
		return types.EVMValidatorHeader{}, err
	}
	return h, nil
}
func (k *StateKeeper) GetEVMValidatorHeaderByOperator(ctx sdk.Context, operator string) (types.EVMValidatorHeader, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return types.EVMValidatorHeader{}, err
	}
	key, err := validatorHeaderOperatorKey(operator)
	if err != nil {
		return types.EVMValidatorHeader{}, err
	}
	reference, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return types.EVMValidatorHeader{}, err
	}
	if reference == nil {
		return types.EVMValidatorHeader{}, ErrNotFound
	}
	h, err := k.readValidatorHeader(ctx, reference)
	if err != nil {
		return h, err
	}
	if h.NativeOperator != operator {
		return types.EVMValidatorHeader{}, errors.New("validator header reverse index points to another operator")
	}
	if err = k.checkValidatorHeaderIndexes(ctx, &h); err != nil {
		return types.EVMValidatorHeader{}, err
	}
	return h, nil
}

type ValidatorHeaderChange struct{ Expected, Next *types.EVMValidatorHeader }

// ApplyValidatorHeaderBatch writes only authenticated lifecycle metadata. Source
// authenticity is a mandatory controller prerequisite; this method does not set
// Online/Jailed, rewards, effective native/Gov power or move any asset. Native
// metadata is applied first inside the coordinator's same outer child cache.
func (k *StateKeeper) ApplyValidatorHeaderBatch(ctx sdk.Context, changes []ValidatorHeaderChange) error {
	if err := k.checkBindingDomain(ctx); err != nil {
		return err
	}
	if len(changes) == 0 || len(changes) > types.MaxBindingBatchSize {
		return errors.New("validator header batch must contain 1..64 changes")
	}
	master, bound, err := k.validatorHeaderMaster(ctx)
	if err != nil {
		return err
	}
	state := k.service.OpenKVStore(ctx)
	writes := map[string][]byte{}
	seen := map[string]bool{}
	for _, change := range changes {
		if change.Next == nil {
			return errors.New("validator header deletion is not a lifecycle transition; retain Deleted tombstone")
		}
		data, err := types.CanonicalValidatorHeaderBytes(change.Next)
		if err != nil {
			return err
		}
		var next types.EVMValidatorHeader
		if err = proto.Unmarshal(data, &next); err != nil {
			return err
		}
		if master == nil {
			master = bytes.Clone(next.Master)
		} else if !bytes.Equal(master, next.Master) {
			return errors.New("authoritative MasterValidator binding is immutable")
		}
		var expected *types.EVMValidatorHeader
		var expectedData []byte
		if change.Expected != nil {
			expectedData, err = types.CanonicalValidatorHeaderBytes(change.Expected)
			if err != nil {
				return err
			}
			expected = &types.EVMValidatorHeader{}
			if err = proto.Unmarshal(expectedData, expected); err != nil {
				return err
			}
			if !bytes.Equal(expected.Master, next.Master) || !bytes.Equal(expected.Validator, next.Validator) || expected.NativeOperator != next.NativeOperator || expected.CreatedHeight != next.CreatedHeight {
				return errors.New("validator source/native identity and creation height are immutable")
			}
			if next.UpdatedHeight < expected.UpdatedHeight || next.LastMetaHeight < expected.LastMetaHeight {
				return errors.New("validator header lifecycle height moved backwards")
			}
		}
		key, err := validatorHeaderKey(next.Master, next.Validator)
		if err != nil {
			return err
		}
		if seen[string(key)] {
			return errors.New("duplicate validator lifecycle header change")
		}
		seen[string(key)] = true
		operatorKey, err := validatorHeaderOperatorKey(next.NativeOperator)
		if err != nil {
			return err
		}
		if seen[string(operatorKey)] {
			return errors.New("duplicate native operator header change")
		}
		seen[string(operatorKey)] = true
		if _, err = k.GetValidator(ctx, next.NativeOperator); err != nil {
			return err
		}
		current, err := state.Get(key)
		if err != nil {
			return err
		}
		reverse, err := state.Get(operatorKey)
		if err != nil {
			return err
		}
		eligibleKey := validatorHeaderEligibleKey(&next)
		eligible, err := state.Get(eligibleKey)
		if err != nil {
			return err
		}
		if bytes.Equal(current, data) {
			existing, err := k.readValidatorHeader(ctx, key)
			if err != nil {
				return err
			}
			if err = k.checkValidatorHeaderIndexes(ctx, &existing); err != nil {
				return err
			}
			continue
		}
		if change.Expected == nil {
			if current != nil || reverse != nil || eligible != nil {
				return errors.New("new validator header conflicts with existing source identity/index")
			}
		} else {
			if !bound {
				return errors.New("existing validator header requires its initialized source-master binding")
			}
			if !bytes.Equal(current, expectedData) {
				return errors.New("validator lifecycle header prestate changed")
			}
			if err = k.checkValidatorHeaderIndexes(ctx, expected); err != nil {
				return err
			}
		}
		// Independent of whether this source is eligible, its persistent identity
		// reverse index remains unique. Candidate/Approved/pause changes touch no RS.
		if reverse != nil && !bytes.Equal(reverse, key) {
			return errors.New("native operator belongs to another authoritative MasterValidator")
		}
		writes[string(key)] = data
		writes[string(operatorKey)] = key
		if next.SourceEligible() {
			writes[string(eligibleKey)] = key
		} else {
			writes[string(eligibleKey)] = nil
		}
	}
	if !bound {
		writes[string([]byte{validatorHeaderMasterKey})] = append([]byte{1}, master...)
	}
	child, commit := ctx.CacheContext()
	target := k.service.OpenKVStore(child)
	keys := make([]string, 0, len(writes))
	for key := range writes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var err error
		if writes[key] == nil {
			err = target.Delete([]byte(key))
		} else {
			err = target.Set([]byte(key), bytes.Clone(writes[key]))
		}
		if err != nil {
			return err
		}
	}
	commit()
	return nil
}

// GetSourceEligibleValidatorHeadersPage enumerates only the source membership
// predicate. Consumers must separately enforce native Online/Jailed/net-power
// and nonempty final-set invariants. Cursor is a full exclusive validator20.
func (k *StateKeeper) GetSourceEligibleValidatorHeadersPage(ctx sdk.Context, master, cursor []byte, limit uint32) (headers []types.EVMValidatorHeader, next []byte, err error) {
	if err = k.checkBindingDomain(ctx); err != nil {
		return nil, nil, err
	}
	if len(master) != 20 || bytes.Equal(master, make([]byte, 20)) || len(cursor) != 0 && len(cursor) != 20 || limit == 0 || limit > types.MaxBindingPageSize {
		return nil, nil, errors.New("invalid source-eligible header pagination")
	}
	fixed, bound, err := k.validatorHeaderMaster(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !bound {
		return nil, nil, nil
	}
	if !bytes.Equal(fixed, master) {
		return nil, nil, errors.New("source-eligible header query uses another MasterValidator")
	}
	prefix := append([]byte{validatorHeaderEligiblePrefix}, master...)
	start := bytes.Clone(prefix)
	if len(cursor) != 0 {
		start = append(start, cursor...)
		start = append(start, 0)
	}
	it, err := k.service.OpenKVStore(ctx).Iterator(start, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if e := it.Close(); err == nil && e != nil {
			headers = nil
			next = nil
			err = e
		}
	}()
	for uint32(len(headers)) < limit && it.Valid() {
		if err = it.Error(); err != nil {
			return nil, nil, err
		}
		key, reference := bytes.Clone(it.Key()), bytes.Clone(it.Value())
		if len(key) != 41 || !bytes.HasPrefix(key, prefix) {
			return nil, nil, errors.New("corrupt source-eligible header key")
		}
		h, readErr := k.GetEVMValidatorHeader(ctx, master, key[21:])
		if readErr != nil {
			return nil, nil, readErr
		}
		expected, _ := validatorHeaderKey(h.Master, h.Validator)
		if !h.SourceEligible() || !bytes.Equal(reference, expected) {
			return nil, nil, errors.New("source-eligible header index points to another record")
		}
		headers = append(headers, h)
		next = bytes.Clone(h.Validator)
		it.Next()
	}
	if err = it.Error(); err != nil {
		return nil, nil, err
	}
	if !it.Valid() {
		next = nil
	}
	return headers, next, nil
}
