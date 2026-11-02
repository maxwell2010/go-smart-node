# Scoped SDK53 tally lifecycle

`Sessions` can be retained as immutable application configuration. Unlike the
low-level `Adapter`, it contains no cached voting snapshot. Each snapshot lives
only inside a private value attached to one SDK context scope.

The lifecycle follows the actual SDK53 default tally contract:

1. `IterateBondedValidatorsByPower` begins a fresh validated ownership snapshot.
2. `IterateDelegations` reuses its owner/validator index for each voting account.
3. `TotalBondedTokens` checks the complete native fingerprint once more and
   closes that tally before returning its voting-weight denominator.
4. The next proposal's validator iteration builds a new snapshot, even at the
   same height after a prior proposal changed native state.

Chain ID, height, block time, reader identity and the actual native KV cache
branch must remain consistent during a tally. Calls outside an open scope,
wrong-order/reentrant/concurrent reads, cache crossing and unclosed tallies fail
closed. Read errors are sticky for the scope, even when a callback ignores them.
Cleanup invalidates leaked contexts and clears snapshots on success, error or
panic; there is no global weight cache.

## Explicit native branch identity

The SDK's cache multistore is a value containing maps and cannot be compared
directly. Supply the genuine native KV branch pointer without inspecting private
SDK fields or comparing store contents:

```go
reader, err := governance.NewSessions(nativeKeeper, ownershipSource, votingScale,
    func(ctx sdk.Context) any {
        return ctx.MultiStore().GetKVStore(nativeStoreKey)
    })
```

The configured native store key must be mounted in that application context.
The function must identify the actual native branch, not return a global token,
store key object or arbitrary shared pointer. Nil functions are rejected during
construction. Nil/non-pointer identities, lookup failures and unsupported
identities are rejected before starting a tally. Real SDK parent/child cache
crossing is covered by the regression suite. No statekeeper API changes or
version-specific production store imports are needed.

## Staged canonical wrappers

Construct the canonical SDK53 governance keeper with this reader. Then:

- `reader.EndBlock(ctx, keeper)` runs the unmodified SDK `gov.EndBlocker` inside
  a session and an outer SDK cache. Successful canonical processing commits;
  a fatal late tally error discards earlier proposal and native hook effects
  from that module invocation.
- `reader.QueryServer(keeper)` forwards canonical v1 queries and wraps live
  `TallyResult` in an always discarded cache. Canonical live tally consumes its
  vote rows in the supplied context; this wrapper guarantees repeated direct
  queries preserve stored votes.
- `reader.Run` and `reader.ReadOnly` expose the same scoped execution/query
  boundaries for reviewed integration code. Callers must propagate canonical
  SDK errors. A successful scope can still be discarded by its outer caller's
  SDK transaction cache.

The canonical keeper and wrappers must use the same reader instance. The
wrappers do not register app modules, authority, message handlers or RPC routes.
The query wrapper covers the v1 query server; any legacy query registration
needs an equivalent deliberate session boundary before activation.

## Bounded work and verification

Full native exports/fingerprint checks occur a constant number of times per
tally, not for each voting account. Delegation lookups use the frozen ownership
index and perform no native KV reads. The actual SDK53 tally fixture measures
21 native KV `Get` calls for both 1 and 51 voters and one ownership-source call
per tally. These counts describe the fixture, not all database/gas operations;
snapshot work grows with native state and deterministic indexing/sorting.

Actual SDK53 tests cover consecutive proposals at one height, fresh weight after
a prior proposal's committed native hook write, canonical EndBlock processing,
late failure rollback, repeated read-only tally queries, vote preservation,
scoped retries, malformed branch identities and read-count independence from
the number of voters. Core lifecycle tests also run on the retained SDK54 graph.

This completes the staged per-tally lifecycle boundary, not application wiring.
The base DEL/NFT-reserve policy and all limitations in [README](README.md) remain.
Custom-asset valuation must provide authoritative same-context inputs and a
coherent fingerprint/revision contract for every additional valuation dependency.
Cross-module ownership/backing, native power lifecycle, removal of the writable
SDK staking scaffold and governance authority execution remain separate gates.

Verified source contract:

- [SDK53 tally order](https://github.com/cosmos/cosmos-sdk/blob/v0.53.8/x/gov/keeper/tally.go)
- [Canonical proposal loop and execution](https://github.com/cosmos/cosmos-sdk/blob/v0.53.8/x/gov/abci.go)
- [Canonical tally query](https://github.com/cosmos/cosmos-sdk/blob/v0.53.8/x/gov/keeper/grpc_query.go)
