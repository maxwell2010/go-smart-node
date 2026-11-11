# NFT receipt planning staging

`Execution.EntryPoint` is a typed route. The ownership planner admits signed
Ethereum and internal governance execution. The withdrawal settlement projector
uses a separate route for authenticated, completed Gateway withdrawals. IBC
remains unsupported until the shared pipeline has actual callback coverage.
Internal receipts can have a zero transaction hash. Transaction hash alone is
never a deduplication key.

The compiler-ABI decoder preserves full uint256 IDs, receipt slice order and
ERC1155 batch item provenance. It validates exact topic/data layout, registered
NFT standard, bounded quantities and canonical ABI payloads. Unknown collections
cannot update native ownership. A later error returns no partial decoded result.
Explicit limits cap logs, bytes, collections, transfer entries and unit work;
decoding charges the inherited SDK gas meter.

Concrete authentication must verify independently approved factory, network
domain, live implementation and compiler-derived child runtime identity. The
interface cannot itself prove those checks; nonzero profile hash fields are not
approval evidence. The final-state reader must use bounded actual EVM calls and
charge its work through the inherited SDK meter in discarded read caches.

`StakeCapture` is called before virtual NFT ownership changes. It captures old
validator stake bindings and exact escrow unit selections. `MoveRef` identifies
receipt slice and batch item positions. Withdrawals must select persisted
stake-owned unit IDs; ordinary owner transfers use deterministic ascending IDs.
The NFT module remains the only NFT identity ledger.

`NewPlanner` implements the ownership-only `Plan`/`Apply` layer. Planning validates
final actual ownership and fingerprints complete relevant native bindings,
token metadata, owner indexes and units. Applying re-authenticates profiles,
validates the captured stake fingerprint, rechecks native prestate and final
actual ownership, then applies all movements in one child cache. A later native
error discards earlier owners, indexes and events. Returned movements are copies;
copied opaque plans share one-use status. Every application attempt consumes its
plan, so outer rollback or a failed application requires replanning. Zero internal
transaction hashes can represent independent executions without dedup collisions.
Failed execution receipts never invoke ownership readers or stake capture.

Snapshots page native ownership and separately cap aggregate units read, in
addition to transfer-unit limits. All SDK reads and explicit decoding, snapshot
and planning work use the inherited gas meter. The planner remains unmounted in
the App pending the production authenticator, bounded final reader and shared
orchestration. It does not admit new financial positions.

Minting, burning, reserve updates and metadata mutation currently fail closed
for registered collections. Factory creation admission needs its own approved
factory/runtime and backing path. Reserve projection additionally needs the
previously reconciled native pool record and final actual pool/supply/rate.
ERC1155 uses floor(pool/supply); no remainder is assigned to arbitrary units.
Changed pool keys must later feed the bounded validator-binding reverse index.

Unit tests use actual SDK native stores with compiler-layout receipts and test
authentication/final-state providers. They check ordered virtual transfers,
exact escrow selection, immutable copies, stale approval/stake/ownership/native
state, one-use plans and atomic later-error rollback. The separate signed App
fixture exercises the decoder against real compiled NFT receipts and independently
pinned constructor/runtime identity. Complete Plan/Apply against shared live EVM
and native state, production authentication and financial admission remain gates
before enabling these routes.
