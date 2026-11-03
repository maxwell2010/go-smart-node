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

Remaining integration: compiler-pinned ABI selection, an authenticated EVM
view/backing reader, genuine signed contract tests, factory/token binding,
metadata-change events, reserve-less token policy, NFT/validator hooks and
coherent application installation. This bridge never mints a bank mirror for
each ERC20 transfer and creates no alternative financial ledger.
