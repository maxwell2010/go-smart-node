# Custom-reserve governance ownership

`AggregateReserveSource` implements an explicit fresh-network voting policy.
It does not mint bank tokens, persist SDK staking shares, or change the native
validator's authoritative integer power. All amounts below are voting-ratio
operands only.

For every denomination the source sums the complete active native delegation
records, across validators. Coin and NFT reserve positions participate together.
Holds are already part of each position's total; queues are not added again.
The base DEL amount is valued directly. For a custom denomination, its validated
same-context coin record supplies volume, reserve and CRR, and the total delegated
amount must not exceed its volume. The source applies the pinned Decimal
`CalculateSaleReturn(volume, reserve, CRR, globalDelegatedAmount)` formula once
to the aggregate. This follows the aggregate-liquidation basis used by legacy
`calculateCustomCoinPrices`, rather than a spot price or an external RPC quote.

The resulting aggregate value is divided between positions in proportion to
their custom amounts. Voting ownership has a fixed resolution of 10^18 subunits
per DEL atomic unit. Integer quotients are assigned first; the remaining subunits
go to the largest rational remainders, with the complete position SHA256 breaking
ties. Thus every denomination's allocated sum equals its full aggregate value at
that resolution, including tiny fractional positions. Input iteration order
cannot affect the allocation. Base DEL ownership uses the same resolution.

This is a new governance rounding policy. It does not claim byte-for-byte parity
with legacy per-position SDK decimal truncation, a bank claim, an executable
minimum-return quote, or reconstruction of historical consensus power. The
validator's aggregate native power continues to determine its total influence;
ownership only divides that influence and the SDK's normal delegator override.

CRR100 uses exact big-integer intermediate arithmetic. Other CRRs retain the
existing pinned 100-bit floating-point formula and its rounding behavior. Formula
panics and results outside [0,reserve] fail closed. This does not establish a new
mathematical accuracy guarantee: independent financial review and comparison
with the actual fresh Solidity contracts remain release gates.

Every relevant custom coin record and the native coin parameters participate in
a framed SHA256 dependency digest, bound to the native state hash. Snapshot
construction checks this digest before and after valuation; every ordinary
adapter guard and each session's final total/close checks it again in a discarded
SDK cache. Changing reserve, volume, CRR or other pinned metadata during a tally
invalidates that tally. Missing/corrupt records and inconsistent chain, height,
base denomination, supply, allocation or integer bounds are errors. Request bytes
are cloned for each source/dependency call to prevent aliasing the saved request.

The SDK53 tests use real native/coin keepers and the unmodified actual SDK gov
tally. A validator with100 power and ownership60 DEL plus40 custom units valued
at20 DEL has80 DEL of ownership; the custom/NFT delegator overrides25 power, not
40. Exact fractional allocation, order independence and stale valuation checks
are also tested. Application routing, NFT-to-actual-contract reconciliation,
genesis reserve backing and authority wiring remain separate required work.

Validation: Go1.26.10/Linux, SDK0.53.8. The complete validator subtree
passes with `go test -mod=readonly -p 1 -count=1 -tags sdk53 ./decimal/validator/...`;
coin/NFT keeper and schema tests also pass. Full external module verification
and both owned-source verifiers pass before and after. The final focused
governance rerun includes the added exact grouped-owner-share assertion.
