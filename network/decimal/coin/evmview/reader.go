// Package evmview hydrates validated native metadata from the same EVM/bank state.
package evmview

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmkeeper "github.com/cosmos/evm/x/vm/keeper"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

type Metadata interface {
	GetCoin(sdk.Context, string) (types.Coin, error)
	GetParams(sdk.Context) (types.Params, error)
}
type Bank interface {
	GetBalance(context.Context, sdk.AccAddress, string) sdk.Coin
}

type BindingSource interface {
	CoinBinding(sdk.Context, string) (Binding, error)
}

// Reader has no financial or mutable cache. Configuration is pinned by its caller.
type Reader struct {
	metadata       Metadata
	bank           Bank
	evm            *evmkeeper.Keeper
	bindings       BindingSource
	data           []byte
	beaconData     []byte
	evmGas, sdkGas uint64
}

func nilDependency(value interface{}) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return v.IsNil()
	}
	return false
}

func New(metadata Metadata, bank Bank, evm *evmkeeper.Keeper, bindings BindingSource, compiledABI abi.ABI, evmGas, sdkGas uint64) (*Reader, error) {
	if nilDependency(metadata) || nilDependency(bank) || evm == nil || nilDependency(bindings) || evmGas < 21000 || evmGas > 5_000_000 || sdkGas == 0 || sdkGas > 20_000_000 {
		return nil, errors.New("coin view requires explicit dependencies and bounded EVM/SDK gas")
	}
	data, err := viewSelector(compiledABI, "totalSupply", abi.UintTy, 256)
	if err != nil {
		return nil, err
	}
	return &Reader{metadata: metadata, bank: bank, evm: evm, bindings: bindings, data: append([]byte(nil), data...), evmGas: evmGas, sdkGas: sdkGas}, nil
}

func (r *Reader) GetParams(ctx sdk.Context) (types.Params, error) {
	if r == nil || nilDependency(r.metadata) {
		return types.Params{}, errors.New("coin view is not initialized")
	}
	return r.metadata.GetParams(ctx)
}

// GetCoin discards every child write, even if an approved runtime misbehaves.
// It returns a validated, detached coin; native metadata is not updated here.
func (r *Reader) GetCoin(ctx sdk.Context, denom string) (coin types.Coin, err error) {
	if r == nil || r.evm == nil {
		return coin, errors.New("coin view is not initialized")
	}
	child, _ := ctx.CacheContext()
	meter := storetypes.NewGasMeter(r.sdkGas)
	child = child.WithGasMeter(meter)
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(storetypes.ErrorOutOfGas); ok {
				coin = types.Coin{}
				err = errors.New("coin view SDK gas limit exceeded")
			} else {
				panic(recovered)
			}
		}
		ctx.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), "bounded native coin view")
	}()
	params, e := r.metadata.GetParams(child)
	if e != nil {
		return coin, e
	}
	if e = params.Validate(); e != nil {
		return coin, e
	}
	if params.BaseDenom != identity.BaseDenom || denom == params.BaseDenom {
		return coin, errors.New("base DEL uses native ownership directly, not wrapped-token valuation")
	}
	coin, e = r.metadata.GetCoin(child, denom)
	if e != nil {
		return types.Coin{}, e
	}
	if e = coin.Validate(); e != nil {
		return types.Coin{}, e
	}
	if coin.Denom != denom || !types.ValidContractAddress(coin.DRC20Contract) {
		return types.Coin{}, errors.New("coin view lacks native token binding")
	}
	binding, e := r.bindings.CoinBinding(child, denom)
	if e != nil {
		return types.Coin{}, e
	}
	address := common.HexToAddress(coin.DRC20Contract)
	if binding.Address != address || binding.Address == (common.Address{}) || binding.RuntimeHash == (common.Hash{}) || r.evm.GetCodeHash(child, address) != binding.RuntimeHash {
		return types.Coin{}, errors.New("coin contract is not the approved runtime")
	}
	// Capture backing before executing even an approved getter. Native
	// precompiles invoked by bad getter code could write in its discarded child;
	// those speculative writes must not become the returned reserve either.
	balance := r.bank.GetBalance(child, sdk.AccAddress(address.Bytes()), identity.BaseDenom)
	if balance.Denom != identity.BaseDenom {
		return types.Coin{}, errors.New("coin reserve bank returned a different denomination")
	}
	if err := r.verifyBinding(ctx, child, binding, address); err != nil {
		return types.Coin{}, err
	}
	word, e := r.readWord(ctx, child, address, r.data)
	if e != nil {
		return types.Coin{}, e
	}
	volume := new(big.Int).SetBytes(word)
	coin.Volume = sdkmath.NewIntFromBigInt(volume)
	coin.Reserve = balance.Amount
	if e = coin.Validate(); e != nil {
		return types.Coin{}, fmt.Errorf("authoritative coin state violates monetary limits: %w", e)
	}
	return coin, nil
}
