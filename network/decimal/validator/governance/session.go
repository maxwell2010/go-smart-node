package governance

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	coreaddress "cosmossdk.io/core/address"
	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// Sessions is immutable reader configuration. Weight snapshots live only in a
// scoped SDK context, never on this long-lived object or in a global cache.
type Sessions struct {
	state  votingStateReader
	source WeightSource
	scale  sdkmath.Int
	branch func(sdk.Context) any
}
type sessionKey struct{}
type session struct {
	mu           sync.Mutex
	owner        *Sessions
	chain        string
	height       int64
	at           time.Time
	closed, busy bool
	snapshot     *Adapter
	store        any
	failure      error
}

func NewSessions(native *nativekeeper.StateKeeper, source WeightSource, scale sdkmath.Int, branch func(sdk.Context) any) (*Sessions, error) {
	if native == nil {
		return nil, errors.New("native keeper and ownership source are required")
	}
	return newSessions(votingStateReader{read: native.ExportGenesis, domain: exportVotingDomain}, source, scale, branch)
}

// NewActiveDELSessions uses the authenticated active ownership projection for
// the DEL-only native network. The full export stays available to staged
// consumers and custom ownership sources through NewSessions.
func NewActiveDELSessions(native *nativekeeper.StateKeeper, scale sdkmath.Int, branch func(sdk.Context) any) (*Sessions, error) {
	if native == nil {
		return nil, errors.New("native keeper is required for active DEL voting")
	}
	return newSessions(votingStateReader{read: native.ReadActiveDELVotingState, domain: activeDELVotingDomain}, BaseReserveSource{}, scale, branch)
}

func newSessions(state votingStateReader, source WeightSource, scale sdkmath.Int, branch func(sdk.Context) any) (*Sessions, error) {
	if state.read == nil || source == nil {
		return nil, errors.New("native keeper and ownership source are required")
	}
	if branch == nil {
		return nil, errors.New("native branch identity function is required")
	}
	v := reflect.ValueOf(source)
	switch v.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Map, reflect.Slice:
		if v.IsNil() {
			return nil, errors.New("ownership source is nil")
		}
	}
	if scale.IsNil() || !scale.IsPositive() {
		return nil, errors.New("voting scale must be initialized and positive")
	}
	return &Sessions{state: state, source: source, scale: sdkmath.NewIntFromBigInt(scale.BigInt()), branch: branch}, nil
}

// Run scopes a sequence of canonical SDK tallies and commits its child SDK
// cache only if all tallies close and the callback succeeds. Each proposal's
// validator iteration starts a fresh snapshot, even at the same block height.
func (r *Sessions) Run(ctx sdk.Context, fn func(sdk.Context) error) error {
	return r.run(ctx, fn, true)
}

// ReadOnly provides the same lifecycle but always discards query-side writes.
func (r *Sessions) ReadOnly(ctx sdk.Context, fn func(sdk.Context) error) error {
	return r.run(ctx, fn, false)
}
func (r *Sessions) run(ctx sdk.Context, fn func(sdk.Context) error, commit bool) error {
	if r == nil || r.state.read == nil || fn == nil {
		return errors.New("native voting session is not configured")
	}
	if ctx.BlockHeight() <= 0 || ctx.ChainID() == "" || ctx.MultiStore() == nil {
		return errors.New("native voting session requires a valid block context")
	}
	if ctx.Value(sessionKey{}) != nil {
		return errors.New("nested native voting sessions are not allowed")
	}
	child, write := ctx.CacheContext()
	h := &session{owner: r, chain: ctx.ChainID(), height: ctx.BlockHeight(), at: ctx.BlockTime()}
	scoped := child.WithValue(sessionKey{}, h)
	defer func() { h.mu.Lock(); h.closed = true; h.snapshot = nil; h.store = nil; h.mu.Unlock() }()
	if e := fn(scoped); e != nil {
		return e
	}
	h.mu.Lock()
	failure := h.failure
	unfinished := h.busy || h.snapshot != nil
	h.mu.Unlock()
	if failure != nil {
		return failure
	}
	if unfinished {
		return errors.New("native voting tally did not close")
	}
	if commit {
		write()
	}
	return nil
}

func sdkContext(ctx context.Context) (sdk.Context, error) {
	if ctx == nil {
		return sdk.Context{}, errors.New("SDK context is missing")
	}
	if s, ok := ctx.(sdk.Context); ok {
		return s, nil
	}
	if s, ok := ctx.Value(sdk.SdkContextKey).(sdk.Context); ok {
		return s, nil
	}
	return sdk.Context{}, errors.New("SDK context is missing")
}

func (r *Sessions) branchIdentity(ctx sdk.Context) (identity any, e error) {
	defer func() {
		if recover() != nil {
			identity = nil
			e = errors.New("native voting branch identity lookup failed")
		}
	}()
	if r.branch == nil {
		return nil, errors.New("native voting branch identity is missing")
	}
	return r.branch(ctx), nil
}

// claim validates only context/session identity during per-voter reads. Native
// state is fingerprinted at snapshot creation and once more at total/close.
func (r *Sessions) claim(ctx context.Context, start bool) (*session, sdk.Context, *Adapter, error) {
	if ctx == nil {
		return nil, sdk.Context{}, nil, errors.New("native voting session is missing")
	}
	h, ok := ctx.Value(sessionKey{}).(*session)
	if !ok || h == nil {
		return nil, sdk.Context{}, nil, errors.New("native voting session is missing")
	}
	s, e := sdkContext(ctx)
	if e != nil {
		return nil, sdk.Context{}, nil, e
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	reject := func(message string) (*session, sdk.Context, *Adapter, error) {
		e := errors.New(message)
		if !h.closed && h.failure == nil {
			h.failure = e
		}
		return nil, sdk.Context{}, nil, e
	}
	if h.closed {
		return reject("native voting session is closed")
	}
	if h.owner != r {
		return reject("native voting session belongs to another reader")
	}
	if h.failure != nil {
		return nil, sdk.Context{}, nil, h.failure
	}
	if s.ChainID() != h.chain || s.BlockHeight() != h.height || !s.BlockTime().Equal(h.at) {
		return reject("native voting session context changed")
	}
	if h.busy {
		return reject("reentrant or concurrent native voting reads are not allowed")
	}
	store, e := r.branchIdentity(s)
	if e != nil {
		return reject(e.Error())
	}
	if store == nil || reflect.TypeOf(store).Kind() != reflect.Pointer || reflect.ValueOf(store).IsNil() || !reflect.TypeOf(store).Comparable() {
		return reject("unsupported voting context store identity")
	}
	if start {
		if h.snapshot != nil {
			return reject("previous native voting tally is still active")
		}
		h.store = store
	} else {
		if h.snapshot == nil {
			return reject("native voting tally has not started")
		}
		if h.store != store {
			return reject("native voting tally crossed SDK cache contexts")
		}
	}
	h.busy = true
	return h, s, h.snapshot, nil
}

func (h *session) finish(snapshot *Adapter, close bool, e error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.busy = false
	if h.closed {
		h.snapshot = nil
		h.store = nil
		return errors.New("native voting session is closed")
	}
	if e != nil && h.failure == nil {
		h.failure = e
	}
	if h.failure != nil {
		return h.failure
	}
	if close {
		h.snapshot = nil
		h.store = nil
	} else if snapshot != nil {
		h.snapshot = snapshot
	}
	return nil
}

func (r *Sessions) ValidatorAddressCodec() coreaddress.Codec {
	return address.NewBech32Codec(identity.Bech32PrefixValAddr)
}
func (r *Sessions) IterateBondedValidatorsByPower(ctx context.Context, fn func(int64, stakingtypes.ValidatorI) bool) error {
	h, s, _, e := r.claim(ctx, true)
	if e != nil {
		return e
	}
	if fn == nil {
		return h.finish(nil, false, errors.New("validator voting callback is missing"))
	}
	a, e := build(s, r.state, r.source, r.scale)
	if e == nil {
		e = a.iterateValidators(fn)
	}
	return h.finish(a, false, e)
}
func (r *Sessions) IterateDelegations(ctx context.Context, delegator sdk.AccAddress, fn func(int64, stakingtypes.DelegationI) bool) error {
	h, _, a, e := r.claim(ctx, false)
	if e != nil {
		return e
	}
	if fn == nil || len(delegator) != 20 {
		return h.finish(nil, false, errors.New("invalid voting delegation callback or owner"))
	}
	e = a.iterateDelegations(delegator, fn)
	return h.finish(nil, false, e)
}
func (r *Sessions) TotalBondedTokens(ctx context.Context) (sdkmath.Int, error) {
	h, s, a, e := r.claim(ctx, false)
	if e != nil {
		return sdkmath.Int{}, e
	}
	e = a.guard(s)
	if e = h.finish(nil, true, e); e != nil {
		return sdkmath.Int{}, e
	}
	return sdkmath.NewIntFromBigInt(a.total.BigInt()), nil
}
