# Disposable independent SDK counterparty

This internal lab binary owns an Apache-2.0 licensed subset of canonical `cosmos/ibc-go` v10.7.0 reference wiring, commit `40c9dbe397c8bb385dc03cd2cfc8e36c4023a2ba`, `testing/simapp/{app,ante,genesis}.go`. The upstream license remains in `referenceapp/LICENSE`. It never imports `network/app`, EVM or the upstream SimApp. Its Cosmos prefix and coin-118 native secp256k1 signer remain independent of the owned application's sealed d0 identity and EVM globals. It is not a release daemon or an alternative MintCandy node.

The owned reference retains standard SDK auth/bank/staking/distribution/slashing/mint/governance/upgrade/genutil/vesting/authz/consensus, the canonical legacy params subspaces, IBC core, Tendermint client and ICS20 transfer. Optional ICA/mock apps, extra light clients, fixture queries and simulation manager are omitted. The actual transfer keeper is wired directly to the channel keeper. Unlike the upstream protocol-only fixture, the ante includes standard native fee deduction.

`prepare` requires a new absolute directory, generates ephemeral consensus/P2P and relayer keys, and writes a funded private test genesis. It refuses existing output. `start` requires its fixture marker and exact `ibc-reference-1` domain, loopback listeners, no seeds/peers and disabled PEX. Generated mnemonic/key files must never be published.

On Linux, its opt-in process test is:

```sh
MC_IBC_REFERENCE_PROCESS_SMOKE=1 go test -p 1 -mod=readonly -run TestDisposableReferenceProcess -v ./internal/ibcreference
```

It builds a production-tag binary into `t.TempDir`, checks overwrite refusal, starts only its own child, requires height3 and stops only that PID. Bring loopback up when using a private network namespace. `TestReferenceSignedNativeFee` executes a genuine signed native transfer through actual ante/FinalizeBlock and verifies exact sender debit, recipient credit, fee collector credit, sequence and charged gas. Unit-fixture inflation is disabled to isolate fee deltas.

Current SDK53/Comet38/IBC10 execution evidence on the coherent graph is recorded below. SDK55 process/fee passes and its failed Hermes compatibility probe are preserved in the separate prior branch; they are not counted as SDK53 evidence. Actual two-process interoperability requires the production-tag owned daemon, this independent counterparty and an external relayer completing handshakes, send/return/refund flows and pending-packet restart. Native Decimal financial parity remains a separate requirement.

Maintained-family component validation on November 2, 2026: signed fee test PASS0.16s; actual production-tag process reached height3, PASS60.05s including cold link; verified official Hermes compatibility/health PASS without WARN/ERROR. The chain's actual IBC expected block-time parameter is 30s and the operator config matches it. Full module verification passed. The independent reference binary/source provenance is recorded in the sibling ibclab documentation. These results do not certify the owned daemon, native economic port or cross-process packet flows.
