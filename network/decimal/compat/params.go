package compat

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"cosmossdk.io/core/store"
	"github.com/cosmos/gogoproto/proto"
)

var (
	ErrMissingParams  = errors.New("module parameters are not initialized")
	ErrInvalidParams  = errors.New("invalid module parameters")
	ErrParamsTooLarge = errors.New("module parameters exceed size limit")
)

// MaxParamsBytes bounds module configuration, not NFT metadata or transaction data.
const MaxParamsBytes = 64 * 1024

// ParamsStore reads configuration from the caller's transactional context.
// It deliberately retains no mutable configuration cache in the keeper.
type ParamsStore[P proto.Message] struct {
	service   store.KVStoreService
	key       []byte
	prototype P
	validate  func(P) error
}

func NewParamsStore[P proto.Message](service store.KVStoreService, key []byte, prototype P, validate func(P) error) (ParamsStore[P], error) {
	if service == nil || len(key) == 0 || nilMessage(prototype) || validate == nil {
		return ParamsStore[P]{}, errors.New("parameter store requires service, key, message and validator")
	}
	return ParamsStore[P]{service: service, key: append([]byte(nil), key...), prototype: proto.Clone(prototype).(P), validate: validate}, nil
}

func (s ParamsStore[P]) Get(ctx context.Context) (P, error) {
	var empty P
	data, err := s.service.OpenKVStore(ctx).Get(s.key)
	if err != nil {
		return empty, fmt.Errorf("read module parameters: %w", err)
	}
	if data == nil {
		return empty, ErrMissingParams
	}
	if len(data) > MaxParamsBytes {
		return empty, ErrParamsTooLarge
	}
	params := proto.Clone(s.prototype).(P)
	// Discard prototype defaults: only committed bytes may supply stored fields.
	params.Reset()
	if err := proto.Unmarshal(data, params); err != nil {
		return empty, fmt.Errorf("decode module parameters: %w", err)
	}
	if err := s.validate(params); err != nil {
		return empty, fmt.Errorf("%w: %v", ErrInvalidParams, err)
	}
	return params, nil
}

func (s ParamsStore[P]) Set(ctx context.Context, params P) error {
	if nilMessage(params) {
		return ErrInvalidParams
	}
	if err := s.validate(params); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidParams, err)
	}
	data, err := proto.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode module parameters: %w", err)
	}
	if len(data) > MaxParamsBytes {
		return ErrParamsTooLarge
	}
	if err := s.service.OpenKVStore(ctx).Set(s.key, data); err != nil {
		return fmt.Errorf("write module parameters: %w", err)
	}
	return nil
}

func nilMessage(message proto.Message) bool {
	if message == nil {
		return true
	}
	value := reflect.ValueOf(message)
	return value.Kind() == reflect.Pointer && value.IsNil()
}
