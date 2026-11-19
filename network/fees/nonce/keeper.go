// Package nonce persists the dedicated DRC20 fee-authorization nonce.
// Consumption must share the outer ante cache with actual conversion/payment.
// No message execution rollback may undo a successfully paid ante nonce.
package nonce

import (
	"errors"
	"math/big"
	"reflect"

	corestore "cosmossdk.io/core/store"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
)

const Prefix = "fees/nonce/v1/"

var (
	ErrReplay    = errors.New("fee authorization nonce does not match current nonce")
	ErrCorrupt   = errors.New("corrupt persisted fee nonce")
	ErrExhausted = errors.New("fee nonce exhausted")
)

type Keeper struct{ service corestore.KVStoreService }

func NewKeeper(service corestore.KVStoreService) (*Keeper, error) {
	if service == nil {
		return nil, errors.New("fee nonce requires a store service")
	}
	v := reflect.ValueOf(service)
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return nil, errors.New("nil fee nonce store service")
	}
	return &Keeper{service}, nil
}
func key(payer common.Address) ([]byte, error) {
	if payer == (common.Address{}) {
		return nil, errors.New("fee nonce requires nonzero payer")
	}
	return append([]byte(Prefix), payer[:]...), nil
}
func (k *Keeper) Current(ctx sdk.Context, payer common.Address) (*big.Int, error) {
	if k == nil || k.service == nil {
		return nil, errors.New("fee nonce keeper is not initialized")
	}
	path, err := key(payer)
	if err != nil {
		return nil, err
	}
	raw, err := k.service.OpenKVStore(ctx).Get(path)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return new(big.Int), nil
	}
	if len(raw) != 32 {
		return nil, ErrCorrupt
	}
	return new(big.Int).SetBytes(raw), nil
}
func (k *Keeper) Consume(ctx sdk.Context, verified authorization.Verified) error {
	if !verified.Valid() {
		return errors.New("fee nonce requires verified authorization")
	}
	child, commit := ctx.CacheContext()
	current, err := k.Current(child, verified.Payer())
	if err != nil {
		return err
	}
	if current.Cmp(verified.FeeNonce()) != 0 {
		return ErrReplay
	}
	next := new(big.Int).Add(current, big.NewInt(1))
	if next.BitLen() > 256 {
		return ErrExhausted
	}
	path, _ := key(verified.Payer())
	if err := k.service.OpenKVStore(child).Set(path, next.FillBytes(make([]byte, 32))); err != nil {
		return err
	}
	commit()
	return nil
}
