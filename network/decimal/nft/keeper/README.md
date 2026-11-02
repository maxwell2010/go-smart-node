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
Mint/burn/reserve transitions, bank backing reconciliation,
authenticated EVM receipt hooks and query/transaction routes are still missing.
No methods here authenticate a transaction signer or system contract. The
keeper is not registered in the owned application or enabled for public use.
