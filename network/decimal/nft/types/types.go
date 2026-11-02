package types

import (
	"fmt"
	collections "github.com/maxwell2010/go-smart-node/network/decimal/collections"
)

const ReservedPool = "reserved_pool"

type (
	SortedStringArray = collections.SortedStringArray
	SortedUintArray   = collections.SortedUintArray
	Tokens            = []*Token
	SubTokens         = []*SubToken
)

// SupplyInvariantCheck requires a fully materialized export, including counters
// and all active sub-tokens. Shallow collection/query records are insufficient.
func SupplyInvariantCheck(collections []Collection) (string, bool) {
	seenTokens := make(map[string]bool)
	var tokenCount, subTokenCount uint64
	for _, collection := range collections {
		if uint64(collection.Supply) != uint64(len(collection.Tokens)) {
			return fmt.Sprintf("NFT collection %q supply differs from token records", collection.Denom), true
		}
		for _, token := range collection.Tokens {
			if token == nil || token.ID == "" {
				return "NFT export contains a missing token", true
			}
			if seenTokens[token.ID] {
				return fmt.Sprintf("NFT token %q appears more than once", token.ID), true
			}
			seenTokens[token.ID] = true
			if token.Burnt > token.Minted || uint64(token.Minted)-uint64(token.Burnt) != uint64(len(token.SubTokens)) {
				return fmt.Sprintf("NFT token %q minted/burnt counters differ from active sub-tokens", token.ID), true
			}
			seenSubTokens := make(map[uint32]bool)
			for _, subToken := range token.SubTokens {
				if subToken == nil || subToken.ID == 0 || subToken.ID > token.Minted || seenSubTokens[subToken.ID] {
					return fmt.Sprintf("NFT token %q has missing, duplicated or out-of-range sub-token IDs", token.ID), true
				}
				seenSubTokens[subToken.ID] = true
				subTokenCount++
			}
			tokenCount++
		}
	}
	return fmt.Sprintf("NFT supply consistent: collections=%d tokens=%d active_sub_tokens=%d", len(collections), tokenCount, subTokenCount), false
}
