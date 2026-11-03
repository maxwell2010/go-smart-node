package keeper

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// ReserveSnapshot reads the final, same-context token supply and native DEL
// backing. Implementations must use the authenticated token contract and actual
// EVM/native state, not an RPC quote. The hook discards the reader's child cache.
type ReserveSnapshot func(sdk.Context, common.Address) (volume, reserve sdkmath.Int, err error)

// ReserveHook synchronizes existing native coin metadata only. It never mints
// bank coins, creates token bindings, or mirrors wrapped DEL into native supply.
type ReserveHook struct {
	keeper  *StateKeeper
	event   abi.Event
	read    ReserveSnapshot
	maxLogs int
}

var _ evmtypes.EvmHooks = (*ReserveHook)(nil)

func NewReserveHook(k *StateKeeper, compiledABI abi.ABI, read ReserveSnapshot, maxLogs int) (*ReserveHook, error) {
	if k == nil || read == nil || maxLogs <= 0 {
		return nil, errors.New("reserve hook requires keeper, state reader and positive receipt limit")
	}
	event, ok := compiledABI.Events["ReserveUpdated"]
	if !ok || event.Anonymous || event.Sig != "ReserveUpdated(uint256,uint256,uint256)" || len(event.Inputs) != 3 {
		return nil, errors.New("unexpected compiled ReserveUpdated ABI")
	}
	inputs := append(abi.Arguments(nil), event.Inputs...)
	for _, input := range inputs {
		if input.Indexed || input.Type.T != abi.UintTy || input.Type.Size != 256 {
			return nil, errors.New("reserve event must contain three non-indexed uint256 fields")
		}
	}
	event.Inputs = inputs
	return &ReserveHook{keeper: k, event: event, read: read, maxLogs: maxLogs}, nil
}

type reserveUpdate struct {
	address common.Address
	denom   string
	volume  sdkmath.Int
	reserve sdkmath.Int
	before  []byte
}

// ReservePlan is opaque and belongs to one hook and one execution context. It is
// not receipt authentication or a replay identifier. Apply consumes the plan,
// including on failure; a discarded transaction must prepare a new plan.
type ReservePlan struct {
	hook       *ReserveHook
	updates    []reserveUpdate
	params     []byte
	chainID    string
	height     int64
	header     []byte
	headerHash []byte
	tx         []byte
	use        *reservePlanUse
}

type reservePlanUse struct {
	owner *ReserveHook
	used  atomic.Bool
}

func (p *ReservePlan) IsNoop() bool {
	return p != nil && p.use != nil && p.use.owner == p.hook && len(p.updates) == 0
}

// Prepare captures existing nonbase metadata and validates final authenticated
// backing before any metadata write. Unrelated receipts perform no KV reads.
func (h *ReserveHook) Prepare(ctx sdk.Context, receipt *ethtypes.Receipt) (*ReservePlan, error) {
	if h == nil || h.keeper == nil || h.read == nil {
		return nil, errors.New("reserve hook is not initialized")
	}
	if receipt == nil {
		return nil, errors.New("missing EVM receipt")
	}
	plan := &ReservePlan{hook: h, use: &reservePlanUse{owner: h}}
	// Cosmos EVM invokes post-processing even for reverted execution. Those
	// receipts must never synchronize native state or call the backing reader.
	if receipt.Status == ethtypes.ReceiptStatusFailed {
		return plan, nil
	}
	if receipt.Status != ethtypes.ReceiptStatusSuccessful || len(receipt.Logs) > h.maxLogs {
		return nil, errors.New("invalid receipt status or receipt exceeds configured log limit")
	}
	indexes := make(map[common.Address]int)
	for _, log := range receipt.Logs {
		if log == nil {
			return nil, errors.New("nil EVM receipt log")
		}
		if len(log.Topics) == 0 || log.Topics[0] != h.event.ID {
			continue
		}
		coin, err := h.keeper.GetCoinByDRC(ctx, log.Address.Hex())
		if errors.Is(err, ErrNotFound) {
			continue // Unregistered emitters cannot control native coin metadata.
		}
		if err != nil {
			return nil, fmt.Errorf("reserve event token lookup: %w", err)
		}
		params, err := h.keeper.GetParams(ctx)
		if err != nil {
			return nil, err
		}
		if coin.Denom == params.BaseDenom {
			return nil, errors.New("reserve events cannot update native base supply")
		}
		if log.Removed || len(log.Topics) != 1 || len(log.Data) != 96 {
			return nil, errors.New("malformed registered-token reserve event")
		}
		values, err := h.event.Inputs.Unpack(log.Data)
		if err != nil {
			return nil, fmt.Errorf("decode reserve event: %w", err)
		}
		// The constructor accepts only static uint256 fields. Exact ABI word
		// lengths above exclude trailing data and truncated encodings.
		reserveValue, reserveOK := values[0].(*big.Int)
		volumeValue, volumeOK := values[1].(*big.Int)
		if !reserveOK || !volumeOK || reserveValue == nil || volumeValue == nil {
			return nil, errors.New("unexpected decoded reserve event fields")
		}
		reserve := sdkmath.NewIntFromBigInt(reserveValue)
		volume := sdkmath.NewIntFromBigInt(volumeValue)
		before, err := proto.Marshal(&coin)
		if err != nil {
			return nil, err
		}
		plan.params, err = proto.Marshal(&params)
		if err != nil {
			return nil, err
		}
		u := reserveUpdate{address: log.Address, denom: coin.Denom, volume: volume, reserve: reserve, before: before}
		if index, exists := indexes[log.Address]; exists {
			plan.updates[index] = u
		} else {
			indexes[log.Address] = len(plan.updates)
			plan.updates = append(plan.updates, u)
		}
	}
	if plan.IsNoop() {
		return plan, nil
	}
	header := ctx.BlockHeader()
	var err error
	plan.header, err = proto.Marshal(&header)
	if err != nil {
		return nil, err
	}
	plan.headerHash = append([]byte(nil), ctx.HeaderHash()...)
	plan.tx = append([]byte(nil), ctx.TxBytes()...)
	plan.chainID, plan.height = ctx.ChainID(), ctx.BlockHeight()
	if err := plan.validate(ctx); err != nil {
		return nil, err
	}
	return plan, nil
}

func (p *ReservePlan) validate(ctx sdk.Context) error {
	if p == nil || p.hook == nil || p.use == nil || p.use.owner != p.hook || p.hook.keeper == nil || p.hook.read == nil {
		return errors.New("reserve plan is not initialized")
	}
	if p.IsNoop() {
		return nil
	}
	header := ctx.BlockHeader()
	encodedHeader, err := proto.Marshal(&header)
	if err != nil {
		return err
	}
	if p.chainID != ctx.ChainID() || p.height != ctx.BlockHeight() || !bytes.Equal(p.header, encodedHeader) || !bytes.Equal(p.headerHash, ctx.HeaderHash()) || !bytes.Equal(p.tx, ctx.TxBytes()) {
		return errors.New("reserve plan execution context changed")
	}
	params, err := p.hook.keeper.GetParams(ctx)
	if err != nil {
		return err
	}
	encodedParams, err := proto.Marshal(&params)
	if err != nil {
		return err
	}
	if !bytes.Equal(p.params, encodedParams) {
		return errors.New("reserve plan parameters changed")
	}
	for _, update := range p.updates {
		coin, err := p.hook.keeper.GetCoinByDRC(ctx, update.address.Hex())
		if err != nil {
			return fmt.Errorf("reserve plan token lookup: %w", err)
		}
		before, err := proto.Marshal(&coin)
		if err != nil {
			return err
		}
		if !bytes.Equal(update.before, before) {
			return errors.New("reserve plan native coin changed")
		}
		coin.Volume, coin.Reserve = update.volume, update.reserve
		if err := coin.Validate(); err != nil {
			return fmt.Errorf("invalid final coin metadata: %w", err)
		}
		readContext, _ := ctx.CacheContext()
		volume, reserve, err := p.hook.read(readContext, update.address)
		if err != nil {
			return fmt.Errorf("read final token backing: %w", err)
		}
		if volume.IsNil() || reserve.IsNil() || !volume.Equal(update.volume) || !reserve.Equal(update.reserve) {
			return errors.New("reserve event differs from final token supply or native backing")
		}
	}
	return nil
}

// Validate rechecks native prestate and actual EVM/bank backing. Context identity
// alone cannot exclude a later EVM change within one height or governance call.
func (p *ReservePlan) Validate(ctx sdk.Context) error {
	if p == nil || p.use == nil || p.use.used.Load() {
		return errors.New("reserve plan is missing or already consumed")
	}
	return p.validate(ctx)
}

// Apply revalidates even for standalone callers, consumes the plan once, and
// keeps all metadata writes and typed events in one child transaction.
func (p *ReservePlan) Apply(ctx sdk.Context) error {
	if p == nil || p.use == nil || !p.use.used.CompareAndSwap(false, true) {
		return errors.New("reserve plan is missing or already consumed")
	}
	if err := p.validate(ctx); err != nil {
		return err
	}
	if p.IsNoop() {
		return nil
	}
	cached, commit := ctx.CacheContext()
	for _, update := range p.updates {
		if err := p.hook.keeper.UpdateCoinVR(cached, update.denom, update.volume, update.reserve); err != nil {
			return fmt.Errorf("apply reserve event: %w", err)
		}
	}
	commit()
	return nil
}

func (h *ReserveHook) PostTxProcessing(ctx sdk.Context, _ common.Address, _ core.Message, receipt *ethtypes.Receipt) error {
	plan, err := h.Prepare(ctx, receipt)
	if err != nil {
		return err
	}
	return plan.Apply(ctx)
}
