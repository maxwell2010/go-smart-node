# Installation for isolated integration testing

No stable release has been published. Build instructions below are for isolated development only. Supported test target currently used by CI: Ubuntu 22.04 amd64, GCC, CGO and Go 1.24.4. This pinned historical toolchain is a reproducibility baseline, not a claim that its security patch level is current. Dependency/toolchain remediation is a production release gate.

## Build from source

```sh
git clone --branch integration/reliability-ibc https://github.com/maxwell2010/go-smart-node.git mintcandy-node
cd mintcandy-node
mkdir -p build
CGO_ENABLED=1 go build -mod=readonly -trimpath -o build/dscd-replay ./cmd/dscd
CGO_ENABLED=1 go build -mod=readonly -trimpath -tags experimental_ibc -o build/dscd-ibc-lab ./cmd/dscd
./build/dscd-replay version --long
./build/dscd-ibc-lab version --long
```

Use dscd-replay only with separate disposable replay data. Use dscd-ibc-lab only with newly generated isolated fixtures. The experimental binary accepts only decimal_900001-1 and decimal_900002-1, with reliability rules starting at height1. Mainnet/public-testnet activation heights are absent. The ordinary journal/replay fix is unconditional and needs historical validation.

## Validate before running

```sh
CGO_ENABLED=1 go test -mod=readonly -count=1 ./app ./app/ante ./integrationconfig ./x/validator/keeper ./x/upgrade ./cmd/config ./integration/ibclab
CGO_ENABLED=1 go test -mod=readonly -tags experimental_ibc -count=1 -run '^TestExperimental' ./app ./app/ante ./integrationconfig
```

The Linux candidate workflow builds both flavors twice and compares their bytes, then archives binaries, LICENSE, BUILD.json and checksums. Download only artifacts matching the intended source commit and flavor. Run sha256sum -c SHA256SUMS in the extracted artifact directory. Check BUILD.json before using a binary. Same-environment double-build equality is not an independent reproducibility certification.

## Isolation and operation

Never use a production home, keyring, validator signing key/state, database or service ports. Do not install or restart a system service from these instructions. Experimental fixture export is opt-in and creates ephemeral private keys; keep those local and never upload them as release assets. Fixture export alone is not a complete operator runbook: two-process bootstrap and an external relayer still require validation.

Do not delete or copy live LevelDB files. Replay requires immutable snapshot chunks or a consistent backup, trusted headers and matching block history. Run it against a new local home without a signer or production peer connections.

The inherited automatic updater can install an upstream-plan binary and remove fork features at a future upgrade. Operator pinning/supervisor policy must be decided before any deployment. Never edit historical upgrade plans or disable necessary migrations merely to preserve a custom executable.

A previous verified binary and a compatible consistent backup are needed for rollback. Do not run an older executable on a newly migrated database without a tested compatibility procedure. No migration or automatic rollback is performed by the build scripts.
