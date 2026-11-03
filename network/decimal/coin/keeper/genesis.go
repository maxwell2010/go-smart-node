package keeper

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
)

// InitGenesis installs validated native metadata and redeemed-check records.
// It creates no bank supply or EVM contracts. Cross-module backing must be
// reconciled by the application before accepting a fresh genesis.
func (k *StateKeeper) InitGenesis(ctx sdk.Context, genesis *types.GenesisState) error {
	if err := genesis.Validate(); err != nil {
		return err
	}
	child, commit := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	iterator, err := state.Iterator(nil, nil)
	if err != nil {
		return err
	}
	occupied := iterator.Valid()
	if occupied {
		if err := iterator.Error(); err != nil {
			_ = iterator.Close()
			return err
		}
	}
	if err := iterator.Close(); err != nil {
		return err
	}
	if occupied {
		return errors.New("coin genesis requires an empty store")
	}
	if err := k.SetParams(child, genesis.Params); err != nil {
		return err
	}
	for _, coin := range genesis.Coins {
		if err := k.SetCoin(child, coin); err != nil {
			return err
		}
	}
	for _, check := range genesis.Checks {
		data, err := proto.Marshal(&check)
		if err != nil {
			return err
		}
		if len(data) > maxCoinRecordBytes {
			return errors.New("redeemed check exceeds native record limit")
		}
		hash := check.HashFull()
		if err := state.Set(append([]byte{0x21}, hash[:]...), data); err != nil {
			return err
		}
	}
	commit()
	return nil
}

// ExportGenesis verifies complete metadata, redeemed checks and address indexes.
// Unknown rows and orphan indexes fail closed rather than disappearing from an
// export/import round trip. KV iteration determines canonical output order.
func (k *StateKeeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	genesis := types.NewGenesisState(params, nil, nil)
	state := k.service.OpenKVStore(ctx)
	iterator, err := state.Iterator(nil, nil)
	if err != nil {
		return nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = iterator.Close()
		}
	}()
	var indexes uint64
	for ; iterator.Valid(); iterator.Next() {
		if err := iterator.Error(); err != nil {
			return nil, err
		}
		key, value := bytes.Clone(iterator.Key()), bytes.Clone(iterator.Value())
		if len(key) == 0 {
			return nil, errors.New("empty native coin key")
		}
		switch key[0] {
		case 0x01:
			if len(key) != 1 {
				return nil, errors.New("unexpected native coin parameter row")
			}
		case 0x11:
			coin, err := k.GetCoin(ctx, string(key[1:]))
			if err != nil {
				return nil, err
			}
			if coin.DRC20Contract != "" {
				indexed, err := state.Get(addressKey(coin.DRC20Contract))
				if err != nil {
					return nil, err
				}
				if string(indexed) != coin.Denom {
					return nil, errors.New("missing or invalid coin address index")
				}
			}
			genesis.Coins = append(genesis.Coins, coin)
		case 0x13:
			address := string(key[1:])
			if address != strings.ToLower(address) {
				return nil, errors.New("non-canonical coin address index")
			}
			coin, err := k.GetCoinByDRC(ctx, address)
			if err != nil {
				return nil, err
			}
			if coin.Denom != string(value) {
				return nil, errors.New("orphan or invalid coin address index")
			}
			indexes++
		case 0x21:
			var check types.Check
			if len(value) > maxCoinRecordBytes {
				return nil, errors.New("redeemed check exceeds native record limit")
			}
			if err := proto.Unmarshal(value, &check); err != nil {
				return nil, err
			}
			if err := check.Validate(); err != nil {
				return nil, err
			}
			hash := check.HashFull()
			if !bytes.Equal(key[1:], hash[:]) {
				return nil, errors.New("redeemed-check key mismatch")
			}
			genesis.Checks = append(genesis.Checks, check)
		default:
			return nil, fmt.Errorf("unknown native coin key prefix %x", key[0])
		}
	}
	err = iterator.Close()
	closed = true
	if err != nil {
		return nil, err
	}
	var expected uint64
	for _, coin := range genesis.Coins {
		if coin.DRC20Contract != "" {
			expected++
		}
	}
	if indexes != expected {
		return nil, errors.New("native coin address index count mismatch")
	}
	if err := genesis.Validate(); err != nil {
		return nil, err
	}
	return genesis, nil
}
