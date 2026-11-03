# Restricted system-contract governance execution

This is a staged message service, not production application wiring. The
application must finish the native validator/coin/NFT ledger and install the
same reviewed native receipt pipeline at every enabled EVM entry point before
enabling this service. No generic staking ledger or hidden financial fallback
is introduced here.

`mintcandy.authority.v1.MsgExecuteSystemContract` declares the actual SDK `gov`
module account as its sole authority. The canonical SDK proposal keeper checks
that signer and the registered service route. The message server checks the
authority again. There is no native value field: every internal EVM call has
value0 and price0, so execution cannot spend refundable governance deposits.
There is no Ethereum signature or fabricated Ethereum transaction receipt for
these SDK-native proposal operations.

The application supplies an explicit reviewed manifest of registry and role
addresses, proxy code hashes and approved implementation/child code hashes.
The keeper deep-copies the manifest and caps each approved hash list at64 entries.
Factory children are approved per method and asset slot: token `upgradeToken`
and `upgradeTokenReserveless` lists are separate; NFT `upgradeNFT` additionally
binds DRC721/DRC1155 and reserve/reserveless flags. Known code in the wrong
financial/asset family is rejected, even when another slot approves it.
At execution it checks the live registry binding, standard ERC1967 implementation
slot, actual proxy and implementation code, proposal-pinned current addresses
and hashes, and actual governance ownership. Getter calls have explicit EVM gas
limits and disposable SDK caches. No RPC quote, mutable global snapshot or
unused CallEVM gas-cap argument supplies these checks.

The initial selector policy permits pause/unpause, master reward overrides,
approved empty-initialization proxy upgrades and approved token/NFT factory
implementation upgrades. Calldata must round-trip through the pinned compiler
ABI exactly. Arbitrary delegatecall migrations, ownership changes, registry
rebinding, value-bearing methods and untested validator/financial operations
are excluded. Existing manifest role addresses are immutable in this stage;
rebinding requires a separately reviewed migration path. Approved replacement
hashes are explicit application policy, not inferred from proposal text.

Every direct ApplyMessage call uses its actual GasLimit and a bounded SDK gas
meter. After successful EVM execution the explicitly injected EvmHooks pipeline
runs exactly once in the same cache. ApplyMessage does not implicitly run the
outer Ethereum transaction hooks. Any execution, binding, native hook, event or
later call error discards the whole batch. The canonical SDK proposal cache
also discards earlier successful messages if a later proposal message fails.
Actual EVM logs are emitted in the typed authority execution event for native
indexers, capped at256 logs/32KiB. Consumers must not deduplicate internal calls
by their empty Ethereum transaction hash.

Internal calls and disposable getters charge the greater of `MaxUsedGas` and
`GasUsed`, bounded by their actual EVM gas limit. Storage refunds do not replenish
the governance resource budget; they belong to fee accounting of ordinary
Ethereum transactions. The real-contract regression clears one versus two
storage slots from the same seeded state and checks identical remaining native
work charges despite the different EVM refunds.

## Resource policy and failed approved proposals

The present constants are conservative staging policy, not final network
parameters: at most8 calls,2M EVM gas per call,8M per batch,100K per getter and
1M additional SDK/native-work allowance per call. A context-scoped governance
EndBlock session reserves up to32M total work across proposals, including
getter/native allowances. Reservations survive discarded proposal caches, so
failed proposals do not repeatedly reclaim the same block budget. Sessions
reject nested, stale-height, wrong-chain and closed use and contain no global
cross-block cache. Query handlers do not establish execution sessions.

If an approved proposal reaches an exhausted budget, execution returns an
error and the unmodified SDK marks that proposal execution failed. This stage
does not silently queue, retry or defer it. Operators must review this policy,
its interaction with voting/deposit/refund rules and worst-case native ledger
cost before release. Governance can resubmit after a failed execution; a future
deferred-execution design needs explicit consensus rules.

## Verified scope

The actual owned SDK53 application test registers the protobuf message service,
uses canonical SubmitProposal/Deposit/Vote and the original SDK governance
EndBlock implementation, and reaches voting end through real signed ABCI
blocks. A passed proposal changes the real contract and a native account hook.
A later failed message marks another proposal failed and restores earlier EVM
and native writes without leaking native hook or authority events. A rejected
native hook, unauthorized caller, absent session, stale implementation,
wrong owner/code, malformed calldata, disallowed selector, unapproved code,
nonempty migration and insufficient actual EVM gas also fail closed.

The native hook in this bounded control is an explicit account/event probe.
It does not certify completed Decimal financial hooks, NFT ownership, rewards,
last-validator removal or native consensus transitions. The test wrapper installs
the execution session around the original generic SDK governance module only;
production wiring remains gated on native Decimal governance integration.

Reproduce generated messages with the pinned local protoc3.21.12 and
protoc-gen-gocosmos1.7.2 using `scripts/generate-authority-proto.sh`. Run
`go test -mod=readonly -p 1 ./authority/... ./systemcontracts` on the selected
Linux Go1.26.10 family. Whole-module verification and separate owned-source
verification remain required.
