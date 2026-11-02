# Separate network compatibility candidate

This nested module builds the owned application for a separate, fresh network. The repository root remains the legacy mainnet replay build, with its own dependency graph. Do not import that legacy module here.

The current candidate follows the active [2025.1 release family](https://docs.cosmos.network/sdk/latest/release-family): SDK `0.53.8`, CometBFT `0.38.26`, Cosmos EVM `0.6.3`, and IBC Go `10.7.0`. Its effective Ethereum implementation is the canonical Cosmos replacement `github.com/cosmos/go-ethereum v1.16.2-cosmos-1`. Exact source, checksums, test scope and remaining gates are recorded in `family-maintained53.json`.

`family-check` constructs the owned application in an empty in-memory database, checks the EVM, IBC and transfer wiring, and exits. It opens no listeners or external connections. It is not a daemon or a completed native feature port.

Run ordinary compatibility checks from this directory with the pinned Go `1.26.10` toolchain:

```sh
GOTOOLCHAIN=local go mod download
GOTOOLCHAIN=local go mod verify
GOTOOLCHAIN=local go test -mod=readonly -p 1 -count=1 ./identity ./decimal/... ./app ./internal/family ./internal/ibcharness ./internal/ibcreference/...
GOTOOLCHAIN=local go test -mod=readonly -p 1 -count=1 -tags=test ./app -run '^TestOwnApplicationICS20TwoChains$'
GOTOOLCHAIN=local go run -mod=readonly ./cmd/family-check
```

The production application tests exercise a signed EVM transfer, exact charged fees, signing-domain rejection and bounded financial rollback. The `test` tag enables independent in-process EVM configurations solely for the two-chain ICS20 harness. Installable daemons must use the default production build and actual separate processes.

Generic SDK staking does not establish Decimal validator, delegation, rewards, coin or NFT parity. These native modules require explicit wiring and state tests. The canonical EVM proposer-address conversion also needs its separately reviewed source correction and an actual `COINBASE` execution regression. A guarded daemon, external relayer tests, feature parity and release review remain necessary before presenting this candidate as a network release.

The older `family-manifest.json` preserves the SDK `0.54` / EVM `0.7` bootstrap snapshot. Its validation and advisory findings describe that historical graph. The SDK `0.55` source adapter is retained on a separate development branch; it is not this maintained-family candidate.
