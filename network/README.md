# Separate network compatibility candidate

[Русская инструкция: установка, зависимости, сервер, синхронизация, пиры и хранение истории](docs/ru/README.md).

This nested module builds the owned application for a separate, fresh network. The repository root remains the legacy mainnet replay build, with its own dependency graph. Do not import that legacy module here.

The current candidate retains the selected [2025.1 release family](https://docs.cosmos.network/sdk/latest/release-family): SDK `0.53.8`, CometBFT `0.38.26`, Cosmos EVM `0.6.3`, and IBC Go `10.7.0`. Its effective Ethereum implementation is declared owned source based on `github.com/cosmos/go-ethereum v1.16.2-cosmos-1`, with the upstream coordinate fix and explicit Pion v3 NAT adaptation. See [source backport](docs/security/owned-geth-coordinate-backport.md) and [transport migration](docs/security/maintained-pion-transport.md). `family-maintained53.json` preserves source-specific historical checks and remaining gates.

`family-check` constructs the owned application in an empty in-memory database, checks the EVM, IBC and transfer wiring, and exits. It opens no listeners or external connections. It is not a daemon or a completed native feature port.

Run ordinary compatibility checks from this directory with the pinned Go `1.26.10` toolchain:

```sh
GOTOOLCHAIN=local go mod download
GOTOOLCHAIN=local go mod verify
python3 scripts/verify_owned_evm.py
python3 scripts/verify_owned_geth.py
python3 scripts/verify_owned_sdk.py --compiled
python3 scripts/verify_pion_transport.py
GOTOOLCHAIN=local go test -mod=readonly -p 1 -count=1 ./identity ./decimal/... ./app ./internal/family ./internal/ibcharness ./internal/ibcreference/...
GOTOOLCHAIN=local go test -mod=readonly -p 1 -count=1 -tags=test ./app -run '^TestOwnApplicationICS20TwoChains$'
GOTOOLCHAIN=local go run -mod=readonly ./cmd/family-check
```

The production application tests exercise a signed EVM transfer, exact charged fees, signing-domain rejection and bounded financial rollback. The `test` tag enables independent in-process EVM configurations solely for the two-chain ICS20 harness. Installable daemons must use the default production build and actual separate processes.

Generic SDK staking does not establish Decimal validator, delegation, rewards, coin or NFT parity. These native modules require explicit wiring and state tests. The owned EVM proposer-address correction and actual signed `COINBASE` execution regression now pass; its complete source provenance is checked separately from module integrity. The guarded daemon lifecycle and external Hermes ICS20 gates now pass on disposable networks. Native feature parity and final integrated release review remain necessary before presenting this candidate as a network release.

The older `family-manifest.json` preserves the SDK `0.54` / EVM `0.7` bootstrap snapshot. Its validation and advisory findings describe that historical graph. The SDK `0.55` source adapter is retained on a separate development branch; it is not this maintained-family candidate.

## Build and run a disposable node

The `mcnd` command uses stateless codecs and genesis defaults, then constructs one application when starting. It validates the actual home's genesis signing domains before configuration writes and application creation, including restarts. Its separate default home is `~/.mcnd`; listeners default to loopback and peer discovery is disabled.

Build with Go `1.26.10`, GCC and CGO enabled. Use the default production build, without the `test` tag:

```sh
go mod download
go mod verify
python3 scripts/verify_owned_evm.py
python3 scripts/verify_owned_geth.py
python3 scripts/verify_owned_sdk.py --compiled
python3 scripts/verify_pion_transport.py
MCND_REVISION=$(git rev-parse HEAD)
MCND="$PWD/build/mcnd-$MCND_REVISION"
env -u GOFLAGS -u GOEXPERIMENT -u CGO_CFLAGS -u CGO_CPPFLAGS -u CGO_CXXFLAGS -u CGO_FFLAGS -u CGO_LDFLAGS GOENV=off GOWORK=off GOTOOLCHAIN=local python3 tools/install_mcnd.py --source .. --revision "$MCND_REVISION" --destination "$MCND"
```

The installation helper requires a clean pinned checkout and default build environment; see the Russian operator guide for source/provenance limits. No production pin is published. Individual ERC1155 collateral positions are the selected target model and are not yet an end-to-end active network feature; previous shared-pool tests do not certify that model.

For a new local network, the following creates test funds and a validator in a separate lab home. The example IDs are disposable lab values. The `test` keyring backend is used only for these newly generated lab keys. `keys add` defaults to `eth_secp256k1`. Operator and governance-owner templates use `eth_secp256k1` with coin type 60, so their native `d0` address and Ethereum EOA share the same 20 bytes. IBC relayers use a separate native Cosmos `secp256k1` fixture; its RIPEMD address is not an Ethereum operator address.

```sh
MCND="$PWD/build/mcnd-$(git rev-parse HEAD)"
NODE_HOME="$HOME/.mcnd-lab-990001"
"$MCND" init local --home "$NODE_HOME" --chain-id mintcandy_990001-1 --evm.evm-chain-id 990001
"$MCND" keys add operator --home "$NODE_HOME" --key-type eth_secp256k1 --coin-type 60 --keyring-backend test
ADDRESS=$("$MCND" keys show operator --home "$NODE_HOME" --keyring-backend test -a)
"$MCND" genesis add-genesis-account "$ADDRESS" 1000000000000000000000000del --home "$NODE_HOME"
"$MCND" genesis reconcile-native-allocation --home "$NODE_HOME"
"$MCND" genesis gentx operator 1000000000000000000del --home "$NODE_HOME" --chain-id mintcandy_990001-1 --keyring-backend test
"$MCND" genesis collect-gentxs --home "$NODE_HOME"
"$MCND" genesis validate-genesis --home "$NODE_HOME"
"$MCND" start --home "$NODE_HOME" --chain-id mintcandy_990001-1 --evm.evm-chain-id 990001
```

The bounded Linux lifecycle gate in `tools/mcnd_process_smoke.py` requires a private network namespace. It creates its own new home, exercises initialization, signed gentx, real blocks, restart and EVM chain-ID queries, then checks that a mismatched restart cannot modify that home. It never uses existing node homes or external RPC endpoints.

## Native asset module admission

The owned application mounts native `coin` and `nft` stores and their read-only
protobuf/gRPC/AutoCLI queries. Native financial message handlers are not enabled.
The native coin `Params.BaseVolume` declares genesis allocation; base DEL
balances and current monetary supply remain the SDK bank ledger. Custom coin
queries currently return validated stored metadata, not an authoritative live
EVM reserve quote. The separate bounded `coin/evmview` is required for monetary
valuation once an authenticated deployment binding is installed. There is no
silent fallback from a failed monetary view to stale metadata.

After all genesis account allocations, explicitly run
`mcnd genesis reconcile-native-allocation --home ...` before validation/start.
This fresh-home builder sums actual genesis bank DEL balances and writes the
same amount into declared native allocation, with an exclusive backup. Startup
rejects mismatches rather than inventing or minting supply. Changing genesis
accounts again requires rebuilding/reviewing that declaration and its backup.
The builder creates no keys, transactions, App or EVM configuration singleton.

Nonempty custom-coin/check/NFT application genesis is currently rejected until
authenticated system-contract bootstrap and backing reconciliation are complete.
Keeper-only nonempty genesis and isolated mounted-store cache fixtures do not
prove accepted financial genesis. Full exports preserve native records; their
exported allocation is explicitly reconciled against the exported bank balances
without mutating runtime parameters. A nonempty metadata export remains blocked
for application reimport until contract bootstrap admission is implemented.
Subset exports must include both bank and coin if either is included.

Query list pagination is key-based, limit up to50, with offset/count-total
rejected to avoid unbounded scans. Complete NFT responses cap nested records
at10000 and encoded native content at2MiB; oversized requests fail explicitly.
Collections are shallow; Collection and CollectionsByCreator include token
metadata without sub-tokens; Token resolves full active sub-tokens. Inherited
reserve is resolved in query responses while explicit zero reserve remains zero.
These response limits do not truncate genesis exports. Published historical
compatibility-candidate one-liners predate this native module genesis schema;
use the matching source/version instructions, not mixed CLI commands.
