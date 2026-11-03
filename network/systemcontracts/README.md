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
