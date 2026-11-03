// Package governance projects authoritative native voting weight to SDK53 gov.
// It creates no second staking ledger, bank balance or redeemable SDK shares.
package governance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sort"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	nativetypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// Request binds valuation to complete canonical native records at tally time.
// NativeGenesis is an independent protobuf byte slice, not mutable keeper data.
type Request struct {
	ChainID       string
	Height        int64
	StateHash     [32]byte
	NativeGenesis []byte
}

func cloneRequest(r Request) Request { r.NativeGenesis = bytes.Clone(r.NativeGenesis); return r }

type PositionWeight struct {
	PositionHash [32]byte
	Ownership    sdkmath.Int
}
type ValidatorOwnership struct {
	Validator string
	Ownership sdkmath.Int
}
type Weights struct {
	ChainID     string
	Height      int64
	StateHash   [32]byte
	ValuationID string
	Positions   []PositionWeight
	Totals      []ValidatorOwnership
}

// WeightSource must return one weight per complete native delegation position,
// including zero-value positions and all validators. Custom valuation must use
// authoritative same-context conversion/ownership data, not an RPC/spot quote.
// DependencySource binds all non-validator valuation inputs to the tally.
type DependencySource interface {
	DependencyHash(context.Context, Request) ([32]byte, error)
}

type WeightSource interface {
	SnapshotWeights(context.Context, Request) (Weights, error)
}

func PositionHash(position nativetypes.Delegation) ([32]byte, error) {
	data, e := proto.Marshal(&position)
	if e != nil {
		return [32]byte{}, e
	}
	return sha256.Sum256(data), nil
}
func stateHash(ctx sdk.Context, g *nativetypes.GenesisState) ([32]byte, []byte, error) {
	data, e := proto.Marshal(g)
	if e != nil {
		return [32]byte{}, nil, e
	}
	hash := sha256.New()
	hash.Write([]byte("native-governance-tally-v1"))
	var height [8]byte
	binary.BigEndian.PutUint64(height[:], uint64(ctx.BlockHeight()))
	hash.Write(height[:])
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(ctx.ChainID())))
	hash.Write(size[:])
	hash.Write([]byte(ctx.ChainID()))
	hash.Write(data)
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest, data, nil
}

type votingValidator struct {
	native    nativetypes.Validator
	power     int64
	tokens    sdkmath.Int
	ownership sdkmath.Int
	consensus []byte
}
type votingDelegation struct {
	delegator, validator string
	ownership            sdkmath.Int
}

// Adapter is a frozen tally-time snapshot, not a proposal-creation snapshot.
// Every SDK read verifies that its chain/height/native records still match.
type Adapter struct {
	native         *nativekeeper.StateKeeper
	chainID        string
	height         int64
	digest         [32]byte
	validators     []votingValidator
	delegations    map[string][]votingDelegation
	total          sdkmath.Int
	valuationID    string
	dependency     DependencySource
	dependencyHash [32]byte
	request        Request
}

func Build(ctx sdk.Context, native *nativekeeper.StateKeeper, source WeightSource, powerScale sdkmath.Int) (*Adapter, error) {
	if native == nil || source == nil {
		return nil, errors.New("native keeper and authoritative ownership source are required")
	}
	value := reflect.ValueOf(source)
	switch value.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Map, reflect.Slice:
		if value.IsNil() {
			return nil, errors.New("ownership source is nil")
		}
	}
	if powerScale.IsNil() || !powerScale.IsPositive() {
		return nil, errors.New("voting power scale must be initialized and positive")
	}
	if ctx.BlockHeight() <= 0 || ctx.ChainID() == "" {
		return nil, errors.New("tally context requires chain identity and positive height")
	}
	g, e := native.ExportGenesis(ctx)
	if e != nil {
		return nil, e
	}
	if g.Params.BaseDenom != identity.BaseDenom {
		return nil, errors.New("native governance requires del denomination")
	}
	digest, data, e := stateHash(ctx, g)
	if e != nil {
		return nil, e
	}
	child, _ := ctx.CacheContext()
	request := Request{ChainID: ctx.ChainID(), Height: ctx.BlockHeight(), StateHash: digest, NativeGenesis: bytes.Clone(data)}
	var dependencyHash [32]byte
	dependency, hasDependency := source.(DependencySource)
	if hasDependency {
		dependencyHash, e = dependency.DependencyHash(child, cloneRequest(request))
		if e != nil {
			return nil, e
		}
	}
	weights, e := source.SnapshotWeights(child, cloneRequest(request))
	if e != nil {
		return nil, e
	}
	after, e := native.ExportGenesis(child)
	if e != nil {
		return nil, e
	}
	afterHash, _, e := stateHash(child, after)
	if e != nil {
		return nil, e
	}
	if afterHash != digest {
		return nil, errors.New("ownership source modified native records")
	}
	if weights.ChainID != ctx.ChainID() || weights.Height != ctx.BlockHeight() || weights.StateHash != digest || weights.ValuationID == "" {
		return nil, errors.New("ownership source snapshot does not match native tally state")
	}
	if hasDependency {
		after, err := dependency.DependencyHash(child, cloneRequest(request))
		if err != nil {
			return nil, err
		}
		if after != dependencyHash {
			return nil, errors.New("valuation dependency changed during snapshot")
		}
	}
	known := map[[32]byte]nativetypes.Delegation{}
	for _, d := range g.Delegations {
		key, e := PositionHash(d)
		if e != nil {
			return nil, e
		}
		if _, ok := known[key]; ok {
			return nil, errors.New("duplicate native position")
		}
		known[key] = d
	}
	if len(weights.Positions) != len(known) {
		return nil, errors.New("ownership source must cover every native position exactly once")
	}
	byValidator := map[string]nativetypes.Validator{}
	sums := map[string]sdkmath.Int{}
	for _, v := range g.Validators {
		byValidator[v.OperatorAddress] = v
		sums[v.OperatorAddress] = sdkmath.ZeroInt()
	}
	maximum := sdkmath.NewIntFromBigInt(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)))
	groups := map[string]map[string]sdkmath.Int{}
	seen := map[[32]byte]bool{}
	for _, weighted := range weights.Positions {
		d, ok := known[weighted.PositionHash]
		if !ok || seen[weighted.PositionHash] {
			return nil, errors.New("unknown or duplicate ownership position")
		}
		seen[weighted.PositionHash] = true
		amount := weighted.Ownership
		if amount.IsNil() || amount.IsNegative() || amount.GT(maximum.Sub(sums[d.Validator])) {
			return nil, errors.New("invalid or overflowing native position ownership")
		}
		sums[d.Validator] = sums[d.Validator].Add(amount)
		if groups[d.Delegator] == nil {
			groups[d.Delegator] = map[string]sdkmath.Int{}
		}
		old, ok := groups[d.Delegator][d.Validator]
		if !ok {
			old = sdkmath.ZeroInt()
		}
		groups[d.Delegator][d.Validator] = old.Add(amount)
	}
	expected := map[string]sdkmath.Int{}
	if len(weights.Totals) != len(g.Validators) {
		return nil, errors.New("ownership totals must cover every native validator")
	}
	for _, v := range weights.Totals {
		if _, ok := byValidator[v.Validator]; !ok {
			return nil, errors.New("unknown validator ownership total")
		}
		if _, ok := expected[v.Validator]; ok {
			return nil, errors.New("duplicate validator ownership total")
		}
		if v.Ownership.IsNil() || v.Ownership.IsNegative() || !v.Ownership.Equal(sums[v.Validator]) {
			return nil, errors.New("native position ownership does not conserve valuation total")
		}
		expected[v.Validator] = v.Ownership
	}
	a := &Adapter{native: native, chainID: ctx.ChainID(), height: ctx.BlockHeight(), digest: digest, delegations: map[string][]votingDelegation{}, total: sdkmath.ZeroInt(), valuationID: weights.ValuationID, dependency: dependency, dependencyHash: dependencyHash, request: request}
	for _, v := range g.Validators {
		power := v.ConsensusPower()
		if power == 0 {
			continue
		}
		ownership := sums[v.OperatorAddress]
		if ownership.IsZero() {
			return nil, fmt.Errorf("positive native power has no voting ownership for %s", v.OperatorAddress)
		}
		tokensBig := new(big.Int).Mul(big.NewInt(power), powerScale.BigInt())
		if tokensBig.Cmp(maximum.BigInt()) > 0 {
			return nil, errors.New("native voting token-weight overflows")
		}
		tokens := sdkmath.NewIntFromBigInt(tokensBig)
		// SDK53 tally multiplies shares by token-weight before division. Bound that
		// intermediate explicitly instead of allowing LegacyDec to panic.
		if new(big.Int).Mul(ownership.BigInt(), tokensBig).Cmp(maximum.BigInt()) > 0 {
			return nil, errors.New("ownership/token-weight product exceeds SDK tally arithmetic bounds")
		}
		if tokens.GT(maximum.Sub(a.total)) {
			return nil, errors.New("total native voting token-weight overflows")
		}
		a.total = a.total.Add(tokens)
		consensus, e := v.GetConsAddr()
		if e != nil {
			return nil, e
		}
		a.validators = append(a.validators, votingValidator{native: v, power: power, tokens: tokens, ownership: ownership, consensus: bytes.Clone(consensus)})
	}
	sort.Slice(a.validators, func(i, j int) bool {
		x, y := a.validators[i], a.validators[j]
		if x.power != y.power {
			return x.power > y.power
		}
		return bytes.Compare(x.consensus, y.consensus) < 0
	})
	eligible := map[string]bool{}
	for _, v := range a.validators {
		eligible[v.native.OperatorAddress] = true
	}
	for delegator, vals := range groups {
		for validator, amount := range vals {
			if eligible[validator] && amount.IsPositive() {
				a.delegations[delegator] = append(a.delegations[delegator], votingDelegation{delegator: delegator, validator: validator, ownership: amount})
			}
		}
		sort.Slice(a.delegations[delegator], func(i, j int) bool {
			return a.delegations[delegator][i].validator < a.delegations[delegator][j].validator
		})
	}
	return a, nil
}

func (a *Adapter) guard(ctx context.Context) error {
	if a == nil || a.native == nil {
		return errors.New("native voting snapshot is not initialized")
	}
	s := sdk.UnwrapSDKContext(ctx)
	if s.BlockHeight() != a.height || s.ChainID() != a.chainID {
		return errors.New("native voting snapshot used outside its tally context")
	}
	g, e := a.native.ExportGenesis(s)
	if e != nil {
		return e
	}
	digest, _, e := stateHash(s, g)
	if e != nil {
		return e
	}
	if digest != a.digest {
		return errors.New("native records changed after voting snapshot")
	}
	if a.dependency != nil {
		child, _ := s.CacheContext()
		current, err := a.dependency.DependencyHash(child, cloneRequest(a.request))
		if err != nil {
			return err
		}
		if current != a.dependencyHash {
			return errors.New("valuation dependency changed after voting snapshot")
		}
	}
	return nil
}

// BaseReserveSource uses actual complete native DEL ownership units, including
// NFT DEL reserve totals. It applies no per-position power floor or hold bonus.
// Custom denominations require a separate authoritative aggregate valuation.
type BaseReserveSource struct{}

func (s BaseReserveSource) SnapshotWeights(_ context.Context, r Request) (Weights, error) {
	var g nativetypes.GenesisState
	if e := proto.Unmarshal(r.NativeGenesis, &g); e != nil {
		return Weights{}, e
	}
	if e := g.Validate(); e != nil {
		return Weights{}, e
	}
	if g.Params.BaseDenom != identity.BaseDenom {
		return Weights{}, errors.New("base reserve source requires del")
	}
	out := Weights{ChainID: r.ChainID, Height: r.Height, StateHash: r.StateHash, ValuationID: "native-del-reserve-v1"}
	totals := map[string]*big.Int{}
	for _, v := range g.Validators {
		totals[v.OperatorAddress] = new(big.Int)
	}
	for _, d := range g.Delegations {
		if d.Stake.Stake.Denom != identity.BaseDenom {
			return Weights{}, errors.New("custom-reserve positions require an authoritative valuation source")
		}
		key, e := PositionHash(d)
		if e != nil {
			return Weights{}, e
		}
		amount := sdkmath.NewIntFromBigInt(d.Stake.Stake.Amount.BigInt())
		out.Positions = append(out.Positions, PositionWeight{PositionHash: key, Ownership: amount})
		totals[d.Validator].Add(totals[d.Validator], amount.BigInt())
	}
	for _, v := range g.Validators {
		amount := totals[v.OperatorAddress]
		if amount.BitLen() > 256 {
			return Weights{}, errors.New("native ownership aggregate overflows")
		}
		out.Totals = append(out.Totals, ValidatorOwnership{Validator: v.OperatorAddress, Ownership: sdkmath.NewIntFromBigInt(amount)})
	}
	return out, nil
}
