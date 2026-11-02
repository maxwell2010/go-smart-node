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

CGO and a supported C compiler are required. The output home must not already exist and must be outside the immutable input directory. Signing keys, signing state and keyrings must never be included.

The block cache uses original Tendermint RPC envelopes named `HEIGHT.block.json` and `HEIGHT.validators.json`. Each replay block requires the prior height's validator set and the next block header. The runner validates block bodies, validator ordering, previous AppHash and the committed AppHash against the next canonical header. It includes canonical votes and Byzantine evidence, saves a checkpoint report after every successful block and requires application height to precede the next requested window. Cached validator sets are verified against the predecessor header hash and may be reused only when that hash is unchanged. Commit and nil votes both count as signed, matching CometBFT; only absent votes count as unsigned. Nonempty blocks require compact HEIGHT.results.json canonical transaction outcomes. A mismatch stops execution; do not continue from a failed committed checkpoint without restoring a clean copy.

## Evidence obtained

On the reviewed reliability base at commit 6f9375335fefda08c3b66de64ef21c35030e1fe3, the existing mainnet snapshot restored with the exact trusted AppHash. All 569 contiguous blocks 34271001 through 34271569 matched canonical AppHashes. They include one real MsgSetOnline transaction and eight Ethereum transactions. Canonical transaction codes are checked, and the extended runner also verifies LastResultsHash (code, data and gas) against the next header. Contract implementation, owner and reward override were inspected on the isolated state.

This is a partial replay, not proof of the recovery at 34342701, full historical compatibility, EVM transaction coverage or activated reliability behavior. Rebuild and repeat the validation for the exact release candidate. Do not label a later revision verified merely because these commands passed on this base.


Long runs append verified records to replay-history.jsonl; the atomic JSON checkpoint contains only the current bounded window. Existing cumulative JSON history is seeded once, preserving all previous verified heights. The history stream is synced and an explicit window marker published at successful window completion. The runner checks database height and AppHash against the preceding checkpoint before resume. A crash between database commit and checkpoint publication requires investigation/rollback; it never silently skips unverified state.
