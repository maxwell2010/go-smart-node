# NFT receipt planning staging

`Execution.EntryPoint` is a typed route. Signed Ethereum execution and internal
governance execution are the only admitted route types. IBC remains unsupported
until the shared pipeline has actual callback entry-point coverage. Internal
governance can have a zero transaction hash; transaction hash alone is never a
deduplication key.

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

The next layer will construct an opaque immutable plan, validate final actual
ownership, fingerprint its native prestate, then apply all native changes in one
child cache. Reapplication and stale prestate must fail. This staging decoder
does not yet implement that Plan/Apply layer and is not wired into the App.

Minting, burning, reserve updates and metadata mutation currently fail closed
for registered collections. Factory creation admission needs its own approved
factory/runtime and backing path. Reserve projection additionally needs the
previously reconciled native pool record and final actual pool/supply/rate.
ERC1155 uses floor(pool/supply); no remainder is assigned to arbitrary units.
Changed pool keys must later feed the bounded validator-binding reverse index.

Unit tests use compiler-layout fixtures and a test authenticator. They do not
prove live code authentication or completed financial admission. Signed App
receipt decoding, complete Plan/Apply tests and shared orchestration remain
required gates before enabling these routes.
