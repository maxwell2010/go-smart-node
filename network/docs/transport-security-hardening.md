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

Fresh reference source/binary vulnerability captures are pending for this patch snapshot. This document does not claim an advisory-free result or add scanner exclusions. Pion DTLS v2 and unsafe OpenPGP remain separate source/exposure reviews. The actual geth NAT path calls plain STUN UDP `Dial`, while DTLS negotiation is in the separate STUN `DialURI` TURNS branch; a generic `errors.Error` static path alone does not prove nonce-generation exposure.

SDK upgrade download utilities are reached through the software-upgrade proposal convenience CLI's remote info parsing and full URL validation. They are not evidence of a newly discovered in-process BeginBlock downloader. A fresh daemon can omit that convenience command while preserving upgrade state and IBC upgrade support. No consensus migrations were disabled in this batch.
