# Production release gates

Status: NOT READY for a stable production IBC release. No stable tag or release should be published while a required gate below is outstanding.

## Separate release tracks

The selected architecture is a fresh network on a maintained modern stack,
with historical Decimal mainnet replay retained as a separate legacy build.
See [the architecture decision](modern-network-architecture.md). The independent
Go module under `network/` does not change the root module's dependencies or
claim compatibility with Decimal consensus. The older experimental IBC build
is an integration reference, not the new-network release candidate.

The candidate now selects the active 2025.1 family: SDK 0.53.8,
CometBFT 0.38.26, Cosmos EVM 0.6.3 and IBC Go 10.7.0. The newer
SDK55 trial is preserved separately; the official Hermes compatibility gate
selected this maintained family. Historical SDK54 security evidence does not
certify this selected graph. See `network/family-maintained53.json`.

The default-build owned ABCI application passes genuine signed EVM transfer,
exact gas accounting, financial rollback and signing-domain tests. Its owned
EVM source correction decodes the validator operator's Bech32 address for the
actual COINBASE opcode; the canonical source failed that execution regression.
Owned source manifests are verified separately because Go module verification
skips local replacements. The selected default daemon now uses canonical Pion
STUN/DTLS v3; the earlier owned STUN v2 backport is historical evidence, not its
current transport selection. Owned SDK Armor and geth patches have separate
source manifests and scoped scan evidence. None of these corrections closes
the final daemon's complete security review.

Official Hermes and an independent SDK53 counterparty passed six phases in
separate default-build OS processes: handshake, native DEL send, voucher return,
both-node disk restart with the exact pending packet commitment, decoded error
acknowledgement refund and an actual timeout refund. All 26 committed relayer
transactions reconcile gas fees, balances, escrow, vouchers and commitments.
This evidence binds source21e1c20, not every later development binary. No
major-version or client-upgrade-in-flight gate is implied. The guarded CLI also
passed operator signing, shared native/EVM nonce, exact fee and restart checks
on its separately recorded source. See
[the scoped process evidence](../network/internal/ibclab/MAINTAINED53_EVIDENCE.json).

The selected governance policy gives native aggregate validator power its
voting weight and divides delegator overrides by native ownership, including
NFT reserve and hold-containing stakes without a hold bonus or double count.
Actual SDK53 governance tally tests pass. Native state and read adapters remain
staging components: long-lived authority wiring, custom-asset valuation,
financial transitions and full application integration are required. The
system-contract source/compiler recipe and registry tests are also staging;
they do not establish a fully reconciled fresh genesis contract set.

The isolated ERC1155 origin/vault contracts now pass ordinary signed owned-App
tests: penalizing A preserves A=9 and B=10 DEL in both withdrawal orders, with
exact native bank-supply destruction and late-error rollback. WDEL controls also
reconcile wrapper supply, native backing and final unwrap. These foundation
contracts do not yet certify the shared native financial receipt controller.
Persisted receipt lineage and domain-scoped source-position bindings are merged;
the restricted financial genesis admits explicitly attested pending claims and
can enable the direct DEL message routes described below.

The separate DEL lifecycle now has signed EVM delegation, top-up, partial and
full withdrawals and a common manual/automatic `complete` path. Native REST
pair queries reconcile the active and queued records at committed heights.
The [Linux process check](https://github.com/maxwell2010/go-smart-node/actions/runs/43031167097)
covered 3 + 2 DEL, payout of 1 DEL, rejection of a repeat and payout of the
remaining 4 DEL after restart. It also checked a previously admitted 1024-unit
isolated NFT withdrawal under the 24-million SDK gas ceiling and repeated the
six Hermes phases.

The [isolated snapshot check](https://github.com/maxwell2010/go-smart-node/actions/runs/43033802795)
restored the active 4 DEL position at snapshot height 1914 and replayed the
withdrawal through the same final application hash. Its two RPC URLs were
aliases of the source process. A separate replayed witness on the same runner
is being tested independently of the source database. Production trust
still requires independently operated RPCs and a trusted block header.

Held DEL uses a separate genesis option, verified expiry and an exact set of
native lots, including the original contribution time for each top-up.
The [Linux module run](https://github.com/maxwell2010/go-smart-node/actions/runs/43033478597)
tested signed held deposits and partial withdrawals in the application EVM.
See the [operator description](../network/docs/ru/STAKE_WITHDRAWAL_SETTLEMENT.md)
for the profile, supported calls and query semantics.

The reviewed development commit `8cf619da1c77ee5efffc80a678272010fd2263dd`
passes all four Linux workflows, including its actual default daemon archive,
source-input inventory and binary build/module metadata checks. It is a
development snapshot, not the completed native-financial/IBC release. Ordinary
CLI regressions verify that information commands leave fresh/existing fixture
homes untouched and that the registered `snapshots` commands require this
network's signing domain.

Modern release blockers remain:

- Complete native Decimal coin, NFT, validator, rewards and fee integration.
  Formula/schema/storage staging and temporary generic SDK staking do not
  establish those financial semantics.
- Supported dependency-family verification and security review of the actual
  final daemon, including the replacement Geth fork's upstream inheritance.
- Authenticated system-contract bootstrap for a new genesis, with bank/EVM
  supply and reserve reconciliation and failure rollback.
- Repeat the established guarded CLI and independent Hermes process gates on
  the final integrated daemon, test clean installation, and exercise actual
  client/module upgrade with packets pending. Same-binary restart has passed.
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

Legacy mainnet reference replay completed 71,701 contiguous blocks
(34271001-34342701) and 862 canonical transactions on clean Linux Go 1.24.4 and
Go 1.26.10. State/result hashes and transaction codes matched, including an
independent official terminal header. Explicit preceding-plan registration
bridges were required before recovery, with the original binaries selected at
the recovery height. Whole-module verification passed before and after builds.
This is legacy state/transaction-result evidence, not complete ABCI-output or
modern-network certification. See the
[exact replay provenance and limitations](../integration/replay/MAINNET_RECOVERY_EVIDENCE.md).

The strict post-upgrade actual-contract test also passed on a disposable copy
of the closed restored state. A genuine delegation event failure rolled back
native delegation, contract state and transaction value while retaining the
exact gas fee. The actual auto-unbond test passed partial failure, retained
retry state, repaired input and exactly-once queue append on retry. Full module
verification passed before and after these Linux tests. See
[the contract rehearsal evidence](../integration/ibclab/MAINNET_CONTRACT_FAULT_REHEARSAL.md).

Windows provenance limitation: whole-module verification failed for the shared cache. Comparison against official archives found missing documentation and a non-compilation zip-bomb fixture, with no detected Go source differences. That cache was preserved. Clean Linux verification and execution are the preferred evidence; do not describe the Windows whole-module gate as passed.

Production security evidence requires a completed scanner capture bound to its actual exit status, output bytes and SHA256. A JSON scan can return zero despite findings; a syntactically complete prefix can also look clean if a file was cut between records. The capture manifest detects such alteration of an honestly completed capture, but is unsigned and does not authenticate a forged scanner or manifest. See docs/dependency-release-blockers.md.

Grouped reliability changes, actual EVM failures, two DSC application instances with signed messages and native fee debit, DEL/bank/EVM balance equivalence in tested flows, voucher return and refunds, LevelDB restart with a packet pending, proof rejection, client expiry, and a current-version module migration with a packet pending are tested. The last test is not a major-version upgrade rehearsal.

Remaining limitations of the legacy and experimental reference builds:

- The completed snapshot window covers recovery height 34342701; it does not
  cover full genesis or unrelated earlier consensus boundaries.
- Maintained dependency/toolchain family or reviewed security remediation. A broad source-symbol scan has findings; they require CLI reachability and platform triage, not an assertion that every advisory is exploitable here.
- Relayer restart and major-version upgrade-in-flight operation. The tested rly configuration needs an explicitly supplied unbonding period and zero declared gas price while Decimal still deducts its computed native commission; document and test an operator-compatible production policy.
- Real fresh-network system-contract bootstrap. A signed Ethereum transfer with the real outer ante and no mock contracts currently pays a fee and rolls back because ContractCenter is absent. The opt-in DSC_REQUIRE_FRESH_EVM_BOOTSTRAP=1 test intentionally fails until this is resolved; ordinary green tests do not satisfy this gate. See integration/ibclab/SYSTEM_CONTRACT_BOOTSTRAP.md.
- Actual post-upgrade contract/fee failure and controlled partial auto-unbond
  retry passed. This does not certify arbitrary pre-existing corrupt queues or
  every deployed-contract execution path.
- Bounded auto-unbond progress and poison-entry policy; conservation invariants; emergency authority and quota/asset policy.
- Historical slashing/grace-period behavior before height29512330: legacy code still references UpdatesInfo there, while later heights have an unconditional grace override. Do not claim full-genesis replay or corrected slashing policy from the current newer snapshot window alone.
- Clean install/bootstrap, independent review and tested rollback procedure. Linux candidate packaging has passed; production artifact publication remains blocked by the other gates.

The legacy experimental build is restricted to exact dedicated IDs. The modern
application rejects Decimal chain IDs and requires matched new Cosmos/EVM
signing domains. Publishing a fork does not activate a production network.
This release gate document does not claim an audit certification.
