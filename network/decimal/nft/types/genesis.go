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
			}
		}
	}
	if message, broken := SupplyInvariantCheck(m.Collections); broken {
		return fmt.Errorf("%s", message)
	}
	return nil
}
