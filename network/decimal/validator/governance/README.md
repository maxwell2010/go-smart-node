# Native validator and delegator governance projection

This package implements the selected SDK53 governance **read** interface over
an immutable native tally-time snapshot. It creates no second staking ledger,
bank balance, token mint, redeemable shares, financial precompile or application
route. SDK governance retains its original quorum, threshold, weighted-vote and
delegator-override calculation.

## Explicit new voting policy

Validator voting weight is its hydrated native `ValidatorRS.Stake` integer power
multiplied by an explicitly supplied positive scale. Only bonded validators with
positive native power participate. Delegators divide that validator's influence
proportionally to complete same-height active native stake ownership.

For the concrete `BaseReserveSource`, ownership is the actual raw `del` amount
in each native position. NFT ownership uses its stored total DEL reserve, not
token/sub-token count. Holds are portions of that same total and receive no
additional governance bonus. Every coin/NFT/hold-containing position belonging
to the same delegator and validator is grouped into one ownership amount.
Pending undelegation and redelegation queue records are not counted again.

This is a new governance policy, not a reconstruction of historical per-position
consensus power. Native records contain validator aggregate integer power, but
no stored per-position power or aggregate raw-ownership field in `ValidatorRS`.
Legacy power updates involve truncation and changing custom-asset prices.
Therefore the adapter does not floor every position separately or pretend raw
ownership amounts equal aggregate native integer power. For example, one native
power unit split between 0.3 and 0.7 DEL owners still divides influence 30/70.
The validator's total voting weight remains authoritative native power.

SDK53's required API names are `TotalBondedTokens`, validator `Tokens` and
`DelegatorShares`, and delegation `Shares`. Here these are temporary voting-ratio
containers: total native power-weight, total ownership and grouped ownership.
They are neither stored nor minted as SDK financial shares/tokens. The normal
SDK tally deducts an independently voting delegator's ownership from validator
influence before adding the delegator's chosen vote. Validator self-voting is
deducted and added once through the same standard path.

The scale must be pinned as reviewed chain policy. It affects integer tally
reporting granularity; it must not be silently taken from mutable SDK defaults.
SDK decimal precision and final integer tally truncation remain unchanged.

## Authoritative ownership-source contract

`Build` reads validated native genesis/export records, including genuine dynamic
rows, then binds a request to chain ID, positive tally height and a digest of
all native records. The source receives independent protobuf bytes and must
return a nonempty reviewed valuation ID, the same snapshot identity, exactly one
nonnegative ownership amount per complete delegation-position digest and one
ownership aggregate per native validator. Position amounts must sum exactly to
those aggregates; grouped ownership must preserve the same amounts.

The base source derives those aggregates directly from validated native DEL
positions, including actual NFT DEL reserve totals. Zero reserve has zero
ownership weight. Positive native power with no ownership denominator is an
error; zero-power validators never acquire influence from NFT counts or raw
balances. Arithmetic bounds are checked before SDK tally multiplication.

Custom-coin stakes and NFTs with custom reserve denominations require an
authoritative same-context aggregate-valuation source. That source must define
and reconcile common ownership units using the actual native conversion,
reserve, supply and ownership state under an approved policy. No RPC quote,
spot-price fallback, invented exchange rate, equal NFT-count weight or missing
asset skip is supplied here. `BaseReserveSource` fails the entire snapshot on
such positions. A valuation ID is an audit identifier, not authentication of an
untrusted source; application configuration must choose a reviewed source.

Source reads execute in a disposable SDK cache. Their native-state writes cause
an error, and no child writes/events are committed. The low-level `Adapter`
verifies its native record digest on every read and is intended for an explicit
immutable tally-time snapshot, not a long-lived application dependency.

[Scoped sessions](SESSIONS.md) provide the staged SDK53 lifecycle: a fresh
snapshot at validator iteration, indexed ownership reads per voter and one final
native fingerprint check at total/close. Canonical EndBlock and read-only v1
tally-query wrappers are tested across multiple proposals and same-height native
changes. Application route/module wiring, production gas sizing and any legacy
query boundary still require deliberate integration; no global weight cache is
introduced.

## Verified scope and remaining gates

The `sdk53` test tag enables an actual SDK53 `GovKeeper.Tally` conformance test,
including the real default delegator override function and real SDK cache stores.
Tests cover validator/delegator voting, grouped DEL and NFT reserves, holds,
fractional ownership and a partial-unbond queue, zero-reserve NFTs, weighted
votes, conservation, missing/duplicate/stale valuation records, unsafe arithmetic
and attempted source writes. Snapshot/core tests also compile on the retained
SDK54 family; this package intentionally does not claim its different governance
staking interface is implemented.

No application wiring, proposal execution, upgrade/parameter authority or
system-contract execution bridge is installed. Cross-module reserve backing,
NFT ownership, native power lifecycle, custom-asset valuation and the coherent
removal of the writable SDK staking/distribution/slashing scaffold remain
required. A successful read projection and tally test do not establish those
financial or consensus invariants.

Final tests pass with Go 1.26.10 on SDK 0.53.8 / Comet 0.38.26 using
`go test -mod=readonly -p 1 -tags sdk53 ./decimal/validator/...`, including the
actual SDK tally cases. The retained SDK 0.54.4 validator subtree passes without
the `sdk53` tag. Whole module verification passes before and after both runs;
the selected-family module files remain unchanged. Only split-module test import
paths are adapted in the isolated SDK53 source copy.

Primary policy integration references:

- [SDK53 governance staking interface](https://github.com/cosmos/cosmos-sdk/blob/v0.53.8/x/gov/types/expected_keepers.go)
- [SDK53 default tally and delegator override](https://github.com/cosmos/cosmos-sdk/blob/v0.53.8/x/gov/keeper/tally.go)
- [Native legacy aggregate valuation](../../../../x/validator/keeper/utils.go)
- [Native legacy custom-price and reserve weighting](../../../../x/validator/keeper/reward.go)

The explicit custom-asset ownership source is now staged in
`AggregateReserveSource`; see [its policy, fingerprinting and limits](CUSTOM_OWNERSHIP.md).
It retains the required separation between native power, voting ownership and
actual redeemable assets. It does not complete application authority wiring.
