# Native validator integration boundary

This document describes a port boundary, not an implemented or certified native
validator adapter. The owned application currently runs canonical SDK staking
as a temporary scaffold. The native Decimal validator schema/state keeper must
be ported and verified before any production adapter is enabled.

The interface inventory below was checked against Cosmos EVM 0.7.3,
IBC 11.2.0 and SDK 0.54.4 source. The active SDK 0.55/Comet 0.40 family trial must
repeat compilation and behavior checks; documentation alone does not establish
compatibility with that graph.

## Actual dependency surface

IBC 11 core does not take a staking keeper. Its constructor receives codec,
store service, upgrade keeper and authority. The 07-Tendermint light-client
module receives codec and client-store provider. Therefore native consensus
does not need to retain the SDK staking ledger merely to initialize IBC.

The actual Cosmos EVM keeper expects this narrow read-only surface:

```go
GetHistoricalInfo(context.Context, int64) (stakingtypes.HistoricalInfo, error)
GetValidatorByConsAddr(context.Context, sdk.ConsAddress) (stakingtypes.Validator, error)
ValidatorAddressCodec() address.Codec
BondDenom(context.Context) (string, error)
```

The canonical ERC20 keeper needs only `BondDenom(context.Context)` from its
staking dependency. These consumers can receive a scoped read-only projection
of the native keeper. They do not require a second writable staking ledger.

The remaining coupling is broader and must be replaced deliberately:

| Current consumer | Coupling | Required action |
| --- | --- | --- |
| EVM keeper | Historical headers, proposer/validator identity, base denom | Read-only native adapter after native state verification |
| ERC20 keeper | Base staking denom | Same validated native base-denom accessor |
| DefaultStaticPrecompiles | Concrete SDK staking/distribution/slashing/governance keepers and their message/query servers | Build an owned precompile set; retain only implemented semantics, then port/test native financial calls |
| SDK staking module | Its own ledger, genesis and validator updates | Replace with native validator module; never run both voting ledgers |
| SDK mint/distribution/slashing/evidence | Emission, fee distribution and infraction processing | Route to verified native logic exactly once; avoid duplicate rewards or penalties |
| SDK governance | Validator iteration, total power and delegation-share voting | Separate governance integration gate; do not reuse zero-return aliases as working economics |
| Relayer client creation | Often queries standard SDK staking params for unbonding period | Define a truthful compatibility query or a verified explicit operator configuration; do not invent SDK shares |

## Native sources and state that must exist first

The source of truth is the native module, not a synthetic SDK validator:

- [keeper/keeper.go](../../x/validator/keeper/keeper.go): native keeper depends
  on account, bank, NFT, coin, multisig and EVM services, plus module store and
  parameter state. The modern port needs explicit interfaces for these services
  and validated whole-protobuf params in transactional KV state.
- [keeper/validator.go](../../x/validator/keeper/validator.go): the consensus
  address index resolves the operator, and `GetValidator` hydrates `Stake`,
  `Rewards` and `TotalRewards` from the separate validator reward/state row.
  Reading only the raw validator record can therefore give the wrong power.
- [keeper/historical_info.go](../../x/validator/keeper/historical_info.go): native
  historical headers and validator sets are stored under native historical
  keys, tracked and pruned by `HistoricalEntries`.
- [keeper/val_state_change.go](../../x/validator/keeper/val_state_change.go):
  native validator-set updates and transitions remain authoritative.
- [keeper/delegation.go](../../x/validator/keeper/delegation.go),
  [types/stake.go](../../x/validator/types/stake.go), NFT/coin keeper interfaces:
  coin stakes, NFT/subtoken reserves and holds are not generic SDK token shares.
- [keeper/reward.go](../../x/validator/keeper/reward.go),
  [keeper/infractions.go](../../x/validator/keeper/infractions.go),
  [keeper/slash.go](../../x/validator/keeper/slash.go), offline tracking and
  auto-unbond state: reward, penalty and retry behavior must be preserved together.
- [types/pool.go](../../x/validator/types/pool.go): native bonded and unbonded
  pools use `bonded_tokens_pool` and `not_bonded_tokens_pool`, the same names
  used by SDK staking. Running both modules would share bank accounts while
  maintaining incompatible ledgers. Module permissions and ownership must be
  switched as one reviewed integration change.

Multisig wallet lookup/update is an explicit native keeper dependency. A dummy
wallet service or a generic group/authz substitute is not a complete port.
The same applies to native fee, emission and reward authorities. Fresh genesis
must receive operator-supplied authorities; no mainnet owner, key or balance is
an acceptable default.

## Existing aliases that are unsafe to treat as parity

[alias_functions.go](../../x/validator/keeper/alias_functions.go) has commented
callback bodies in `IterateBondedValidatorsByPower` and `IterateDelegations`.
`TotalBondedTokens` returns zero. The native validator's
`GetBondedTokens` and SDK-shaped `GetConsensusPower(sdk.Int)` also return zero
as TODO implementations in [types/validator.go](../../x/validator/types/validator.go).
The legacy SDK-shaped `GetValidatorByConsAddr` constructs a new SDK validator
without faithfully copying its complete native status/power state.

These are existing integration gaps, not rules to reproduce as financial
behavior. Modern governance expects `TotalValidatorPower`, validator iteration
and delegation shares, so changing function signatures around these stubs would
only produce a misleading compiling adapter. A voting policy for multi-asset
and NFT-backed native stakes must be explicit before that integration is enabled.

Native actual consensus power is `Validator.ConsensusPower()` /
`PotentialConsensusPower()`, driven by the native computed `Stake` field.
It must not be replaced by the zero SDK-shaped methods or recalculated from a
generic single-denom SDK ledger.

## Scoped read-only adapter design

After native schemas and state invariants are available, the adapter can expose:

1. Native historical lookup and native consensus-address lookup, using the
   fully hydrated native getter. Missing entries and malformed pubkeys return
   explicit errors rather than empty successful validators or headers.
2. Exact operator/consensus pubkey, jailed/status mapping and header fields.
   Existing historical projection uses native consensus power multiplied by
   the EVM SDK power-reduction constant solely as an SDK validator container
   for conversion back to a Comet validator. It must be tested to reproduce
   the same consensus power/hash. This container is not a real token balance,
   minted asset, delegation share or governance weight.
3. A fixed `d0valoper` address codec and a base denom validated against native
   params/coin state and network identity. Do not silently fall back to `stake`.

Give this object only to the narrow EVM/ERC20 consumers. It must have no bank
mint/burn/send authority or stake mutation methods and must never write SDK
staking state. Do not expose projected token containers as a complete standard
staking financial API.

The native schema is not yet present in the modern module, so a production
implementation against invented structs or the current temporary SDK keeper
would not establish this boundary and is intentionally deferred.

## Lifecycle and EVM call integration

Legacy native BeginBlock consumes last-commit signatures and Byzantine evidence,
tracks history and applies native downtime/double-sign rules. EndBlock computes
native validator updates and rewards; periodic work includes transactional
reward payout, hold processing and auto-unbond. Port those operations to the
modern application lifecycle with the same ordering and failure boundaries.
Use modern vote flags correctly: an absent or nil vote is not a signed commit.
Preserve Comet's validator-update delay and verify headers at the boundary.

Do not also run generic SDK slashing/evidence handlers over the same infraction
or generic distribution over the native reward path. Disable or replace the
temporary modules as a coherent group after native tests pass.

The modern EVM `CallEVM` interface has explicit `StateDB`, `callFromPrecompile`
and gas-cap arguments in addition to the old ABI/from/target/commit arguments.
An app-owned bridge must distinguish an internal system call from an actual
precompile call and preserve the caller's transactional context. Creating a
separate state DB or committing outside the parent's cache can defeat rollback.
Test real native writes, errors, gas limits and nested rollback before use.

Modern `PostTxProcessing` also receives an authenticated sender separately.
Port event-envelope validation and decoding against the actual new signatures;
do not trust a declared sender or replace malformed-event failures with success.
The existing fresh-genesis bootstrap blocker remains relevant if native hooks
unconditionally require a missing ContractCenter. Enabling the native hook
requires authenticated, complete operator genesis/deployment state or a reviewed
native binding design. Empty fallback addresses and mock system contracts do
not solve production bootstrap.

## Required acceptance evidence

- Adapter tests use the real regenerated native validator/history schema and
  verified native store, including missing/invalid records. Reading the adapter
  leaves bank supply, balances, native delegations and SDK stores unchanged.
- Native validator updates, historical projected sets and Comet header hashes
  agree across activation/jail/unbond transitions and the two-block update delay.
- Native coin reserve valuation, NFT/subtoken stake and hold accounting,
  commission, rewards, penalties and multi-asset delegation outcomes match the
  intended native rules. No automatic voucher-to-voting-token conversion occurs.
- Actual signed native/EVM messages exercise the real ante, authorities and fees;
  all touched native, bank and EVM state rolls back together on failure.
- Repeat the bounded precompile financial rollback regression after replacing
  temporary SDK staking with native financial operations.
- Fresh production-tag node processes advance through real commit votes and
  restart correctly; unit contexts with empty last-commit votes are insufficient.
- Complete external-relayer handshakes/send/return/refund/restart against an
  independent SDK counterparty, using real native unbonding/validator metadata.

Until these gates pass, the current scaffold, read-only projection design and
successful generic SDK tests must not be described as complete native Decimal
validator integration.
