# External relayer smoke for isolated DSC processes

This opt-in test uses two NEW local DSC processes and the official Cosmos relayer v2.6.0. It never imports production data, keys or endpoints. Generated private fixtures live under Go test temporary directories and must not be uploaded as release artifacts.

## Verified source tool

The tested relayer was built from `github.com/cosmos/relayer/v2@v2.6.0` using Go's module checksum verification, CGO and a C compiler. Module checksum: `h1:JPUs8fyhC+Sz/lqBaITehf98NUqM6hru6QIFgUVKpYA=`. Source commit: `de04d53da595b64dc88a5d818b1156370e272832`.

The test verifies the provided executable's Go build info identifies that exact module and version. It does not download binaries. CGO-disabled compilation failed in this relayer's pinned Geth dependency; the CGO source build succeeded.

## Run

Set `DSC_IBC_PROCESS_SMOKE=1` to compile and boot two disposable nodes. Additionally set `DSC_IBC_RELAYER_BINARY` to the source-built relayer executable to execute external handshakes and transfers. Then run:

```sh
go test -mod=readonly -tags experimental_ibc -run '^TestExperimentalICS20ProcessSmoke$' -v -timeout 240s ./app
```

CGO must be enabled with a working C compiler. Without the process variable this test skips. Without the relayer path it only verifies the two node processes advance blocks.

The test owns its temporary directory and child PIDs. It refuses occupied test ports, checks exact chain IDs, binds RPC/P2P to loopback, disables peer discovery, and enables separate loopback gRPC ports only in external relayer mode. HTTP API, gRPC-Web and EVM RPC remain disabled. On Windows children are hidden. Cleanup cancels only owned child processes.

## Protocol and fee behavior

Fresh funded relayer signers derive from fresh disposable mnemonics on HD path m/44'/60'/0'/0/0. The relayer uses Ethermint codec and coin type 60. Keys are not printed.

Decimal does not expose the standard staking parameter query expected by this relayer. The test creates clients explicitly, deriving the unbonding period from each controlled genesis and setting its trusting period to half that value. On-chain self-client validation remains active. This is an operator compatibility workaround, not implementation of a standard staking query API.

The relayer is configured with ZERO DECLARED fee (`gas-prices: 0del`). This uses Decimal's existing native commission calculation: actual commissions are still debited. Tests assert native debits for client creation, both sides of the handshake and outgoing/return transfers. A nonzero declared gas price combined with the relayer's unbounded simulation gas initially caused an unrealistic estimated fee and insufficient-funds failure; the test does not conceal that by inflating fixture funds.

The external test checks native escrow, exact voucher units, voucher burning and release of native escrow on return. Committed receipts must succeed with bounded positive gas limits. The in-process DSC tests separately cover error acknowledgement, timeout, proof rejection, expired client, disk reopen and the current-version migration path.

## Remaining limits

This smoke does not establish production readiness of SDK 0.46 / IBC v6, public-network compatibility, every operator relayer, or a major-version migration. Historical mainnet replay and supported dependency modernization remain release gates. Zero-declared-fee operator mode and explicit unbonding configuration must be documented for the exact release; do not assume generic Cosmos defaults work.

Official references:
- https://github.com/cosmos/relayer/tree/v2.6.0
- https://github.com/cosmos/relayer/blob/v2.6.0/relayer/chains/cosmos/keys.go
- https://github.com/cosmos/relayer/blob/v2.6.0/relayer/chains/cosmos/query.go
