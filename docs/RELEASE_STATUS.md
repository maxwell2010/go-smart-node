# Production release gates

Status: NOT READY for a stable production IBC release. No stable tag or release should be published while a required gate below is outstanding.

## Separate release tracks

The selected architecture is a fresh network on a maintained modern stack,
with historical Decimal mainnet replay retained as a separate legacy build.
See [the architecture decision](modern-network-architecture.md). The independent
Go module under `network/` does not change the root module's dependencies or
claim compatibility with Decimal consensus. The older experimental IBC build
is an integration reference, not the new-network release candidate.

The owned modern ABCI application already passes genuine signed EVM transfer,
exact gas accounting, precompile rollback and direct Ethereum signing-domain
tests. An IBC11 unit harness checks native send/return, error acknowledgement,
timeout refund and membership-proof rejection using the actual application.
Its two in-process instances require the upstream `test` configuration reset;
this does not satisfy the separate production-process release gate. A disposable
independent SDK reference process has reached consensus height3 in a local lab.

The current tagged EVM reference pins SDK0.54/Comet0.39, while the published
active family lists SDK0.55/Comet0.40. An active-family compile and behavior
trial is in progress; matching EVM/IBC major versions alone is insufficient
support evidence. Transport remediations and scanner evidence are documented
under `network/docs/`; remaining advisories require individual disposition.

Modern release blockers remain:

- Complete native Decimal coin, NFT, validator, rewards and fee integration.
  Formula/schema/storage staging and temporary generic SDK staking do not
  establish those financial semantics.
- Supported dependency-family verification and security review of the actual
  final daemon, including the replacement Geth fork's upstream inheritance.
- Authenticated system-contract bootstrap for a new genesis, with bank/EVM
  supply and reserve reconciliation and failure rollback.
- Guarded production CLI, clean install and restart; separate OS processes,
  maintained Hermes relayer and an independent counterparty, including refunds,
  restart and upgrade with packets pending.
- Validator-set and consensus-parameter transitions, full event reconciliation,
  bounded auto-unbond work, operator authority and asset policy, independent
  review and a tested rollback procedure.

## Legacy reference evidence and limitations

The following evidence concerns the root legacy build and its experimental
IBC reference. A missing legacy ContractCenter in fresh genesis is a blocker
for that experimental bootstrap, not proof that the owned modern EVM transfer
fails. Historical replay remains necessary evidence for the legacy reliability
patches; a fresh modern network requires its own genesis and financial tests.

Verified: local default go test ./... completed successfully with the legacy public-devnet diagnostic explicitly opt-in. Linux validation and both candidate artifact jobs passed for commit be3cf30, including explicit whole-module verification, two independent DSC OS processes and source-built official rly v2.6.0, signed native DEL send/return, exact native credit to a distinct return recipient, bounded positive gas and actual native fee debit. Candidate archives include checksums, build provenance and a CycloneDX SBOM validated against pinned official schemas. These are development artifacts, not a production release.

A verified pre-upgrade mainnet snapshot was restored at height34271000 using exact runtime 8f1c8d4, independently downloaded Go1.24.4 and a fresh Linux module cache. Whole-module verification passed before and after helper builds. The first 7,070 blocks through34278070 passed every AppHash, LastResultsHash and canonical transaction-code check, including 60 EVM transactions and one native transaction. Actual inputs include delegation, held delegation and withdrawal calls. Continuation is in progress; the recovery height itself is not yet covered. The replay harness maps NIL votes exactly as Tendermint does. Progress beyond this documented checkpoint requires local replay evidence rather than inference from a running process. This is not yet proof for a different compiler or dependency graph.

Windows provenance limitation: whole-module verification failed for the shared cache. Comparison against official archives found missing documentation and a non-compilation zip-bomb fixture, with no detected Go source differences. That cache was preserved. Clean Linux verification and execution are the preferred evidence; do not describe the Windows whole-module gate as passed.

Production security evidence requires a completed scanner capture bound to its actual exit status, output bytes and SHA256. A JSON scan can return zero despite findings; a syntactically complete prefix can also look clean if a file was cut between records. The capture manifest detects such alteration of an honestly completed capture, but is unsigned and does not authenticate a forged scanner or manifest. See docs/dependency-release-blockers.md.

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

The legacy experimental build is restricted to exact dedicated IDs. The modern
application rejects Decimal chain IDs and requires matched new Cosmos/EVM
signing domains. Publishing a fork does not activate a production network.
This release gate document does not claim an audit certification.
