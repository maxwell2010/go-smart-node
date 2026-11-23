# Software release verification

## 1.1.5 software release

[MintCandy Node 1.1.5](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.1.5)
uses MintCandy and MSP in a fresh `mcnd init` genesis while the bank
base denom remains `del`. The packaged binary passed the CLI genesis
validation and metadata assertions. The public genesis approval tools
check the WMSP name and ticker in the EVM storage and match the ticker
against the cold rehearsal report.

The attested Linux amd64 binary passed quorum recovery, follower replay,
systemd restart, snapshot restore, block retention, ten Hermes scenarios
and signed media publication on isolated networks.
[Packaging and attestation](https://github.com/maxwell2010/go-smart-node/actions/runs/45122760631) ·
[Binary rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45124076770) ·
[Signed media](https://github.com/maxwell2010/go-smart-node/actions/runs/45124389500) ·
[Public genesis checks](https://github.com/maxwell2010/go-smart-node/actions/runs/45124032353) ·
[Release evidence](../network/docs/ru/evidence/software-release-1.1.5.json).

The separately attested launch toolkit was unpacked, cold-verified and
used to install a signed WMSP test genesis in a disposable validator home
with the published v1.1.5 daemon.
[Toolkit and verification](../network/docs/ru/INSTALL_LAUNCH_TOOLS.md) ·
[Toolkit rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45126815779).

The published archive passed the same snapshot and ten Hermes scenarios.
The published toolkit verified all four offline operator acknowledgments
for the installed test genesis and rejected a set of three. The test
genesis SHA256 is the same as in the v1.1.4 rehearsal.
[Published runtime rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45126815678) ·
[Founding approvals rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45127945020).

The exact published binaries passed a separate `v1.1.4 → v1.1.5 → v1.1.4`
switch with ICS-20 packet 3 pending on the same validator homes. All four
signing states advanced from height 40 to 41. The reverted binary preserved
the v1.1.5 checkpoint and the four validators agreed on the AppHash at
height 42. Hermes delivered the packet and the source recorded one
acknowledgement, at height 45. Each release archive was verified separately.
The exercise covers binary replacement with intact state for this pair.
[Release-switch run](https://github.com/maxwell2010/go-smart-node/actions/runs/45131811346) ·
[Report artifact](https://github.com/maxwell2010/go-smart-node/actions/runs/45131811346#artifacts).

The independently attested v1.1.4 and v1.1.5 archives also passed the
systemd backup and restore rehearsal on a non-signing observer. With the
observer stopped, the bank IAVL store at height 22 used format 1.0.0.
The v1.1.5 observer enabled the fast index and persisted format 1.1.0.
It synchronized with the validators through height 25. The original
binary and byte-checked backup reopened height 22 with the original
AppHash and format 1.0.0. The restored observer synchronized through
height 26 and persisted the original format at height 27.
[Systemd restore run and report](https://github.com/maxwell2010/go-smart-node/actions/runs/45134099952).

The combined IBC and systemd rehearsal made an offline observer backup at
height 39 while ICS-20 packet 3 was pending. The v1.1.5 upgrade and v1.1.4
restore agreed with four validators through heights 42 and 43 respectively.
The observer reported the same packet commitment and 1 MSP in escrow
after each operation. The bank IAVL store migrated to 1.1.0 and was
restored to 1.0.0 from the backup. Hermes subsequently delivered the
same packet and one acknowledgment was recorded at height 46.
[Combined rehearsal and report](https://github.com/maxwell2010/go-smart-node/actions/runs/45136627719).

The first-block verification tool checked the approved test genesis SHA256
and verified block 1 as reported by two isolated RPC endpoints. All four
Ed25519 signatures verified with 40,000 of 40,000 voting power. The tool
rejected a modified AppHash in the signed header. In the same run,
a fresh observer restored snapshot 40 and agreed with the validators on
the AppHash at height 44. A separate push run repeated these checks
alongside the block retention and Hermes scenarios.
[First block and snapshot rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45142480619) ·
[Combined node, retention and Hermes run](https://github.com/maxwell2010/go-smart-node/actions/runs/45142445494) ·
[Verification instructions](../network/docs/ru/VALIDATOR_LAUNCH_INTAKE.md#проверка-подписей-первого-блока).

An independently attested toolkit adds `install-observer` and a prebuilt
`comet-commit-probe` for Linux amd64. The v1.1.5 daemon validated the signed
fixture genesis before installation in a fresh observer home. The installer
preserved the other six home files, rejected a founding validator's consensus
key and returned `changed: false` on repeat.
[Toolkit rehearsal and attestation](https://github.com/maxwell2010/go-smart-node/actions/runs/45145835371) ·
[Download and verify](../network/docs/ru/INSTALL_LAUNCH_TOOLS.md#комплект-ci-для-наблюдающей-ноды).

The current toolkit also contains the offline founder wallet proof. Linux CI
accepted the disposable founder's signature and rejected a different signer,
a different challenge and a byte-modified allocation plan. The attested
toolkit retains the same v1.1.5 daemon compatibility. The same source revision
passed the repeat observer, quorum, snapshot, block retention and Hermes gates.
[Founder proof tests and toolkit](https://github.com/maxwell2010/go-smart-node/actions/runs/45148803166) ·
[Repeat published-daemon rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45148747172).

The attested toolkit from revision `bad9d2a` is also available as a
separately named v1.1.5 release asset with its SHA256 file and Sigstore
bundle. The expected archive SHA256 and steps to verify both source
and signer are in the operator guide. A fresh download from GitHub
Releases passed both the archive SHA256 and pinned provenance verification.
[Download and verify the release toolkit](../network/docs/ru/INSTALL_LAUNCH_TOOLS.md#комплект-ci-для-наблюдающей-ноды).

The source tools can bind the opt-in persistent peer and seed configuration
to the same signed genesis manifest. With the published v1.1.5 daemon, the
validator and observer installers both returned the authenticated test
peer. The published consent binary verified four operator acknowledgments
for those exact manifest bytes. Linux OpenPGP tests rejected a substituted
peer even when the replacement file had a recomputed digest.
[Signed installer and approvals rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45157485725) ·
[Manifest checks](https://github.com/maxwell2010/go-smart-node/actions/runs/45157486104).

The separately named v1.1.5 toolkit from revision `52f67be` includes these
scripts. Linux CI ran the OpenPGP and validator and observer installation
checks using modules loaded from the extracted archive. The published
archive digest matches the attested CI archive. The operator guide pins
this toolkit revision and SHA256.
[Archive validation](https://github.com/maxwell2010/go-smart-node/actions/runs/45159268185) ·
[Release toolkit installation](../network/docs/ru/INSTALL_LAUNCH_TOOLS.md#комплект-ci-для-наблюдающей-ноды).

The isolated v1.1.5 observer installed the signed genesis into its fresh
home and joined the four-validator network using only the one persistent
peer from the verified manifest. The hostname resolved to loopback in the
test namespace. The chain was at height 27 when the observer joined.
The observer had zero voting power and matched validator AppHash at height
31. The same run covered systemd restart and snapshot restore on a separate
observer.
[Signed bootstrap and node rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45162703235).

The isolated media gateway passed TLS 1.2 and TLS 1.3 publication and CID
reads with Nginx and Gunicorn. Clients refused an untrusted certificate
and a mismatched hostname. The gateway rejected plaintext on the HTTPS
port. Tests also covered restored media and a separate systemd restart.
[HTTPS media validation](https://github.com/maxwell2010/go-smart-node/actions/runs/45150996621).

The attested daemon then confirmed a signed reserveless token creation
at height 23 in the isolated EVM network. The live media API read the
on-chain CID through an observer RPC, rejected modified metadata and
returned the same version on retry over HTTPS. With the original API and
store offline, a separate HTTPS export served both the restored PNG and
JSON with their original CIDs and bytes.
[Signed EVM to HTTPS restore rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45152969534) ·
[Repeat quorum, snapshot, retention and Hermes run](https://github.com/maxwell2010/go-smart-node/actions/runs/45152968810).

## 1.1.4 software release

[MintCandy Node 1.1.4](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.1.4)
reserves the WMSP name and ticker in DRC20 registration. The founding genesis
assembler sets the ERC-20 wrapper name to MintCandy Wrapped MSP and its ticker
to WMSP. The exact attested binary returned this metadata through EVM RPC.

The rehearsal verified the RPC checkpoint before snapshot restoration and
passed quorum recovery, systemd restart, block retention and ten Hermes
scenarios on the isolated genesis. A separate run checked committed token media.
[Binary rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45117195355) ·
[Signed media](https://github.com/maxwell2010/go-smart-node/actions/runs/45118559306) ·
[Release evidence](../network/docs/ru/evidence/software-release-1.1.4.json).

The separately attested offline toolkit cold-verified the WMSP genesis
and installed it in a disposable validator home using the published v1.1.4 daemon.
[Toolkit and instructions](../network/docs/ru/INSTALL_LAUNCH_TOOLS.md) ·
[Toolkit rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45119625770).

The published archive repeated the snapshot and ten Hermes scenarios.
The published toolkit verified four offline operator signatures for the same
signed test genesis and rejected a set of three signatures.
[Published runtime rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45119625380) ·
[Founding approvals rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45120650589).

## 1.1.3 software release

[MintCandy Node 1.1.3](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.1.3)
preserves the full evidence and snapshot retention windows at the block pruning
boundary. A separate short-window genesis verified pruning, a local restart,
and matching AppHash across the observer and four validators. The archive node
retained historical blocks.
[Block retention](https://github.com/maxwell2010/go-smart-node/actions/runs/45012994146).

The attested Linux amd64 binary also passed quorum recovery, follower sync,
systemd restart, snapshot restore, ten ICS-20 scenarios, and media publication
after signed DRC20 creation.
[Node and Hermes](https://github.com/maxwell2010/go-smart-node/actions/runs/45012997466) ·
[Signed media](https://github.com/maxwell2010/go-smart-node/actions/runs/45013060357) ·
[Release evidence](../network/docs/ru/evidence/software-release-1.1.3.json).

The rehearsal from the published archive verified the same binary digest,
restored a height-40 snapshot, and confirmed all four validator signatures
at height 58 after Hermes testing.
[Published archive rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45110885039).

The release includes separately attested offline launch tools for v1.1.3.
CI checked the unpacked tools and installed a signed test genesis with the
published daemon in a fresh validator home.
[Download and verification](../network/docs/ru/INSTALL_LAUNCH_TOOLS.md) ·
[Tools rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45110885140).

The published toolkit also verified four offline operator signatures bound to
the same signed test manifest and genesis used for installation. Verification
rejected a set of three signatures.
[Founding approvals rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45113292355).

## 1.1.2 software release

[MintCandy Node 1.1.2](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.1.2)
is published for Linux amd64 at source `bf788083671e05d330df7ee48c711ff7fbb046ea`.
Reserve-backed and reserve-free DRC20 assets share a registry of unique uppercase
Latin names. Native validator and NFT admission reject blank or invisible names.

The attested binary passed installation, four-validator quorum recovery,
follower replay, systemd restart, snapshot restore and all ten Hermes scenarios
on the same isolated signing domain. The final IBC checkpoint had all four
signatures at height 58. A separate rehearsal checked media publication after a
signed reserve-free token creation.
[Attested rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45005205221) ·
[Signed media rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45005487421) ·
[Release evidence](../network/docs/ru/evidence/software-release-1.1.2.json).

The rehearsal was repeated from the published archive with the same binary and
isolated genesis checksums. It also verified historical state retention and AppHash
after restart. [Published archive rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45007278028).

The release also includes separately attested offline launch tools for this
binary. CI unpacked them, checked the draft, assembled an isolated genesis
and installed the signed test genesis in a disposable validator home.
[Download and verification guide](../network/docs/ru/INSTALL_LAUNCH_TOOLS.md) ·
[Tools rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/45007662799).

## 1.1.1 software release

[MintCandy Node 1.1.1](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.1.1)
is published for Linux amd64 at source `3880898bf95fe701b9d6f28d6e029a63f88a16ae`.
It enforces a 5% minimum native validator commission at genesis,
registration and signed updates.

The exact attested archive passed the four-validator launch, quorum recovery,
follower replay, systemd restart, snapshot restore and ten ICS-20 scenarios
on the same assembled test genesis. The background Hermes process delivered a
new packet and recovered a queued packet after restart. The rehearsal checked
acknowledgements, balances, fees and the four-validator AppHash at height 58.
[Isolated rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/44681169004) ·
[Release evidence](../network/docs/ru/evidence/software-release-1.1.1.json) ·
[Hermes operator guide in Russian](../network/docs/ru/HERMES_RELAYER.md).
The [public-release rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/44539903038)
downloaded the published package, repeated the daemon checks and captured the
[validated fresh defaults](../network/docs/ru/evidence/default-launch-parameters-1.1.1.json).

## 1.1.0 software release

[MintCandy Node 1.1.0](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.1.0)
is published for Linux amd64 at source `b90b5a9348658627a54d0e5b5002ee1d1b394cc2`.
The package adds empty asset genesis, public asset creation, validator controls,
avatar metadata transfer and an isolated test grace period.
[Final release CI](https://github.com/maxwell2010/go-smart-node/actions/runs/44516758296)
passed module, EVM, mass delegation, IBC, package and publisher checks. The
archive and GitHub OIDC attestation were verified locally. The separate
[four-validator rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/44516752555)
used the approved MSP allocation amounts on an isolated signing domain and
matched AppHash after quorum recovery and follower catch-up. See the
[recorded revisions and report](../network/docs/ru/evidence/software-release-1.1.0.json).

A [separate rehearsal of the published archive](https://github.com/maxwell2010/go-smart-node/actions/runs/44521204468)
downloaded the GitHub Release assets and installed the attested Linux amd64
binary. On the isolated four-validator network, the same binary recovered
quorum, replayed history on a new follower, resumed after a systemd restart,
and restored a fresh peer from a height-40 snapshot. The restored peer matched
the validator AppHash at height 44.

Public signing domain `mintcandy_9905401-1` / EVM `9905401` and the 5,041,000 MSP
allocation are approved. Final genesis requires public operator applications,
wallets, network endpoints and a confirmed start time.

## 1.0.0 software release

The software-only [MintCandy Node 1.0.0](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.0.0)
is published from immutable source `993c4ae6cca8b66a2c714542e9881424952ec021`.
The final [44479249249](https://github.com/maxwell2010/go-smart-node/actions/runs/44479249249)
passed module, real EVM, mass delegation, governance, IBC harness, reproducibility,
SBOM/security and strict GitHub OIDC gates. Its actual binary installer passed
and rejected overwriting an existing destination. All twelve release assets
matched local SHA256/size; the archive, manifest and OIDC bundle were also
verified locally without executing the binary. Installation instructions,
SBOM and preserved proof files are permanent release assets. Tag `v1.0.0`
continues to identify the exact tested source; this record update changes docs only.
At the time of 1.0.0, the public genesis and the operator roster were still drafts.

| Verification | Successful evidence |
| --- | --- |
| Whole module, reward escrow, approved migration with unpaid credits | [44470608682](https://github.com/maxwell2010/go-smart-node/actions/runs/44470608682) |
| Signed EVM token features, fee conversion, nonce and receipt accounting | [44470617638](https://github.com/maxwell2010/go-smart-node/actions/runs/44470617638) |
| Actual EVM with 10/100/1000 positions, paged historical penalty, restart and crash replay | [44467813041](https://github.com/maxwell2010/go-smart-node/actions/runs/44467813041) |
| Three daemon replicas: fee API, envelope CLI/Comet broadcast, EVM revert and replay rejection | [44471630412](https://github.com/maxwell2010/go-smart-node/actions/runs/44471630412) |
| Signed test launch package, SBOM, source/binary symbol scan | [44470745629](https://github.com/maxwell2010/go-smart-node/actions/runs/44470745629) |
| Signed package systemd installation, restart and snapshot catch-up | [44471634618](https://github.com/maxwell2010/go-smart-node/actions/runs/44471634618) |
| Four-validator coordinated module migration, backup rollback, missing-block startup replay and native Hermes | [44472448125](https://github.com/maxwell2010/go-smart-node/actions/runs/44472448125) |
| Different-version migration and backup rollback with a pending IBC packet | [44472452007](https://github.com/maxwell2010/go-smart-node/actions/runs/44472452007) |
| Signed package systemd upgrade, physical IAVL schema migration and backup rollback | [44473340885](https://github.com/maxwell2010/go-smart-node/actions/runs/44473340885) |

[Machine-readable revisions and limits](../network/docs/ru/evidence/release-candidate-20261119/summary.json)
record each source SHA. Runtime Go sources are unchanged between the
`2a5f4ff` module/package baseline and the `9ef244f` process checkpoint fixes;
changes in that interval are fixture tests and operator verification tools.
The final software workflow repeats module, signed EVM, mass delegation,
governance, IBC harness, contract reproduction and source/binary checks on
its own exact source commit before issuing an attestation.

The reward mechanism transfers collected DEL, without reward emission.
Automatic payouts are bounded and retain unpaid liabilities. Financial
profile genesis export/import remains fail-closed. The former two-position
penalty and 128-row reward pilot limits are historical, not current runtime
capacity claims. Cold financial genesis and direct governance votes have
separate documented limits.

The scanner found zero distinct symbol advisories in its 2026-11-11 database
snapshot. This is a scoped automated result, not a formal security audit.
The OpenPGP launch archive contains a disposable test genesis and uses a
one-time test signer. It must not be relabelled as an approved public network.
The versioned software archive omits genesis/keys and uses a separate
[source-bound publisher procedure](../network/docs/ru/SOFTWARE_RELEASE_VERIFICATION.md).

DEX, vesting, a proprietary stablecoin, DePIN and MEV remain separate stages.
The Decimal bridge prototype is not a production two-way funds bridge.
Working nodes were not changed; heavy builds ran on isolated GitHub runners.

## Historical evidence

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

The later `9c0b2e4` candidate adds governed retirement for an empty operator.
It retains validator history, permits settlement of valid earlier penalties,
releases the live roster slot and blocks new stake and unjail. Its module
suite and clean archive audit passed. The packaged scanner reported zero
symbol advisories in the same 2026-11-11 database snapshot.
[Retirement and module suite](https://github.com/maxwell2010/go-smart-node/actions/runs/43202494582)
[Archive and scanner](https://github.com/maxwell2010/go-smart-node/actions/runs/43202494583)

The isolated `824e469` process checks passed state sync with an independent
witness, partial and repeated DEL payouts, jail recovery, native Hermes
ICS-20, and a pending-packet client upgrade on its own profile.
[Process checks](https://github.com/maxwell2010/go-smart-node/actions/runs/43200233345)

On `62d3e09`, the module suite also passed genesis parsing with all profile
flags in an indented JSON envelope. The packaged Linux amd64 daemon passed
clean installation and the source and binary symbol scans.
[Combined module checks](https://github.com/maxwell2010/go-smart-node/actions/runs/43203291269)
[Archive and scanner](https://github.com/maxwell2010/go-smart-node/actions/runs/43203291717)

On `4ddae0c`, three private nodes with the historical penalty, native IBC,
and governance profile halted at block 31, applied the rehearsal handler
at block 32 and agreed on AppHash at block 47. The signed 0.25 DEL pending
claim and 28-day deadline survived in both native state and EVM. The IBC
assertion covers unbonding parameter continuity. The same-family pending
packet rehearsal is tracked separately above.
[Full-profile handoff](https://github.com/maxwell2010/go-smart-node/actions/runs/43204900931)

Opt-in DRC20 admission verifies a real TokenCenter deployment, token supply
and spendable native backing at genesis and restore. Signed conversion with
`minOut` updates the token's native reserve record and the ordinary DEL stake
in one transaction. The isolated NFT route updates the source DRC20, retires
the selected origin units and funds the new receipt with DEL. Its signed
withdrawal and automatic completion return that receipt after a restart.
[Direct DEL stake](https://github.com/maxwell2010/go-smart-node/actions/runs/43210762436)
[NFT reserve and completion](https://github.com/maxwell2010/go-smart-node/actions/runs/43212664914)

The 1024-unit custom NFT fixture used 19,496,944 SDK gas with an EVM
peak of 19,639,044 under the 24,000,000 transaction ceiling. The 1025th
origin unit retained its reserve. The compiler reproduced the 24,536-byte
Gateway and 24,530-byte linked NFT library within the EVM code-size limit.
[Maximum-size workload](https://github.com/maxwell2010/go-smart-node/actions/runs/43213505574)

The `de3851c` Linux amd64 archive passed clean installation and CycloneDX
validation. Source and binary scans reported zero symbol advisories in the
2026-11-11 scanner database.
[Combined module and EVM tests](https://github.com/maxwell2010/go-smart-node/actions/runs/43213937326)
[Archive and scanner](https://github.com/maxwell2010/go-smart-node/actions/runs/43213937130)

On the same revision, three isolated nodes carried a genuine 1 DEL ICS-20
commitment across the approved height-56 handoff. Hermes updated the
counterparty client, delivered one success acknowledgement and returned the
voucher for the original principal. Restoring closed checkpoints of both
chains recovered the same pending commitment and escrow, the 28-day DEL
claim and the exact AppHash after reapplying height 56. This rehearsal uses
two builds of the same source revision.
[Pending IBC packet and coordinated restore](https://github.com/maxwell2010/go-smart-node/actions/runs/43213910119)

The `3688b0b` candidate enables the wallet owner to redeem a refundable,
burnable NFT receipt for the remaining DEL. Signed EVM tests reconcile the
recipient credit and vault debit, reject a repeated payment, and verify
atomic rollback when the recipient fails native admission. Receipts from
1 and 1024 origin units fit the 24 million gas limit. A separate signed EVM
case verifies closure after the entire reserve was slashed.
[Redemption and module checks](https://github.com/maxwell2010/go-smart-node/actions/runs/43217878365)
[Linux archive and scanner](https://github.com/maxwell2010/go-smart-node/actions/runs/43217879863)

The cross-version rehearsal moved from archived `de3851c` to `3688b0b`
with the approved handler. The rebuilt predecessor matched the archived
binary SHA256. It committed height 55 and the successor applied height 56.
Hermes delivered the pending 1 DEL ICS-20 packet and returned its voucher
for the original principal. A coordinated restore of both private chains
recovered the pending commitment, escrow and 28-day withdrawal claim.
Replaying height 56 produced the same AppHash as the primary upgrade.
Both candidates use the same SDK and IBC dependency family.
[Cross-version IBC and restore](https://github.com/maxwell2010/go-smart-node/actions/runs/43217877949)

The isolated launch rehearsal on `aafd22b` validated the fresh genesis and
started three validators with voting power 4/4/2 from the exact packaged
Linux amd64 daemon. All three signed blocks. An ordinary peer with a new
home joined when the validators reached height 8 and synchronized from
block 1 over P2P. All four agreed on AppHash at height 12 and returned the
same DEL balance through EVM JSON-RPC and Cosmos REST. The public rehearsal
artifact binds the genesis SHA256 to the allocation and network reports.
The bank allocation totals 49,787.680854 DEL, including 10 DEL backing the
initial stake in WDEL and 10 DEL backing the test DRC20. The fixture has
deterministic CI identities and loopback endpoints for the private runner.
The public launch still requires approved allocations, operator identities,
and independently operated bootstrap and RPC endpoints.
[Linux module and cold genesis checks](https://github.com/maxwell2010/go-smart-node/actions/runs/43221565437)
[Packaged launch](https://github.com/maxwell2010/go-smart-node/actions/runs/43221986446)
[Launch evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43221986446/artifacts/13946004522)

The four-validator rehearsal on `da1d68c` used `mintcandy_990012-1` and
reconciled all 100,000 DEL against the draft distribution. The cold app
verified four 10,000 DEL positions, 40,000 DEL in WDEL, and DRC20 and
ERC1155 collateral of 300 and 100 DEL. The four validators initially
signed. After validator `d` stopped at height 12, the other three made
canonical commits at heights 14-16 with 30,000 of the configured 40,000
voting power. The ordinary peer joined after height 17, synchronized from
block 1 and agreed on the AppHash at height 21. EVM JSON-RPC and Cosmos
REST agreed at heights 1 and 21. The fixture used one synthetic community
account and verified the treasury's 3-of-5 public key at genesis.
[Four-validator CI](https://github.com/maxwell2010/go-smart-node/actions/runs/43226218714)
[Public test evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43226218714/artifacts/13948965875)

The test treasury on `fec0d83` rejected the 10 DEL transfer with two
valid member signatures. App checks forced the rejected transaction through
block execution and confirmed that balances, sequence and supply stayed
unchanged after commit. On the isolated packaged network, `CheckTx` returned
`sdk/4`, and the later snapshot showed no fee or sequence change. Adding a
third member signature committed the transfer at height 25. The treasury
debit was 11 DEL, the recipient credit was 10 DEL, the fee collector credit
was 1 DEL, and the treasury sequence advanced from 0 to 1. The three remaining
test validators and the ordinary peer agreed on the committed balances and AppHash.
[Treasury CI](https://github.com/maxwell2010/go-smart-node/actions/runs/43229438556)
[Treasury evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43229438556/artifacts/13950146919)

The signed test launch package on `6f91783` contains Linux amd64 `mcnd`,
the four-validator test genesis, provenance, and an OpenPGP-signed SHA256
manifest. CI independently exported and pinned its per-run signing key,
verified the manifest and every payload checksum, and booted the verified
binary with the verified genesis. The isolated observer replayed from block 1
and agreed on the AppHash at height 21. The signed files and the launch report
share identical genesis, binary and archive hashes.
[Signed package CI](https://github.com/maxwell2010/go-smart-node/actions/runs/43232698507)
[Test package](https://github.com/maxwell2010/go-smart-node/actions/runs/43232698507/artifacts/13952121624) · [Evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43232698507/artifacts/13952116559)

The systemd rehearsal on `4d828e3` downloaded that same signed test archive,
verified its signature and checksums, and installed `mcnd` into a clean prefix.
The transient service ran as UID 65534 in the test validators' isolated network
namespace. Its MainPID executable and genesis matched the signed package.
The observer synchronized from block 1 and agreed on the AppHash at height 21.
The rehearsal stopped its service after verification.
[Systemd CI](https://github.com/maxwell2010/go-smart-node/actions/runs/43237343370) · [Evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43237343370/artifacts/13953721863)

The restart and snapshot rehearsal on `6f2cfb1` used the same signed test
archive. After `systemctl restart`, a new systemd invocation loaded the
same data directory at height 22 and agreed on the AppHash at height 25.
A separate fresh observer restored the height 40 snapshot using two trust
RPC processes on the isolated runner. Its first available block was 41.
It reached a common AppHash and DEL balance at height 44.
[Restart and snapshot CI](https://github.com/maxwell2010/go-smart-node/actions/runs/43239340819) · [Evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43239340819/artifacts/13954741374)

The systemd update rehearsal upgraded the observer from signed `6f91783` to
signed `6802845` with identical genesis bytes. The stopped service's home and
binary were copied and checked before updating. The new binary loaded height
22 and agreed on the AppHash at height 25. After verifying and restoring the
offline backup, the original binary loaded the same height 22 AppHash and
caught up to a common AppHash at height 26. Both binaries use the same Go
application code and database schema.
[Upgrade and rollback CI](https://github.com/maxwell2010/go-smart-node/actions/runs/43243377624) · [Evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43243377624/artifacts/13957190361)

The systemd rehearsal on `877b501` enabled IAVL Fast Storage in a new
signed build. The offline backup held application height 22 and used IAVL
storage version `1.0.0`. After the update, an offline inspection of the
`bank` store found version `1.1.0` and 25 FastNode records. The observer
agreed with the validators on AppHash at height 25. Restoring the backup
recovered the original binary and the height 22 AppHash. The observer
caught up and agreed on AppHash at height 26. A final inspection of the
stopped database at height 27 confirmed version `1.0.0` and zero FastNode
records in `bank`. The rehearsal exercised the local IAVL storage format
with the existing application and consensus rules.
[IAVL migration and rollback CI](https://github.com/maxwell2010/go-smart-node/actions/runs/43246566894) · [Evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43246566894/artifacts/13958681719)

The coordinated systemd upgrade on `32b7f4a` ran three isolated replicas,
with two active validators signing blocks. The governance-approved plan
halted the predecessor at committed height 31. After closing the services
and backing up all homes, the signed successor applied `coin` module
migration 1 to 2 at height 32. The DRC20 address index changed from text to
binary keys and the primary coin record retained its SHA256. The services
agreed on AppHash at height 47. Recovery verified the three backup hashes
and the height 31 AppHash, then replayed the signed history using fresh
non-voting identities. AppHash matched the original run at heights 32
and 47. The module migration is enabled in the CI successor build.
[Coordinated systemd CI](https://github.com/maxwell2010/go-smart-node/actions/runs/43446310577) · [Evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43446310577/artifacts/14048472406)

The `native-systemd-four-validator-recovery` scope on `0a77558` repeated the
governed migration with four voting systemd validators. Both signed packages
used the same test genesis. The four validators migrated `coin` from 1 to 2
at height 32 and produced a commit with four verified Ed25519 signatures at
height 33. After restoring all four height-31 backups, the replicas replayed
the migration with zero voting power. AppHash matched the primary run at
heights 32 and 47, and the pending 0.25 DEL withdrawal survived.

The restored replicas independently reached the same AppHash at height 62
before enabling signing. They reused their original keys and byte-for-byte
latest signing states, each retained at height 62. The signed block and
commit at that height were preserved. All four signed the new canonical
block at height 63 and agreed on its resulting AppHash. The gate runs on a
GitHub-hosted worker in a loopback-only namespace with disposable keys.
All four replicas reached height 62 over P2P in this run, so startup replay
of a missing final block was outside this run's coverage.
[Four-validator recovery CI](https://github.com/maxwell2010/go-smart-node/actions/runs/43452128668) · [Evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43452128668/artifacts/14053602020)

The `native-systemd-startup-replay` scope on `3c8a243` exercised that case.
All four restored replicas reached height 61, and each local blockstore
reported block 62 as missing. CI supplied the closed canonical CometBFT
stores and verified that each restored `application.db` stayed byte-for-byte
intact before restart. Each non-voting replica executed exactly block 62
during startup with zero peers and matched the witness AppHash. After restoring
the original keys and latest signing states, all four validators signed block
63 and agreed on its resulting AppHash.
[Startup replay CI](https://github.com/maxwell2010/go-smart-node/actions/runs/43456522216) · [Evidence](https://github.com/maxwell2010/go-smart-node/actions/runs/43456522216/artifacts/14055371202)

The new standalone `launch_four_validators` development run adds loss of
two equal-power validators, a bounded no-progress observation, and recovery
of the original signing homes without changing the validator set or quorum.
The owned commit probe verifies canonical Ed25519 precommits and strict
voting-power quorum for three or four signers. An offline health observer
reports stale or inconsistent RPC observations without authorizing bridge
execution. The first [Linux rehearsal](https://github.com/maxwell2010/go-smart-node/actions/runs/44246062525)
passed on `ea558b6`: height 17 stayed unchanged for 12 seconds with two
live validators, three verified signers resumed at height 19, and all four
signed height 26. The new follower matched the shared history and AppHash
at height 31. Its health monitor paused conservatively on an absent proposal
reported as invalid input. A follow-up fixes that legitimate no-proposal
state and requires a valid stale-source diagnosis. That stricter run
[rejected the snapshot](https://github.com/maxwell2010/go-smart-node/actions/runs/44249902396)
because the monitor also needed the Comet 0.38 vote-extension display format.
Both supported RPC layouts now have regression tests. The corrected
[process gate](https://github.com/maxwell2010/go-smart-node/actions/runs/44255724857)
passed on `90690ba`: two live validators stopped at height 17 for 12 seconds,
the monitor accepted a valid stale-source snapshot, and the original three-
and four-signer quorums resumed. The new follower matched height 31.
[Stored process evidence](../network/docs/ru/evidence/quorum-recovery-90690ba-20261118/network.json)
preserves the report beyond the Actions artifact retention period. This
scenario uses graceful stops, not a 2/2 network partition or a deployed bridge.
See [the operator liveness guide](../network/docs/ru/CONSENSUS_LIVENESS.md).

The bridge design selects code-verified finality and state, without a separate
attestation committee. The first pure Go finality verifier binds an external
trusted checkpoint, verifies adjacent or skipping Comet light-client updates,
checks all present Ed25519 signatures and bounds the trusting policy. It is
not wired to public application transfers and has no RPC bootstrap. The
internal persistent integration is described below; mainnet bridge readiness
is not established.
The targeted [finality and quorum unit scope](https://github.com/maxwell2010/go-smart-node/actions/runs/44255729190)
passed on `90690ba`, including signature tampering, validator rotation, trust
expiry and cached-power regressions. The combined
[finality, storage and quorum scope](https://github.com/maxwell2010/go-smart-node/actions/runs/44258136467)
passed on `6b4f587`. It verified the fixed historical ContractCenter storage
membership at state height 34604902 against the observed header 34604903 root,
plus tampered claim, root, path, height and malformed-proof rejection. This is
Merkle-format conformance, not authenticated mainnet bootstrap or a bridge
deposit. [Stored validation summary](../network/docs/ru/evidence/bridge-primitives-6b4f587-20261118/summary.json).
The internal persistent client, fixed route, canonical deposit commitment and
real SDK-bank voucher/replay journal now compose atomically. The
[bridge/London CI](https://github.com/maxwell2010/go-smart-node/actions/runs/44266044517)
and [whole-module CI](https://github.com/maxwell2010/go-smart-node/actions/runs/44266535485)
passed on `248e97f`, including cache rollback, reentrancy rejection and LevelDB
reopen. The deposit-only escrow prototype has no release path and must never
hold real funds. Public messages, genesis authority, production gas calibration,
reverse release and two-daemon bridge tests remain open.
On `b1919be`, the internal route additionally pins an expected escrow runtime
hash. Exact legacy EthAccount bytes from the `acc` store must authenticate
that hash under the same H+1 root as the deposit storage proof. The
[account-proof bridge CI](https://github.com/maxwell2010/go-smart-node/actions/runs/44273486176)
passed, including proven unknown-code rejection, malformed account data,
mixed-root rejection, rollback and retry. This scope reruns all bridge packages
and London/SP1 tests, not the whole module or daemon/process suite. Old
development routes without a code hash fail closed; no migration is supplied.
Code-byte identity still requires an audited immutable/nonproxy deployment
policy and does not prove financial semantics or source-bank reserves.
On `5043694`, deterministic experimental `bridge/workgas/v1` charges precede
client decode, every actual bootstrap/update verification pass and both proof
decoders. The [finite-meter bridge scope](https://github.com/maxwell2010/go-smart-node/actions/runs/44281836892)
passed: OOG at entry, before either proof and after real staged bank mutation
discards checkpoint, bank, marker and events while retaining consumed gas.
Retry with a new funded meter pays exactly once. The four-validator fixture
uses 3,796,839 SDK gas for Receive and 7,251,654 for ReceiveWithUpdate, both
under a finite 24m budget. This does not cover full daemon ante/block gas or
prove the 1000-validator maximum fits 24m. Worst-case tariff calibration,
raw-message predecode bounds and conflicting-header detection/freeze remain
release gates. [Tariff and evidence](../network/docs/ru/BRIDGE_WORK_GAS.md).
The [whole-module scope](https://github.com/maxwell2010/go-smart-node/actions/runs/44283538704)
also passed on `5043694`, including ordinary crypto regressions, SDK keyring
compatibility, governance tally and ICS20 client upgrade with a pending packet.
It does not constitute a running two-daemon bridge test.
Current upstream IBC Solidity requires Cancun; read-only Decimal simulations
reject PUSH0 and TSTORE. A canonical upstream SP1 v6.0.0 sample proof passes
against the original standalone verifier compiled for London in CI. This is
neither a Decimal/Tendermint proof nor v6.1.0/full IBC Solidity validation.
See [the keeper rehearsal and remaining gates](../network/docs/ru/BRIDGE_KEEPER_REHEARSAL.md).

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
1024 origin units and 127 DEL tranches. Settlement used 22,820,051 of the
24,000,000 SDK gas budget and burned 108.7 DEL while retaining two tranches
funded at and after the infraction height.
[Historical penalty workload](https://github.com/maxwell2010/go-smart-node/actions/runs/43202494582)

The public release requires an approved genesis for the separate network,
including contract roles, initial allocations, operator public keys and
bootstrap and trust data. Bind published checksums and provenance to the
reviewed release binaries and genesis.

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
