package types

import (
	"bytes"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/config"
)

const (
	IsolatedReserveDEL   uint32 = 1
	IsolatedReserveERC20 uint32 = 2
)

func nonzeroFixed(value []byte, length int) bool {
	return len(value) == length && !bytes.Equal(value, make([]byte, length))
}

func (v *IsolatedVaultState) Validate() error {
	if v == nil {
		return fmt.Errorf("isolated vault state is missing")
	}
	if err := ValidateExternalDomain(v.ChainID, v.EVMChainID); err != nil {
		return err
	}
	if !nonzeroFixed(v.Vault, common.AddressLength) || !nonzeroFixed(v.NextPositionID, 32) {
		return fmt.Errorf("invalid isolated vault identity or next uint256 ID")
	}
	collection := Collection{Creator: v.ReceiptCreator, Denom: v.ReceiptDenom, TypeNft: NftType_NFT721, AddressDRC: common.BytesToAddress(v.Vault).Hex()}
	return collection.Validate()
}

func (r *IsolatedReserveSnapshot) Validate() error {
	if r == nil || len(r.Token) != common.AddressLength || len(r.Amount) != 32 {
		return fmt.Errorf("invalid isolated reserve snapshot width")
	}
	switch r.Kind {
	case IsolatedReserveDEL:
		if nonzeroFixed(r.Token, common.AddressLength) {
			return fmt.Errorf("native isolated reserve cannot name an ERC20")
		}
	case IsolatedReserveERC20:
		if !nonzeroFixed(r.Token, common.AddressLength) {
			return fmt.Errorf("ERC20 isolated reserve requires a token")
		}
	default:
		return fmt.Errorf("isolated reserve must retain a backed currency kind")
	}
	return nil
}

func (p *IsolatedPositionLineage) Validate() error {
	if p == nil {
		return fmt.Errorf("isolated position lineage is missing")
	}
	if !nonzeroFixed(p.Vault, common.AddressLength) || !nonzeroFixed(p.PositionID, 32) || len(p.ParentPositionID) != 32 || bytes.Compare(p.ParentPositionID, p.PositionID) >= 0 {
		return fmt.Errorf("invalid isolated position or informational parent identity")
	}
	if !nonzeroFixed(p.OriginContract, common.AddressLength) || len(p.OriginTokenID) != 32 || bytes.Equal(p.OriginContract, p.Vault) {
		return fmt.Errorf("invalid isolated origin identity")
	}
	if !config.CollectionDenomValidator.MatchString(p.ReceiptNativeTokenID) || !config.CollectionDenomValidator.MatchString(p.OriginNativeTokenID) || p.ReceiptNativeTokenID == p.OriginNativeTokenID || p.ReceiptSubTokenID == 0 {
		return fmt.Errorf("invalid isolated native identity")
	}
	if len(p.OriginSubTokenIDs) == 0 || uint64(len(p.OriginSubTokenIDs)) > uint64(MaxIsolatedOriginUnits) {
		return fmt.Errorf("isolated origin quantity exceeds the shared position limit")
	}
	var previous uint32
	for _, id := range p.OriginSubTokenIDs {
		if id == 0 || id <= previous {
			return fmt.Errorf("isolated origin IDs must be nonzero, unique and sorted")
		}
		previous = id
	}
	return p.Reserve.Validate()
}

// ValidateIsolation checks the complete current-state population. It does not
// approve EVM runtime code or prove bank backing; application admission must do
// that before installing financial receipt processing.
func (g *GenesisState) ValidateIsolation() error {
	if g == nil {
		return fmt.Errorf("NFT genesis is missing")
	}
	vaults := make(map[string]IsolatedVaultState, len(g.IsolatedVaults))
	var domainChain string
	var domainEVM uint64
	activeOrigins := make(map[string]map[uint32]bool)
	positions := make(map[string]bool, len(g.IsolatedPositions))
	receipts := make(map[string]bool, len(g.IsolatedPositions))
	consumed := make(map[string]map[uint32]bool)
	bindings := make(map[string]ExternalNFTBinding, len(g.ExternalBindings))
	tokens := make(map[string]*Token)
	collections := make(map[string]Collection)
	for _, b := range g.ExternalBindings {
		bindings[b.NativeTokenID] = b
	}
	for _, c := range g.Collections {
		collections[c.Creator+"\x00"+c.Denom] = c
		for _, t := range c.Tokens {
			if t == nil {
				return fmt.Errorf("NFT token is missing")
			}
			tokens[t.ID] = t
		}
	}
	for _, v := range g.IsolatedVaults {
		if err := v.Validate(); err != nil {
			return err
		}
		if _, found := vaults[string(v.Vault)]; found {
			return fmt.Errorf("duplicate isolated vault")
		}
		c, found := collections[v.ReceiptCreator+"\x00"+v.ReceiptDenom]
		if !found || c.TypeNft != NftType_NFT721 || c.ReserveLess || !bytes.Equal(common.HexToAddress(c.AddressDRC).Bytes(), v.Vault) {
			return fmt.Errorf("isolated vault differs from its receipt collection")
		}
		if len(g.ExternalBindings) > 0 && (v.ChainID != g.ExternalBindings[0].ChainID || v.EVMChainID != g.ExternalBindings[0].EVMChainID) {
			return fmt.Errorf("isolated vault domain differs from NFT bindings")
		}
		if domainChain != "" && (v.ChainID != domainChain || v.EVMChainID != domainEVM) {
			return fmt.Errorf("isolated vaults contain mixed domains")
		}
		domainChain, domainEVM = v.ChainID, v.EVMChainID
		vaults[string(v.Vault)] = v
	}
	for _, p := range g.IsolatedPositions {
		if err := p.Validate(); err != nil {
			return err
		}
		v, found := vaults[string(p.Vault)]
		if !found || bytes.Compare(p.PositionID, v.NextPositionID) >= 0 {
			return fmt.Errorf("isolated position is outside its vault high-water mark")
		}
		key := string(append(append([]byte{}, p.Vault...), p.PositionID...))
		if positions[key] || receipts[p.ReceiptNativeTokenID] {
			return fmt.Errorf("duplicate isolated position or receipt")
		}
		positions[key], receipts[p.ReceiptNativeTokenID] = true, true
		receipt, found := bindings[p.ReceiptNativeTokenID]
		if !found || receipt.NFTType != NftType_NFT721 || !bytes.Equal(receipt.Contract, p.Vault) || !bytes.Equal(receipt.TokenID, p.PositionID) {
			return fmt.Errorf("isolated receipt identity differs from NFT binding")
		}
		token := tokens[p.ReceiptNativeTokenID]
		if token == nil || token.Creator != v.ReceiptCreator || token.Denom != v.ReceiptDenom || token.ReserveLess || len(token.SubTokens) != 1 || token.SubTokens[0] == nil || token.SubTokens[0].ID != p.ReceiptSubTokenID {
			return fmt.Errorf("isolated receipt must have exactly one live native unit")
		}
		origin, found := bindings[p.OriginNativeTokenID]
		if !found || origin.NFTType != NftType_NFT1155 || !bytes.Equal(origin.Contract, p.OriginContract) || !bytes.Equal(origin.TokenID, p.OriginTokenID) {
			return fmt.Errorf("isolated origin identity differs from NFT binding")
		}
		originToken := tokens[p.OriginNativeTokenID]
		if originToken == nil || originToken.ReserveLess {
			return fmt.Errorf("isolated origin must be backed")
		}
		active, knownOrigin := activeOrigins[p.OriginNativeTokenID]
		if !knownOrigin {
			active = make(map[uint32]bool, len(originToken.SubTokens))
			for _, sub := range originToken.SubTokens {
				if sub == nil {
					return fmt.Errorf("NFT origin sub-token is missing")
				}
				active[sub.ID] = true
			}
			activeOrigins[p.OriginNativeTokenID] = active
		}
		if consumed[p.OriginNativeTokenID] == nil {
			consumed[p.OriginNativeTokenID] = make(map[uint32]bool)
		}
		for _, id := range p.OriginSubTokenIDs {
			if id > originToken.Minted || active[id] {
				return fmt.Errorf("isolated origin unit is not an archived burned unit")
			}
			if consumed[p.OriginNativeTokenID][id] {
				return fmt.Errorf("isolated origin unit is assigned to multiple live receipts")
			}
			consumed[p.OriginNativeTokenID][id] = true
		}
	}
	for _, b := range g.ExternalBindings {
		if _, found := vaults[string(b.Contract)]; found && tokens[b.NativeTokenID] != nil && len(tokens[b.NativeTokenID].SubTokens) > 0 && !receipts[b.NativeTokenID] {
			return fmt.Errorf("live isolated vault receipt is missing its lineage")
		}
	}
	return nil
}
