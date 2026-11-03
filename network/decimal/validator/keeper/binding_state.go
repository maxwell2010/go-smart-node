package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"reflect"
	"sort"
	"time"

	"cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const bindingDomainMarker byte = 0x05
const (
	bindingRevisionKey         byte = 0x50
	stakeBindingPrefix         byte = 0x51
	frozenBindingPrefix        byte = 0x52
	custodyUnitBindingPrefix   byte = 0x53
	custodyAssetBindingPrefix  byte = 0x54
	nativeQueueBindingPrefix   byte = 0x55
	activePenaltyBindingPrefix byte = 0x56
)

var ErrBindingDomain = errors.New("native bindings require the configured initialized App domain")
var ErrStaleBindingPlan = errors.New("native binding prestate changed")

func NewStateKeeperWithDomain(service store.KVStoreService, chainID string, evmChainID uint64) (*StateKeeper, error) {
	if nilInterface(service) {
		return nil, errors.New("native binding store service is missing")
	}
	domain := &types.EVMSourceDomain{ChainID: chainID, EVMChainID: evmChainID}
	if err := domain.Validate(); err != nil {
		return nil, err
	}
	keeper, err := NewStateKeeper(service)
	if err != nil {
		return nil, err
	}
	keeper.bindingDomain = domain
	return keeper, nil
}
func nilInterface(value interface{}) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
func (k *StateKeeper) ConfiguredEVMSourceDomain() (types.EVMSourceDomain, bool) {
	if k == nil || k.bindingDomain == nil {
		return types.EVMSourceDomain{}, false
	}
	return *k.bindingDomain, true
}
func (k *StateKeeper) checkBindingDomain(ctx sdk.Context) error {
	if k == nil || nilInterface(k.service) || k.bindingDomain == nil || ctx.ChainID() != k.bindingDomain.ChainID {
		return ErrBindingDomain
	}
	data, err := k.service.OpenKVStore(ctx).Get([]byte{bindingDomainMarker})
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > 512 {
		return ErrBindingDomain
	}
	var actual types.EVMSourceDomain
	if err := proto.Unmarshal(data, &actual); err != nil {
		return err
	}
	canonical, err := proto.Marshal(&actual)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, canonical) || actual.Validate() != nil || !types.SameEVMSourceDomain(&actual, k.bindingDomain) {
		return ErrBindingDomain
	}
	return nil
}
func (k *StateKeeper) GetEVMSourceDomain(ctx sdk.Context) (types.EVMSourceDomain, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return types.EVMSourceDomain{}, err
	}
	return *k.bindingDomain, nil
}
func (k *StateKeeper) BindingRevision(ctx sdk.Context) (uint64, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return 0, err
	}
	data, err := k.service.OpenKVStore(ctx).Get([]byte{bindingRevisionKey})
	if err != nil {
		return 0, err
	}
	if len(data) != 8 {
		return 0, errors.New("missing or corrupt native binding revision")
	}
	return binary.BigEndian.Uint64(data), nil
}

// BindingReference identifies only a source row. Frozen IDs and stake IDs both
// retain 32 bytes, but have separate namespaces. It grants no EVM authority.
type BindingReference struct {
	Frozen      bool
	Kind        types.EVMStakeBindingKind
	Gateway, ID []byte
}

func (r BindingReference) key() ([]byte, error) {
	if r.Frozen {
		if r.Kind != types.EVMStakeBindingKind_EVM_STAKE_BINDING_UNSPECIFIED {
			return nil, errors.New("frozen reference cannot alias a stake key kind")
		}
		key, err := types.EVMFrozenBindingKey(r.Gateway, r.ID)
		return append([]byte{frozenBindingPrefix}, key...), err
	}
	key, err := types.EVMStakeBindingKey(r.Kind, r.Gateway, r.ID)
	return append([]byte{stakeBindingPrefix}, key...), err
}

type BindingAssignment struct {
	Stake  *types.EVMStakeBinding
	Frozen *types.EVMFrozenBinding
}

// Reference returns independent source-key bytes for a captured assignment.
func (a BindingAssignment) Reference() (BindingReference, error) {
	if (a.Stake == nil) == (a.Frozen == nil) {
		return BindingReference{}, errors.New("assignment must identify exactly one source row")
	}
	if a.Stake != nil {
		if err := a.Stake.Validate(); err != nil {
			return BindingReference{}, err
		}
		return BindingReference{Kind: a.Stake.Kind, Gateway: bytes.Clone(a.Stake.Gateway), ID: bytes.Clone(a.Stake.StakeID)}, nil
	}
	if err := a.Frozen.Validate(); err != nil {
		return BindingReference{}, err
	}
	return BindingReference{Frozen: true, Gateway: bytes.Clone(a.Frozen.Gateway), ID: bytes.Clone(a.Frozen.FrozenIndex)}, nil
}

type bindingRecord struct {
	key, data []byte
	stake     *types.EVMStakeBinding
	frozen    *types.EVMFrozenBinding
}

func (r *bindingRecord) active() bool {
	if r == nil {
		return false
	}
	if r.stake != nil {
		return r.stake.Active
	}
	return r.frozen.Active
}
func (r *bindingRecord) nft() *types.NFTPositionIdentity {
	if r == nil {
		return nil
	}
	if r.stake != nil {
		return r.stake.NFTPosition
	}
	return r.frozen.NFTPosition
}
func (r *bindingRecord) allClaimKeys() ([][]byte, error) {
	if r == nil {
		return nil, nil
	}
	var result [][]byte
	if r.stake != nil {
		result = append(result, activePenaltyBindingKey(r.stake.Validator, r.stake.PenaltyCursor, r.key))
	}
	if r.frozen != nil {
		key := append([]byte{nativeQueueBindingPrefix}, r.frozen.NativeEntryHash...)
		result = append(result, append(key, r.key...))
	}
	p := r.nft()
	if p == nil {
		return result, nil
	}
	for _, id := range p.Custody.NativeSubTokenIDs {
		key, err := types.CustodyUnitKey(p.Custody.NativeTokenID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, append([]byte{custodyUnitBindingPrefix}, key...))
	}
	key := append([]byte{custodyAssetBindingPrefix}, p.Custody.Contract...)
	result = append(result, append(key, p.Custody.TokenID...))
	return result, nil
}
func recordFromStake(value *types.EVMStakeBinding) (*bindingRecord, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	key, err := types.EVMStakeBindingKey(value.Kind, value.Gateway, value.StakeID)
	if err != nil {
		return nil, err
	}
	data, err := types.CanonicalBindingBytes(value)
	if err != nil {
		return nil, err
	}
	return decodeBindingRecord(append([]byte{stakeBindingPrefix}, key...), data)
}
func recordFromFrozen(value *types.EVMFrozenBinding) (*bindingRecord, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	key, err := types.EVMFrozenBindingKey(value.Gateway, value.FrozenIndex)
	if err != nil {
		return nil, err
	}
	data, err := types.CanonicalBindingBytes(value)
	if err != nil {
		return nil, err
	}
	return decodeBindingRecord(append([]byte{frozenBindingPrefix}, key...), data)
}
func decodeBindingRecord(key, data []byte) (*bindingRecord, error) {
	if len(key) == 0 || len(data) == 0 || len(data) > MaxRecordBytes {
		return nil, errors.New("invalid native binding row")
	}
	r := &bindingRecord{key: bytes.Clone(key), data: bytes.Clone(data)}
	var expected, canonical []byte
	var err error
	switch key[0] {
	case stakeBindingPrefix:
		r.stake = &types.EVMStakeBinding{}
		if err = proto.Unmarshal(data, r.stake); err != nil {
			return nil, err
		}
		if err = r.stake.Validate(); err != nil {
			return nil, err
		}
		expected, err = types.EVMStakeBindingKey(r.stake.Kind, r.stake.Gateway, r.stake.StakeID)
		if err == nil {
			canonical, err = types.CanonicalBindingBytes(r.stake)
		}
	case frozenBindingPrefix:
		r.frozen = &types.EVMFrozenBinding{}
		if err = proto.Unmarshal(data, r.frozen); err != nil {
			return nil, err
		}
		if err = r.frozen.Validate(); err != nil {
			return nil, err
		}
		expected, err = types.EVMFrozenBindingKey(r.frozen.Gateway, r.frozen.FrozenIndex)
		if err == nil {
			canonical, err = types.CanonicalBindingBytes(r.frozen)
		}
	default:
		return nil, errors.New("invalid native binding namespace")
	}
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(key[1:], expected) || !bytes.Equal(data, canonical) {
		return nil, errors.New("noncanonical native binding or source key mismatch")
	}
	return r, nil
}
func (k *StateKeeper) readBindingRow(ctx sdk.Context, key []byte) (*bindingRecord, error) {
	data, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, ErrNotFound
	}
	return decodeBindingRecord(key, data)
}
func (k *StateKeeper) checkBindingIndexes(ctx sdk.Context, r *bindingRecord) error {
	keys, err := r.allClaimKeys()
	if err != nil {
		return err
	}
	state := k.service.OpenKVStore(ctx)
	for _, key := range keys {
		owner, err := state.Get(key)
		if err != nil {
			return err
		}
		if r.active() && !bytes.Equal(owner, r.key) {
			return errors.New("active native custody binding reverse index mismatch")
		}
		if !r.active() && bytes.Equal(owner, r.key) {
			return errors.New("inactive binding still owns a custody reverse index")
		}
	}
	return nil
}
func (k *StateKeeper) GetEVMStakeBinding(ctx sdk.Context, kind types.EVMStakeBindingKind, gateway, id []byte) (types.EVMStakeBinding, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return types.EVMStakeBinding{}, err
	}
	key, err := (BindingReference{Kind: kind, Gateway: gateway, ID: id}).key()
	if err != nil {
		return types.EVMStakeBinding{}, err
	}
	r, err := k.readBindingRow(ctx, key)
	if err != nil {
		return types.EVMStakeBinding{}, err
	}
	if err = k.checkBindingIndexes(ctx, r); err != nil {
		return types.EVMStakeBinding{}, err
	}
	return *r.stake, nil
}
func (k *StateKeeper) GetEVMFrozenBinding(ctx sdk.Context, gateway, index []byte) (types.EVMFrozenBinding, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return types.EVMFrozenBinding{}, err
	}
	key, err := (BindingReference{Frozen: true, Gateway: gateway, ID: index}).key()
	if err != nil {
		return types.EVMFrozenBinding{}, err
	}
	r, err := k.readBindingRow(ctx, key)
	if err != nil {
		return types.EVMFrozenBinding{}, err
	}
	if err = k.checkBindingIndexes(ctx, r); err != nil {
		return types.EVMFrozenBinding{}, err
	}
	return *r.frozen, nil
}
func (k *StateKeeper) lookupBindingIndex(ctx sdk.Context, key []byte) (BindingAssignment, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return BindingAssignment{}, err
	}
	owner, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return BindingAssignment{}, err
	}
	if owner == nil {
		return BindingAssignment{}, ErrNotFound
	}
	r, err := k.readBindingRow(ctx, owner)
	if err != nil {
		return BindingAssignment{}, err
	}
	if !r.active() {
		return BindingAssignment{}, errors.New("custody reverse index points to an inactive binding")
	}
	if err = k.checkBindingIndexes(ctx, r); err != nil {
		return BindingAssignment{}, err
	}
	claims, err := r.allClaimKeys()
	if err != nil {
		return BindingAssignment{}, err
	}
	found := false
	for _, claim := range claims {
		if bytes.Equal(claim, key) {
			found = true
			break
		}
	}
	if !found {
		return BindingAssignment{}, errors.New("custody reverse index points to a different NFT")
	}
	return BindingAssignment{Stake: r.stake, Frozen: r.frozen}, nil
}
func (k *StateKeeper) LookupCustodyUnitBinding(ctx sdk.Context, nativeID string, id uint32) (BindingAssignment, error) {
	key, err := types.CustodyUnitKey(nativeID, id)
	if err != nil {
		return BindingAssignment{}, err
	}
	return k.lookupBindingIndex(ctx, append([]byte{custodyUnitBindingPrefix}, key...))
}
func (k *StateKeeper) LookupCustodyNFTBinding(ctx sdk.Context, contract, tokenID []byte) (BindingAssignment, error) {
	if len(contract) != 20 || bytes.Equal(contract, make([]byte, 20)) || len(tokenID) != 32 {
		return BindingAssignment{}, errors.New("invalid full custody NFT identity")
	}
	key := append([]byte{custodyAssetBindingPrefix}, contract...)
	return k.lookupBindingIndex(ctx, append(key, tokenID...))
}

// BindingMutation is an internal metadata transition, not a financial message.
// Production controller authenticates source EVM state and NFT lineage first.
type BindingMutation struct {
	Stake  *types.EVMStakeBinding
	Frozen *types.EVMFrozenBinding
	Remove *BindingReference
}
type BindingPlan struct {
	owner                       *StateKeeper
	chain                       string
	height                      int64
	at                          time.Time
	revision                    uint64
	beforeRows, afterRows       map[string][]byte
	beforeIndexes, afterIndexes map[string][]byte
}

func sortedBindingKeys(values map[string][]byte) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
func (k *StateKeeper) PrepareBindingPlan(ctx sdk.Context, mutations []BindingMutation) (*BindingPlan, error) {
	revision, err := k.BindingRevision(ctx)
	if err != nil {
		return nil, err
	}
	if len(mutations) == 0 || len(mutations) > types.MaxBindingBatchSize {
		return nil, errors.New("native binding batch must contain 1..64 changes")
	}
	p := &BindingPlan{owner: k, chain: ctx.ChainID(), height: ctx.BlockHeight(), at: ctx.BlockTime(), revision: revision, beforeRows: map[string][]byte{}, afterRows: map[string][]byte{}, beforeIndexes: map[string][]byte{}, afterIndexes: map[string][]byte{}}
	nextRecords := map[string]*bindingRecord{}
	oldRecords := map[string]*bindingRecord{}
	for _, mutation := range mutations {
		present := 0
		if mutation.Stake != nil {
			present++
		}
		if mutation.Frozen != nil {
			present++
		}
		if mutation.Remove != nil {
			present++
		}
		if present != 1 {
			return nil, errors.New("binding change requires exactly one typed operation")
		}
		var key []byte
		var record *bindingRecord
		if mutation.Stake != nil {
			record, err = recordFromStake(mutation.Stake)
		} else if mutation.Frozen != nil {
			record, err = recordFromFrozen(mutation.Frozen)
		} else {
			key, err = mutation.Remove.key()
		}
		if err != nil {
			return nil, err
		}
		if record != nil {
			key = record.key
		}
		if _, exists := p.afterRows[string(key)]; exists {
			return nil, errors.New("duplicate source binding change")
		}
		if record != nil {
			p.afterRows[string(key)] = bytes.Clone(record.data)
			nextRecords[string(key)] = record
		} else {
			p.afterRows[string(key)] = nil
		}
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range sortedBindingKeys(p.afterRows) {
		r, readErr := k.readBindingRow(ctx, []byte(key))
		if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return nil, readErr
		}
		if readErr == nil {
			if err := k.checkBindingIndexes(ctx, r); err != nil {
				return nil, err
			}
			oldRecords[key] = r
			p.beforeRows[key] = bytes.Clone(r.data)
		} else {
			p.beforeRows[key] = nil
		}
		next := nextRecords[key]
		if next != nil {
			var asset *types.EVMReserveAsset
			var validators [][]byte
			if next.stake != nil {
				asset = next.stake.ReserveAsset
				if next.stake.Active {
					validators = append(validators, next.stake.Validator)
				}
			} else {
				asset = next.frozen.ReserveAsset
				if next.frozen.Active {
					validators = append(validators, next.frozen.SourceValidator)
					if next.frozen.FreezeType == 2 {
						validators = append(validators, next.frozen.DestinationValidator)
					}
				}
			}
			if asset.ReserveKind == 1 && asset.NativeDenom != params.BaseDenom {
				return nil, errors.New("binding native reserve differs from base denomination")
			}
			for _, raw := range validators {
				operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(raw)
				if err != nil {
					return nil, err
				}
				if _, err = k.GetValidator(ctx, operator); err != nil {
					return nil, err
				}
			}
		}
		for _, record := range []*bindingRecord{r, next} {
			if record == nil {
				continue
			}
			claims, err := record.allClaimKeys()
			if err != nil {
				return nil, err
			}
			for _, claim := range claims {
				if _, ok := p.beforeIndexes[string(claim)]; !ok {
					value, err := k.service.OpenKVStore(ctx).Get(claim)
					if err != nil {
						return nil, err
					}
					p.beforeIndexes[string(claim)] = bytes.Clone(value)
					p.afterIndexes[string(claim)] = bytes.Clone(value)
				}
			}
		}
	}
	// Remove old assignments in the planned final state first, so atomic
	// activeв†’frozen and beneficiary/validator reassignments have no false conflict.
	for _, key := range sortedBindingKeys(p.afterRows) {
		r := oldRecords[key]
		if r != nil && r.active() {
			claims, err := r.allClaimKeys()
			if err != nil {
				return nil, err
			}
			for _, claim := range claims {
				p.afterIndexes[string(claim)] = nil
			}
		}
	}
	assigned := map[string]string{}
	for _, key := range sortedBindingKeys(p.afterRows) {
		r := nextRecords[key]
		if r == nil || !r.active() {
			continue
		}
		claims, err := r.allClaimKeys()
		if err != nil {
			return nil, err
		}
		for _, claim := range claims {
			name := string(claim)
			if other, exists := assigned[name]; exists && other != key {
				return nil, errors.New("two final bindings claim the same custody receipt")
			}
			assigned[name] = key
			current := p.afterIndexes[name]
			if current != nil && !bytes.Equal(current, []byte(key)) {
				return nil, errors.New("custody receipt belongs to another active/frozen binding")
			}
			p.afterIndexes[name] = []byte(key)
		}
	}
	return p, nil
}
func (k *StateKeeper) ApplyBindingPlan(ctx sdk.Context, p *BindingPlan) error {
	if p == nil || p.owner != k || p.chain != ctx.ChainID() || p.height != ctx.BlockHeight() || !p.at.Equal(ctx.BlockTime()) {
		return errors.New("binding plan belongs to another keeper or execution context")
	}
	revision, err := k.BindingRevision(ctx)
	if err != nil {
		return err
	}
	state := k.service.OpenKVStore(ctx)
	desired := true
	for _, set := range []map[string][]byte{p.afterRows, p.afterIndexes} {
		for _, key := range sortedBindingKeys(set) {
			value, err := state.Get([]byte(key))
			if err != nil {
				return err
			}
			if !bytes.Equal(value, set[key]) {
				desired = false
			}
		}
	}
	if desired {
		return nil
	} // Exact target replay does not increment revision.
	if revision != p.revision {
		return ErrStaleBindingPlan
	}
	for _, set := range []map[string][]byte{p.beforeRows, p.beforeIndexes} {
		for _, key := range sortedBindingKeys(set) {
			value, err := state.Get([]byte(key))
			if err != nil {
				return err
			}
			if !bytes.Equal(value, set[key]) {
				return ErrStaleBindingPlan
			}
		}
	}
	if revision == math.MaxUint64 {
		return errors.New("native binding revision exhausted")
	}
	child, commit := ctx.CacheContext()
	target := k.service.OpenKVStore(child)
	for _, set := range []map[string][]byte{p.afterRows, p.afterIndexes} {
		for _, key := range sortedBindingKeys(set) {
			value := set[key]
			if value == nil {
				err = target.Delete([]byte(key))
			} else {
				err = target.Set([]byte(key), bytes.Clone(value))
			}
			if err != nil {
				return err
			}
		}
	}
	var next [8]byte
	binary.BigEndian.PutUint64(next[:], revision+1)
	if err = target.Set([]byte{bindingRevisionKey}, next[:]); err != nil {
		return err
	}
	commit()
	return nil
}

func (k *StateKeeper) BindingPlanTargets(p *BindingPlan) ([]types.EVMStakeBinding, []types.EVMFrozenBinding, error) {
	if p == nil || p.owner != k {
		return nil, nil, errors.New("foreign binding plan")
	}
	var stakes []types.EVMStakeBinding
	var frozen []types.EVMFrozenBinding
	for _, key := range sortedBindingKeys(p.afterRows) {
		if p.afterRows[key] == nil {
			continue
		}
		r, err := decodeBindingRecord([]byte(key), p.afterRows[key])
		if err != nil {
			return nil, nil, err
		}
		if r.stake != nil {
			stakes = append(stakes, *r.stake)
		} else {
			frozen = append(frozen, *r.frozen)
		}
	}
	return stakes, frozen, nil
}

func (k *StateKeeper) GetEVMStakeBindingsPage(ctx sdk.Context, after []byte, limit uint32) ([]types.EVMStakeBinding, []byte, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return nil, nil, err
	}
	if limit == 0 || limit > types.MaxBindingPageSize {
		return nil, nil, errors.New("binding page limit must be1..256")
	}
	if len(after) != 0 && (len(after) != 54 || after[0] != stakeBindingPrefix) {
		return nil, nil, errors.New("invalid binding page cursor")
	}
	start := []byte{stakeBindingPrefix}
	if len(after) != 0 {
		start = append(bytes.Clone(after), 0)
	}
	iterator, err := k.service.OpenKVStore(ctx).Iterator(start, []byte{stakeBindingPrefix + 1})
	if err != nil {
		return nil, nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = iterator.Close()
		}
	}()
	var out []types.EVMStakeBinding
	var cursor []byte
	for ; iterator.Valid() && uint32(len(out)) < limit; iterator.Next() {
		if err := iterator.Error(); err != nil {
			return nil, nil, err
		}
		r, err := decodeBindingRecord(bytes.Clone(iterator.Key()), bytes.Clone(iterator.Value()))
		if err != nil {
			return nil, nil, err
		}
		if err = k.checkBindingIndexes(ctx, r); err != nil {
			return nil, nil, err
		}
		out = append(out, *r.stake)
		cursor = bytes.Clone(iterator.Key())
	}
	if err := iterator.Error(); err != nil {
		return nil, nil, err
	}
	if !iterator.Valid() {
		cursor = nil
	}
	closed = true
	if err := iterator.Close(); err != nil {
		return nil, nil, err
	}
	return out, cursor, nil
}

// A multiset is required: two same-block withdrawals can have identical native
// entry bytes while their source frozen uint256 indexes remain distinct.
func (k *StateKeeper) GetNativeQueueBindingsPage(ctx sdk.Context, entryHash, after []byte, limit uint32) ([]types.EVMFrozenBinding, []byte, error) {
	if err := k.checkBindingDomain(ctx); err != nil {
		return nil, nil, err
	}
	if len(entryHash) != 32 || limit == 0 || limit > types.MaxBindingPageSize {
		return nil, nil, errors.New("invalid native queue binding page")
	}
	prefix := append([]byte{nativeQueueBindingPrefix}, entryHash...)
	end := bytes.Clone(prefix)
	for i := len(end) - 1; i >= 0; i-- {
		end[i]++
		if end[i] != 0 {
			end = end[:i+1]
			break
		}
	}
	start := bytes.Clone(prefix)
	if len(after) != 0 {
		if len(after) != 86 || !bytes.HasPrefix(after, prefix) {
			return nil, nil, errors.New("invalid native queue binding cursor")
		}
		start = append(bytes.Clone(after), 0)
	}
	iterator, err := k.service.OpenKVStore(ctx).Iterator(start, end)
	if err != nil {
		return nil, nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = iterator.Close()
		}
	}()
	var out []types.EVMFrozenBinding
	var cursor []byte
	for ; iterator.Valid() && uint32(len(out)) < limit; iterator.Next() {
		if err := iterator.Error(); err != nil {
			return nil, nil, err
		}
		key, value := bytes.Clone(iterator.Key()), bytes.Clone(iterator.Value())
		row, err := k.readBindingRow(ctx, value)
		if err != nil {
			return nil, nil, err
		}
		if row.frozen == nil || !row.active() || !bytes.Equal(row.frozen.NativeEntryHash, entryHash) || !bytes.Equal(key, append(bytes.Clone(prefix), row.key...)) {
			return nil, nil, errors.New("invalid native queue binding index")
		}
		if err = k.checkBindingIndexes(ctx, row); err != nil {
			return nil, nil, err
		}
		out = append(out, *row.frozen)
		cursor = key
	}
	if err := iterator.Error(); err != nil {
		return nil, nil, err
	}
	if !iterator.Valid() {
		cursor = nil
	}
	closed = true
	if err := iterator.Close(); err != nil {
		return nil, nil, err
	}
	return out, cursor, nil
}
