package keeper

import (
	"context"
	"testing"
	"time"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/stretchr/testify/require"
)

func TestMintCandyPreflightGas(t *testing.T) {
	parentMeter := storetypes.NewGasMeter(100)
	ctx := sdk.Context{}.WithContext(context.Background()).WithGasMeter(parentMeter).WithBlockTime(time.Unix(10, 0))
	key := struct{ purpose string }{"disposable reservation"}
	msg := core.Message{GasLimit: 20}
	var k Keeper
	prepared, used, exhausted := k.prepareNativeContext(ctx, msg)
	require.False(t, exhausted)
	require.Zero(t, used)
	require.Same(t, parentMeter, prepared.GasMeter())
	k.SetTransactionContext(func(child sdk.Context, msg core.Message) sdk.Context {
		child.GasMeter().ConsumeGas(15, "verified pre-state")
		return child.WithValue(key, true)
	})
	prepared, used, exhausted = k.prepareNativeContext(ctx, msg)
	require.False(t, exhausted)
	require.Equal(t, uint64(15), used)
	require.Same(t, parentMeter, prepared.GasMeter())
	require.Equal(t, ctx.KVGasConfig(), prepared.KVGasConfig())
	require.Equal(t, true, prepared.Value(key))
	require.Nil(t, ctx.Value(key))
	require.Zero(t, parentMeter.GasConsumed())

	msg.GasLimit = 14
	// No EVM bytecode is executed once preflight runs out of signed gas.
	after, res, err := k.applyOuterMessage(ctx, msg, false, nil, statedb.TxConfig{})
	require.NoError(t, err)
	require.Equal(t, vm.ErrOutOfGas.Error(), res.VmError)
	require.Equal(t, msg.GasLimit, res.GasUsed)
	require.Equal(t, msg.GasLimit, res.MaxUsedGas)
	require.Nil(t, after.Value(key))
	require.Zero(t, parentMeter.GasConsumed())
}
