# Separate-network application scaffold

This is an owned ABCI application derived from the canonical Cosmos EVM
`evmd` application at commit `c3ae9067a7eadcdea55a5192226916deb44c71c4`,
under the Apache-2.0 license retained in this directory. It wires EVM,
IBC11 clients/router, ICS20 and the upstream callback middleware directly.
It does not wrap an upstream `NewExampleApp` instance.

The constructor and InitChain reject legacy Decimal signing domains and require
matching `mintcandy_<evm-id>-<revision>` and EVM IDs. SDK address prefixes are
fixed to `d0` and sealed. Default genesis uses `del`, has no preminted balances,
sample wrapped-token pairs or cloned mainnet owners. Generic SDK mint inflation
is inert. Operators must explicitly provide distribution and authorities.

The current application still uses canonical SDK staking, distribution and
slashing as temporary constructor dependencies. It does not implement Decimal
coin reserve semantics, NFT delegation, custom validator voting, fees or the
full financial module port. These are blocking integration requirements, not
features supplied by the generic SDK modules. No public launch is authorized
by a passing scaffold test.

`go test -mod=readonly ./app` constructs this application and initializes a
fresh funded test genesis. A genuinely signed native EVM transfer passes the
outer ante and FinalizeBlock path; the test checks exact recipient credit,
serialized EVM result and gas fee debit. Test funding uses ephemeral keys and
does not modify default operator genesis. No network connections are opened.

The upstream EVM chain configuration is process-global. Production builds
cannot instantiate two different EVM configurations in one process. Future
interchain process tests must use separate disposable daemons. Any use of the
upstream `test` build tag to reset configuration in a unit harness must be
identified as such, and cannot replace the process-level release gate.
