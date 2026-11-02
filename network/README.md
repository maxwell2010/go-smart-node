# Modern network dependency bootstrap

This nested Go module is the compatibility baseline for a separate, fresh network. The repository root remains the legacy mainnet replay build. Their dependency graphs are separate; do not add a dependency on the legacy root module here.

`family-check` constructs the pinned official Cosmos EVM example application in an empty in-memory database, checks that EVM, IBC and transfer keepers were wired, then exits. It opens no listeners and sends no transactions. It is a dependency wiring probe, not a runnable Decimal node, a completed feature port or a mainnet migration.

Run from this directory with the exact supported toolchain recorded in `family-manifest.json`:

```sh
GOTOOLCHAIN=local go mod download
GOTOOLCHAIN=local go mod verify
GOTOOLCHAIN=local go test -mod=readonly -p 1 -count=1 ./...
GOTOOLCHAIN=local go run -mod=readonly ./cmd/family-check
```

The official reference is Cosmos EVM `v0.7.3`, exact source commit `c3ae9067a7eadcdea55a5192226916deb44c71c4`. Its `evmd/` example is a separate module; the nested module requires the exact matching pseudo-version while explicitly selecting root EVM `v0.7.3`. Dependency replacements are not inherited, so this module carries the reference's required replacements explicitly. SDK, Comet, IBC and transport patches are selected within the tested compatible family rather than mixing unrelated latest major versions.

The Ethereum import path is replaced by the Cosmos geth fork. Its actual replacement version and checksum matter; the upstream `require` version alone does not describe the executable. Govulncheck queries the replacement module path, so a clean fork-path scan does not establish that upstream geth advisories have been reviewed or backported. The family manifest retains this coverage limitation.

Decimal native coin creation, reserve accounting and conversion formulas, NFT semantics, delegation, rewards, validator selection, contract interfaces and events still require an explicit port and parity tests. Generic SDK staking and distribution are not substitutes for these financial semantics. Fresh chain identity, genesis, a local application constructor, a guarded daemon CLI and multi-chain tests must be completed before presenting this as a network release.
