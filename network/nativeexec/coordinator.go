// Package nativeexec coordinates staged native receipt plans after EVM execution.
// It does not authenticate assets or activate application financial routes.
package nativeexec

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"reflect"

	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

type EntryPoint uint8

const (
	Unspecified EntryPoint = iota
	SignedEthereum
	Governance
	NativeSDK // Reserved until its complete execution wrapper is tested.
	IBC       // Reserved until complete callback/internal-call coverage is tested.
)

type Execution struct {
	EntryPoint EntryPoint
	Sender     common.Address
	Message    core.Message
	Receipt    *ethtypes.Receipt
}

// Prepared validates OLD native prestate before any stage writes. Apply may
// recheck its own rows, but must not reject legitimate earlier-stage changes by
// comparing an unrelated module to its old snapshot. No stage mutates the EVM.
type Prepared interface {
	Validate(sdk.Context) error
	Apply(sdk.Context) error
}

type Preparer interface {
	Prepare(sdk.Context, Execution) (Prepared, error)
}

type Finalizer interface {
	ValidateFinal(sdk.Context, Execution) error
}

type Limits struct {
	MaxLogs       uint32
	MaxBytes      uint32 // Variable payload/entry budget, not a Go heap-size claim.
	MaxCopyWeight uint32 // Logical field/container weight, not allocated Go heap.
}

type Coordinator struct {
	stages [3]Preparer // Assets, NFT state, validator/bindings, in this order.
	final  Finalizer
	limits Limits
}

func missing(value interface{}) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return v.IsNil()
	}
	return false
}

func New(assets, nft, validators Preparer, final Finalizer, limits Limits) (*Coordinator, error) {
	if missing(assets) || missing(nft) || missing(validators) || missing(final) {
		return nil, errors.New("native execution requires all three planners and final invariants")
	}
	if limits.MaxLogs == 0 || limits.MaxLogs > 1024 || limits.MaxBytes == 0 || limits.MaxBytes > 1024*1024 || limits.MaxCopyWeight < 2048 || limits.MaxCopyWeight > 8*1024*1024 {
		return nil, errors.New("invalid native execution input bounds")
	}
	return &Coordinator{stages: [3]Preparer{assets, nft, validators}, final: final, limits: limits}, nil
}

type scopeKey struct{}

// Process commits one outer SDK cache only after all plans and final invariants
// succeed. It inherits the caller's meter. OOG panics propagate to the ordinary
// Ethereum/SDK/governance boundary; they can never commit this child cache.
func (c *Coordinator) Process(ctx sdk.Context, execution Execution) error {
	if c == nil || c.limits.MaxCopyWeight < 2048 || execution.Receipt == nil {
		return errors.New("native execution coordinator or receipt is missing")
	}
	if execution.EntryPoint != SignedEthereum && execution.EntryPoint != Governance {
		return errors.New("native execution entry point has no reviewed adapter")
	}
	if ctx.ChainID() == "" || ctx.BlockHeight() <= 0 || ctx.Value(scopeKey{}) != nil {
		return errors.New("invalid, nested or stale native execution scope")
	}
	if execution.Sender != execution.Message.From {
		return errors.New("native execution sender differs from EVM message")
	}
	if execution.Receipt.Status == ethtypes.ReceiptStatusFailed {
		return nil
	}
	if execution.Receipt.Status != ethtypes.ReceiptStatusSuccessful || execution.Receipt.GasUsed > execution.Message.GasLimit || execution.Message.GasLimit == 0 {
		return errors.New("invalid successful native execution receipt")
	}
	// Internal governance and the current gas estimator omit inclusion metadata.
	// Where supplied, it must describe this context. This is a consistency check,
	// not receipt authentication or permission to dispatch it a second time.
	if number := execution.Receipt.BlockNumber; number != nil && (!number.IsInt64() || number.Int64() != ctx.BlockHeight()) {
		return errors.New("native receipt belongs to a different block height")
	}
	if hash := execution.Receipt.BlockHash; hash != (common.Hash{}) && hash != common.BytesToHash(ctx.HeaderHash()) {
		return errors.New("native receipt belongs to a different block hash")
	}
	work, err := c.inputWork(execution)
	if err != nil {
		return err
	}
	// Each stage receives its own detached copy. Charge for those copies before
	// allocation, retaining the caller's transaction/governance work boundary.
	ctx.GasMeter().ConsumeGas(work*5, "bounded native execution input copies")
	execution = cloneExecution(execution)
	child, commit := ctx.CacheContext()
	child = child.WithValue(scopeKey{}, struct{}{})
	var plans [3]Prepared
	for i, stage := range c.stages {
		read, _ := child.CacheContext()
		plans[i], err = stage.Prepare(read, cloneExecution(execution))
		if err != nil {
			return fmt.Errorf("prepare native stage %d: %w", i, err)
		}
		if missing(plans[i]) {
			return fmt.Errorf("native stage %d returned no plan", i)
		}
	}
	for i, plan := range plans {
		read, _ := child.CacheContext()
		if err := plan.Validate(read); err != nil {
			return fmt.Errorf("validate native stage %d: %w", i, err)
		}
	}
	for i, plan := range plans {
		if err := plan.Apply(child); err != nil {
			return fmt.Errorf("apply native stage %d: %w", i, err)
		}
	}
	read, _ := child.CacheContext()
	if err := c.final.ValidateFinal(read, cloneExecution(execution)); err != nil {
		return fmt.Errorf("final native invariants: %w", err)
	}
	// SDK CacheContext already forwards its events on commit, exactly once.
	commit()
	return nil
}

func (c *Coordinator) inputWork(e Execution) (uint64, error) {
	if uint64(len(e.Receipt.Logs)) > uint64(c.limits.MaxLogs) {
		return 0, errors.New("native execution log count exceeds bound")
	}
	var total uint64
	// Fixed protocol weights cover both envelopes, pointers and bounded uint256
	// values. These weights bound represented fields/containers, independently
	// of payload. Go allocator rounding and runtime heap overhead are not a
	// byte-exact consensus resource metric and are not claimed here.
	copied := uint64(2048)
	add := func(payload, memory uint64) bool {
		if payload > uint64(c.limits.MaxBytes)-total || memory > uint64(c.limits.MaxCopyWeight)-copied {
			return false
		}
		total += payload
		copied += memory
		return true
	}
	items := func(count int, payload, memory uint64) bool {
		if uint64(count) > (uint64(c.limits.MaxBytes)-total)/payload || uint64(count) > (uint64(c.limits.MaxCopyWeight)-copied)/memory {
			return false
		}
		return add(uint64(count)*payload, uint64(count)*memory)
	}
	if !add(uint64(len(e.Message.Data)), uint64(len(e.Message.Data))) || !add(uint64(len(e.Receipt.PostState)), uint64(len(e.Receipt.PostState))) || !items(len(e.Message.BlobHashes), 32, 32) || !items(len(e.Message.SetCodeAuthorizations), 128, 160) {
		return 0, errors.New("native execution input exceeds byte bound")
	}
	for _, number := range []*big.Int{e.Message.Value, e.Message.GasPrice, e.Message.GasFeeCap, e.Message.GasTipCap, e.Message.BlobGasFeeCap, e.Receipt.BlockNumber, e.Receipt.EffectiveGasPrice, e.Receipt.BlobGasPrice} {
		if number != nil && (number.Sign() < 0 || number.BitLen() > 256) {
			return 0, errors.New("invalid native execution numeric input")
		}
	}
	for _, tuple := range e.Message.AccessList {
		if !add(20, 64) || !items(len(tuple.StorageKeys), 32, 32) {
			return 0, errors.New("native execution access list exceeds byte bound")
		}
	}
	for _, log := range e.Receipt.Logs {
		if log == nil || log.Removed || len(log.Topics) > 4 || !add(20, 288) || !items(len(log.Topics), 32, 32) || !add(uint64(len(log.Data)), uint64(len(log.Data))) {
			return 0, errors.New("invalid or oversized native execution log")
		}
	}
	return total + uint64(len(e.Receipt.Logs))*100 + uint64(len(e.Message.AccessList))*32 + uint64(len(e.Message.SetCodeAuthorizations))*32, nil
}

func cloneBig(v *big.Int) *big.Int {
	if v == nil {
		return nil
	}
	return new(big.Int).Set(v)
}

func cloneExecution(e Execution) Execution {
	m := e.Message
	if m.To != nil {
		address := *m.To
		m.To = &address
	}
	m.Value, m.GasPrice, m.GasFeeCap, m.GasTipCap, m.BlobGasFeeCap = cloneBig(m.Value), cloneBig(m.GasPrice), cloneBig(m.GasFeeCap), cloneBig(m.GasTipCap), cloneBig(m.BlobGasFeeCap)
	m.Data = bytes.Clone(m.Data)
	m.BlobHashes = append([]common.Hash(nil), m.BlobHashes...)
	m.SetCodeAuthorizations = append([]ethtypes.SetCodeAuthorization(nil), m.SetCodeAuthorizations...)
	m.AccessList = append(ethtypes.AccessList(nil), m.AccessList...)
	for i := range m.AccessList {
		m.AccessList[i].StorageKeys = append([]common.Hash(nil), m.AccessList[i].StorageKeys...)
	}
	e.Message = m
	r := *e.Receipt
	r.PostState = bytes.Clone(r.PostState)
	r.BlockNumber, r.EffectiveGasPrice, r.BlobGasPrice = cloneBig(r.BlockNumber), cloneBig(r.EffectiveGasPrice), cloneBig(r.BlobGasPrice)
	r.Logs = make([]*ethtypes.Log, len(e.Receipt.Logs))
	for i, original := range e.Receipt.Logs {
		log := *original
		log.Topics = append([]common.Hash(nil), original.Topics...)
		log.Data = bytes.Clone(original.Data)
		r.Logs[i] = &log
	}
	e.Receipt = &r
	return e
}

type routeHook struct {
	coordinator *Coordinator
	route       EntryPoint
}

func (h routeHook) PostTxProcessing(ctx sdk.Context, sender common.Address, message core.Message, receipt *ethtypes.Receipt) error {
	return h.coordinator.Process(ctx, Execution{EntryPoint: h.route, Sender: sender, Message: message, Receipt: receipt})
}

func (c *Coordinator) EthereumHooks() evmtypes.EvmHooks {
	return routeHook{coordinator: c, route: SignedEthereum}
}

func (c *Coordinator) GovernanceHooks() evmtypes.EvmHooks {
	return routeHook{coordinator: c, route: Governance}
}
