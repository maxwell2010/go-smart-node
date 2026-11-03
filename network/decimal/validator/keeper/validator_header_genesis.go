package keeper

import (
	"bytes"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

func (k *StateKeeper) initValidatorHeaders(ctx sdk.Context, g *types.GenesisState) error {
	state := k.service.OpenKVStore(ctx)
	marker := []byte{0}
	if len(g.EVMValidatorHeaders) > 0 {
		marker = append([]byte{1}, g.EVMValidatorHeaders[0].Master...)
	}
	if err := state.Set([]byte{validatorHeaderMasterKey}, marker); err != nil {
		return err
	}
	for i := range g.EVMValidatorHeaders {
		h := &g.EVMValidatorHeaders[i]
		data, err := types.CanonicalValidatorHeaderBytes(h)
		if err != nil {
			return err
		}
		if _, err = k.GetValidator(ctx, h.NativeOperator); err != nil {
			return err
		}
		key, err := validatorHeaderKey(h.Master, h.Validator)
		if err != nil {
			return err
		}
		reverse, err := validatorHeaderOperatorKey(h.NativeOperator)
		if err != nil {
			return err
		}
		if err = state.Set(key, data); err != nil {
			return err
		}
		if err = state.Set(reverse, key); err != nil {
			return err
		}
		if h.SourceEligible() {
			if err = state.Set(validatorHeaderEligibleKey(h), key); err != nil {
				return err
			}
		}
	}
	return nil
}
func (k *StateKeeper) exportValidatorHeaders(ctx sdk.Context, g *types.GenesisState) error {
	master, bound, err := k.validatorHeaderMaster(ctx)
	if err != nil {
		return err
	}
	expected := map[string][]byte{}
	if err = k.scan(ctx, validatorHeaderPrefix, func(key, data []byte) error {
		h, err := decodeValidatorHeader(key, data)
		if err != nil {
			return err
		}
		if !bound || !bytes.Equal(master, h.Master) {
			return errors.New("validator header source differs from initialized MasterValidator")
		}
		if err = k.checkValidatorHeaderIndexes(ctx, &h); err != nil {
			return err
		}
		reverse, err := validatorHeaderOperatorKey(h.NativeOperator)
		if err != nil {
			return err
		}
		if _, exists := expected[string(reverse)]; exists {
			return errors.New("duplicate native operator header reference")
		}
		expected[string(reverse)] = bytes.Clone(key)
		if h.SourceEligible() {
			expected[string(validatorHeaderEligibleKey(&h))] = bytes.Clone(key)
		}
		g.EVMValidatorHeaders = append(g.EVMValidatorHeaders, h)
		return nil
	}); err != nil {
		return err
	}
	if bound && len(g.EVMValidatorHeaders) == 0 {
		return errors.New("bound source MasterValidator has no retained lifecycle headers")
	}
	actual := 0
	for _, prefix := range []byte{validatorHeaderOperatorPrefix, validatorHeaderEligiblePrefix} {
		if err = k.scan(ctx, prefix, func(key, data []byte) error {
			want, exists := expected[string(key)]
			if !exists || !bytes.Equal(want, data) {
				return errors.New("orphan or corrupt validator header reverse/eligibility index")
			}
			actual++
			return nil
		}); err != nil {
			return err
		}
	}
	if actual != len(expected) {
		return errors.New("incomplete validator lifecycle header indexes")
	}
	return nil
}
