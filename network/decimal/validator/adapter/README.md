# Narrow native EVM metadata adapter

`EVM` implements exactly the four methods required by Cosmos EVM 0.6.3's
`x/vm/types.StakingKeeper`: historical information, validator lookup by consensus
address, validator address codec and bond denomination. It wraps the native
state keeper privately, installs no routes and exposes no financial mutators,
governance iterations, bank minting or SDK staking ledger.

Current validator lookup reads the genuine native metadata and separate
`ValidatorRS` reward/power row through the transactional keeper. Missing or
invalid financial rows remain errors. Native rewards have no corresponding
field in the SDK metadata return type and are not invented or redistributed.
All context-dependent reads use the caller's SDK cache context. Addresses remain
honest `d0valoper` strings; the codec decodes their 20-byte identity explicitly.
The adapter rejects native parameters whose bond denomination is not `del`.

## SDK return types are metadata containers

The constructor requires an explicit positive, bounded power-reduction scale
and copies it independently of mutable SDK global defaults. The returned SDK
validator's `Tokens` field contains **native integer power multiplied by this
scale solely to reproduce native `ConsensusPower(scale)`**. It is not an asset
balance, bank supply, escrow amount, mint authority or a count of staking shares.
The scale is bounded before multiplication so every allowed native consensus
power fits the SDK integer container.
Application integration must pin the same scale for every consumer that calls
SDK power helpers on these metadata values. A different global reduction must
not be used to reinterpret the container as native power or token supply.

No SDK delegation-share, minimum-self-delegation or commission-change policy
is manufactured. Those unsupported financial fields remain unset. The SDK
struct exposes SDK financial methods because of its upstream type, but callers
must not use those methods to derive native economics. The adapter itself does
not implement the SDK governance staking interface or financial precompile
interfaces. Do not store these projected SDK structs as a financial ledger.

History comes from the native keeper's explicitly recorded effective consensus
set. It does not hydrate historical power from current `ValidatorRS` rows. The
adapter preserves native power/consensus-address ordering and independently
recomputes the Comet validator hash through the projected SDK power containers.
It preserves the header and effective set across delayed validator updates.
Generic SDK historical address-order helpers must not replace Comet's actual
power/address ordering. Returned headers, keys and power containers are copies.

## Consumer boundaries still blocking application wiring

SDK staking/distribution/slashing precompiles and the temporary writable SDK
staking scaffold must be removed or replaced coherently before this adapter is
connected to the application. SDK53 governance requires native policy for
bonded voting weight and delegation iteration; no zero callbacks or fabricated
token/share economics are supplied here. Native consensus lifecycle, financial
execution, reward/slashing/unbonding invariants and relayer-facing queries remain
separate activation gates.

There is also a concrete upstream consumer issue to resolve deliberately:
Cosmos EVM 0.6.3 `x/vm/keeper/block_proposer.go` builds coinbase from
`[]byte(validator.GetOperator())`, while SDK53 returns a Bech32 string from
`GetOperator`. Its validator-account gRPC query correctly decodes that string
with `ValidatorAddressCodec`. The adapter preserves correct address semantics;
it must not put raw address bytes into a Bech32 metadata field to hide this
consumer mismatch. Correct coinbase decoding requires a reviewed consumer-level
change and integration test before application wiring.

Primary source references:

- [EVM 0.6.3 staking interface](https://github.com/cosmos/evm/blob/v0.6.3/x/vm/types/interfaces.go)
- [EVM proposer consumer](https://github.com/cosmos/evm/blob/v0.6.3/x/vm/keeper/block_proposer.go)
- [EVM validator-account query](https://github.com/cosmos/evm/blob/v0.6.3/x/vm/keeper/grpc_query.go)
- [SDK53 validator metadata and power helpers](https://github.com/cosmos/cosmos-sdk/blob/v0.53.8/x/staking/types/validator.go)

The ordinary SDK cache tests cover native dynamic-row hydration, context
commit/discard, independent return values, exact metadata power, historical
Comet hash and update delay, explicit codec/denomination/scale bounds and absence
of financial/governance APIs. These checks establish the narrow projection, not
a complete native consensus, governance or IBC integration certificate.

The adapter, native state keeper and native schema tests pass on the selected
SDK 0.53.8 / Comet 0.38.26 / EVM 0.6.3 family and the retained SDK 0.54.4 /
EVM 0.7.3 family with Go 1.26.10. Whole module verification passes before and
after both final runs; the selected-family `go.mod` and `go.sum` remain unchanged.
The selected-family test source only adapts split store/transaction module import
paths. The adapter's production code and native financial semantics are shared.
