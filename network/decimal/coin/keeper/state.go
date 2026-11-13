// Package keeper stages Decimal coin state for the separate modern network.
// No bank mint/burn, native transaction route or EVM event hook is enabled here.
package keeper

import (
	"errors"
	"fmt"
	"strings"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/compat"
)

var ErrNotFound = errors.New("coin state does not exist")

const maxCoinRecordBytes = 64 * 1024

// StateKeeper uses one complete coin record and an address index. This is a new
// network layout, not a migration or an alternate reader for the legacy database.
// Every method reads the caller's context; there is no mutable financial cache.
type StateKeeper struct {
	service store.KVStoreService
	params  compat.ParamsStore[*types.Params]
	emit    func(sdk.Context, proto.Message) error
}

func NewStateKeeper(service store.KVStoreService) (*StateKeeper, error) {
	params, err := compat.NewParamsStore(service, []byte{0x01}, &types.Params{}, func(p *types.Params) error { return p.Validate() })
	if err != nil {
		return nil, err
	}
	return &StateKeeper{service: service, params: params, emit: func(ctx sdk.Context, event proto.Message) error {
		return ctx.EventManager().EmitTypedEvent(event)
	}}, nil
}

func (k *StateKeeper) GetParams(ctx sdk.Context) (types.Params, error) {
	p, err := k.params.Get(ctx)
	if err != nil {
		return types.Params{}, err
	}
	return *p, nil
}

func (k *StateKeeper) SetParams(ctx sdk.Context, params types.Params) error {
	// The base asset's identity cannot change after initialization. Monetary
	// allocation is reconciled by application genesis, not this metadata store.
	previous, err := k.params.Get(ctx)
	if err != nil && !errors.Is(err, compat.ErrMissingParams) {
		return err
	}
	if err == nil && previous.BaseDenom != params.BaseDenom {
		return errors.New("base denomination is immutable")
	}
	return k.params.Set(ctx, &params)
}

func coinKey(denom string) []byte { return append([]byte{0x11}, denom...) }
func addressKey(address string) []byte {
	return append([]byte{legacyAddressIndexPrefix}, strings.ToLower(address)...)
}

func (k *StateKeeper) GetCoin(ctx sdk.Context, denom string) (types.Coin, error) {
	if !config.CoinDenomValidator.MatchString(denom) {
		return types.Coin{}, errors.New("invalid coin denomination")
	}
	data, err := k.service.OpenKVStore(ctx).Get(coinKey(denom))
	if err != nil {
		return types.Coin{}, err
	}
	if data == nil {
		return types.Coin{}, ErrNotFound
	}
	var coin types.Coin
	if len(data) > maxCoinRecordBytes {
		return coin, errors.New("coin record exceeds size limit")
	}
	if err := proto.Unmarshal(data, &coin); err != nil {
		return coin, fmt.Errorf("decode coin: %w", err)
	}
	if coin.Denom != denom {
		return types.Coin{}, errors.New("coin record key mismatch")
	}
	if err := coin.Validate(); err != nil {
		return types.Coin{}, fmt.Errorf("invalid stored coin: %w", err)
	}
	return coin, nil
}

func (k *StateKeeper) GetCoinByDRC(ctx sdk.Context, address string) (types.Coin, error) {
	if !types.ValidContractAddress(address) {
		return types.Coin{}, errors.New("invalid token contract address")
	}
	state := k.service.OpenKVStore(ctx)
	index, err := activeAddressIndex(state)
	if err != nil {
		return types.Coin{}, err
	}
	data, err := state.Get(index.key(address))
	if err != nil {
		return types.Coin{}, err
	}
	if data == nil {
		return types.Coin{}, ErrNotFound
	}
	coin, err := k.GetCoin(ctx, string(data))
	if err != nil {
		return types.Coin{}, err
	}
	if !strings.EqualFold(coin.DRC20Contract, address) {
		return types.Coin{}, errors.New("coin contract index mismatch")
	}
	return coin, nil
}

// SetCoin atomically updates metadata and index in the caller's transaction.
// Once assigned, a contract cannot be cleared or reassigned by a regular write.
func (k *StateKeeper) SetCoin(ctx sdk.Context, coin types.Coin) error {
	if err := coin.Validate(); err != nil {
		return err
	}
	data, err := proto.Marshal(&coin)
	if err != nil {
		return err
	}
	if len(data) > maxCoinRecordBytes {
		return errors.New("coin record exceeds size limit")
	}
	cached, write := ctx.CacheContext()
	previous, err := k.GetCoin(cached, coin.Denom)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil && previous.DRC20Contract != "" && !strings.EqualFold(previous.DRC20Contract, coin.DRC20Contract) {
		return errors.New("token contract assignment is immutable")
	}
	state := k.service.OpenKVStore(cached)
	var indexKey []byte
	if coin.DRC20Contract != "" {
		index, err := activeAddressIndex(state)
		if err != nil {
			return err
		}
		indexKey = index.key(coin.DRC20Contract)
		indexed, err := state.Get(indexKey)
		if err != nil {
			return err
		}
		if indexed != nil && string(indexed) != coin.Denom {
			return errors.New("token contract already belongs to another coin")
		}
	}
	if err := state.Set(coinKey(coin.Denom), data); err != nil {
		return err
	}
	if indexKey != nil {
		if err := state.Set(indexKey, []byte(coin.Denom)); err != nil {
			return err
		}
	}
	write()
	return nil
}

// UpdateCoinVR keeps state and its SDK event in the same child transaction.
// An outer EVM hook must still make its entire receipt batch atomic.
func (k *StateKeeper) UpdateCoinVR(ctx sdk.Context, denom string, volume, reserve sdkmath.Int) error {
	cached, write := ctx.CacheContext()
	coin, err := k.GetCoin(cached, denom)
	if err != nil {
		return err
	}
	coin.Volume, coin.Reserve = volume, reserve
	if err := k.SetCoin(cached, coin); err != nil {
		return err
	}
	if err := k.emit(cached, &types.EventUpdateCoinVR{Denom: denom, Volume: volume.String(), Reserve: reserve.String()}); err != nil {
		return err
	}
	write()
	return nil
}
