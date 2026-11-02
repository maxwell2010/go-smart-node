# Offline mainnet snapshot replay helpers

These tools operate only on local copies. They never start consensus, connect peers, sign transactions or open a production database. The window runner disables the default Go HTTP transport to stop legacy upgrade downloads during replay.

This is a reference harness for the legacy mainnet runtime. The separate modern-network module has its own genesis, dependencies and release gates. Legacy replay results, including a newer Go toolchain comparison, do not certify that new network binary or its dependency graph.

## Verified input and limits

The restore helper currently targets snapshot height 34271000, format 2, chunks 0 through 12. Supply immutable chunk files under `INPUT/snapshots/34271000/2/` and the matching public genesis under `INPUT/config/genesis.json`. Derive trusted AppHash from canonical block 34271001, because a header records the preceding committed application's hash. Compare checkpoints from independent endpoints.

```sh
go build -mod=readonly -o replay-restore ./cmd/replay-restore
go build -mod=readonly -o replay-window ./cmd/replay-window
./replay-restore -input /isolated/input -home /isolated/new-home -trusted-app-hash E3770265509656FCB3752772DDDB54DC772C8CC9737B7A654C6A007142ACB378
./replay-window -home /isolated/new-home -blocks /isolated/cached-rpc -from 34271001 -to 34271010
```

CGO and a supported C compiler are required. Use an isolated module/build cache and verify the official toolchain archive, compiler provenance and all modules before and after building. A failed `go mod verify` is a failed whole-module integrity gate, even when the missing file appears to be a test fixture. Do not repair or bypass endpoint protection to make this gate pass. Use a clean environment instead. Historical toolchains used to reproduce mainnet behavior are not a claim of supported release security.

The output home must not already exist and must be outside the immutable input directory. Signing keys, signing state and keyrings must never be included. On Linux, isolate each application helper process at the operating-system level as well:

```sh
unshare --net -- ./replay-restore -input /isolated/input -home /isolated/new-home -trusted-app-hash E3770265509656FCB3752772DDDB54DC772C8CC9737B7A654C6A007142ACB378
unshare --net -- ./replay-window -home /isolated/new-home -blocks /isolated/cached-rpc -from 34271001 -to 34271010
```

Keep the optional read-only RPC collector outside this network namespace. Retrieve bounded batches, retain canonical inputs and stop on source lag, excessive latency or response size. The application helper itself needs no network access.

The block cache uses original Tendermint RPC envelopes named `HEIGHT.block.json` and `HEIGHT.validators.json`. Each replay block requires the prior height's validator set and the next block header. The runner validates block bodies, validator ordering, previous AppHash and the committed AppHash against the next canonical header. It includes canonical votes and Byzantine evidence, saves a checkpoint report after every successful block and requires application height to precede the next requested window. Cached validator sets are verified against the predecessor header hash and may be reused only when that hash is unchanged. Commit and nil votes both count as signed, matching CometBFT; only absent votes count as unsigned. Nonempty blocks require compact HEIGHT.results.json canonical transaction outcomes. A mismatch stops execution; do not continue from a failed committed checkpoint without restoring a clean copy.

## Evidence obtained

The completed legacy mainnet reference covers all 71,701 contiguous blocks
34271001 through 34342701 and 862 canonical transactions on both Linux Go 1.24.4
and Go 1.26.10. State/result hashes and transaction codes matched, and the final
AppHash matched an independent official header. A documented preceding-plan
registration bridge was required before the recovery height; this is not a
single-binary or modern-network certification claim.

See [the final evidence record](MAINNET_RECOVERY_EVIDENCE.md) for exact source,
linker flags, binary hashes, execution ranges, contract transition, closed-copy
handoff and ABCI-output limitations. Keep those qualifications with any summary
of the successful replay.

Long runs append verified records to replay-history.jsonl; the atomic JSON checkpoint contains only the current bounded window. Existing cumulative JSON history is seeded once, preserving all previous verified heights. The history stream is synced and an explicit window marker published at successful window completion. The runner checks database height and AppHash against the preceding checkpoint before resume. A crash between database commit and checkpoint publication requires investigation/rollback; it never silently skips unverified state.
