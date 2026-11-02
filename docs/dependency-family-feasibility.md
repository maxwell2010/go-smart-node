# Dependency family feasibility

Research date: 2026-11-02. Baseline: 8088d58. No dependency changes from this experiment are approved for integration or historical mainnet replay.

## Current-family security assessment

The ordinary CLI entrypoint was scanned with govulncheck v1.1.4 and the current Go vulnerability database on Go 1.24.4. Scanning ./cmd/dscd still reported 85 distinct OSV IDs with symbol-level traces, including 12469 symbol findings. The three IBC advisory records each annotate thousands of generated symbols. Traces terminate in cmd/dscd main/init, so the result is not explained solely by exported test helpers. It does not establish 85 independent exploitable vulnerabilities: reflection, registered message handlers, advisory symbol breadth, runtime configuration and network exposure still require manual triage.

GO-2024-3339 has three CLI-scoped traces, including UnpackAny via the transfer CLI and Cobra. This transaction-decoding issue cannot be dismissed because mainnet transfer routing is absent. IBC records GO-2024-2874, GO-2025-3494 and GO-2025-3517 remain a blocker to public production IBC on the current v6 line. The scanner's JSON mode exited zero despite reporting findings; a security gate must parse findings or use an appropriate text-mode exit check.

## Attempted historical minimum family

Trial requirements: SDK v0.47.17 and IBC-Go v7.10.0, rewriting local IBC imports from v6 to v7. The official v7.10.0 go.mod requires SDK v0.47.13 and CometBFT v0.37.5; choosing SDK v0.47.17 avoids stopping below the SDK advisory patch release. Proxy checks found no v7.10.1 or v7.10.2. These are historical patch releases, not the currently supported family.

The SDK and Comet module archives stalled through the module proxy. For the compilation experiment only, official tagged source archives were fetched from GitHub and installed as local module replacements. A full CLI compilation attempt and a narrower app/ante compilation attempt both failed during package resolution. Representative errors:

```text
finding module for package github.com/cosmos/cosmos-sdk/simapp/params
finding module for package github.com/cosmos/cosmos-sdk/server/rosetta
finding module for package github.com/cosmos/cosmos-sdk/pruning/types
finding module for package github.com/cosmos/cosmos-sdk/simapp
toolchain upgrade needed to resolve github.com/cosmos/cosmos-sdk/simapp/params
.../simapp@v0.0.0-20261028165927-e25c86fdb329 requires go >= 1.27.3
```

This is dependency/package-resolution failure, not a completed API compilation. Go attempted to find a separately versioned current simapp module for imports missing from the selected SDK package tree; installing the suggested newer Go toolchain does not make that old/new module mixture a correct SDK migration.

Decimal Ethermint v0.22.13 itself pins SDK v0.46.16, IBC v6.1.0 and Tendermint v0.34.29. Its app/ante/handler_options.go and eip712.go directly import ibc-go/v6/modules/core/ante. Its encoding/config.go and ethereum/eip712/encoding.go import the old SDK simapp/params package. Thus root-only import/version changes do not remove v6 or migrate the EVM fork. Root app construction, custom ValidatorKeeper, ABI/state migration tests, old Tendermint types and SDK simulation/helper imports also need an explicit port.

The failed experiment patch and logs are retained outside the source tree. No failed go.mod/go.sum or import rewrite is committed here.

## Maintained-family direction

The official IBC-Go README currently lists v10 and v11 as supported. A directly inspected tagged Cosmos EVM v0.7.3 go.mod uses Go 1.25.9, SDK v0.54.3, CometBFT v0.39.3 and IBC v11.0.0. This demonstrates a coherent newer EVM-compatible family; it is a research starting point, not proof that those exact versions are vulnerability-free or compatible with Decimal's fork. Current patches and advisories must be checked before pinning a target.

Production migration therefore needs a dedicated branch and coordinated chain upgrade: port custom EVM fee/hooks/state behavior and staking interfaces, replace removed SDK packages deliberately, adapt transaction signing/admission, migrate stores/genesis/module versions, and rerun historical replay plus two-DSC proof/refund/restart/upgrade tests. Retain the old-family replay baseline for comparison. Do not hide vulnerabilities with module exclusions, blanket latest upgrades or a newer toolchain alone.

## Primary sources

- [IBC acknowledgement advisory and patched releases](https://github.com/cosmos/ibc-go/security/advisories/GHSA-4wf3-5qj9-368v)
- [SDK transaction-decoding advisory](https://pkg.go.dev/vuln/GO-2024-3339)
- [SDK v0.47.17 release](https://github.com/cosmos/cosmos-sdk/releases/tag/v0.47.17)
- [IBC v7.10.0 manifest](https://github.com/cosmos/ibc-go/blob/v7.10.0/go.mod)
- [IBC supported release lines](https://github.com/cosmos/ibc-go#version-matrix)
- [Cosmos EVM v0.7.3 manifest](https://github.com/cosmos/evm/blob/v0.7.3/go.mod)
