package fees

import (
	"math"
	"testing"

	storetypes "cosmossdk.io/store/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/stretchr/testify/require"
)

// This is precisely the reset performed by the existing Cosmos EVM keeper.
// Run multiple times to prove replacement does not erase or accumulate offset.
func evmReset(meter storetypes.GasMeter, used uint64) {
	meter.RefundGas(meter.GasConsumed(), "reset the gas count")
	meter.ConsumeGas(used, "apply evm transaction")
}

func TestReservedPaidWorkSurvivesRepeatedSDKMeterReplacement(t *testing.T) {
	ordinary := storetypes.NewGasMeter(100_000)
	ordinary.ConsumeGas(21_000, "intrinsic ante")
	meter, err := WithReservedGas(ordinary, 12_345)
	require.NoError(t, err)
	require.EqualValues(t, 112_345, meter.Limit())
	require.EqualValues(t, 33_345, meter.GasConsumed())
	require.EqualValues(t, 79_000, meter.GasRemaining(), "reservation grants no additional EVM execution budget")
	for _, actual := range []uint64{30_000, 50_000, 21_000, 0, 100_000} {
		evmReset(meter, actual)
		require.Equal(t, actual, ordinary.GasConsumed())
		require.Equal(t, actual+12_345, meter.GasConsumed())
		require.Equal(t, actual+12_345, meter.GasConsumedToLimit())
		require.Equal(t, uint64(100_000)-actual, meter.GasRemaining())
	}
	require.True(t, meter.IsOutOfGas())
	require.False(t, meter.IsPastLimit(), "exactly at limit is not past limit in the SDK")
	require.PanicsWithValue(t, storetypes.ErrorOutOfGas{Descriptor: "one too many"}, func() {
		meter.ConsumeGas(1, "one too many")
	})
	require.True(t, meter.IsPastLimit())
	require.EqualValues(t, 112_346, meter.GasConsumed())
	require.Equal(t, meter.Limit(), meter.GasConsumedToLimit(), "block charging clamps failed transaction to combined limit")
	require.Zero(t, meter.GasRemaining())
}

func TestOnlyOrdinaryRefundsAreRefundable(t *testing.T) {
	ordinary := storetypes.NewGasMeter(1_000)
	ordinary.ConsumeGas(200, "bank reads")
	meter, err := WithReservedGas(ordinary, 70)
	require.NoError(t, err)
	meter.RefundGas(50, "ordinary state refund")
	require.EqualValues(t, 150, ordinary.GasConsumed())
	require.EqualValues(t, 220, meter.GasConsumed())
	require.PanicsWithValue(t, storetypes.ErrorNegativeGasConsumed{Descriptor: "ordinary state refund"}, func() {
		meter.RefundGas(151, "ordinary state refund")
	})
	require.EqualValues(t, 220, meter.GasConsumed())
	require.PanicsWithValue(t, storetypes.ErrorNegativeGasConsumed{Descriptor: evmGasCountReset}, func() {
		meter.RefundGas(1, evmGasCountReset)
	})
	meter.RefundGas(150, "ordinary state refund")
	require.EqualValues(t, 70, meter.GasConsumed())
	evmReset(meter, 300)
	require.EqualValues(t, 370, meter.GasConsumed())
}

func TestReservedGasEnforcesActualEVMInfiniteWithLimitMeter(t *testing.T) {
	for _, path := range []string{"increment", "keeper-reset"} {
		t.Run(path, func(t *testing.T) {
			ordinary := evmtypes.NewInfiniteGasMeterWithLimit(100_000)
			ordinary.ConsumeGas(21_000, "intrinsic ante")
			meter, err := WithReservedGas(ordinary, 12_345)
			require.NoError(t, err)
			require.EqualValues(t, 79_000, meter.GasRemaining())
			evmReset(meter, 40_000)
			require.EqualValues(t, 52_345, meter.GasConsumed())
			require.EqualValues(t, 60_000, meter.GasRemaining())
			evmReset(meter, 100_000)
			require.True(t, meter.IsOutOfGas())
			require.False(t, meter.IsPastLimit())
			require.False(t, ordinary.IsOutOfGas(), "underlying Cosmos EVM meter deliberately never enforces its published limit")
			require.EqualValues(t, 0, meter.GasRemaining())
			descriptor := "increment beyond signed gas"
			if path == "keeper-reset" {
				descriptor = "apply evm transaction"
			}
			require.PanicsWithValue(t, storetypes.ErrorOutOfGas{Descriptor: descriptor}, func() {
				if path == "keeper-reset" {
					evmReset(meter, 100_001)
				} else {
					meter.ConsumeGas(1, descriptor)
				}
			})
			require.EqualValues(t, 100_001, ordinary.GasConsumed(), "overflowing charge must remain observable")
			require.EqualValues(t, 112_346, meter.GasConsumed())
			require.True(t, meter.IsPastLimit())
			require.True(t, meter.IsOutOfGas())
			require.EqualValues(t, 112_345, meter.GasConsumedToLimit())
			require.Zero(t, meter.GasRemaining())
		})
	}
}

func TestReservedGasSupportsSDKInfiniteSimulationMeter(t *testing.T) {
	ordinary := storetypes.NewInfiniteGasMeter()
	meter, err := WithReservedGas(ordinary, 90)
	require.NoError(t, err)
	require.EqualValues(t, uint64(math.MaxUint64), meter.Limit())
	require.EqualValues(t, uint64(math.MaxUint64), meter.GasRemaining())
	for _, used := range []uint64{50, 100_000_000, 0} {
		evmReset(meter, used)
		require.Equal(t, used+90, meter.GasConsumed())
		require.Equal(t, used+90, meter.GasConsumedToLimit())
		require.False(t, meter.IsPastLimit())
		require.False(t, meter.IsOutOfGas())
	}
	require.Contains(t, meter.String(), "reserved=90")
}

func TestReservedGasGuardsOverflowAndCannotBeInstalledTwice(t *testing.T) {
	var typedNil *reservedGasMeter
	for _, meter := range []storetypes.GasMeter{nil, typedNil} {
		_, err := WithReservedGas(meter, 1)
		require.Error(t, err)
	}
	_, err := WithReservedGas(storetypes.NewGasMeter(math.MaxUint64), 1)
	require.Error(t, err, "finite max-uint limit must not masquerade as infinite simulation")
	ordinary := storetypes.NewInfiniteGasMeter()
	ordinary.ConsumeGas(math.MaxUint64, "maximum")
	_, err = WithReservedGas(ordinary, 1)
	require.Error(t, err)
	base := storetypes.NewGasMeter(100)
	meter, err := WithReservedGas(base, 10)
	require.NoError(t, err)
	_, err = WithReservedGas(meter, 10)
	require.Error(t, err, "duplicate reservation would double charge the ante work")
	unchanged, err := WithReservedGas(meter, 0)
	require.NoError(t, err)
	require.Same(t, meter, unchanged)
	infinite, err := WithReservedGas(storetypes.NewInfiniteGasMeter(), 10)
	require.NoError(t, err)
	infinite.ConsumeGas(math.MaxUint64-10, "maximum combined")
	require.EqualValues(t, uint64(math.MaxUint64), infinite.GasConsumed())
	require.PanicsWithValue(t, storetypes.ErrorGasOverflow{Descriptor: "overflow"}, func() {
		infinite.ConsumeGas(1, "overflow")
	})
	require.EqualValues(t, uint64(math.MaxUint64), infinite.GasConsumedToLimit(), "overflow recovery must not panic while reporting gas")
	require.True(t, infinite.IsOutOfGas())
	require.Zero(t, infinite.GasRemaining())
}
