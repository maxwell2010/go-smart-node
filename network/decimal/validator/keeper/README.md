# Native validator transactional state foundation

This keeper stores native Decimal validator metadata, separate `ValidatorRS`
rewards/power, consensus-address indexes, custom-coin/NFT delegations and holds,
genesis queues, previous-power records and offline trackers. It uses SDK cache
contexts and serializes financial messages instead of cloning their math fields.
Writes to metadata, dynamic rows and indexes commit together. Returned records
are independent decoded values. Genesis requires an empty dedicated store and
imports all records atomically; export checks native schema consistency again.

The layout is for a fresh network. It is not a legacy database migration. The
record limits are 64 KiB per ordinary record and 2 MiB per historical record;
these staging limits require reviewed genesis policy before application wiring.
Export includes native financial queues and offline/power trackers. Historical
headers are a retained runtime cache, omitted from the native genesis schema.

`SetDelegation` stores the native total reserve and its held portions without
adding holds twice. It does not transfer assets or calculate custom-coin/NFT
conversion prices. `SetValidator` accepts validated, already computed native
integer power. It does not invent SDK staking tokens or delegation shares.

Delegation updates use bounded `ApplyDelegationBatch` compare-and-swap records.
It checks the exact previous records, validator counts and unique NFT-unit
indexes, removes old indexes before final-state reassignment, and commits all
changes in one cache. The hot path uses point reads rather than exporting every
unrelated position. Genesis/import builds the indexes; export checks both their
contents and populations. Marker06 and prefixes23/24/25 are a fresh-layout
change requiring explicit migration for older development stores. These indexes
cover active delegation records, not the separate pending-custody binding ledger.
They authorize no financial operation and do not calculate voting power.
Consensus-key identity is immutable through this ordinary metadata setter.

`ConsensusSet` builds actual Comet validators directly from native consensus
keys and integer power. `SetHistoricalInfo` requires an explicitly supplied
effective set and verifies its Comet validator hash against the header. Current
metadata cannot substitute for this effective set because consensus updates
have a delayed activation. Historical records are immutable, new heights must
advance, and pruning occurs in the same child transaction as insertion.

## IBC 10 and EVM integration boundary

The maintained-family source check used IBC 10.7.0, SDK 0.53.8, Comet 0.38.26
and Cosmos EVM 0.6.3. The schema/state foundation is first tested on the retained
SDK 0.54.4 family; compilation on the maintained family is a separate gate.
That separate focused gate also passes for the validator schemas and this keeper
on the maintained family. Only test import paths change for the split
`cosmossdk.io/store` and `cosmossdk.io/x/tx` modules; state code and native
financial semantics are unchanged. Both runs use Go 1.26.10 and whole module
verification before and after testing. This does not certify the complete
maintained-family application or every other native package.

IBC 10.7.0 core `NewKeeper` receives codec, store service, legacy parameter
subspace, upgrade keeper and authority. Its client keeper's expected interfaces
contain the upgrade keeper and parameter subspace, without a staking keeper.
The Tendermint light-client constructor receives codec and client-store provider.
Thus IBC 10 also does not justify a second writable SDK staking ledger. Its
legacy parameter-subspace constructor argument differs from IBC 11.

Verified upstream sources:

- [IBC core keeper](https://github.com/cosmos/ibc-go/blob/v10.7.0/modules/core/keeper/keeper.go)
- [IBC client expected keepers](https://github.com/cosmos/ibc-go/blob/v10.7.0/modules/core/02-client/types/expected_keepers.go)
- [Tendermint client module](https://github.com/cosmos/ibc-go/blob/v10.7.0/modules/light-clients/07-tendermint/light_client_module.go)

The native read-only projection provided here is:

| Query | Authoritative native source |
| --- | --- |
| `GetValidatorByConsAddr` | Validated consensus index and hydrated native validator record |
| `GetHistoricalInfo` | Effective native validator set with matching header validator hash |
| `ValidatorAddressCodec` | Explicit `d0valoper` codec |
| `BondDenom` | Native parameters |
| `UnbondingTime` | Native `UndelegationTime`, including caller's pending cache context |

The first two queries return **native** types. They do not yet implement EVM's
SDK-staking-typed return interfaces. Cosmos EVM 0.6.3 retains the narrow
historical/consensus identity/validator-codec/base-denomination interface checked
for 0.7.3. The future application adapter must project only genuinely supported
read-only identity/header fields. Generic SDK financial precompiles remain
incompatible with native staking semantics and must not be enabled through zero
token/share aliases.

SDK 0.53 governance additionally requires `TotalBondedTokens`, bonded-validator
iteration and delegator iteration through SDK financial interfaces. Native
multi-asset power and NFT reserves cannot be substituted with zero callbacks or
fabricated SDK token/share values. Governance voting-weight semantics and these
consumers need a deliberate native replacement before the scaffold is removed.

Relayer-facing unbonding discovery, including any compatibility implementation
of the SDK staking `Query/Params` route, must read native `UndelegationTime`.
Registering that read-only projection must not activate SDK staking genesis,
bonded pools, messages, slashing or distribution. Trusting-period policy still
depends on the future native unbonding/slashing state machine; this accessor
alone cannot establish IBC safety.

## Remaining activation gates

This package installs no application routes, BeginBlock/EndBlock handlers,
financial EVM hooks, reward payout, slashing, auto-unbond executor or authority.
Queue records currently enter through validated genesis and leave through
validated export; queue execution is not implemented here. Bank/NFT reserve
backing, native conversion prices, ownership, supply and reward reconciliation
remain cross-module financial responsibilities.

Before activation, replace the temporary SDK staking/distribution/slashing
scaffold as one coherent change, implement native lifecycle operations, account
for Comet's update delay, provide honest consumer/query adapters and verify
historical headers, unbonding and cross-module financial invariants on the
selected maintained stack. Two writable staking ledgers must never share pool
accounts or process the same delegation, reward or infraction.

The ordinary regression suite uses real SDK cache stores to check full native
genesis/export round trips, dynamic-row hydration, consensus indexing, outer
commit/discard, injected partial-write rollback, corruption errors, native Comet
power/hash projection, immutable history and transactional pruning. These are
state-foundation tests, not a completed consensus or IBC integration certificate.
