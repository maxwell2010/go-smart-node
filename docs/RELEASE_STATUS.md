# Production release gates

Status: NOT READY for a stable production IBC release. No stable tag or release should be published while a required gate below is outstanding.

Verified: local default go test ./... completed successfully with the legacy public-devnet diagnostic explicitly opt-in. Linux validation and both candidate artifact jobs passed for commit 04bc3ef, including two independent DSC OS processes and source-built official rly v2.6.0, signed native DEL send/return, bounded positive gas and actual native fee debit. Candidate archives include checksums, build provenance and a CycloneDX SBOM validated against pinned official schemas. These are development artifacts, not a production release.

A verified pre-upgrade mainnet snapshot was restored at height34271000. Replay has passed height34275069 with canonical AppHash agreement, including native and actual EVM transactions; continuation is in progress and the recovery height itself is not yet covered. The replay harness now maps NIL votes exactly as Tendermint does and checks canonical transaction outcomes, validator hashes and result hashes. Progress beyond this documented checkpoint must be supported by local replay evidence rather than inferred from a running process.

Grouped reliability changes, actual EVM failures, two DSC application instances with signed messages and native fee debit, DEL/bank/EVM balance equivalence in tested flows, voucher return and refunds, LevelDB restart with a packet pending, proof rejection, client expiry, and a current-version module migration with a packet pending are tested. The last test is not a major-version upgrade rehearsal.

Required remaining evidence:

- Historical mainnet replay spanning recovery height34342701 and relevant earlier boundaries, with canonical AppHash agreement and actual deployed-contract state checks.
- Maintained dependency/toolchain family or reviewed security remediation. A broad source-symbol scan has findings; they require CLI reachability and platform triage, not an assertion that every advisory is exploitable here.
- Relayer restart and major-version upgrade-in-flight operation. The tested rly configuration needs an explicitly supplied unbonding period and zero declared gas price while Decimal still deducts its computed native commission; document and test an operator-compatible production policy.
- Real fresh-network system-contract bootstrap. A signed Ethereum transfer with the real outer ante and no mock contracts currently pays a fee and rolls back because ContractCenter is absent. The opt-in DSC_REQUIRE_FRESH_EVM_BOOTSTRAP=1 test intentionally fails until this is resolved; ordinary green tests do not satisfy this gate. See integration/ibclab/SYSTEM_CONTRACT_BOOTSTRAP.md.
- Rehearsal with deployed production contract implementations, complete outer ante/fee failure behavior and reconciliation of existing partial queues.
- Bounded auto-unbond progress and poison-entry policy; conservation invariants; emergency authority and quota/asset policy.
- Historical slashing/grace-period behavior before height29512330: legacy code still references UpdatesInfo there, while later heights have an unconditional grace override. Do not claim full-genesis replay or corrected slashing policy from the current newer snapshot window alone.
- Clean install/bootstrap, independent review and tested rollback procedure. Linux candidate packaging has passed; production artifact publication remains blocked by the other gates.

The experimental build is restricted to exact dedicated IDs. Production consensus activation requires a separate coordinated plan; publishing a fork is not that plan. This release gate document does not claim an audit certification.
