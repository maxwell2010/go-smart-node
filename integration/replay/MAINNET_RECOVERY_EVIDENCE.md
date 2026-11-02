# Legacy mainnet recovery replay evidence

Validation completed on 2026-11-02. This is a legacy mainnet state and
transaction-result reference. It does not certify the separate modern network,
its dependency graph, native validator adapter or production readiness.

## Result

Both Linux compiler runs replayed all **71,701 contiguous blocks**, from
34,271,001 through the recovery at **34,342,701**, with **862 canonical
transactions** per run. Every committed AppHash, LastResultsHash and transaction
code matched the cached canonical chain. Comparing the two complete histories
found no differences in height, AppHash, transaction count or transaction codes.

The final AppHash is:

```text
3E4FA6EDCA1C4EFE905F032E5891632D5918481B5E95AB9151A8F129507F4E9B
```

It also matches the preceding-state AppHash in the independent
[official header at 34,342,702](https://node.decimalchain.com/rpc/block?height=34342702).
The official block ID matched the cached block ID:
`BC27CB1518ABD1FD12A77E053EAE59663710BB0289096E01B15DFB8BEE7D1874`.
That response was captured with SHA256
`e33a9032552626ea76e2c7cc01a73357d4ed85c855006afc8d100949eb50b278`.

## Inputs and build provenance

The reference source was commit
`8f1c8d43cc3fa0613404a7f5195309347467bf55`, without experimental IBC tags.
The runs used official Go 1.24.4 and Go 1.26.10 on Linux amd64, CGO enabled,
and Ubuntu GCC 13.3. Whole-module verification passed before and after building
the original and bridge helpers. Go 1.24.4 is a historical reproduction baseline,
not a supported release-security recommendation.

Both began from the existing mainnet snapshot at 34,271,000, format 2, chunks
0 through 12, with the matching complete public genesis for `decimal_75-1`.
The imported AppHash matched an independent official header:
`E3770265509656FCB3752772DDDB54DC772C8CC9737B7A654C6A007142ACB378`.
The public genesis SHA256 is
`e3fa0c3d311c6ffcf49135f49e440e69fbe20077139b4535d60e2e3a80f3a58b`.
No InitChain/genesis replay is claimed by this snapshot-based run.

## Historical registration bridge

This was **not one unmodified binary across the entire window**. At
34,341,377 the current binary correctly refused to run a future registered
upgrade before its scheduled height: `BINARY UPDATED BEFORE TRIGGER`.
The preceding commit at 34,341,376 matched the canonical AppHash; the panic
was not a committed hash mismatch. Original logs and checkpoint markers were
preserved before recovery orchestration.

The same source was built with transparent linker substitutions restoring the
preceding upstream handler registration. No consensus code or upgrade guard was
disabled. The original binary was selected again exactly at 34,342,701.

```text
-X bitbucket.org/decimalteam/go-smart-node/app.DesyncRepairPlanName=https://repo.decimalchain.com/34197201
-X bitbucket.org/decimalteam/go-smart-node/app.DesyncRepairResumeAt=2026-10-20T11:30:00Z
```

| Compiler | Original registration before plan | Preceding-plan bridge | Original registration at recovery |
| --- | --- | --- | --- |
| Go 1.24.4 | 34,271,001-34,341,376 | 34,341,377-34,342,700 | 34,342,701 |
| Go 1.26.10 | 34,271,001-34,341,070 | 34,341,071-34,342,700 | 34,342,701 |

| Helper | SHA256 |
| --- | --- |
| Original Go 1.24.4 | `3356635ce8ae1af9cb13fabb4761158fd6919096915a58fb1c45431efd4e4944` |
| Bridge Go 1.24.4 | `67b4ce873c3186c92fc0e23fd1e60ab925098a6087e4fc3ba89e26927bb02230` |
| Original Go 1.26.10 | `b9a7b7a99f72deee6cded7a98166ad9c56461046f8631dd48f98d9db538f19c5` |
| Bridge Go 1.26.10 | `7939da8ba8268119209418c7fca09f29977b7429907c81f098acb823327ca29b` |

## Observed contract transition

Cached-context inspection at 34,342,700 and 34,342,701 observed the authentic
delegation proxy `0xA16c34Ed1c0601c0E749e17ebeF19752A15FaA01` change its EIP-1967
implementation from `0xa4567e1a2182b630fb57441ae788dce98721d358` to
`0x2f29724dcf9e3fe2864db03c5310cb368aa96f09`.
The owner remained `0x8c91c1bd5fed8efb46ae3b5802510187aea084db` and the enabled
reward override remained `600000000000000000`.
The complete recovered application state matched the independent canonical hash.

## Isolation and handoff

Application helpers ran under `unshare --net`, without consensus services,
peers, signing keys or production database access. Only one collector performed
bounded read-only RPC requests: at most 20 calls per batch, at least one second
idle gap, an 8 MiB response cap, 12-second timeout, an 8-second latency stop,
source-sync checks and a 15 GiB host-disk guard. No production transactions were
sent or services changed.

Both workers exited before a separate closed post-upgrade copy was made at
`/opt/mintcandy-replay/rehearsal-34342701-clean`. The copy's public genesis hash
and absence of signing keys were checked, and the source was made read-only.
Ordinary contract regressions must copy it into fresh temporary children on the
separate C-volume `TMPDIR`; the completed replay homes and read-only seed must
not be opened or mutated by tests. Separate contract-test results are not implied
by this replay result.

Local artifacts outside Git include `legacy-final-replay-result.json`,
`legacy-toolchain-comparison.json`, `terminal-independent-checkpoint.json`,
`terminal-official-header-34342702.json`, `post-upgrade-closed-copy-manifest.json`
and `post-upgrade-closed-copy-provenance.json`. Execution phases and the
pre-trigger stop are retained rather than hidden by a single-binary claim.

## Limits

The runner did not independently compare ABCI event lists or emitted EndBlock
validator/consensus-parameter updates. LastResultsHash does not cover event
lists. A Cosmos code of zero does not establish an empty EVM VmError.
This evidence therefore does not prove every EVM path, indexer compatibility,
all consensus responses, activated reliability rules or modern-network behavior.
Earlier shared-cache Windows results were superseded for provenance by these
clean Linux runs; their whole-module verification had failed.
