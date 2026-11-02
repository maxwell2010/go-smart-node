# Modern transport and telemetry patch batch

This batch changes only the modern network module. The legacy root module and replay binaries remain outside its scope. Cosmos SDK 0.54.4, Comet 0.39.4, IBC 11.2.0, Cosmos EVM 0.7.3, the exact evmd example revision, the Cosmos geth replacement, math and store versions and module checksums are unchanged. `transport-core-family.json` records the before/after comparison. Tidy also normalizes direct/indirect markers for existing native feature imports.

| Component | Before | Patched selection |
| --- | --- | --- |
| Pion DTLS v3 | 3.1.2 | 3.1.4 |
| Pion STUN v3 | 3.1.1 | 3.1.5 |
| WebTransport | 0.10.0 | 0.11.1 |
| QUIC, selected by WebTransport | 0.59.1 | 0.60.0 |
| OTEL core, SDK, trace and metric exporters | 1.43.0 | 1.45.0 |
| OTEL log SDK and exporters | 0.19.0 | 0.21.0 |
| OTEL slog bridge / config / gRPC instrumentation | 0.18.0 / 0.23.0 / 0.68.0 | 0.20.0 / 0.25.0 / 0.70.0 |

Contrib versions are aligned with their declared OTEL 1.45/log 0.21 dependencies. An initial compile with the old slog bridge failed on removed log value types; the matching bridge/config/instrumentation family resolves this without disabling checks. Other MVS changes include the required telemetry propagation, Prometheus, compression and transport dependencies; they are visible in the module diff.

The complete modern module suite passed on clean Linux with Go 1.26.10, CGO/GCC, `-mod=readonly`, `GOMAXPROCS=1` and `-p 1`. It includes the native financial math/SDK compatibility tests available at this branch's base and the upstream memory-app constructor. Whole-module integrity verification passed before and after the builds.

The STUN regression supplies a valid STUN envelope with an empty address attribute and an exactly bounded backing array. The same test in an isolated module reproduces a slice-bounds panic on 3.1.1, then passes with an EOF error on 3.1.5. No sockets or remote services are involved. The pinned DTLS upstream malformed ServerKeyExchange regression also passed. WebTransport client- and server-initiated bidirectional stream tests passed inside a private network namespace containing only loopback.

Both the memory probe and the official upstream reference CLI compile with `-trimpath -buildvcs=false`. The probe executes successfully. This is not proof that the new native daemon starts: that daemon requires its own single-app process lifecycle, strict fresh-chain identity and actual OS-process tests. The official example's temporary-app command setup must not be reused to create a second EVM keeper in a production process.

New manifest-bound captures for source revision `d86f095655cf9791b40a973d13b642e5b92a7014` report three distinct advisory IDs in source and three in the actual reference binary, down from eight and nine respectively. Both gates remain FAIL with exit 1; scanner JSON processes returned zero. There are no stdlib symbol findings. `transport-security-captures.json` retains their raw hashes, byte counts, database timestamp, scope and remaining IDs. There are no scanner exclusions.

Remaining IDs are GO-2025-3684, GO-2026-4479 and GO-2026-5932. The Cosmos EVM report has a conflicting unreviewed Go range versus the primary GitHub range and requires its separate source/behavioral review. Pion DTLS v2 and unsafe OpenPGP remain source/exposure reviews. The actual geth NAT path calls plain STUN UDP `Dial`, while DTLS negotiation is in the separate STUN `DialURI` TURNS branch; a generic `errors.Error` static path alone does not prove nonce-generation exposure.

SDK upgrade download utilities are reached through the software-upgrade proposal convenience CLI's remote info parsing and full URL validation. They are not evidence of a newly discovered in-process BeginBlock downloader. A fresh daemon can omit that convenience command while preserving upgrade state and IBC upgrade support. No consensus migrations were disabled in this batch.

The [official release-family matrix](https://docs.cosmos.network/sdk/latest/release-family), checked on 2026-11-02, lists SDK 0.55/Comet 0.40/EVM 0.7/IBC 11 as family 2026.1. The tested released EVM 0.7.3 tag declares SDK 0.54.3/Comet 0.39.3 dependencies; this bootstrap used their patch lines, 0.54.4/0.39.4. Code compatibility with that tag is not proof of active-family support. A separate 0.55/0.40 compatibility trial and migration is required before claiming alignment with the published support matrix; this transport batch deliberately does not change the core family during tests.
