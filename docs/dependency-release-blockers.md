# Dependency release blockers: deduplicated advisory inventory

Snapshot date: 2026-11-02. CLI scan: govulncheck v1.1.4, Go 1.24.4, current database at scan time, ordinary `./cmd/dscd` entrypoint. This is a historical scan baseline, not a scan of an upgraded toolchain or dependency set.

## Counts and meaning

85 distinct symbol-level advisories, grouped exclusively by dependency provenance:

| Provenance bucket | Distinct advisory IDs |
| --- | ---: |
| Direct modules only | 23 |
| Toolchain/standard library only | 43 |
| Both toolchain and transitive module | 7 |
| Transitive modules only | 12 |

Thus 50 IDs involve the standard library, but seven also involve a module such as x/net. These numbers must not be added again. Direct/transitive describes go.mod provenance; runtime describes exposure and is an independent axis. Standard-library issues execute in the node process, not only during compilation. A module imported only for tooling can still be present in a binary inventory without an exploitable server path.

Static symbol reports are not separate exploits. The CLI scan contains 12469 symbol findings but only the 85 IDs below. The generator deduplicates IDs, rejects truncated JSON, ignores non-symbol findings for this count and preserves multi-module provenance. Generated protobuf symbols, reflection, interface dispatch and init paths make some traces extremely broad.

## Priority runtime review

| Area | Evidence and confidence | Required decision |
| --- | --- | --- |
| gRPC network server | GO-2026-4762 includes a concrete main -> Ethermint StartCmd -> startInProcess -> SDK StartGRPCServer -> grpc.Serve trace. This is a credible server exposure path, conditional on gRPC configuration; exploit prerequisites are not yet tested. | Treat remotely exposed gRPC as high priority. Restrict network access first, then test a patched transport version and authorization behavior. |
| Transaction decoding | GO-2024-3339 has three symbol traces to main, including UnpackAny via IBC transfer CLI and Cobra. This exact trace is a CLI query path, not proof that malformed transactions exploit consensus. The SDK decoder is shared elsewhere and requires separate adversarial request tests. | Do not dismiss the SDK advisory simply because production ICS-20 is absent. A supported SDK migration or reviewed backport is a release gate. |
| IBC acknowledgement/proof rules | Four v6 IDs appear. Three annotate thousands of generated symbols, so a generated Size/Reset path is not proof of an acknowledgement exploit. Nevertheless public IBC activation exposes the affected protocol family. | Keep isolated-only. Current v6 has no recorded fixed version for several IDs; migrate supported family or obtain an audited backport before production channels. |
| Geth P2P | Some shortest traces end at geth/common.init rather than a P2P handler. Decimal uses CometBFT networking; this scan does not establish that the Geth devp2p endpoint is started. | Inspect actual registration/configuration before assigning remote P2P exposure. Do not blindly replace Geth: it is also the consensus EVM dependency. |
| Module math/cryptography | cosmossdk.io/math has an init -> Quo trace; gnark and Geth affect cryptographic/EVM code. These paths are potentially consensus sensitive, not independent web-server patches. | Separate semantic review, vectors and replay. Do not batch-upgrade them with HTTP dependencies. |
| Downloaders and key tooling | Shortest go-getter trace can be only Cobra -> getter.Error; openpgp can be armor.init. These are not demonstrated untrusted-download/key-import exploit paths. | Inventory actual URI schemes, extraction and operator key-import surfaces. Disable unnecessary public exposure; patch libraries with compatibility tests. |

An example of imprecision: a reported init -> crypto/rand.Read -> io.ReadFull -> gRPC transport.Read chain reflects interface call-graph breadth. It is not evidence that random-byte generation itself accepts remote HTTP/2 frames.

## Independent remediation batches

These are candidates for isolated testing without changing SDK-family or consensus rules. None is asserted fixed by this document.

1. **Build toolchain and scanner.** Rebuild ordinary and experimental binaries using a currently patched supported Go line, then run a compatible current scanner on those exact binaries and source entrypoints. Go 1.26.10 is a candidate floor for the last standard-library advisory in this snapshot; it is not a permanent latest-version recommendation. Re-run EVM, IBC, parser, cross-platform and replay tests because compiler/runtime changes are not proof of replay equivalence. Do not count the old-Go baseline as a current scan.
2. **Transport/web libraries.** Test gRPC 1.83.2 or a later supported patched release, websocket >=1.5.3 and cors >=1.11.0 on a separate branch. Verify service registration, HTTP/2 bounds, RPC/WS reconnects, authorization and shutdown. Restrict unauthenticated service bindings and apply proxy request/connection limits without changing chain state.
3. **Download/extraction helpers.** Test go-getter >=1.8.6, jose2go >=1.7.0 and xz >=0.5.15. Exercise hostile archives, symlinks, URL schemes and size/time limits using fixtures. These candidates still require compilation/API and behavior checks; version floors come from this snapshot, not a claim of a fully clean family.
4. **Auxiliary parsing modules.** Assess protobuf >=1.33.0, x/text >=0.39.0 and x/net >=0.60.0 with their Go-version constraints. Keep gogo-generated consensus serialization separately verified; do not assume a protobuf dependency bump is automatically state neutral.
5. **Unmaintained OpenPGP.** GO-2026-5932 has no fixed version. Determine whether legacy armor/key import is required, restrict it to trusted offline operator input, and design a maintained replacement rather than suppressing the advisory.

Do not change SDK, IBC, Geth, gnark or arithmetic semantics in the replay baseline merely to reduce the count. Their structural migration/backport and full protocol/state validation remain separate work. No release dependency graph was changed for this audit.

## Per-advisory inventory

Fixed versions are only those recorded for the affected modules present in the scan. Empty means the record supplies no fixed version on that scanned module path, not that no newer family exists. All links point to primary Go vulnerability records.

| Advisory | Module provenance | Recorded fixed versions |
| --- | --- | --- |
| [GO-2023-1821](https://pkg.go.dev/vuln/GO-2023-1821) | github.com/cosmos/cosmos-sdk (direct) | github.com/cosmos/cosmos-sdk: none recorded |
| [GO-2023-1881](https://pkg.go.dev/vuln/GO-2023-1881) | github.com/cosmos/cosmos-sdk (direct) | github.com/cosmos/cosmos-sdk: none recorded |
| [GO-2023-2153](https://pkg.go.dev/vuln/GO-2023-2153) | google.golang.org/grpc (direct) | google.golang.org/grpc: 1.56.3, 1.57.1, 1.58.3 |
| [GO-2023-2409](https://pkg.go.dev/vuln/GO-2023-2409) | github.com/dvsekhvalnov/jose2go (transitive) | github.com/dvsekhvalnov/jose2go: 1.5.1-0.20231206184617-48ba0b76bc88 |
| [GO-2024-2571](https://pkg.go.dev/vuln/GO-2024-2571) | github.com/cosmos/cosmos-sdk (direct) | github.com/cosmos/cosmos-sdk: 0.47.9, 0.50.4 |
| [GO-2024-2572](https://pkg.go.dev/vuln/GO-2024-2572) | github.com/cosmos/cosmos-sdk (direct) | github.com/cosmos/cosmos-sdk: 0.47.9, 0.50.4 |
| [GO-2024-2584](https://pkg.go.dev/vuln/GO-2024-2584) | github.com/cosmos/cosmos-sdk (direct) | github.com/cosmos/cosmos-sdk: 0.47.10, 0.50.5 |
| [GO-2024-2611](https://pkg.go.dev/vuln/GO-2024-2611) | google.golang.org/protobuf (direct) | google.golang.org/protobuf: 1.33.0 |
| [GO-2024-2687](https://pkg.go.dev/vuln/GO-2024-2687) | golang.org/x/net (transitive) | golang.org/x/net: 0.23.0 |
| [GO-2024-2694](https://pkg.go.dev/vuln/GO-2024-2694) | github.com/cosmos/ibc-go/v6 (direct) | github.com/cosmos/ibc-go/v6: 6.3.0 |
| [GO-2024-2800](https://pkg.go.dev/vuln/GO-2024-2800) | github.com/hashicorp/go-getter (transitive) | github.com/hashicorp/go-getter: 1.7.4 |
| [GO-2024-2819](https://pkg.go.dev/vuln/GO-2024-2819) | github.com/ethereum/go-ethereum (direct) | github.com/ethereum/go-ethereum: 1.13.15 |
| [GO-2024-2874](https://pkg.go.dev/vuln/GO-2024-2874) | github.com/cosmos/ibc-go/v6 (direct) | github.com/cosmos/ibc-go/v6: none recorded |
| [GO-2024-2883](https://pkg.go.dev/vuln/GO-2024-2883) | github.com/rs/cors (transitive) | github.com/rs/cors: 1.11.0 |
| [GO-2024-2948](https://pkg.go.dev/vuln/GO-2024-2948) | github.com/hashicorp/go-getter (transitive) | github.com/hashicorp/go-getter: 1.7.5 |
| [GO-2024-3279](https://pkg.go.dev/vuln/GO-2024-3279) | cosmossdk.io/math (direct) | cosmossdk.io/math: 1.4.0 |
| [GO-2024-3339](https://pkg.go.dev/vuln/GO-2024-3339) | github.com/cosmos/cosmos-sdk (direct) | github.com/cosmos/cosmos-sdk: 0.47.15, 0.50.11 |
| [GO-2025-3494](https://pkg.go.dev/vuln/GO-2025-3494) | github.com/cosmos/ibc-go/v6 (direct) | github.com/cosmos/ibc-go/v6: none recorded |
| [GO-2025-3517](https://pkg.go.dev/vuln/GO-2025-3517) | github.com/cosmos/ibc-go/v6 (direct) | github.com/cosmos/ibc-go/v6: none recorded |
| [GO-2025-3849](https://pkg.go.dev/vuln/GO-2025-3849) | stdlib (toolchain) | stdlib: 1.23.12, 1.24.6 |
| [GO-2025-3892](https://pkg.go.dev/vuln/GO-2025-3892) | github.com/hashicorp/go-getter (transitive) | github.com/hashicorp/go-getter: 1.7.9 |
| [GO-2025-3922](https://pkg.go.dev/vuln/GO-2025-3922) | github.com/ulikunitz/xz (transitive) | github.com/ulikunitz/xz: 0.5.15 |
| [GO-2025-3956](https://pkg.go.dev/vuln/GO-2025-3956) | stdlib (toolchain) | stdlib: 1.23.12, 1.24.6 |
| [GO-2025-4007](https://pkg.go.dev/vuln/GO-2025-4007) | stdlib (toolchain) | stdlib: 1.24.9, 1.25.3 |
| [GO-2025-4008](https://pkg.go.dev/vuln/GO-2025-4008) | stdlib (toolchain) | stdlib: 1.24.8, 1.25.2 |
| [GO-2025-4009](https://pkg.go.dev/vuln/GO-2025-4009) | stdlib (toolchain) | stdlib: 1.24.8, 1.25.2 |
| [GO-2025-4010](https://pkg.go.dev/vuln/GO-2025-4010) | stdlib (toolchain) | stdlib: 1.24.8, 1.25.2 |
| [GO-2025-4011](https://pkg.go.dev/vuln/GO-2025-4011) | stdlib (toolchain) | stdlib: 1.24.8, 1.25.2 |
| [GO-2025-4012](https://pkg.go.dev/vuln/GO-2025-4012) | stdlib (toolchain) | stdlib: 1.24.8, 1.25.2 |
| [GO-2025-4013](https://pkg.go.dev/vuln/GO-2025-4013) | stdlib (toolchain) | stdlib: 1.24.8, 1.25.2 |
| [GO-2025-4014](https://pkg.go.dev/vuln/GO-2025-4014) | stdlib (toolchain) | stdlib: 1.24.8, 1.25.2 |
| [GO-2025-4087](https://pkg.go.dev/vuln/GO-2025-4087) | github.com/consensys/gnark-crypto (transitive) | github.com/consensys/gnark-crypto: 0.18.1, 0.19.2 |
| [GO-2025-4123](https://pkg.go.dev/vuln/GO-2025-4123) | github.com/dvsekhvalnov/jose2go (transitive) | github.com/dvsekhvalnov/jose2go: 1.7.0 |
| [GO-2025-4155](https://pkg.go.dev/vuln/GO-2025-4155) | stdlib (toolchain) | stdlib: 1.24.11, 1.25.5 |
| [GO-2025-4175](https://pkg.go.dev/vuln/GO-2025-4175) | stdlib (toolchain) | stdlib: 1.24.11, 1.25.5 |
| [GO-2026-4314](https://pkg.go.dev/vuln/GO-2026-4314) | github.com/ethereum/go-ethereum (direct) | github.com/ethereum/go-ethereum: 1.16.8 |
| [GO-2026-4315](https://pkg.go.dev/vuln/GO-2026-4315) | github.com/ethereum/go-ethereum (direct) | github.com/ethereum/go-ethereum: 1.16.8 |
| [GO-2026-4337](https://pkg.go.dev/vuln/GO-2026-4337) | stdlib (toolchain) | stdlib: 1.24.13, 1.25.7, 1.26.0-rc.3 |
| [GO-2026-4340](https://pkg.go.dev/vuln/GO-2026-4340) | stdlib (toolchain) | stdlib: 1.24.12, 1.25.6 |
| [GO-2026-4341](https://pkg.go.dev/vuln/GO-2026-4341) | stdlib (toolchain) | stdlib: 1.24.12, 1.25.6 |
| [GO-2026-4507](https://pkg.go.dev/vuln/GO-2026-4507) | github.com/ethereum/go-ethereum (direct) | github.com/ethereum/go-ethereum: 1.16.9 |
| [GO-2026-4508](https://pkg.go.dev/vuln/GO-2026-4508) | github.com/ethereum/go-ethereum (direct) | github.com/ethereum/go-ethereum: 1.17.0 |
| [GO-2026-4601](https://pkg.go.dev/vuln/GO-2026-4601) | stdlib (toolchain) | stdlib: 1.25.8, 1.26.1 |
| [GO-2026-4602](https://pkg.go.dev/vuln/GO-2026-4602) | stdlib (toolchain) | stdlib: 1.25.8, 1.26.1 |
| [GO-2026-4603](https://pkg.go.dev/vuln/GO-2026-4603) | stdlib (toolchain) | stdlib: 1.25.8, 1.26.1 |
| [GO-2026-4762](https://pkg.go.dev/vuln/GO-2026-4762) | google.golang.org/grpc (direct) | google.golang.org/grpc: 1.79.3 |
| [GO-2026-4865](https://pkg.go.dev/vuln/GO-2026-4865) | stdlib (toolchain) | stdlib: 1.25.9, 1.26.2 |
| [GO-2026-4869](https://pkg.go.dev/vuln/GO-2026-4869) | stdlib (toolchain) | stdlib: 1.25.9, 1.26.2 |
| [GO-2026-4870](https://pkg.go.dev/vuln/GO-2026-4870) | stdlib (toolchain) | stdlib: 1.25.9, 1.26.2 |
| [GO-2026-4918](https://pkg.go.dev/vuln/GO-2026-4918) | golang.org/x/net (transitive); stdlib (toolchain) | golang.org/x/net: 0.53.0; stdlib: 1.25.10, 1.26.3 |
| [GO-2026-4946](https://pkg.go.dev/vuln/GO-2026-4946) | stdlib (toolchain) | stdlib: 1.25.9, 1.26.2 |
| [GO-2026-4947](https://pkg.go.dev/vuln/GO-2026-4947) | stdlib (toolchain) | stdlib: 1.25.9, 1.26.2 |
| [GO-2026-4971](https://pkg.go.dev/vuln/GO-2026-4971) | stdlib (toolchain) | stdlib: 1.25.10, 1.26.3 |
| [GO-2026-4980](https://pkg.go.dev/vuln/GO-2026-4980) | stdlib (toolchain) | stdlib: 1.25.10, 1.26.3 |
| [GO-2026-4982](https://pkg.go.dev/vuln/GO-2026-4982) | stdlib (toolchain) | stdlib: 1.25.10, 1.26.3 |
| [GO-2026-5026](https://pkg.go.dev/vuln/GO-2026-5026) | golang.org/x/net (transitive); stdlib (toolchain) | golang.org/x/net: 0.55.0; stdlib: 1.25.13, 1.26.6, 1.27.0-rc.3 |
| [GO-2026-5037](https://pkg.go.dev/vuln/GO-2026-5037) | stdlib (toolchain) | stdlib: 1.25.11, 1.26.4 |
| [GO-2026-5039](https://pkg.go.dev/vuln/GO-2026-5039) | stdlib (toolchain) | stdlib: 1.25.11, 1.26.4 |
| [GO-2026-5058](https://pkg.go.dev/vuln/GO-2026-5058) | github.com/hashicorp/go-getter (transitive) | github.com/hashicorp/go-getter: 1.8.6 |
| [GO-2026-5856](https://pkg.go.dev/vuln/GO-2026-5856) | stdlib (toolchain) | stdlib: 1.25.12, 1.26.5, 1.27.0-rc.2 |
| [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) | golang.org/x/crypto (direct) | golang.org/x/crypto: none recorded |
| [GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970) | golang.org/x/text (transitive) | golang.org/x/text: 0.39.0 |
| [GO-2026-5972](https://pkg.go.dev/vuln/GO-2026-5972) | stdlib (toolchain) | stdlib: 1.25.13, 1.26.6, 1.27.0-rc.3 |
| [GO-2026-6061](https://pkg.go.dev/vuln/GO-2026-6061) | google.golang.org/grpc (direct) | google.golang.org/grpc: 1.82.1 |
| [GO-2026-6088](https://pkg.go.dev/vuln/GO-2026-6088) | stdlib (toolchain) | stdlib: 1.25.13, 1.26.6, 1.27.0-rc.3 |
| [GO-2026-6089](https://pkg.go.dev/vuln/GO-2026-6089) | stdlib (toolchain) | stdlib: 1.25.13, 1.26.6, 1.27.0-rc.3 |
| [GO-2026-6090](https://pkg.go.dev/vuln/GO-2026-6090) | stdlib (toolchain) | stdlib: 1.25.13, 1.26.6, 1.27.0-rc.3 |
| [GO-2026-6091](https://pkg.go.dev/vuln/GO-2026-6091) | stdlib (toolchain) | stdlib: 1.25.13, 1.26.6, 1.27.0-rc.3 |
| [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218) | stdlib (toolchain) | stdlib: 1.25.13, 1.26.6, 1.27.0-rc.3 |
| [GO-2026-6278](https://pkg.go.dev/vuln/GO-2026-6278) | github.com/gorilla/websocket (transitive) | github.com/gorilla/websocket: 1.5.3 |
| [GO-2026-6348](https://pkg.go.dev/vuln/GO-2026-6348) | google.golang.org/grpc (direct) | google.golang.org/grpc: 1.83.1 |
| [GO-2026-6443](https://pkg.go.dev/vuln/GO-2026-6443) | google.golang.org/grpc (direct) | google.golang.org/grpc: 1.82.2, 1.83.2, 1.84.0-dev.0.20260825144003-d5a41119e0e3, 1.85.0-dev.0.20260825072537-93e31b48545e |
| [GO-2026-6599](https://pkg.go.dev/vuln/GO-2026-6599) | stdlib (toolchain) | stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6600](https://pkg.go.dev/vuln/GO-2026-6600) | stdlib (toolchain) | stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6603](https://pkg.go.dev/vuln/GO-2026-6603) | golang.org/x/net (transitive); stdlib (toolchain) | golang.org/x/net: 0.60.0; stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6605](https://pkg.go.dev/vuln/GO-2026-6605) | stdlib (toolchain) | stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6607](https://pkg.go.dev/vuln/GO-2026-6607) | stdlib (toolchain) | stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6608](https://pkg.go.dev/vuln/GO-2026-6608) | stdlib (toolchain) | stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6609](https://pkg.go.dev/vuln/GO-2026-6609) | stdlib (toolchain) | stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6610](https://pkg.go.dev/vuln/GO-2026-6610) | golang.org/x/net (transitive); stdlib (toolchain) | golang.org/x/net: 0.60.0; stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6611](https://pkg.go.dev/vuln/GO-2026-6611) | golang.org/x/net (transitive); stdlib (toolchain) | golang.org/x/net: 0.60.0; stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6612](https://pkg.go.dev/vuln/GO-2026-6612) | golang.org/x/net (transitive); stdlib (toolchain) | golang.org/x/net: 0.60.0; stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6613](https://pkg.go.dev/vuln/GO-2026-6613) | stdlib (toolchain) | stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6617](https://pkg.go.dev/vuln/GO-2026-6617) | golang.org/x/net (transitive); stdlib (toolchain) | golang.org/x/net: 0.60.0; stdlib: 1.26.9, 1.27.2 |
| [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809) | stdlib (toolchain) | stdlib: 1.26.10, 1.27.3 |

## Reproduction

`python scripts/audit/summarize_govuln.py SCAN.json --go-mod go.mod --output inventory.json`

`python -m unittest discover -s scripts/audit -p "test_*.py"`

The source scan is retained in the local validation artifacts, not embedded in the repository. The script emits advisory counts, module provenance, fixed versions, entrypoints and shortest static paths. JSON-mode scanner exit zero alone is not an advisory-free result.
