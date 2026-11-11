package keeper

import (
	"fmt"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
)

// SetInternalCommitGuard installs the application's check for internal EVM
// state transitions that do not pass through signed transaction hooks.
func (k *Keeper) SetInternalCommitGuard(guard func(sdk.Context, core.Message, []*ethtypes.Log) error) {
	if guard == nil || k.internalCommitGuard != nil {
		panic("internal EVM commit guard must be installed exactly once")
	}
	k.internalCommitGuard = guard
}

// nativeWorkConfig postpones the minimum fee floor until EVM and native work
// have both completed. The floor must not consume the native execution budget.
func nativeWorkConfig(cfg *statedb.EVMConfig, limit uint64) (*statedb.EVMConfig, uint64, error) {
	multiplier := cfg.FeeMarketParams.MinGasMultiplier
	if multiplier.IsNil() {
		multiplier = math.LegacyZeroDec()
	}
	floor := math.LegacyNewDecFromInt(math.NewIntFromUint64(limit)).Mul(multiplier).TruncateInt()
	if !floor.IsUint64() || floor.Uint64() > limit {
		return nil, 0, fmt.Errorf("invalid native transaction minimum gas")
	}
	copy := *cfg
	copy.FeeMarketParams.MinGasMultiplier = math.LegacyZeroDec()
	return &copy, floor.Uint64(), nil
}

// meteredNativeWork charges native post-processing at one EVM gas unit per SDK
// gas unit, within the signed transaction's remaining gas allowance. Reverted
// EVM execution has no native financial post-processing in this network.
func (k Keeper) meteredNativeWork(ctx sdk.Context, sender common.Address, msg core.Message, receipt *ethtypes.Receipt) (used uint64, err error) {
	if receipt == nil || receipt.GasUsed > msg.GasLimit {
		return 0, fmt.Errorf("invalid native receipt gas")
	}
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return 0, nil
	}
	meter := storetypes.NewGasMeter(msg.GasLimit - receipt.GasUsed)
	native := ctx.WithGasMeter(meter).
		WithKVGasConfig(storetypes.KVGasConfig()).
		WithTransientKVGasConfig(storetypes.TransientGasConfig())
	defer func() {
		used = meter.GasConsumedToLimit()
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(storetypes.ErrorOutOfGas); ok {
				err = vm.ErrOutOfGas
			} else {
				panic(recovered)
			}
		}
	}()
	return 0, k.PostTxProcessing(native, sender, msg, receipt)
}
