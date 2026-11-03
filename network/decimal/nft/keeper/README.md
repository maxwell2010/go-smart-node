# Native NFT state staging

This storage layer imports validated complete genesis records atomically and
keeps token metadata, active sub-tokens and ownership indexes separately. It
retains the difference between an inherited reserve and an explicit zero
reserve. Transfers leave reserve values and minted/burnt counters unchanged.
Storage errors, a later invalid item and event conversion failures discard the
entire owner/index/event batch. Tests use actual SDK stores and CacheContext.

This is a new-network layout with a 64 KiB bound per metadata record. Collection
and token reads are shallow; never pass those records to a full supply checker.
`ExportGenesis` hydrates counters and active sub-tokens, verifies secondary
indexes and rejects orphan records before running the complete supply checker.
The explicit external binding layout preserves network domain, EVM chain ID,
20-byte collection address and every byte of the uint256 external token ID.
Legacy native IDs stay unchanged; new IDs use the versioned domain-separated
`FreshExternalTokenID` recipe. Primary/native and reverse/external indexes must
agree. `NextSubTokenID` is lifetime minted count plus one, with terminal `2^32`
cursor supported; previously burned unit IDs are never reused. The domain-aware
constructor requires the matching SDK context chain ID; the legacy constructor
rejects bound genesis.

`ListActiveSubTokenIDs` pages ownership by ascending unit ID, at most 256 per
request, using an exclusive uint32 cursor. Collection-wide ERC1155 URIs use an
explicit multimap. `ListTokenIDsByURI` pages by stored ID hash; `GetTokenByURI`
returns an ambiguity error if multiple native tokens share the URI. Legacy
unbound URI uniqueness remains enforced. Complete export checks both mappings,
all URI and owner indexes, counters and collection membership before returning
genesis. Export/reimport retains explicit reserve-less metadata and display
labels as well as bindings.

Reserve-less is an explicit profile requiring an assigned external collection
and token identity, with exactly zero DEL default backing. A slashed explicit
zero on a reserve-backed unit remains a separate state. A binding is identity,
not proof of emitter/code authentication or financial backing. Actual receipt
admission still needs independently approved factory, family and code hashes.
No mint/burn/reserve pipeline is enabled by these Go APIs. ERC1155 financial
projection remains sealed pending actual contract claim reconciliation: the
contract uses floor(pool/supply), with no invented remainder allocation.

Mint/burn/reserve transitions, bank backing reconciliation, authenticated EVM
receipt admission and public transaction routes remain separate gates. No
methods here authenticate a transaction signer or system contract. The owned
application must continue rejecting nonempty collections or bindings until
those gates are satisfied; empty module/query mounting does not activate them.
