package types

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// ValidateEVMReferences validates this metadata schema, not EVM authenticity or
// NFT ownership. Production controller attestation remains mandatory.
func (g GenesisState) ValidateEVMReferences() error {
	if g.BindingDomain == nil {
		if len(g.EVMStakeBindings) != 0 || len(g.EVMFrozenBindings) != 0 || len(g.EVMValidatorHeaders) != 0 || g.BindingRevision != 0 {
			return errors.New("EVM binding records require a source domain")
		}
		return nil
	}
	if err := g.BindingDomain.Validate(); err != nil {
		return err
	}
	validators := map[string]bool{}
	codec := address.NewBech32Codec(identity.Bech32PrefixValAddr)
	for _, v := range g.Validators {
		raw, err := codec.StringToBytes(v.OperatorAddress)
		if err != nil {
			return err
		}
		validators[string(raw)] = true
	}
	keys := map[string]bool{}
	units := map[string]bool{}
	assets := map[string]bool{}
	claim := func(position *NFTPositionIdentity) error {
		if position == nil {
			return nil
		}
		// Only current custody is live. Origin IDs are burned lineage and may
		// appear in archived ancestors; the NFT keeper owns lineage rules.
		for _, id := range position.Custody.NativeSubTokenIDs {
			key, err := CustodyUnitKey(position.Custody.NativeTokenID, id)
			if err != nil {
				return err
			}
			if units[string(key)] {
				return errors.New("custody NFT unit is assigned to multiple active/frozen positions")
			}
			units[string(key)] = true
		}
		key := string(position.Custody.Contract) + string(position.Custody.TokenID)
		if assets[key] {
			return errors.New("custody receipt identity is assigned more than once")
		}
		assets[key] = true
		return nil
	}
	asset := func(value *EVMReserveAsset) error {
		if value.ReserveKind == 1 && value.NativeDenom != g.Params.BaseDenom {
			return errors.New("native reserve denomination differs from configured base")
		}
		return nil
	}
	for i := range g.EVMStakeBindings {
		b := &g.EVMStakeBindings[i]
		if err := b.Validate(); err != nil {
			return fmt.Errorf("stake binding%d: %w", i, err)
		}
		key, err := EVMStakeBindingKey(b.Kind, b.Gateway, b.StakeID)
		if err != nil {
			return err
		}
		key = append([]byte{'s'}, key...)
		if keys[string(key)] {
			return errors.New("duplicate source stake binding")
		}
		keys[string(key)] = true
		if err := asset(b.ReserveAsset); err != nil {
			return err
		}
		if b.Active {
			if !validators[string(b.Validator)] {
				return errors.New("active binding references unknown native validator")
			}
			if err := claim(b.NFTPosition); err != nil {
				return err
			}
		}
	}
	for i := range g.EVMFrozenBindings {
		b := &g.EVMFrozenBindings[i]
		if err := b.Validate(); err != nil {
			return fmt.Errorf("frozen binding%d: %w", i, err)
		}
		key, err := EVMFrozenBindingKey(b.Gateway, b.FrozenIndex)
		if err != nil {
			return err
		}
		key = append([]byte{'f'}, key...)
		if keys[string(key)] {
			return errors.New("duplicate source frozen binding")
		}
		keys[string(key)] = true
		if err := asset(b.ReserveAsset); err != nil {
			return err
		}
		if b.Active {
			if !validators[string(b.SourceValidator)] || (b.FreezeType == 2 && !validators[string(b.DestinationValidator)]) {
				return errors.New("active frozen binding references unknown native validator")
			}
			if err := claim(b.NFTPosition); err != nil {
				return err
			}
		}
	}
	if err := g.ValidateEVMValidatorHeaders(); err != nil {
		return err
	}
	return g.ValidateEVMNativeCoverage()
}

func SameEVMSourceDomain(a, b *EVMSourceDomain) bool {
	return a != nil && b != nil && a.ChainID == b.ChainID && a.EVMChainID == b.EVMChainID
}
func SameNativeUnitIDs(a, b []uint32) bool { return sameUnitIDs(a, b) }
func SameExternalNFTIdentity(a, b *EVMNFTReference) bool {
	return a != nil && b != nil && bytes.Equal(a.Contract, b.Contract) && bytes.Equal(a.TokenID, b.TokenID) && a.NativeTokenID == b.NativeTokenID && a.Standard == b.Standard
}
