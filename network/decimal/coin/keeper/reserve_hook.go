package keeper

import (
	"errors"
	"fmt"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
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

func (h *ReserveHook) PostTxProcessing(ctx sdk.Context, _ common.Address, _ core.Message, receipt *ethtypes.Receipt) error {
	if h == nil || h.keeper == nil || h.read == nil {
		return errors.New("reserve hook is not initialized")
	}
	if receipt == nil {
		return errors.New("missing EVM receipt")
	}
	// Cosmos EVM invokes post-processing even for reverted execution. Those
	// receipts must never synchronize native state or call the backing reader.
	if receipt.Status == ethtypes.ReceiptStatusFailed {
		return nil
	}
	if receipt.Status != ethtypes.ReceiptStatusSuccessful || len(receipt.Logs) > h.maxLogs {
		return errors.New("invalid receipt status or receipt exceeds configured log limit")
	}
	type update struct {
		address common.Address
		denom   string
		volume  sdkmath.Int
		reserve sdkmath.Int
	}
	var updates []update
	indexes := make(map[common.Address]int)
	for _, log := range receipt.Logs {
		if log == nil {
			return errors.New("nil EVM receipt log")
		}
		if len(log.Topics) == 0 || log.Topics[0] != h.event.ID {
			continue
		}
		coin, err := h.keeper.GetCoinByDRC(ctx, log.Address.Hex())
		if errors.Is(err, ErrNotFound) {
			continue // Unregistered emitters cannot control native coin metadata.
		}
		if err != nil {
			return fmt.Errorf("reserve event token lookup: %w", err)
		}
		params, err := h.keeper.GetParams(ctx)
		if err != nil {
			return err
		}
		if coin.Denom == params.BaseDenom {
			return errors.New("reserve events cannot update native base supply")
		}
		if log.Removed || len(log.Topics) != 1 || len(log.Data) != 96 {
			return errors.New("malformed registered-token reserve event")
		}
		values, err := h.event.Inputs.Unpack(log.Data)
		if err != nil {
			return fmt.Errorf("decode reserve event: %w", err)
		}
		// The constructor accepts only static uint256 fields. Exact ABI word
		// lengths above exclude trailing data and truncated encodings.
		reserveValue, reserveOK := values[0].(*big.Int)
		volumeValue, volumeOK := values[1].(*big.Int)
		if !reserveOK || !volumeOK || reserveValue == nil || volumeValue == nil {
			return errors.New("unexpected decoded reserve event fields")
		}
		reserve := sdkmath.NewIntFromBigInt(reserveValue)
		volume := sdkmath.NewIntFromBigInt(volumeValue)
		u := update{address: log.Address, denom: coin.Denom, volume: volume, reserve: reserve}
		if index, exists := indexes[log.Address]; exists {
			updates[index] = u
		} else {
			indexes[log.Address] = len(updates)
			updates = append(updates, u)
		}
	}
	cached, commit := ctx.CacheContext()
	for _, update := range updates {
		readContext, _ := cached.CacheContext()
		volume, reserve, err := h.read(readContext, update.address)
		if err != nil {
			return fmt.Errorf("read final token backing: %w", err)
		}
		if volume.IsNil() || reserve.IsNil() || !volume.Equal(update.volume) || !reserve.Equal(update.reserve) {
			return errors.New("reserve event differs from final token supply or native backing")
		}
		if err := h.keeper.UpdateCoinVR(cached, update.denom, update.volume, update.reserve); err != nil {
			return fmt.Errorf("apply reserve event: %w", err)
		}
	}
	commit()
	return nil
}
