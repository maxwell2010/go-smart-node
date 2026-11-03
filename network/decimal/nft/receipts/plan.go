package receipts

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync/atomic"

	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

type Planner struct {
	decoder *Decoder
	state   NativeState
	reader  FinalReader
	stakes  StakeCapture
}

func NewPlanner(decoder *Decoder, state NativeState, reader FinalReader, stakes StakeCapture) (*Planner, error) {
	if decoder == nil || state == nil || reader == nil || stakes == nil {
		return nil, errors.New("NFT planner requires decoding, native state, final reader and stake capture")
	}
	return &Planner{decoder: decoder, state: state, reader: reader, stakes: stakes}, nil
}

type Movement struct {
	Transfer      Transfer
	NativeTokenID string
	SubTokenIDs   []uint32
}

// Plan has no exported mutable state. Copies share the same one-use status;
// transaction hash is not a replay key. A failed application requires replanning.
type Plan struct{ data *planData }
type planData struct {
	owner       *Planner
	route       EntryPoint
	noop        bool
	used        atomic.Bool
	profiles    map[common.Address]CollectionProfile
	final       []expectedOwnership
	fingerprint common.Hash
	stakes      CapturedStakes
	moves       []Movement
}

func (p *Plan) Movements() []Movement {
	if p == nil || p.data == nil {
		return nil
	}
	return copyMovements(p.data.moves)
}
func copyMovements(source []Movement) []Movement {
	copy := append([]Movement(nil), source...)
	for i := range copy {
		copy[i].SubTokenIDs = append([]uint32(nil), source[i].SubTokenIDs...)
	}
	return copy
}
func copyCapture(source CapturedStakes) CapturedStakes {
	copy := CapturedStakes{PrestateFingerprint: source.PrestateFingerprint, Selections: make(map[MoveRef][]uint32, len(source.Selections))}
	for ref, ids := range source.Selections {
		copy.Selections[ref] = append([]uint32(nil), ids...)
	}
	return copy
}

type assetState struct {
	key     AssetKey
	binding types.ExternalNFTBinding
	token   types.Token
	owners  []common.Address
	units   map[uint32]types.SubToken
}
type expectedOwnership struct {
	key    AssetKey
	owners []common.Address
	counts []uint32
}

func (p *Planner) Plan(ctx sdk.Context, execution Execution) (*Plan, error) {
	if p == nil || p.decoder == nil {
		return nil, errors.New("NFT planner is not initialized")
	}
	decoded, err := p.decoder.Decode(ctx, execution)
	if err != nil {
		return nil, err
	}
	if execution.Receipt.Status == ethtypes.ReceiptStatusFailed {
		return &Plan{data: &planData{owner: p, route: execution.EntryPoint, noop: true}}, nil
	}
	// Stake capture happens before any virtual ownership moves or native writes.
	readCtx, _ := ctx.CacheContext()
	captured, err := p.stakes.Capture(readCtx, copyExecution(execution), append([]Transfer(nil), decoded.Transfers...))
	if err != nil {
		return nil, fmt.Errorf("capture old NFT stake bindings: %w", err)
	}
	if uint64(len(captured.Selections)) > uint64(len(decoded.Transfers)) {
		return nil, errors.New("NFT stake capture exceeds transfer count")
	}
	var capturedUnits uint64
	for _, ids := range captured.Selections {
		capturedUnits += uint64(len(ids))
		if capturedUnits > uint64(p.decoder.limits.MaxUnits) {
			return nil, errors.New("NFT stake capture exceeds unit-work limit")
		}
	}
	if len(captured.Selections) > 0 && captured.PrestateFingerprint == (common.Hash{}) {
		return nil, errors.New("NFT escrow selection lacks captured stake fingerprint")
	}
	ctx.GasMeter().ConsumeGas(capturedUnits*100+uint64(len(decoded.Transfers))*200, "NFT bounded virtual ownership planning")
	captured = copyCapture(captured)
	assets, err := p.snapshot(ctx, decoded.Transfers, decoded.Profiles)
	if err != nil {
		return nil, err
	}
	fingerprint, err := stateFingerprint(assets, decoded.Profiles)
	if err != nil {
		return nil, err
	}
	data := &planData{owner: p, route: execution.EntryPoint, profiles: copyProfiles(decoded.Profiles), fingerprint: fingerprint, stakes: captured}
	lookup := make(map[AssetKey]*assetState, len(assets))
	virtual := make(map[AssetKey]map[uint32]common.Address, len(assets))
	for i := range assets {
		asset := &assets[i]
		lookup[asset.key] = asset
		owners := make(map[uint32]common.Address, len(asset.units))
		for id, unit := range asset.units {
			owner, err := nativeAddress(unit.Owner)
			if err != nil {
				return nil, err
			}
			owners[id] = owner
		}
		virtual[asset.key] = owners
	}
	usedSelections := make(map[MoveRef]bool)
	for _, move := range decoded.Transfers {
		asset := lookup[move.Asset]
		owners := virtual[move.Asset]
		available := make([]uint32, 0)
		for id, owner := range owners {
			if owner == move.From {
				available = append(available, id)
			}
		}
		sort.Slice(available, func(i, j int) bool { return available[i] < available[j] })
		if uint64(move.Quantity) > uint64(len(available)) {
			return nil, errors.New("NFT transfer exceeds authenticated native ownership")
		}
		var selected []uint32
		selection, provided := captured.Selections[move.Ref]
		fromEscrow := containsAddress(decoded.Profiles[move.Asset.Contract].Escrows, move.From)
		if fromEscrow && move.Quantity > 0 {
			if !provided || uint64(len(selection)) != uint64(move.Quantity) {
				return nil, errors.New("NFT escrow transfer lacks exact persisted stake unit selection")
			}
			selected = append([]uint32(nil), selection...)
			usedSelections[move.Ref] = true
		} else {
			if provided {
				return nil, errors.New("NFT stake capture supplied unexpected ordinary-owner selection")
			}
			selected = append([]uint32(nil), available[:int(move.Quantity)]...)
		}
		seen := make(map[uint32]bool, len(selected))
		for _, id := range selected {
			if seen[id] || owners[id] != move.From {
				return nil, errors.New("NFT escrow selection differs from current virtual owner")
			}
			seen[id] = true
			owners[id] = move.To
		}
		data.moves = append(data.moves, Movement{Transfer: move, NativeTokenID: asset.binding.NativeTokenID, SubTokenIDs: selected})
	}
	if len(usedSelections) != len(captured.Selections) {
		return nil, errors.New("NFT stake capture contains unused escrow selections")
	}
	for _, asset := range assets {
		readCtx, _ := ctx.CacheContext()
		actual, err := p.reader.Ownership(readCtx, copyProfile(decoded.Profiles[asset.key.Contract]), asset.key, append([]common.Address(nil), asset.owners...))
		if err != nil {
			return nil, fmt.Errorf("read final NFT ownership: %w", err)
		}
		if len(actual) != len(asset.owners) {
			return nil, errors.New("final NFT reader returned unexpected owners")
		}
		final := expectedOwnership{key: asset.key, owners: append([]common.Address(nil), asset.owners...)}
		for _, owner := range asset.owners {
			var expected uint32
			for _, holder := range virtual[asset.key] {
				if holder == owner {
					expected++
				}
			}
			value, found := actual[owner]
			if !found || value != expected {
				return nil, errors.New("NFT receipt ownership differs from final actual contract state")
			}
			final.counts = append(final.counts, expected)
		}
		data.final = append(data.final, final)
	}
	return &Plan{data: data}, nil
}

func (p *Planner) Apply(ctx sdk.Context, plan *Plan) error {
	if p == nil || plan == nil || plan.data == nil || plan.data.owner != p {
		return errors.New("NFT plan does not belong to this planner")
	}
	data := plan.data
	if !data.used.CompareAndSwap(false, true) {
		return errors.New("NFT plan was already applied or attempted")
	}
	if data.noop {
		return nil
	}
	child, commit := ctx.CacheContext()
	targets := make([]common.Address, 0, len(data.profiles))
	for target := range data.profiles {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return bytes.Compare(targets[i][:], targets[j][:]) < 0 })
	for _, target := range targets {
		expected := data.profiles[target]
		readCtx, _ := child.CacheContext()
		actual, known, err := p.decoder.auth.AuthenticateCollection(readCtx, target, data.route)
		if err != nil {
			return err
		}
		if !known || !profilesEqual(expected, actual) {
			return errors.New("NFT approved collection changed after planning")
		}
	}
	readCtx, _ := child.CacheContext()
	if err := p.stakes.ValidateCapture(readCtx, copyCapture(data.stakes)); err != nil {
		return fmt.Errorf("NFT stake bindings changed after planning: %w", err)
	}
	transfers := make([]Transfer, len(data.moves))
	for i, move := range data.moves {
		transfers[i] = move.Transfer
	}
	assets, err := p.snapshot(child, transfers, data.profiles)
	if err != nil {
		return err
	}
	fingerprint, err := stateFingerprint(assets, data.profiles)
	if err != nil {
		return err
	}
	if fingerprint != data.fingerprint {
		return errors.New("NFT native prestate changed after planning")
	}
	for _, final := range data.final {
		readCtx, _ := child.CacheContext()
		actual, err := p.reader.Ownership(readCtx, copyProfile(data.profiles[final.key.Contract]), final.key, append([]common.Address(nil), final.owners...))
		if err != nil {
			return err
		}
		if len(actual) != len(final.owners) {
			return errors.New("NFT final ownership changed after planning")
		}
		for i, owner := range final.owners {
			value, present := actual[owner]
			if !present || value != final.counts[i] {
				return errors.New("NFT final ownership changed after planning")
			}
		}
	}
	for _, move := range data.moves {
		if move.Transfer.From == move.Transfer.To || len(move.SubTokenIDs) == 0 {
			continue
		}
		from, err := nativeOwner(move.Transfer.From)
		if err != nil {
			return err
		}
		to, err := nativeOwner(move.Transfer.To)
		if err != nil {
			return err
		}
		if err := p.state.TransferSubTokens(child, move.NativeTokenID, append([]uint32(nil), move.SubTokenIDs...), from, to); err != nil {
			return err
		}
	}
	commit()
	return nil
}

func (p *Planner) snapshot(ctx sdk.Context, transfers []Transfer, profiles map[common.Address]CollectionProfile) ([]assetState, error) {
	requests := make(map[AssetKey]map[common.Address]bool)
	for _, move := range transfers {
		if requests[move.Asset] == nil {
			requests[move.Asset] = make(map[common.Address]bool)
		}
		requests[move.Asset][move.From] = true
		requests[move.Asset][move.To] = true
	}
	keys := make([]AssetKey, 0, len(requests))
	for key := range requests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Contract != keys[j].Contract {
			return bytes.Compare(keys[i].Contract[:], keys[j].Contract[:]) < 0
		}
		return bytes.Compare(keys[i].TokenID[:], keys[j].TokenID[:]) < 0
	})
	assets := make([]assetState, 0, len(keys))
	var readUnits uint64
	for _, key := range keys {
		binding, err := p.state.ResolveExternalNFT(ctx, key.Contract.Bytes(), key.TokenID.Bytes())
		if err != nil {
			return nil, err
		}
		profile := profiles[key.Contract]
		if binding.ChainID != profile.ChainID || binding.EVMChainID != profile.EVMChainID || binding.NFTType != profile.NFTType || !bytes.Equal(binding.Contract, key.Contract[:]) || !bytes.Equal(binding.TokenID, key.TokenID[:]) {
			return nil, errors.New("NFT native binding differs from approved collection")
		}
		token, err := p.state.GetToken(ctx, binding.NativeTokenID)
		if err != nil {
			return nil, err
		}
		if token.ReserveLess != profile.ReserveLess {
			return nil, errors.New("NFT native reserve profile differs from approved collection")
		}
		asset := assetState{key: key, binding: binding, token: token, units: make(map[uint32]types.SubToken)}
		for owner := range requests[key] {
			asset.owners = append(asset.owners, owner)
		}
		sort.Slice(asset.owners, func(i, j int) bool { return bytes.Compare(asset.owners[i][:], asset.owners[j][:]) < 0 })
		for _, owner := range asset.owners {
			text, err := nativeOwner(owner)
			if err != nil {
				return nil, err
			}
			var after uint32
			for {
				limit := uint32(256)
				remaining := uint64(p.decoder.limits.MaxUnits) - readUnits
				if remaining < uint64(limit) {
					limit = uint32(remaining)
				}
				if limit == 0 {
					limit = 1
				} // One bounded empty-page probe permits exact exhaustion.
				ids, cursor, err := p.state.ListActiveSubTokenIDs(ctx, binding.NativeTokenID, text, after, limit)
				if err != nil {
					return nil, err
				}
				if len(ids) == 0 {
					break
				}
				readUnits += uint64(len(ids))
				if readUnits > uint64(p.decoder.limits.MaxUnits) {
					return nil, errors.New("NFT native snapshot read-unit limit exceeded")
				}
				ctx.GasMeter().ConsumeGas(uint64(len(ids))*100, "NFT bounded native unit snapshot")
				for _, id := range ids {
					if id <= after {
						return nil, errors.New("NFT native owner page is not strictly ordered")
					}
					after = id
					if _, present := asset.units[id]; present {
						return nil, errors.New("NFT native unit appears under multiple owners")
					}
					unit, err := p.state.GetSubToken(ctx, binding.NativeTokenID, id)
					if err != nil {
						return nil, err
					}
					actual, err := nativeAddress(unit.Owner)
					if err != nil || actual != owner {
						return nil, errors.New("NFT native unit differs from owner index")
					}
					asset.units[id] = unit
				}
				if cursor != after {
					return nil, errors.New("NFT native owner cursor differs from page")
				}
			}
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

func stateFingerprint(assets []assetState, profiles map[common.Address]CollectionProfile) (common.Hash, error) {
	hash := sha256.New()
	write := func(data []byte) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(data)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(data)
	}
	write([]byte("mintcandy:nft-ownership-prestate:v1"))
	for _, asset := range assets {
		write(asset.key.Contract[:])
		write(asset.key.TokenID[:])
		profile, err := json.Marshal(profiles[asset.key.Contract])
		if err != nil {
			return common.Hash{}, err
		}
		write(profile)
		for _, record := range []proto.Message{&asset.binding, &asset.token} {
			data, err := proto.Marshal(record)
			if err != nil {
				return common.Hash{}, err
			}
			write(data)
		}
		for _, owner := range asset.owners {
			write(owner[:])
		}
		ids := make([]uint32, 0, len(asset.units))
		for id := range asset.units {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			unit := asset.units[id]
			data, err := proto.Marshal(&unit)
			if err != nil {
				return common.Hash{}, err
			}
			write(data)
		}
	}
	return common.BytesToHash(hash.Sum(nil)), nil
}

func nativeOwner(value common.Address) (string, error) {
	return address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(value.Bytes())
}
func nativeAddress(value string) (common.Address, error) {
	data, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).StringToBytes(value)
	if err != nil || len(data) != 20 {
		return common.Address{}, errors.New("invalid native NFT owner")
	}
	return common.BytesToAddress(data), nil
}
func containsAddress(values []common.Address, value common.Address) bool {
	for _, a := range values {
		if a == value {
			return true
		}
	}
	return false
}
func copyProfile(value CollectionProfile) CollectionProfile {
	value.Escrows = append([]common.Address(nil), value.Escrows...)
	return value
}
func copyProfiles(source map[common.Address]CollectionProfile) map[common.Address]CollectionProfile {
	copy := make(map[common.Address]CollectionProfile, len(source))
	for a, p := range source {
		copy[a] = copyProfile(p)
	}
	return copy
}
func profilesEqual(a, b CollectionProfile) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func copyExecution(source Execution) Execution {
	copy := Execution{EntryPoint: source.EntryPoint}
	if source.Receipt == nil {
		return copy
	}
	r := *source.Receipt
	r.PostState = append([]byte(nil), source.Receipt.PostState...)
	if r.EffectiveGasPrice != nil {
		r.EffectiveGasPrice = new(big.Int).Set(r.EffectiveGasPrice)
	}
	if r.BlobGasPrice != nil {
		r.BlobGasPrice = new(big.Int).Set(r.BlobGasPrice)
	}
	if r.BlockNumber != nil {
		r.BlockNumber = new(big.Int).Set(r.BlockNumber)
	}
	r.Logs = make([]*ethtypes.Log, len(source.Receipt.Logs))
	for i, sourceLog := range source.Receipt.Logs {
		if sourceLog == nil {
			continue
		}
		log := *sourceLog
		log.Topics = append([]common.Hash(nil), sourceLog.Topics...)
		log.Data = append([]byte(nil), sourceLog.Data...)
		r.Logs[i] = &log
	}
	copy.Receipt = &r
	return copy
}
