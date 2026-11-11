# Disposable two-process Hermes lab

`hermes_lab.py` is an opt-in operator interoperability harness. It requires a separate Linux network namespace with only the loopback interface and no routes. It generates its own fixture homes, consensus/P2P keys and relayer mnemonics under a fresh temporary directory. Child `HOME` and Hermes keystore are redirected there. Only child PIDs created by the harness are stopped. No existing home, keyring, public endpoint or peer can be supplied.

The official Linux Hermes v1.13.3 binary is pinned by SHA256 `ed8d57781f57a93e74259c12c8feee661b9633c882307f3a6236e41dbf5ffe35`. The verified release archive SHA256 is `3582136824ac1984c8884b310c63940a1489f091fa45faca38ed80e5dfe7d51b`. The binary reports `1.13.2+bab3b80`; v1.13.3 fixed Linux ARM packaging. Node binaries require explicit matching SHA256 and full source commit labels. The labels must come from the verified build job; they are provenance assertions, not proofs reconstructed from an executable.

Run inside an already isolated namespace, for example:

```sh
unshare --net -- bash -c 'ip link set lo up; exec python3 network/internal/ibclab/hermes_lab.py \
  --mode reference-probe \
  --reference-binary /absolute/path/ibcreference \
  --reference-sha256 VERIFIED_REFERENCE_BINARY_SHA256 \
  --reference-source-commit VERIFIED_FULL_SOURCE_COMMIT \
  --hermes-binary /absolute/path/hermes \
  --report /absolute/path/new-report.json'
```

The `reference-probe` mode starts the independent SDK node, requires the exact `ibc-reference-1` domain and advancing height, queries genuine bank/escrow state through local ABCI, imports only its ephemeral mnemonic and runs the real Hermes health check. It never claims the two-process release gate. Reports are created with exclusive-create semantics and contain public addresses and receipts, never mnemonic material.

The `full` mode additionally requires `--mcnd-binary`, `--mcnd-sha256` and `--mcnd-source-commit`. Its intended actual phases are client/connection/channel handshake, native DEL send, voucher return, both-node restart with a pending packet, error-acknowledgement refund and timeout refund. It queries actual channel and packet commitments, escrow and voucher balances. For each phase, committed transaction fee events and charged gas must reconcile exactly with both relayer accounts' native balances. No keeper or proof-verification stub is used. The mcnd init/genesis/key/start command contract was exercised by the actual process suite recorded below.

The native mcnd relayer key uses Cosmos `secp256k1` and an explicit coin-60 derivation path, mirrored by Hermes. It is not an Ethereum transaction signer. The independent reference uses the Cosmos prefix and coin-118 path. The installable daemon's identity and native Decimal economic parity are separate requirements.

## Actual SDK 0.55 probe result

On November 2, 2026, a verified Go 1.26.10 Linux build of reference commit `e9f926ad0146263cb9fcff6ea978e1750ab5d932` (binary SHA256 `fb1cc49af22258359022deb84aa17160a7eb88a3b67ae12ed904345292be7b8f`) advanced to height 3 and passed real bank/escrow queries. The official Hermes binary rejected compatibility: SDK 0.55.0 is outside `>=0.46, <0.54`, and Comet 0.40 was not recognized, falling back to compatibility mode 0.37. The corrected probe exits nonzero and leaves `release_gate=false`.

Hermes `health-check` can exit zero and print a final `status=success` even while per-chain WARN records say that health failed. The harness treats WARN/ERROR records as a failing health gate. `test_lab_guards.py` locks this behavior and verifies refusal of the initial network namespace. A success summary alone is not accepted as evidence.

The [official latest release](https://github.com/informalsystems/hermes/releases/tag/v1.13.3) remains v1.13.3. Its [immutable master compatibility source](https://github.com/informalsystems/hermes/blob/5e78a424904ff669a787a6c0eda53353c8096c11/crates/relayer/src/chain/cosmos/compatibility.rs) also restricts ibc-go to `>=6.3.1, <11`. Both [cosmos/relayer](https://github.com/cosmos/relayer) and [cosmos/ibc-relayer](https://github.com/cosmos/ibc-relayer) are archived. These observations do not establish an operational maintained SDK55/IBC11 relayer path.

The compatible-family trial uses a separately preserved SDK 0.53 / Comet 0.38 / IBC10 reference/application branch, with component evidence below. Version checks will not be widened to conceal an unsupported family. The SDK55 branch has no passing operator compatibility gate. The maintained53 two-process evidence is recorded below.

## Maintained-family execution evidence

On November 2, 2026, the SDK0.53.8 / Comet0.38.26 / IBC10.7 candidate passed the genuine signed native fee test (0.16 seconds) and a separate production-tag node process reached height3 (60.05 seconds including the cold binary link). Source `c911426c280024f7462e6a5946d5cf258717a4e2`, reference binary SHA256 `29f9d9188439750a7eae2e3e8574dc32ad2c4e4693cf990af7b037d28f70aa72`. The real official Hermes health check then passed without WARN/ERROR records, after configuring `max_block_time=30s` to match the chain's actual IBC connection parameter. Real bank and zero-escrow queries and ephemeral relayer-key import also passed. Full module verification passed after these component checks.

The reference probe alone establishes the independent counterparty/operator component; the subsequent full process suite is recorded below. The planned restart phase reopens both databases using the same binaries. It is not proof of an SDK major-version upgrade, module migration or IBC client-upgrade workflow.


## Actual two-process ICS20 result

On November 2, 2026, the full production-binary suite passed against owned `mcnd` source `21e1c205daaf7564d316a9e130f129d93c36cc83` (binary SHA256 `e2d72ed9b86bacf2adae903db428ae59ce80bec940841f410698cbabd2aa4612`) and the independent reference binary above. The verified official Hermes completed all six actual phases: client/connection/channel handshake, native DEL send, voucher return, both-node disk restart with a pending packet, error-ACK refund and timeout refund. Both actual transfer channels were `channel-0`.

The final run compares the exact pending packet sequence and commitment hash before/after restart. Error-ACK is decoded from the genuine committed `write_acknowledgement` event; timeout is confirmed by the genuine `timeout_packet` event. The timeout test reads the packet's actual emitted timestamp and waits until committed counterparty block time exceeds it, including Hermes safety margins. It does not infer expiration from a guessed sleep.

Every phase reconciles both relayer accounts' native balances exactly against principal movements and committed native fee events, with positive charged gas. Real voucher/escrow state and packet commitment clearing are checked. `MAINTAINED53_EVIDENCE.json` records the source report hash, binary provenance and per-phase receipt counts/fee totals without fixture keys or genesis assets. The source report remains a local artifact; generated fixture homes and all child processes were removed/stopped by the harness.

This passes the current application's isolated ICS20 process-interoperability component. It does not prove native Decimal validator/coin/NFT/fee parity, system-contract bootstrap, SDK major-version migration or an IBC client upgrade. The lab validator/relayer uses native Cosmos secp256k1; it is not a production template for legacy Ethereum contract ownership roles. Repeat the process gate when native consensus/financial wiring or the daemon runtime changes. No public network transaction was broadcast.


## Isolated IBC client upgrade rehearsal

Dispatch `Modern network validation` with `scope=process` and `ibc_client_upgrade=true`. The optional gate starts a fresh chain pair, sends a packet, and schedules a same-family upgrade through a signed governance proposal and vote. It checks the old daemon's upgrade hint and the two committed proofs, runs pinned Hermes against the original client, starts the rehearsal binary on the same disposable home, and verifies the packet acknowledgement, voucher return, refund and actual fees.

The rehearsal binary uses one overlay that registers a handler changing the staking unbonding period. Its hash, overlay hash and matching dependency graph are recorded in the CI evidence. The default build carries no rehearsal handler. This gate covers binary replacement within the pinned SDK/IBC family. A major version migration requires its own binaries, store migrations and replay evidence.

Protocol and CLI references are pinned to [IBC-Go v10.7.0 client proof verification](https://github.com/cosmos/ibc-go/blob/v10.7.0/modules/light-clients/07-tendermint/upgrade.go), [SDK x/upgrade v0.2.0 boundary handling](https://github.com/cosmos/cosmos-sdk/blob/x/upgrade/v0.2.0/x/upgrade/abci.go), and [Hermes v1.13.3 client upgrade](https://github.com/informalsystems/hermes/blob/v1.13.3/guide/src/templates/help_templates/upgrade/client.md).
