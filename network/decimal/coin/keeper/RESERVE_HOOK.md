# Authenticated existing-token reserve synchronization

`ReserveHook` is a native metadata bridge for tokens already bound by the
validated native coin address index. It accepts the compiled DRC20 ABI and a
same-context final-state reader supplied by application wiring. Unknown emitters
cannot create bindings or modify native state. The base DEL record cannot be
updated from an ERC20 event, including a wrapped-DEL mint.

Every matching registered-token event must decode exactly. Multiple events for
the same token are checked and reduced to that token's final event in receipt
order. Intermediate mint/burn events may legitimately precede final reserve
changes; they are not independently installed as complete final metadata.
Different tokens retain separate records. Final supply and reserve must match
the actual final token supply and native backing returned by the state reader.
The reader executes in a discarded child SDK cache.

## Prepared existing-coin updates

`Prepare(ctx, receipt)` returns an opaque `ReservePlan` with `Validate(ctx)` and
`Apply(ctx)` methods. It captures only registered nonbase coins, their address
index resolution, complete native records and parameters, the complete block
header, header hash and transaction bytes. It captures detached event values;
later mutations of the caller's receipt cannot change the plan. Receipts with
no matching reserve topic, and failed receipts, return a no-op before any native
KV or backing read. A matching unknown emitter requires an address-index lookup
but still cannot create native coin state.

Preparation checks all final updates before writing any coin. Validation repeats
both native prestate checks and the authenticated final EVM/bank read. Apply
also performs fresh validation, including for standalone callers. One height,
header or empty governance transaction hash does not prove that the authoritative
backing remained unchanged between API calls. An ordinary same-context bank
credit is therefore enough to invalidate a previously prepared reserve value.

Apply consumes the plan on the first attempt, including on failure; shallow
copies share the private consumption token. A failed/discarded transaction must
prepare a new plan. This is an internal execution lifecycle, not persistent
receipt replay protection, public transaction authorization or asset admission.
The caller must retain its own complete EVM/native transaction cache boundary.

The compatibility `PostTxProcessing` wrapper prepares and applies directly, so
it verifies backing twice. A coordinator that performs Prepare, Validate and
Apply verifies it three times per affected token. Every source read inherits the
caller gas meter, and reader-side writes/events remain discarded. These reads
are intentionally not replaced by stale caches. Combined financial routing must
measure the full source authorization, token views, native writes and other
stages before choosing a network work budget; no allowance is raised here.
The Linux Go1.26.10 direct-proxy fixture measured 125706 SDK gas in Prepare,
121752 in Validate and 138133 in Apply (385591 total). Its binding policy is
explicitly a fixture; these numbers do not include the production SourceAuth
pipeline or establish a per-token production limit.

The entire receipt's metadata writes and typed SDK events share one cache. A
later decoding, backing-read, state-validation or event-emission error leaves
all earlier records and events unchanged. Failed EVM receipts perform no
synchronization. The configured positive receipt log limit is explicit chain
policy, not an assertion of charged EVM gas for post-processing.

Real SDK cache tests cover final-event grouping, registered and foreign
emitters, malformed data/topics, final-backing mismatch, reader errors and
discarded reader writes, later typed-event failure and outer commit/discard.
These tests exercise the native bridge; they do not claim completed application
wiring or execution of the full newly deployed contract set.

The `coin/evmview` one-App test reuses its genuinely signed deployed DRC20 and
approved runtime binding with the real native coin keeper. It verifies all three
phases, measured inherited gas, discarded fixture state, and rejection after a
normal same-height native bank credit before validation or after validation.
These are existing-token controls with explicit fixture admission, not an
authenticated nonempty genesis or new-token economic model.

Remaining integration includes production factory/token admission, metadata-change
events, reserve-less policy, NFT/validator plans, complete final invariants and
coherent application entry-point installation. This bridge never mints a bank
mirror for each ERC20 transfer and creates no alternative financial ledger.
