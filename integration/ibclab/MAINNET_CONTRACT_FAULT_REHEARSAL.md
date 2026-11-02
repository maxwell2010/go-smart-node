# Snapshot-derived system-contract fault rehearsal

`TestMainnetSystemContractFaultRehearsal` is an opt-in, default-build test. It
does not start a node process, connect to an RPC endpoint or broadcast anything.
It copies only `data/application.db` and public genesis from an explicitly
CLOSED snapshot-derived home into a fresh `t.TempDir`. It never copies node
keys or opens the source database. Do not provide a working node home.

The test verifies the copied application's committed hash and height against
the supplied next-block header, starts that local block, and creates/funds an
ephemeral account only in the copied state. It signs EIP-155 transactions and
submits their canonical SDK envelopes through the real outer ante and fee path.
No changes are committed back to the source or exported as a live chain state.

```powershell
$env:DSC_CLOSED_MAINNET_REHEARSAL_HOME='D:/closed-snapshot-home'
$env:DSC_MAINNET_REHEARSAL_NEXT_BLOCK='D:/canonical-next-block.block.json'
go test -mod=readonly -run '^TestMainnetSystemContractFaultRehearsal$' -v -timeout 300s ./app
```

The normal test skips when inputs are missing. A pre-recovery snapshot records
the activation incompatibility described below. To require the complete actual
post-recovery contract fault scenario, also set:

```powershell
$env:DSC_REQUIRE_MAINNET_CONTRACT_FAULT_REHEARSAL='1'
```

This strict gate fails if the copied chain has not actually committed the
legacy-delegation recovery migration. The test does not set that migration flag.

## Evidence and limits

An exploratory execution against restored height 34271000, whose AppHash matches
the canonical header 34271001, passed a genuine signed native DEL EVM transfer
through the real registry and production hook chain. Activating strict
reliability handling on this pre-recovery state rejects a genuine signed
`delegateDEL` transaction: its actual `StakeUpdated` topic
`0x71a822c8b2dd1c5369373dd93ec6a6b04cf7d41eb154314433c73f4f8856c03b`
is unknown until legacy-event recovery is enabled by the real migration.
This confirms that reliability activation cannot be scheduled independently of
the contract-event compatibility state.

That exploratory run used a snapshot restore built before a shared module-cache
integrity problem was detected. It establishes an investigation target, not
final clean dependency provenance. Repeat the test with independently verified
dependencies and a clean restore before accepting release evidence.

The post-recovery branch of the test is prepared but remains unvalidated until
a real migrated snapshot is available. It must first execute the actual
delegation contract successfully and decode its genuine emitted event. It then
corrupts that emitted receipt's event data through a test-only hook wrapper,
while retaining the actual multi-module production hooks and deployed code.
Assertions cover EVM stake storage, native delegation state and transferred
value rollback. Fault injection targets the exact legacy `StakeUpdated` topic
and asserts that the corrupted topic matches. The failed transaction's sender
debit must equal exactly the signed legacy gas price multiplied by the EVM
response's charged `GasUsed`, with no extra stake-value loss. The native transfer
baseline likewise requires exact receiver credit and sender debit equal to
value plus this gas fee. This legacy EVM path deducts gas-limit fees and refunds
unused gas; the separate native-message commission schedule is not an additional
charge. This is controlled
receipt fault injection, not a claim that production contracts naturally emit
malformed events. This post-recovery event-fault branch remains outstanding.

## Actual deployed auto-unbond exploration

Two additional opt-in settings select a separate scenario:

```powershell
$env:DSC_MAINNET_AUTO_UNBOND_PROBE='1'
$env:DSC_MAINNET_AUTO_UNBOND_BATCH='1'
```

Do not combine this mode with `DSC_REQUIRE_MAINNET_CONTRACT_FAULT_REHEARSAL`:
the two scenarios have different prerequisites and release claims.

Exploratory execution on the copied pre-recovery snapshot confirmed a genuine
queue length of 16952. The real deployed contract accepts `autoUnbondEnqueue`
only from the system zero address; a signed generated EOA transaction reverts
with decoded reason `Not authorized`. A zero-sender write in a discarded cache
increments the actual queue and leaves its parent unchanged.

The controlled batch scenario creates a genuine `delegateDEL` stake through a
signed Ethereum transaction under the snapshot's historical hook behavior.
It decodes the real emitted stake and checks its token against native metadata.
Only in a discarded child context, it isolates that validator's native batch
index and mirrors this genuine stake, adding a deliberately invalid second
native coin record. It also isolates offline tracking and sets a local timeout.
These are declared native test fixtures, not unchanged production state.

The test proves that the first real contract enqueue writes, the second native
coin lookup fails, the complete queue batch rolls back and the retry marker
remains. Removing the corrupt native test record permits a successful retry.
The resulting real queue entry must match validator, delegator, amount, token
and hold timestamp of the genuine stake. Another retry does not duplicate it.
The outer parent queue remains unchanged when the controlled cache is discarded.
No contract runtime, registry mapping or ownership is replaced.

This scenario passed during exploratory Windows execution, with the same
dependency-provenance caveat above. It must be repeated with the verified Linux
toolchain and clean restored state before it becomes final release evidence.

### Clean Linux repetition

Both pre-recovery scenarios were repeated successfully on November 2, 2026
using source commit `719aa70` and the CLOSED clean-restored snapshot at height
34271000, with the committed AppHash checked against header 34271001. Go 1.24.4,
CGO/GCC, `GOMAXPROCS=1`, `-p 1` and `GOMEMLIMIT=1GiB` were used. The entire
run occurred in an isolated network namespace with `GOPROXY=off`. Full
`go mod verify` passed before and after both tests. Native transfer/exact fee
and the genuine pre-recovery event rollback passed; the deployed auto-unbond
authorization, partial queue rollback and repaired exact-once retry passed.
This removes the earlier exploratory dependency-provenance limitation for
these two scenarios. It does not validate the still-pending post-recovery
receipt-corruption scenario or certify a separate modern application.

### Clean post-recovery repetition

Both scenarios passed on November 2, 2026 against the CLOSED clean replay state
at height 34342701, using test commit `6445e29`. The canonical next-block header
34342702 independently confirmed committed AppHash
`3E4FA6EDCA1C4EFE905F032E5891632D5918481B5E95AB9151A8F129507F4E9B`.
The official header response SHA256 was
`e33a9032552626ea76e2c7cc01a73357d4ed85c855006afc8d100949eb50b278`;
the public genesis SHA256 was
`e3fa0c3d311c6ffcf49135f49e440e69fbe20077139b4535d60e2e3a80f3a58b`.

The original v7-only fixture correctly stopped when the genuine post-recovery
runtime emitted the current eight-field `StakeUpdated` tuple. The corrected
test identifies only the two known current/v7 event IDs from the actual
receipt, decodes with that ABI and queries the actual `getStake` layout. It
does not replace bytecode, registry ownership or production hooks. The fault
wrapper corrupts the exact observed `StakeUpdated` topic's data, rather than
an arbitrary log. The final formatted test source SHA256 was
`49c42e9fa9afbbc33c00a489b84c633c2a8a8c6824cf83e7ddf662cac2b6766c`.

With the strict post-recovery gate required, the signed native transfer passed
signature, outer ante, exact fee and production hook checks. A genuine
`delegateDEL` transaction created matching contract/native delegation state.
The injected event decode error then rolled back real contract stake storage,
native delegation state, contract value transfer and response logs. The sender
lost exactly the charged gas fee, `1587300000001500000` raw DEL, with no extra
stake-value loss. This gate passed in 90.90 seconds. The actual delegation
address was `0xA16c34Ed1c0601c0E749e17ebeF19752A15FaA01`.

The separate controlled auto-unbond scenario observed the actual queue length
17481. System authorization and signed EOA rejection passed. The first genuine
enqueue wrote before the second controlled native-record failure; the whole
queue batch rolled back and its retry tracker remained. Removing that corrupt
record allowed exactly one matching real queue entry, removed the tracker and
prevented a duplicate on another retry. The parent queue stayed unchanged.
This scenario passed in 82.01 seconds.

The entire run used Go 1.24.4 Linux CGO/GCC, `GOMAXPROCS=1`, `-p 1`,
`GOMEMLIMIT=1GiB`, `GOPROXY=off` and a private network namespace. Full
`go mod verify` passed before and after. Each test copied only the CLOSED
application database and public genesis into its own temporary child directory
on a separate volume; the two clones ran sequentially and were cleaned up.
Neither the source nor working replay database was opened. No transaction was
broadcast and no generated signing key was printed. Evidence log:
`clean-go124-postH-contract-rehearsal-v2.log` (finished 09:46:43 UTC).

This closes the stated post-recovery legacy contract fault gate. It does not
certify the separate modern network, native financial port or external-relayer
interoperability, and it does not activate these fork fixes on public mainnet.

## Dependency diagnostic correction is consensus-visible

The pinned Decimal Ethermint dependency unconditionally overwrites
`MsgEthereumTxResponse.PostTxError` with `test`. Correcting that field changes
the serialized transaction response data. For a mainnet replay, such response
changes can change result commitments including `LastResultsHash`, even when
the application state and AppHash do not change. Do not blindly replace the
dependency or rewrite responses for historical blocks. A correction requires a
coordinated consensus activation, explicit legacy response behavior before the
activation boundary, and historical replay plus boundary tests. This rehearsal
does not modify the dependency; it captures the actual hook error independently.
