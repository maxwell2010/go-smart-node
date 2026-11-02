# Disposable independent SDK counterparty

This internal lab binary imports the pinned IBC SDK reference application and
never imports `network/app`. Its Cosmos prefix and standard secp256k1 signer
therefore remain independent of the owned application's sealed `d0` identity
and EVM globals. It is not a release daemon or an alternative MintCandy node.

`prepare` requires a new absolute directory, generates ephemeral consensus/P2P
and relayer keys, and writes a funded private test genesis. It refuses existing
output. `start` requires its disposable fixture marker and exact
`ibc-reference-1` chain ID, loopback listeners, no peers/seeds and disabled PEX.
Generated mnemonic/key files must never be committed or published.

On Linux, opt in to its controlled process smoke test:

```sh
MC_IBC_REFERENCE_PROCESS_SMOKE=1 go test -p 1 -mod=readonly -run TestDisposableReferenceProcess -v ./internal/ibcreference
```

The test builds its own binary into `t.TempDir`, checks overwrite refusal,
starts only its own child process, verifies the expected chain reaches height 3
and stops only that child PID. It uses fresh dynamically selected loopback
ports. When run inside a private network namespace, bring up its loopback
interface first. The smoke test passed with the pinned SDK 0.54.4/Comet 0.39.4
reference family; a release-family update requires repetition.

This proves a working independent counterparty process only. Interoperability
certification still requires the owned production-tag daemon and an external
relayer to complete actual client/channel handshakes, send/return/refund flows
and restart recovery between the two independent processes.
