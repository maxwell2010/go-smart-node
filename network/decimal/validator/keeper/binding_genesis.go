package keeper

import (
	"bytes"
	"encoding/binary"
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

func (k *StateKeeper) prepareEVMGenesis(ctx sdk.Context, input *types.GenesisState) (*types.GenesisState, error) {
	if input == nil {
		return nil, errors.New("native validator genesis is missing")
	}
	if err := input.Validate(); err != nil {
		return nil, err
	}
	data, err := proto.Marshal(input)
	if err != nil {
		return nil, err
	}
	g := &types.GenesisState{}
	if err = proto.Unmarshal(data, g); err != nil {
		return nil, err
	}
	if k.bindingDomain == nil {
		if g.BindingDomain != nil || len(g.EVMStakeBindings) != 0 || len(g.EVMFrozenBindings) != 0 || g.BindingRevision != 0 {
			return nil, ErrBindingDomain
		}
		return g, nil
	}
	if ctx.ChainID() != k.bindingDomain.ChainID {
		return nil, ErrBindingDomain
	}
	if g.BindingDomain == nil {
		domain := *k.bindingDomain
		g.BindingDomain = &domain
	}
	if !types.SameEVMSourceDomain(g.BindingDomain, k.bindingDomain) {
		return nil, ErrBindingDomain
	}
	if err = g.Validate(); err != nil {
		return nil, err
	}
	return g, nil
}
func (k *StateKeeper) initEVMGenesis(ctx sdk.Context, g *types.GenesisState) error {
	if k.bindingDomain == nil {
		return nil
	}
	if ctx.ChainID() != k.bindingDomain.ChainID || !types.SameEVMSourceDomain(g.BindingDomain, k.bindingDomain) {
		return ErrBindingDomain
	}
	state := k.service.OpenKVStore(ctx)
	domain, err := proto.Marshal(g.BindingDomain)
	if err != nil {
		return err
	}
	if err = state.Set([]byte{bindingDomainMarker}, domain); err != nil {
		return err
	}
	rows := map[string]*bindingRecord{}
	for i := range g.EVMStakeBindings {
		row, err := recordFromStake(&g.EVMStakeBindings[i])
		if err != nil {
			return err
		}
		rows[string(row.key)] = row
	}
	for i := range g.EVMFrozenBindings {
		row, err := recordFromFrozen(&g.EVMFrozenBindings[i])
		if err != nil {
			return err
		}
		rows[string(row.key)] = row
	}
	values := map[string][]byte{}
	for key, row := range rows {
		values[key] = row.data
	}
	for _, key := range sortedBindingKeys(values) {
		row := rows[key]
		if err = state.Set(row.key, row.data); err != nil {
			return err
		}
		if row.active() {
			claims, err := row.allClaimKeys(true)
			if err != nil {
				return err
			}
			for _, claim := range claims {
				present, err := state.Has(claim)
				if err != nil {
					return err
				}
				if present {
					return errors.New("duplicate initial custody/queue binding index")
				}
				if err = state.Set(claim, row.key); err != nil {
					return err
				}
			}
		}
	}
	if err := state.Set([]byte{validatorFrozenIndexMarker}, []byte{1}); err != nil {
		return err
	}
	var revision [8]byte
	binary.BigEndian.PutUint64(revision[:], g.BindingRevision)
	return state.Set([]byte{bindingRevisionKey}, revision[:])
}
func (k *StateKeeper) checkEVMExportDomain(ctx sdk.Context) error {
	if k.bindingDomain != nil {
		return k.checkBindingDomain(ctx)
	}
	marker, err := k.service.OpenKVStore(ctx).Get([]byte{bindingDomainMarker})
	if err != nil {
		return err
	}
	if marker != nil {
		return ErrBindingDomain
	}
	found := false
	for _, prefix := range []byte{bindingRevisionKey, stakeBindingPrefix, frozenBindingPrefix, custodyUnitBindingPrefix, custodyAssetBindingPrefix, nativeQueueBindingPrefix, activePenaltyBindingPrefix, withdrawalDuePrefix, withdrawalCursorKey, validatorFrozenBindingPrefix, validatorFrozenIndexMarker} {
		if err := k.scan(ctx, prefix, func(_, _ []byte) error { found = true; return nil }); err != nil {
			return err
		}
	}
	if found {
		return errors.New("unconfigured native binding records cannot be silently omitted from export")
	}
	return nil
}
func (k *StateKeeper) exportEVMGenesis(ctx sdk.Context, g *types.GenesisState) error {
	if err := k.checkEVMExportDomain(ctx); err != nil {
		return err
	}
	if k.bindingDomain == nil {
		return nil
	}
	if _, err := k.hasValidatorFrozenIndex(ctx); err != nil {
		return err
	}
	domain := *k.bindingDomain
	g.BindingDomain = &domain
	revision, err := k.BindingRevision(ctx)
	if err != nil {
		return err
	}
	g.BindingRevision = revision
	expected := map[string][]byte{}
	for _, prefix := range []byte{stakeBindingPrefix, frozenBindingPrefix} {
		if err := k.scan(ctx, prefix, func(key, data []byte) error {
			row, err := decodeBindingRecord(key, data)
			if err != nil {
				return err
			}
			if err = k.checkBindingIndexes(ctx, row); err != nil {
				return err
			}
			if row.stake != nil {
				g.EVMStakeBindings = append(g.EVMStakeBindings, *row.stake)
			} else {
				g.EVMFrozenBindings = append(g.EVMFrozenBindings, *row.frozen)
			}
			if row.active() {
				claims, err := k.bindingClaimKeys(ctx, row)
				if err != nil {
					return err
				}
				for _, claim := range claims {
					if previous, ok := expected[string(claim)]; ok && !bytes.Equal(previous, row.key) {
						return errors.New("two source records claim one native custody unit")
					}
					expected[string(claim)] = bytes.Clone(row.key)
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	actual := 0
	for _, prefix := range []byte{custodyUnitBindingPrefix, custodyAssetBindingPrefix, nativeQueueBindingPrefix, activePenaltyBindingPrefix, withdrawalDuePrefix, validatorFrozenBindingPrefix} {
		if err := k.scan(ctx, prefix, func(key, data []byte) error {
			want, exists := expected[string(key)]
			if !exists || !bytes.Equal(want, data) {
				return errors.New("orphan or corrupt native custody/queue reverse index")
			}
			actual++
			return nil
		}); err != nil {
			return err
		}
	}
	if actual != len(expected) {
		return errors.New("incomplete native binding reverse indexes")
	}
	return nil
}
