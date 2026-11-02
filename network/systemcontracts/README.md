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
setup contract. Before application integration, prove actual deployment,
ownership, initialization and registry operations in the selected EVM and then
the owned ABCI application. Validator, delegation, token/NFT centers and wrapped
native contracts, linked libraries, bank backing and the final authority
manifest remain separate required steps.

## Reproduction and observed scope

`SOLC=/absolute/path/to/solc python3 build.py` requires the exact official
Linux Solidity 0.8.20 compiler with SHA256
`0479d44fdf9c501c25337fdc540419f1593b884a87b47f023da4f1c700fda782`.
The [official compiler artifact](https://github.com/ethereum/solc-bin/blob/gh-pages/linux-amd64/solc-linux-amd64-v0.8.20%2Bcommit.a1b79de6)
is a source-reproduction pin, not a general recommendation to use an old
compiler for unrelated new contracts. Standard JSON settings are checked in;
there are no mainnet library links or network imports during compilation.

The compiled targets are the base `DecimalContractCenter`, its genuine proxy,
and the new setup implementation. The source also contains historical migration
contracts; those are not the selected fresh-registry implementation. Original
Decimal and OpenZeppelin MIT notices are retained alongside the sources.

The bounded Go test passes on the selected Cosmos Geth 1.17.2 reference: real
EVM creation installs the exact artifact runtimes, the proxy exposes the explicit
owner and final implementation, owner registry updates work, direct/repeated
setup is rejected, and a zero-owner constructor leaves no proxy code. These
are genuine EVM constructor/delegatecall checks without a mock registry. They
do not exercise signed ABCI transactions, native fees, a complete system genesis
or the newly selected maintained-family Geth version; those gates remain open.
