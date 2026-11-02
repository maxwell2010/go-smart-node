# Production release gates

Status: NOT READY for a stable production IBC release. No stable tag or release should be published while a required gate below is outstanding.

Verified: local default go test ./... completed successfully with the legacy public-devnet diagnostic explicitly opt-in. A verified pre-upgrade mainnet snapshot was restored and 69 contiguous blocks replayed with canonical AppHash agreement, including one real MsgSetOnline; the recovery height itself is not yet covered. Grouped reliability changes, actual EVM failures, two DSC application instances with signed messages and native fee debit, DEL/bank/EVM balance equivalence in tested flows, voucher return and refunds, LevelDB restart with a packet pending, proof rejection, client expiry, and a current-version module migration with a packet pending. The last test is not a major-version upgrade rehearsal.

Required remaining evidence:

- Historical mainnet replay spanning recovery height34342701 and relevant earlier boundaries, with canonical AppHash agreement and actual deployed-contract state checks.
- Maintained dependency/toolchain family or reviewed security remediation. A broad source-symbol scan has findings; they require CLI reachability and platform triage, not an assertion that every advisory is exploitable here.
- Two independent OS processes, an external operator relayer, fee/gas compatibility, relayer restart and upgrade-in-flight operation.
- Rehearsal with deployed production contract implementations, complete outer ante/fee failure behavior and reconciliation of existing partial queues.
- Bounded auto-unbond progress and poison-entry policy; conservation invariants; emergency authority and quota/asset policy.
- Historical slashing/grace-period behavior before height29512330: legacy code still references UpdatesInfo there, while later heights have an unconditional grace override. Do not claim full-genesis replay or corrected slashing policy from the current newer snapshot window alone.
- Linux release archives/checksums/provenance, clean install/bootstrap, independent review and tested rollback procedure.

The experimental build is restricted to exact dedicated IDs. Production consensus activation requires a separate coordinated plan; publishing a fork is not that plan. This release gate document does not claim an audit certification.
