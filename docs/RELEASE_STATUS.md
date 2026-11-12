# Production release gates

Status: NOT READY for a stable production IBC release. No stable tag or release should be published while a required gate below is outstanding.

The historical duplicate-vote development branch verifies a 5% debit from
surviving pre-infraction DEL principal and isolated NFT reserves. Later
funding survives, including late tranches held in frozen claims. Signed EVM
tests cover rollback and one successful retry for active and frozen DEL and
NFT. The same Linux suite checks permanent exclusion of the old consensus
key and preservation of a pending job on rollback.
[Linux module and owned EVM tests](https://github.com/maxwell2010/go-smart-node/actions/runs/43192837081)

The fresh profile wires this work to finalized Comet metadata, fixed genesis
evidence age limits and the shared manual/automatic payout gate. The Linux ABCI
rehearsal started from a fresh height-one genesis and verified rollback before
commit, a durable 0.1 DEL burn on block 5, removal of the old key at block 7,
and a single 0.95 DEL payout on block 10. The rehearsal uses authenticated-shape
metadata supplied by the test harness. [Fresh-profile lifecycle](https://github.com/maxwell2010/go-smart-node/actions/runs/43196854310)

The governed replacement-key test rejects an excluded key from a new
operator, registers a new key in the native and MasterValidator records with
zero power and activates it at H+2 after a signed DEL delegation. It uses
signed SDK proposals and weighted votes in the fresh-profile ABCI test.
[Registration lifecycle](https://github.com/maxwell2010/go-smart-node/actions/runs/43198473506)

The combined module suite on `824e469` passed the historical penalty,
registration, cold EVM balance query, zero-reserve NFT exit and the
offline registration command. The archive and scanner run on `507e5f2`
passed clean installation and CycloneDX validation and reported zero
symbol advisories in its source and Linux amd64 binary scans. The scanner
database was dated 2026-11-11. The difference between these revisions is
in a test helper.
[Combined module suite](https://github.com/maxwell2010/go-smart-node/actions/runs/43199989617)
[Archive and scanner](https://github.com/maxwell2010/go-smart-node/actions/runs/43199545547)

Published operational data must include the approved genesis and independent
bootstrap and RPC endpoints for this separate network.

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

The modern build pins Go 1.26.11 and `klauspost/compress` v1.20.2 to address
GO-2026-6859 and GO-2026-6865. [The audit and clean archive installation](https://github.com/maxwell2010/go-smart-node/actions/runs/43041904175)
passed on `250faa059c5e01b46eeb10b6d0de2f9814d25691`. Both the source and binary
scans reported zero symbol advisories with that run's database snapshot.
The [isolated process run](https://github.com/maxwell2010/go-smart-node/actions/runs/43042136814)
also passed on the same updated dependency pins.

The refreshed source and Linux amd64 binary scans for
`496ee4ba52953e348ab82c8c2c01affb304c4177` completed with zero symbol
advisories in the scanner database dated 2026-11-11. This evidence covers
that exact daemon build and database snapshot.
[GitHub CI security audit](https://github.com/maxwell2010/go-smart-node/actions/runs/43176718744)

The Linux development archive on `e8e5dcf` includes a schema-validated
CycloneDX 1.5 SBOM bound to the source revision and binary hash. It records
the Go dependencies and toolchain, including the manifest and compiled
source hashes for the owned SDK, EVM and geth replacements. Both scanner
captures passed on that build.
[Archive and security audit](https://github.com/maxwell2010/go-smart-node/actions/runs/43177540530)


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
The [Linux process check](https://github.com/maxwell2010/go-smart-node/actions/runs/43040767203)
covered 3 + 2 DEL, payout of 1 DEL, rejection of a repeat and payout of the
remaining 4 DEL after restart. It also checked a previously admitted 1024-unit
isolated NFT withdrawal under the 24-million SDK gas ceiling and repeated the
six Hermes phases.

The [isolated snapshot check](https://github.com/maxwell2010/go-smart-node/actions/runs/43033802795)
restored the active 4 DEL position at snapshot height 1914 and replayed the
withdrawal through the same final application hash. Its two RPC URLs were
aliases of the source process. A separate replayed witness on the same runner
then confirmed snapshot height 1920, replay of the withdrawal and the same
AppHash on all three processes at height 1931. Production trust still requires
independently operated RPCs and a trusted block header. See the
[three-process evidence](../network/docs/ru/evidence/del-state-sync-c83a193-20261111/README.md).

Held DEL uses a separate genesis option, verified expiry and an exact set of
native lots, including the original contribution time for each top-up.
The [Linux module run](https://github.com/maxwell2010/go-smart-node/actions/runs/43033478597)
tested signed held deposits and partial withdrawals in the application EVM.
The [combined module run](https://github.com/maxwell2010/go-smart-node/actions/runs/43035085805)
also proved a beneficiary-signed reset at the exact expiry, preservation of
other hold lots and a subsequent ordinary DEL withdrawal and payout.
See the [operator description](../network/docs/ru/STAKE_WITHDRAWAL_SETTLEMENT.md)
for the profile, supported calls and query semantics.

A [signed owned-App test](https://github.com/maxwell2010/go-smart-node/actions/runs/43039725259)
started from a verified cold genesis, converted 1024 origin ERC1155 units into
one independently backed ERC721 receipt and returned it through `complete`.
The 2048 DEL reserve stayed with the receipt and the owner's direct 5 DEL stake
survived. Late failure, scheduled deferral and one successful signed retry were
verified. Peak gas was 18,171,007 for admission and 19,373,196 for completion.
This profile starts with approved native DEL origins. Wallet lifecycle routes,
new NFT holds, custom conversion and slashing still need integration tests.

An [application IBC upgrade test](https://github.com/maxwell2010/go-smart-node/actions/runs/43036744774)
executed a signed governance proposal, a scheduled SDK `x/upgrade` handler and
a signed `MsgUpgradeClient` verified against both IAVL proofs. A pending packet
was relayed and acknowledged on the existing channel, then the voucher was
returned for its original DEL principal. The test uses the current SDK/IBC
family in two application instances.

The [isolated daemon rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/43041499036)
scheduled the upgrade at height 157 with a packet pending. The old daemon
committed height 156, and Hermes upgraded the counterparty's Tendermint client
from two proofs at that committed height. The replacement ran from the same
home and replayed the boundary block with the same block hash. The existing
channel carried the pending packet and acknowledgement. Voucher return and
error acknowledgement refund reconciled principal and fees. This rehearsal
uses the same SDK/IBC module graph in both binaries and an isolated upgrade
handler. A major-version migration and the final release binary need their
own evidence.

The reviewed development commit `8cf619da1c77ee5efffc80a678272010fd2263dd`
passes all four Linux workflows, including its actual default daemon archive,
source-input inventory and binary build/module metadata checks. It is a
development snapshot, not the completed native-financial/IBC release. Ordinary
CLI regressions verify that information commands leave fresh/existing fixture
homes untouched and that the registered `snapshots` commands require this
network's signing domain.

The isolated native consensus profile passed on `7d7ef4e`. A signed DEL
delegation at height 35 replaced a validator at height 37, and three
processes agreed through a same-home restart and height 40. This run used
an empty SDK staking ledger and real Gateway-backed DEL positions.
[Process evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43154836869)

The native consensus profile has also passed the isolated fee-only reward,
hold downtime, jail recovery and active NFT downtime scenarios. The active
NFT path burned 0.02 DEL from the affected 2 DEL reserve, preserved receipt
custody and WDEL supply, and took 17,312,534 of the 24,000,000 SDK gas budget.
The combined module suite on `496ee4b` processed two frozen 1024-unit receipts
in 21,543,784 SDK gas, with one claim eligible and one ineligible. It checked
atomic rollback, retry and receipt return at zero reserve.
[Hold payout](https://github.com/maxwell2010/go-smart-node/actions/runs/43167145189)
[Signed jail recovery](https://github.com/maxwell2010/go-smart-node/actions/runs/43167339181)
[Active NFT downtime](https://github.com/maxwell2010/go-smart-node/actions/runs/43169900579)
[Combined module suite](https://github.com/maxwell2010/go-smart-node/actions/runs/43172175492)

On `496ee4b`, a rejected governance proposal left the upgrade state unchanged.
A second proposal passed at height 40 and scheduled an upgrade at 64. The
predecessor committed through 63, the successor applied the approved plan at
64, and three isolated nodes agreed on continued consensus through 67. Both
binaries use the same source and the plan is dedicated to this rehearsal.
[Two-binary governance rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/43172176025)

### Approved basis for duplicate-vote penalties

The selected penalty is 5% of slashable DEL exposure at the infraction.
Subsequent deposits do not fund this penalty. The planned ledger boundary
for infraction height H is the committed state immediately before H. Keep
the deposit heights when ordinary DEL and held DEL split into withdrawal
claims, and keep the original staking height and receipt lineage when an
isolated NFT becomes a claim. Both the native projection and EVM settlement
need to advance the corresponding penalty cursor when the eligible loss is
zero.

The withdrawal timer must be supplemented by the evidence window and any
accepted pending liability. CometBFT v0.38.26's nominal evidence expiry
requires both age thresholds to be exceeded, using the preceding committed
height and time. The application needs a deterministic cutoff using the
same clock for admission, payout, compaction and replay. These rules apply
to the common manual and automatic payout path.
[CometBFT evidence age check](https://github.com/cometbft/cometbft/blob/v0.38.26/evidence/verify.go#L309-L318)

The Linux module and fresh ABCI suites cover a paused validator, a late
top-up, partial withdrawal, restart and exact burns from eligible principal.
The owned EVM workload test signed NFT admission and DEL top-ups to exercise
1024 origin units and 127 DEL tranches. Settlement used 22,817,925 of the
24,000,000 SDK gas budget and burned 108.7 DEL while retaining two tranches
funded at and after the infraction height.
[Historical penalty workload](https://github.com/maxwell2010/go-smart-node/actions/runs/43199989617)

Modern release blockers remain:

- Complete native Decimal coin, NFT, validator, rewards and fee integration.
  Formula/schema/storage staging and temporary generic SDK staking do not
  establish those financial semantics.
- Supported dependency-family verification and security review of the actual
  final daemon, including the replacement Geth fork's upstream inheritance.
- Authenticated system-contract bootstrap for a new genesis, with bank/EVM
  supply and reserve reconciliation and failure rollback.
- Repeat the guarded CLI, clean installation, independent Hermes process
  gates and pending-packet upgrade rehearsal on the final integrated daemon.
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
