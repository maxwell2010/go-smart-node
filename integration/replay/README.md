# Offline mainnet snapshot replay helpers

These tools operate only on local copies. They never start consensus, connect peers, sign transactions or open a production database. The window runner disables the default Go HTTP transport to stop legacy upgrade downloads during replay.

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

On runtime commit `8f1c8d43cc3fa0613404a7f5195309347467bf55`, a fresh Linux environment restored the existing mainnet snapshot with the exact trusted AppHash. The build used official Go 1.24.4, CGO and Ubuntu GCC 13.3, with no experimental IBC tag. Whole-module verification passed before and after helper builds, and the commit/nil/absent vote and checkpoint regression tests passed. Restore and replay helpers ran in separate network namespaces.

All 7,070 contiguous blocks 34271001 through 34278070 matched canonical AppHashes and LastResultsHashes (code, data and gas). Canonical transaction codes also matched. This window contains one real MsgSetOnline transaction and 60 Ethereum transactions, including registry calls to delegateByPermit, delegateDEL, delegateHoldByPermit, hold, transfer and withdrawHold. These are observed historical calls; a Cosmos code of zero alone does not establish an empty EVM VmError. Contract implementation, owner and reward override were inspected on the isolated state.

The imported checkpoint is `E3770265509656FCB3752772DDDB54DC772C8CC9737B7A654C6A007142ACB378` at 34271000. The verified checkpoint at 34278070 is `8858C306589DF2F5350AB84525F9CE9077770FEC3C3020B72F591F7C40CC968E`. The imported hash was compared with an independent official RPC endpoint. A later replay must independently confirm its terminal checkpoint as well.

This remains a partial replay. It does not prove the recovery at 34342701, all EVM paths, activated reliability behavior, IBC production readiness or compatibility of a different release toolchain. The longer contiguous replay is still in progress. Rebuild and repeat the validation for the exact release runtime and toolchain. Earlier Windows results were superseded for dependency provenance by this clean Linux run; their shared module cache failed whole-module verification.

For fault rehearsals, wait for the restore or window process to exit, then copy the closed disposable home to a separate location. Record its height, AppHash and build provenance and keep that source copy read-only. Never copy a live LevelDB directory or open the working replay database from another process.


Long runs append verified records to replay-history.jsonl; the atomic JSON checkpoint contains only the current bounded window. Existing cumulative JSON history is seeded once, preserving all previous verified heights. The history stream is synced and an explicit window marker published at successful window completion. The runner checks database height and AppHash against the preceding checkpoint before resume. A crash between database commit and checkpoint publication requires investigation/rollback; it never silently skips unverified state.
