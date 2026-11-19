// Package fees contains bounded helpers for explicitly authorized reserve fees.
package fees

import (
	"errors"
	"fmt"
	"math"
	"reflect"

	storetypes "cosmossdk.io/store/types"
)

// This descriptor is the existing Cosmos EVM keeper's complete meter reset.
// An ordinary refund may never refund the paid conversion-work reservation.
const evmGasCountReset = "reset the gas count"

// WithReservedGas preserves already paid ante conversion work when Cosmos EVM
// replaces intrinsic/SDK consumption with its final EVM gas count. Call exactly
// once, AFTER the ordinary EVM ante has installed its meter. Extra work must
// already have been bounded, performed, and paid by trusted ante code. This
// helper neither authorizes a fee payer nor executes or charges a conversion.
//
// Finite EVM budget remains unchanged: adding the same reservation to limit and
// consumption adds no spendable EVM gas. The combined consumed gas is what SDK
// transaction/block accounting observes. Native SDK infinite simulation meters
// preserve their unbounded sentinel. Other MaxUint64-limit implementations are
// rejected on addition overflow instead of guessing their infinite semantics.
func WithReservedGas(meter storetypes.GasMeter, extraPaidGas uint64) (storetypes.GasMeter, error) {
	if meter == nil {
		return nil, errors.New("reserve fee gas requires a meter")
	}
	value := reflect.ValueOf(meter)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		if value.IsNil() {
			return nil, errors.New("reserve fee gas requires a nonnil meter")
		}
	}
	if extraPaidGas == 0 {
		return meter, nil
	}
	if _, nested := meter.(*reservedGasMeter); nested {
		return nil, errors.New("reserve fee gas is already installed")
	}
	infinite := reflect.TypeOf(meter) == reflect.TypeOf(storetypes.NewInfiniteGasMeter())
	limit := meter.Limit()
	if !infinite {
		if limit > math.MaxUint64-extraPaidGas {
			return nil, errors.New("combined reserve fee gas limit overflows")
		}
		limit += extraPaidGas
	}
	if meter.GasConsumed() > math.MaxUint64-extraPaidGas {
		return nil, errors.New("combined reserve fee consumption overflows")
	}
	return &reservedGasMeter{underlying: meter, extra: extraPaidGas, limit: limit, infinite: infinite}, nil
}

type reservedGasMeter struct {
	underlying storetypes.GasMeter
	extra      uint64
	limit      uint64
	infinite   bool
	overflow   bool
}

var _ storetypes.GasMeter = (*reservedGasMeter)(nil)

func (g *reservedGasMeter) GasConsumed() uint64 {
	consumed := g.underlying.GasConsumed()
	if g.overflow || consumed > math.MaxUint64-g.extra {
		return math.MaxUint64
	}
	return consumed + g.extra
}

func (g *reservedGasMeter) GasConsumedToLimit() uint64 {
	if g.IsPastLimit() {
		return g.limit
	}
	return g.GasConsumed()
}

func (g *reservedGasMeter) GasRemaining() uint64 {
	if g.overflow {
		return 0
	}
	if g.infinite {
		return math.MaxUint64
	}
	consumed := g.GasConsumed()
	if consumed >= g.limit {
		return 0
	}
	return g.limit - consumed
}

func (g *reservedGasMeter) Limit() uint64 { return g.limit }

func (g *reservedGasMeter) ConsumeGas(amount uint64, descriptor string) {
	if g.overflow || amount > math.MaxUint64-g.GasConsumed() {
		g.overflow = true
		panic(storetypes.ErrorGasOverflow{Descriptor: descriptor})
	}
	g.underlying.ConsumeGas(amount, descriptor)
	// Cosmos EVM's InfiniteGasMeterWithLimit publishes a finite limit but
	// intentionally does not enforce it. This wrapper must enforce the
	// original signed EVM allowance independently of that implementation.
	if !g.infinite && g.GasConsumed() > g.limit {
		panic(storetypes.ErrorOutOfGas{Descriptor: descriptor})
	}
}

func (g *reservedGasMeter) RefundGas(amount uint64, descriptor string) {
	if descriptor == evmGasCountReset {
		// The keeper passes the wrapper's TOTAL count. Only its ordinary EVM
		// part is reset; otherwise the conversion offset would be refunded or
		// the SDK meter would panic because total exceeds underlying count.
		if amount != g.GasConsumed() {
			panic(storetypes.ErrorNegativeGasConsumed{Descriptor: descriptor})
		}
		g.underlying.RefundGas(g.underlying.GasConsumed(), descriptor)
		g.overflow = false
		return
	}
	// Only the ordinary work is refundable; even an amount less than total
	// consumption must not eat into the paid conversion-work reservation.
	if amount > g.underlying.GasConsumed() {
		panic(storetypes.ErrorNegativeGasConsumed{Descriptor: descriptor})
	}
	g.underlying.RefundGas(amount, descriptor)
}

func (g *reservedGasMeter) IsPastLimit() bool {
	return g.overflow || (!g.infinite && g.GasConsumed() > g.limit)
}

func (g *reservedGasMeter) IsOutOfGas() bool {
	return g.overflow || (!g.infinite && g.GasConsumed() >= g.limit)
}

func (g *reservedGasMeter) String() string {
	return fmt.Sprintf("ReservedFeeGasMeter: limit=%d consumed=%d reserved=%d infinite=%t",
		g.Limit(), g.GasConsumed(), g.extra, g.infinite)
}
