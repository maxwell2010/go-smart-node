package types

import (
	"fmt"
	"strings"
)

// NewGenesisState creates a new genesis state.
func NewGenesisState(params Params, collections []Collection) *GenesisState {
	return &GenesisState{
		Params:      params,
		Collections: collections,
	}
}

// DefaultGenesisState returns a default genesis state.
func DefaultGenesisState() *GenesisState {
	return NewGenesisState(DefaultParams(), []Collection{})
}

// Validate performs basic genesis state validation returning an error upon any failure.
func (m *GenesisState) Validate() error {
	if m == nil {
		return fmt.Errorf("NFT genesis is missing")
	}
	if err := m.Params.Validate(); err != nil {
		return err
	}
	seenCollections := make(map[string]bool)
	seenContracts := make(map[string]bool)
	type tokenLocation struct {
		token      Token
		collection Collection
	}
	locations := make(map[string]tokenLocation)
	for _, coll := range m.Collections {
		if err := coll.Validate(); err != nil {
			return fmt.Errorf("NFT collection %q: %w", coll.Denom, err)
		}
		key := coll.Creator + "\x00" + coll.Denom
		if seenCollections[key] {
			return fmt.Errorf("duplicated NFT collection")
		}
		seenCollections[key] = true
		if uint64(len(coll.Tokens)) > uint64(m.Params.MaxCollectionSize) {
			return fmt.Errorf("NFT collection exceeds token limit")
		}
		if coll.AddressDRC != "" {
			contract := strings.ToLower(coll.AddressDRC)
			if seenContracts[contract] {
				return fmt.Errorf("duplicated NFT contract assignment")
			}
			seenContracts[contract] = true
		}
		for _, token := range coll.Tokens {
			if token == nil {
				return fmt.Errorf("NFT token is missing")
			}
			if err := token.Validate(); err != nil {
				return fmt.Errorf("NFT token %q: %w", token.ID, err)
			}
			if token.Creator != coll.Creator || token.Denom != coll.Denom {
				return fmt.Errorf("NFT token does not belong to its collection")
			}
			if token.ReserveLess != coll.ReserveLess {
				return fmt.Errorf("NFT token reserve profile differs from collection")
			}
			locations[token.ID] = tokenLocation{*token, coll}
			if uint64(len(token.SubTokens)) > uint64(m.Params.MaxTokenQuantity) {
				return fmt.Errorf("NFT token exceeds active sub-token limit")
			}
			// iterate subtokens
			for _, subToken := range token.SubTokens {
				if subToken == nil {
					return fmt.Errorf("NFT sub-token is missing")
				}
				// validate subtoken
				if err := subToken.Validate(); err != nil {
					return fmt.Errorf("NFT token %q sub-token %d: %w", token.ID, subToken.ID, err)
				}
				if subToken.Reserve != nil && subToken.Reserve.Denom != token.Reserve.Denom {
					return fmt.Errorf("NFT sub-token reserve denomination differs from its token")
				}
				if token.ReserveLess && subToken.Reserve != nil && !subToken.Reserve.IsZero() {
					return fmt.Errorf("reserveless NFT sub-token cannot have backing")
				}
			}
		}
	}
	seenNative := make(map[string]bool)
	seenExternal := make(map[string]bool)
	for _, binding := range m.ExternalBindings {
		if err := binding.Validate(); err != nil {
			return err
		}
		if binding.ChainID != m.ExternalBindings[0].ChainID || binding.EVMChainID != m.ExternalBindings[0].EVMChainID {
			return fmt.Errorf("NFT genesis contains mixed external domains")
		}
		key := ExternalIdentityKey(binding)
		if seenNative[binding.NativeTokenID] || seenExternal[key] {
			return fmt.Errorf("duplicate external NFT binding")
		}
		seenNative[binding.NativeTokenID], seenExternal[key] = true, true
		location, ok := locations[binding.NativeTokenID]
		if !ok {
			return fmt.Errorf("external NFT binding has no native token")
		}
		if err := ValidateBindingToken(binding, location.token, location.collection); err != nil {
			return err
		}
	}
	for id, location := range locations {
		if location.token.ReserveLess && !seenNative[id] {
			return fmt.Errorf("reserveless NFT requires an explicit external identity binding")
		}
	}
	if err := m.ValidateIsolation(); err != nil {
		return err
	}
	if message, broken := SupplyInvariantCheck(m.Collections); broken {
		return fmt.Errorf("%s", message)
	}
	return nil
}
