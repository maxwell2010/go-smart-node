# Node integration branch

Branch: integration/reliability-ibc. Upstream baseline: a46cb48d7965c1a64a495cebb22e735dfe4b9da3 (mainnet recovery height 34342701).

## Included groups

1. Upgrade replay uses committed state instead of a local completion journal; diagnostic writes use file replacement.
2. Activation-gated auto-unbond batches retain retry state and roll back partial enqueue writes.
3. Activation-gated financial-event errors propagate after legacy normalization, preserving upstream NFT exit pairing and legacy conversion.
4. Actual EVM subprocess crash, partial-unbond and event-rollback tests, plus Linux CI.
5. Isolated ICS-20 proof/escrow/refund baseline using two SDK applications. See integration/ibclab/README.md for its explicit limits.

The reliability rules activate at height 1 only for exact isolated IDs decimal_900001-1 and decimal_900002-1. Production IDs remain absent. Experimental ICS-20 requires a separate tagged build that refuses production genesis IDs. The journal/replay correction is unconditional and still requires historical replay validation before any mainnet use.

## Historical replay input still required

Need a trusted genesis, executable/build identifiers for the historical upgrade boundaries, a local snapshot before the selected interval, and the corresponding block history including commits. A latest-state snapshot alone cannot reproduce earlier migrations. Record the expected canonical AppHash for every checked boundary from an independent source. First target: a window spanning height 34342701; expand to earlier recovery/denomination boundaries only after the first window matches.

Run baseline and candidate against separate disposable copies. Never share a writable database, validator signing state, production keyring or service ports. Compare height, AppHash, delegation/hold records, frozen queues, reward override and contract implementation/owner slots. A block header's AppHash describes the preceding committed application state; compare at the correct offset. RPC reads or a successful sync alone are not historical replay evidence.

Current EVM crash tests prove recovery of a synthetic pending upgrade on a real database; they do not prove historical mainnet replay. No such replay has been completed without the necessary state/history inputs.

## Next implementation gates

- Resolve replay data source and verify integrity, checkpoint heights and available history.
- Run the current failure regressions and reference IBC lab on Linux.
- Select a maintained, mutually compatible SDK/consensus/IBC/EVM family before a production IBC implementation.
- Wire transfer store, module account, capabilities, keeper, router, genesis/services and lifecycle into an isolated DSC application.
- Define exact-chain isolation and an all-message ante policy; do not enable new routes by node-local flags on mainnet.
- Test Decimal historical validator sets, native DEL/EVM conservation and exclusions for vouchers/custom coins/NFT reserves.
- Replace the reference-chain milestone with two DSC-capable chains and an external relayer; test invalid proofs, expired clients, restart recovery and upgrades with packets in flight.

Other audit findings remain tracked in reliability-validation.md and dependency-modernization.md. This branch does not claim to fix every finding, implement production IBC, or certify a release.
