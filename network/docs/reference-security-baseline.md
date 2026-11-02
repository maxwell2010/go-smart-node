# Modern reference security baseline

On 2026-11-02 the patched family in `family-manifest.json` compiled both the memory-only probe and the exact official Cosmos EVM `evmd` example CLI. The probe ran successfully, the official CLI returned help from a disposable home, and complete module verification passed. This establishes an upstream compatibility baseline, not the Decimal feature port or a runnable MintCandy daemon.

Govulncheck 1.9.0 analyzed the official `github.com/cosmos/evm/evmd/cmd/evmd` entrypoint and its actual compiled reference binary, with the patched network module graph and Go 1.26.10. Captures retained actual process completion, raw SHA256/byte count, scope and compiler metadata. The database timestamp was `2026-10-27T21:16:18Z`. Source analysis reported eight distinct advisory IDs, binary analysis nine. Both manifest-bound gates returned 1 although the scanner JSON processes returned zero. Neither result is a clean security gate. There were no stdlib symbol findings.

| Advisory | Scanned component | Recorded fix or follow-up |
| --- | --- | --- |
| [GO-2025-3684](https://pkg.go.dev/vuln/GO-2025-3684) | Cosmos EVM | Go report is unreviewed and uses an all-versions range; [primary GitHub advisory](https://github.com/cosmos/evm/security/advisories/GHSA-mjfq-3qr2-6g84) currently identifies `= 0.1.0`. Source/fix ancestry and behavioral regression testing are needed for 0.7.3. |
| [GO-2026-4479](https://pkg.go.dev/vuln/GO-2026-4479) | Pion DTLS v2 | No fixed v2 version recorded; inspect the actual transport dependency and runtime configuration rather than blindly substituting v3. |
| [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) | x/crypto OpenPGP | No version fix for the unmaintained design; inspect actual key-management exposure and migration options. |
| [GO-2026-6099](https://pkg.go.dev/vuln/GO-2026-6099) | webtransport-go | Recorded fix 0.11.1; requires a compatible transport family and tests. |
| [GO-2026-6165](https://pkg.go.dev/vuln/GO-2026-6165) | Pion DTLS v3 | Recorded fix 3.1.4. |
| [GO-2026-6505](https://pkg.go.dev/vuln/GO-2026-6505) | OTEL trace exporters | Recorded fix 1.45.0 across affected trace modules. |
| [GO-2026-6508](https://pkg.go.dev/vuln/GO-2026-6508) | OTEL gRPC log exporter | Recorded fix 0.21.0. |
| [GO-2026-6615](https://pkg.go.dev/vuln/GO-2026-6615) | OTEL log SDK | Recorded fix 0.21.0. |
| [GO-2026-6163](https://pkg.go.dev/vuln/GO-2026-6163) | Pion STUN v3, binary only | Recorded fix 3.1.5. |

The static paths do not prove exploitability. The Cosmos EVM report's shortest path ends at `server.init`; the DTLS v2 and OpenPGP shortest paths include generic `errors.Error` dispatch. Binary findings identify retained symbols, without a source call path from daemon startup. Preserve the findings until source and configuration evidence resolves them; no automatic exclusions were added.

The actual Ethereum dependency is `github.com/cosmos/go-ethereum v1.17.2-cosmos-1`, not the upstream version named in the original require statement. Govulncheck queries replacement module identities, so upstream geth inheritance needs supplemental checks. GitHub's [exact commit comparison](https://github.com/cosmos/go-ethereum/compare/be4dc0c4be2fe316dbdd0a73e48421f64978232f...v1.17.2-cosmos-1) confirms the fork contains upstream tag 1.17.2 at `be4dc0c4be2fe316dbdd0a73e48421f64978232f`, with two additional commits and zero commits behind. An OSV API query for upstream 1.17.2 returned no matching published advisories, while a 1.13.12 positive control returned the known advisories. This checks the upstream base against that query's database state; it does not audit the Cosmos changes or cover future reports.

The raw captures, completion manifests, summaries, official advisory JSON, ancestry response and binary build information remain in local validation artifacts. The tracked family manifest records their identities and the failed gate results. Further changes need tests and new captures, including a fresh scan of the eventual Decimal daemon and its native feature wiring.
