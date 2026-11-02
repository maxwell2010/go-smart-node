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
value rollback, with real execution fees still debited. This is controlled
receipt fault injection, not a claim that production contracts naturally emit
malformed events. Auto-unbond fault rehearsal on migrated real contracts is
still outstanding.

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
