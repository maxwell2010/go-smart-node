# New-network system-contract bootstrap staging

This directory investigates a reproducible fresh deployment. It is not a
mainnet migration, a complete system-contract genesis or enabled application
code. Explorer-published flattened MIT sources are pinned by raw SHA256 and
origin in `source-manifest.json`; their original line endings are preserved.

`NewNetworkProxyBootstrap.sol` is new constructor-only setup source. It calls
the verified implementation's OpenZeppelin owner initializer and ERC1967
upgrade utility, rather than guessing owner/storage slots. The caller supplies
the fresh owner and final implementation explicitly. The proxy constructor
must execute the setup atomically; the setup implementation disables direct
initialization. No mainnet address, owner or balance becomes a fresh default.

This deliberately does not claim to reconstruct the unverified historical
setup contract. Actual deployment, ownership, initialization and registry operations now pass
in the owned ABCI application, including the expanded fresh system proxy set.
A final genesis allocation/authority manifest and complete native financial
integration remain separate required steps.

## Reproduction and observed scope

`SOLC=/absolute/path/to/solc python3 build.py` requires the exact official
Linux Solidity 0.8.20 compiler with SHA256
`0479d44fdf9c501c25337fdc540419f1593b884a87b47f023da4f1c700fda782`.
The [official compiler artifact](https://github.com/ethereum/solc-bin/blob/gh-pages/linux-amd64/solc-linux-amd64-v0.8.20%2Bcommit.a1b79de6)
is a source-reproduction pin, not a general recommendation to use an old
compiler for unrelated new contracts. Standard JSON settings are checked in;
there are no mainnet library links or network imports during compilation.

The original compiled targets are the base `DecimalContractCenter`, its genuine
proxy and the new setup implementation. The expanded targets are listed below. The source also contains historical migration
contracts; those are not the selected fresh-registry implementation. Original
Decimal and OpenZeppelin MIT notices are retained alongside the sources.

The bounded Go test passes on the selected Cosmos Geth 1.17.2 reference: real
EVM creation installs the exact artifact runtimes, the proxy exposes the explicit
owner and final implementation, owner registry updates work, direct/repeated
setup is rejected, and a zero-owner constructor leaves no proxy code. These
are genuine EVM constructor/delegatecall checks without a mock registry. They
do not exercise signed ABCI transactions, native fees, a complete system genesis
or the newly selected maintained-family Geth version; those gates remain open.


## Expanded fresh-network artifacts and actual ABCI scope

The pinned source set now includes the base validator, unified delegation and
its DelegationNFTLib, NFT delegation, token/NFT factories and their reserve and
reserveless token implementations. Migration variants are not deployment
targets. `CompiledABI(name)` returns the exact compiler ABI independently of
linking. `LoadLinked` requires fully qualified solc library names and explicit
fresh nonzero addresses; it rejects missing and unused library configuration.
No mainnet library address is substituted. Only the delegation source unit uses
`viaIR=true`, as its verified compilation metadata requires; all outputs record
the exact fresh compilation settings. This is a fresh recipe, not a claim that
all resulting bytecodes equal historical mainnet deployments.

`NewNetworkMasterValidator` is an explicit new initialization extension. The
verified base implementation exposes no initializer or external registry
setter. The extension accesses its own verified internal storage accessor,
sets a nonzero deployed registry while the proxy-constructor initialization is
active, and rejects later/direct calls. No storage slots are guessed.

`MintCandyWrappedDEL` is NEW source for the fresh network. Historical WDEL
Solidity source has not been authenticated and is not reconstructed from
observed runtime. The wrapper uses pinned ERC20Upgradeable and official
OpenZeppelin5.0.2 ReentrancyGuard source with retained MIT notices. Deposit or
plain native transfer locks and mints exactly the same18-decimal units;
withdraw burns then transfers, reverting on transfer failure. There is no
owner, sweep or arbitrary mint function. Native bridge hooks must not mint
native DEL again for its ERC20 mint logs. Unsolicited forced native transfers
can overcollateralize a wrapper; the invariant is backing >= wrapped supply,
with equality in the tested ordinary deposit/withdraw flows.

The default-production owned-App test executes real signed deployment and
calls through FinalizeBlock/Commit, with exact fee/value and nonce checks.
Registry ownership, rejected authorization/reinitialization, fresh validator
initialization, the five-role proxy set, fresh linked library, registry lookups,
owner-only token/NFT implementation configuration, wrapper supply/backing,
transfer/allowance, overdraw and
receiver-rejected withdrawal rollback are tested. `WrappedDELTestReceiver` is
an explicitly named test fixture, never a system contract or authority.
The earlier bare-VM Geth1.17.2 paragraph above is historical evidence; the
signed application tests use the selected SDK53/Cosmos Geth1.16.2 family.

Required next gates include a reviewed genesis allocation/authority manifest,
real governance execution as owner, native coin/NFT/delegation receipt wiring,
consensus validator lifecycle, supply/reserve reconciliation across modules,
and a complete deploy/install/restart/upgrade rehearsal. These artifacts and
bounded tests do not alone establish a production-ready genesis.


## Atomic constructor configuration and governance handoff

`bootstrapConfigured` is a new method on the NEW setup contract; the existing
`bootstrap` method remains compatible. During proxy construction only, it gives
the original constructor caller temporary ownership, invokes the explicit final
implementation and its reviewed ABI-packed setup calls, then transfers ownership
to the nonzero final authority. Owner and ERC1967 implementation must remain
unchanged after initialization and every setup call. There is no configuration
entry point on the final implementation after construction. Calls are capped
at32 and32KiB; malformed selectors, changed authority/implementation and any
later failure revert the entire creation including earlier external effects.

`PackConstructorConfiguration` uses compiler ABIs and a narrow setup-method
allowlist. `ConfiguredProxyDeployment` returns unsigned creation data and checks
explicit nonzero addresses and the EIP3860 initcode limit. It does not certify
supplied addresses or execute a network transaction. The reviewed genesis
manifest must bind the implementation/library addresses to their exact code,
and finalOwner to the actual governance module account.

The signed ABCI test now creates a registry and all five system proxies with
the actual SDK governance module address as final owner, complete token/NFT
implementation configuration and deterministic fresh registry links. Both the
deployer and another signer fail later upgrades. A failed later setup call
also restores a previously modified external registry and leaves no proxy code,
while charging the exact failed transaction fee. Canonical delegation runtime
stays24316bytes because the constructor setup is a separate temporary artifact.
This verifies genesis handoff, not subsequent approved governance proposal
execution, which remains a required application integration gate.


## Bounded validator penalty views

The NEW MasterValidator extension exposes `getPenaltyCount(address)` and
`getPenaltiesRange(address,start,limit)`. The latter returns at most256 entries,
rejects start beyond the end, permits start equal to count and zero-size pages,
and uses subtraction before indexing to avoid arithmetic overflow. The verified
base source and monetary logic are unchanged. Signed actual-App tests append
real penalties and compare pages with the canonical full-array getter, including
empty pages and rejected bounds.

Native reconciliation should request a smaller page fitting its own explicit
EVM/SDK gas budget (for example32 entries for a100K EVM allowance), bind count
and page results to the same context, and retain an idempotent processed index.
The256 cap is a hard maximum, not a guarantee that every caller's smaller gas
budget can read256 cold storage slots. Runtime governance selectors for adding
penalties or changing validator membership remain separately gated on native
financial and final validator-set validation.


Measured on the actual257-entry signed application fixture: a32-entry page used
58290 intrinsic-inclusive EVM MaxUsedGas, and a256-entry page used257595.
The100K native-reader allowance therefore uses32-entry pages in this fixture;
these are bounded local measurements, not a universal future compiler/fork gas
promise. The full test also reads the final partial page and checks parity.


## Fresh factory beacon initialization

Canonical TokenCenter/NFTCenter `initialize()` methods do not populate the
beacon addresses required by fresh token creation. The prior owner/implementation
setup controls alone did not establish working collection minting. The signed
regression confirms canonical fresh NFT and reserveless-token creation reject.

The NEW `NewNetworkNFTCenter.initializeFresh(registry)` constructs four verified
canonical BeaconNFT instances and stores them using the inherited verified
accessor. Each beacon retains the exact NFT_CENTER/TYPE_NFT/WITH_RESERVE API
used by NFT implementations. `NewNetworkTokenCenter.initializeFresh(registry)`
constructs a new immutable-center beacon for its reserveless ERC20 slot. Both
initializers are constructor-only, require an active initialization and explicit
nonzero owner/registry, and inherit the canonical financial methods unchanged.
No mainnet addresses or manually guessed slots are supplied. The ordinary
configured bootstrap then installs reviewed child implementations and hands off
ownership to the actual governance module account atomically.

Actual signed ABCI controls create/mint all four NFT variants, preserve a full
2^200 ERC1155 token ID, reconcile native reserve backing, create a reserveless
ERC20 and retain reserve-backed coin creation with exact net reserve. Failed
later factory configuration also removes the newly constructed beacons and
proxy, retaining only the exact transaction fee. These are real contract tests;
complete native NFT binding/admission/receipt wiring remains a separate gate.


## Fresh unified routing profile

The updated fresh profile deploys one UnifiedDelegation financial contract.
The registry labels `delegation` and `delegation-nft` resolve to that SAME proxy:
NFT reserve code authenticates `delegation-nft`, while current coin and NFT
operations both originate from UnifiedDelegation. The standalone legacy
DecimalDelegationNFT artifact and earlier five-proxy setup controls are retained
as historical/reference evidence, not the current fresh financial recipe.

The actual configured governance-owned profile now tests the canonical registry,
master validator, unified delegation and token/NFT factories with this explicit
routing alias. Authority admits only the canonical `delegation` administration
slot; the NFT alias is not a duplicate administrator/ledger. Arbitrary duplicate
authority targets remain rejected. The alias fixes authentication routing without
changing the canonical NFT guard or claiming completed native financial parity.


## Fresh pause-aware child proxies

Canonical factory implementation getters return zero while paused, which also
blocks balance/supply/reserve reads through canonical beacon proxies. The fresh
profile retains those canonical getters and financial pause behavior, while
its NEW readable child proxies permit only the exact view/pure selectors from
the pinned compiler ABI. They resolve the current implementation through the
explicit `readImplementation(uint8 family)` getter when the factory is paused.
No stored supply cache, fabricated pause state or arbitrary method forwarding
is used. Family0/1 means reserve/reserveless ERC20;2/3 means reserve721/1155;
4/5 means reserveless721/1155.

Raw canonical sources remain under `sources/`. The owned source copies and
`pause-source-patches.json` declare every transformation and original/owned
hash. Creation/predict helpers become virtual solely to select the fresh child
proxy. The builder reconstructs the owned bytes and rejects undeclared changes.
Canonical child/reference artifacts remain distinct from NEW reserve NFT
implementations. The NEW factories preserve ordinary mint/transfer behavior;
while paused, financial selectors and native donations through NFT receive
are rejected, while ordinary metadata reads remain live. Unpause restores
financial operations. The canonical binding profile must still reject zero
implementation; only the explicit fresh profile uses the reviewed raw getter.

Compiler immutable references and AST variable identities are recorded in the
artifacts. `PauseAwareProxyRuntime`, `NFTBeaconRuntime`,
`ReservelessTokenBeaconRuntime` and generic `ExpectedRuntime` build expected
runtime bytes from known constructor inputs. Actual chain code is compared
against that independent template; an observed hash is never its own approval.
The compiler-defined library deployment-address reference is also supported.

## Explicit native destruction

The user-selected fresh policy destroys real native DEL bank supply rather
than transferring reserves to the zero address. NEW reserve NFT implementations
replace penalty and nonrefundable native destruction with zero-value
`nativeBurn(uint256)` calls to the reserved0900 precompile and require an exact
acknowledged bool response. No-code or failed backends revert all reserve and
ownership changes. ERC20 reserve destruction remains the token's explicit burn
path; the NEW WDEL public `burn(uint256)` invokes the same native primitive.
Token-creation commission policy is separate and unchanged.

Ordinary signed controls check paused reads/financial rejection/recovery,
constructor-derived runtime templates and missing-backend rollback. Where the
application registers the active native primitive, the NFT destruction control
also reconciles independent bank supply with block mint/burn events and checks
the native burn receipt and account balances. The test reports which backend
profile actually ran. The standalone native primitive and complete application
integration have their own required positive tests; a missing-backend control
is not proof of those gates. Runtime monetary penalty corrections and full
native NFT reconciliation remain separate review gates.

`NativeBurnTestTarget` contains only ordinary funding, holder-burn and outer
financial rollback controls. Deferred security execution-mode scenarios are
not executed, are not included in that fixture, and are not claimed verified.
