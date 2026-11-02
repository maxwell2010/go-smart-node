# Fresh-network EVM bootstrap release gate

The disposable ICS20 genesis is not a complete Decimal network genesis. It has
funded accounts, a native validator set and IBC modules, but no deployed Decimal
ContractCenter, MasterValidator or delegation system contracts. Do not use this
fixture to launch a public network or advertise working EVM transfers.

## Reproduced behavior

`TestExperimentalFreshGenesisEVMBootstrap` creates a genuine Ethermint account
in fresh genesis and signs a native EIP-155 Ethereum transfer for chain 900001.
It submits the canonical Ethereum extension transaction through BaseApp's actual
DeliverTx path, including signature verification and the Decimal fee decorators.
Strict reliability hooks are activated at height 1. No mock contract is installed.

The SDK envelope is successful, but the EVM response reports
`VmError="failed to execute post processing"`. The registry lookup fails because
the fallback ContractCenter address
`0x481487AEafc60512233a08Da240FC7AF99c0f696` has no account. The recipient
receives nothing and the sender still pays the actual execution fee. With the
current genesis fee parameters the observed debit is 95238000000050000 raw DEL
for 50000 gas. Fees and gas are measured rather than bypassed.

The pinned Decimal Ethermint v0.22.13 additionally overwrites `PostTxError` with
the literal `test` in `x/evm/keeper/state_transition.go`. The test therefore uses
the VM error and an independent registry lookup to establish the failure.
The dependency must be repaired or replaced with a reviewed version; treating
the literal as a useful transaction diagnostic is unsafe.

Run the passing regression that records this limitation:

```powershell
go test -mod=readonly -tags experimental_ibc -run '^TestExperimentalFreshGenesisEVMBootstrap$' -v ./app
```

Run the currently failing release gate that requires a usable fresh EVM network:

```powershell
$env:DSC_REQUIRE_FRESH_EVM_BOOTSTRAP='1'
go test -mod=readonly -tags experimental_ibc -run '^TestExperimentalFreshGenesisEVMBootstrap$' -v ./app
```

Normal CI must not mistake the passing regression for bootstrap readiness.
The opt-in gate currently returns failure by design. All commands require the
same supported Go/CGO toolchain as the other EVM tests.

## Required bootstrap design and evidence

Before implementation, choose whether this is a separate sovereign chain or a
Decimal-compatible upgrade. A sovereign network needs its own chain identity,
genesis distribution and explicit system-contract authorities. An upgrade must
retain the deployed contracts, balances, validator state and agreed activation
height, with historical replay. A fresh empty genesis cannot substitute for that
upgrade state.

For either architecture, review and pin the actual contract source, compiler,
creation/runtime bytecode hashes, dependency graph and initialization order.
Define registry addresses in consensus configuration for the exact chain IDs;
do not silently inherit a fallback deployment belonging to another network.
Specify who initially owns and upgrades the registry and each system contract,
how ownership transfers to the final authority, and how that transfer is proved.

Bootstrapping through ordinary Ethereum deployment transactions cannot work if
strict hooks require the registry before the first deployment. Resolve this
circular dependency through a reviewed, deterministic genesis allocation or a
consensus upgrade handler that installs the complete real initialized state.
A bootstrap phase is an alternative only with a fixed consensus schedule,
closed authorization rules and an irreversible audited finalization. Do not
add a node-local switch that changes consensus validation.

Verify the resulting registry resolves every required contract, each account
has the expected runtime hash and initialized storage, and authority changes
cannot be replayed or bypassed. Prove native EVM transfer and contract creation,
validator registration and voting-power updates, coin/NFT delegation and
unbonding, commission distribution, event-error rollback, restart and upgrade
preservation through real signed transactions. Include deliberately missing,
incorrect and unauthorized deployment states as failing cases. Keep ICS20
send/return/refund and external-relayer tests passing against this same complete
genesis before considering a full release.

## Repository artifact inventory

The current tree does not contain enough information to construct authentic
bootstrap fixtures. `contracts/center/DecimalContractCenter.json` and
`contracts/validator/DecimalMasterValidator.json` contain only ABI arrays.
The same is true for DelegationNFT, TokenCenter, NFTCenter and the token/NFT
implementation artifacts. Their generated Go metadata provides ABI, but no
deployment bytecode or deployment function. No production Solidity source or
deployment scripts accompany these files.

The exception is `contracts/delegation/DecimalDelegation.json`: it contains
Hardhat creation bytecode (23268 bytes) and runtime bytecode (23230 bytes), with
empty link references. Its artifact SHA256 is
`f4de3095b0bb70a689f742c2c3c5beb1da630114fd69195a2b6b31e0094aed59`.
It names `contracts/DecimalDelegation.sol`, but that source and compiler build
information are absent. The bytecode alone cannot establish the full network
deployment or its storage initialization and authority guarantees.

The following dependency edges are inferred from ABI and node usage, and must
be confirmed against the real source and deployment scripts:

| Component | Evidence | Bootstrap requirement |
| --- | --- | --- |
| ContractCenter | `getContractAddress`, `setContractAddress`, `owner`, `getImpl`, `upgrade` | Real deployment/proxy code, exact address, implementation, initialized owner and registry entries |
| MasterValidator | `addValidatorDEL`, `getValidator`, `owner`, `getImpl`, `upgrade`; node queries registry slug `master-validator` | Real implementation/proxy and initialization, validator metadata consistent with native genesis, authority rules |
| Delegation | `getContractCenter`, `setContractCenter`, `WETH`, `getFreezeTime`, `getImpl`, `upgrade` | Registry pointer, wrapped native token dependency, withdrawal configuration, real implementation/proxy storage |
| DelegationNFT | `setContractCenter`, `getContractCenter`, `getFreezeTime`, `getImpl`, `upgrade` | Registry pointer, NFT implementation dependencies and authority initialization |
| TokenCenter / NFTCenter | `initialize`, `setContractCenter` and node registry slugs | Center initialization, implementation addresses, metadata and reserve consistency |
| Wrapped native token | Delegation exposes `WETH`; registry slug `wdel` | Actual source/artifact, constructor/initializer, native backing and accounting rules |

`getImpl` and `upgrade` indicate implementation indirection, but ABI alone does
not identify the proxy standard or storage slots. Writing guessed storage slots
or putting a replacement registry contract at the fallback address would hide
the bootstrap defect. Do not use the reliability regression mock contracts as
deployment fixtures.

The next required inputs are the pinned system-contract repository including
proxy sources, compiler/optimizer settings and build metadata; a reproducible
deployment manifest; initialization and ownership-transfer calldata; and the
intended isolated-network authority model. Once obtained, create fixtures by
executing the actual deployment/initialization code in an isolated genesis
builder, verify hashes and storage through public getters, and export the
resulting authentic EVM allocations. Only then rerun the strict fresh EVM gate
and the broader validator/delegation integration matrix.

## Read-only verified-source investigation

The official explorer publishes source and compiler metadata in its page data.
The [mainnet registry page](https://explorer.decimalchain.com/contracts/0xc108715a06f76caa96fa2c943ebf05159c29a87d)
contains the `DecimalContractCenterProxy` flattened source. Its metadata states
Solidity 0.8.20+commit.a1b79de6, optimizer enabled with 200 runs, Paris EVM and
an ERC1967Proxy dependency from OpenZeppelin 5.0.2. The source SHA256 is
`278769c90cdd676abbf24742655e4563be9256806e180578ba8f81ac0b9efb6c`.

The [migration implementation page](https://explorer.decimalchain.com/contracts/0x87e94ae0c62d9078afdda9aadb42c80d21ac5ef5)
contains `DecimalContractCenterMigration`, including the underlying registry
implementation and its named storage location. Its source SHA256 is
`52b27671e415837eb81c7d4f4aa17ee5e120d8a29fcceddcb9a837df7790d790`.
These are explorer-published artifacts; independent bytecode reproduction and
current implementation identification remain required.

The proxy creation payload references a separate setup contract at
`0x607b332529967c428b522fdc353b7299a9ade72c`. Its explorer page did not expose
source verification data during this investigation. The verified registry
implementation itself has no public initializer that initializes ownership.
Consequently a direct deployment of that implementation and proxy is not a
complete authenticated bootstrap recipe. Obtain or independently reconstruct
and audit the setup source before producing a fresh deployment fixture.
Do not reuse the mainnet owner or migration calldata in an isolated genesis.

Read-only Sourcify full-match and partial-match queries for the registry at
chain ID 75 both returned 404. No runtime state or keys were copied from mainnet.

### Pinned implementation inventory

Read-only calls to the official `https://node.decimalchain.com/web3/` endpoint
at block `0x20cdcb6` resolved the following deployments. These addresses identify
research inputs and must not become fresh-network authorities or allocations.

| Component | Registry target | Implementation |
| --- | --- | --- |
| ContractCenter | `0xc108715a06f76caa96fa2c943ebf05159c29a87d` | `0x642b69a1adb72ab26b8d911887811f0e2ba11490` |
| MasterValidator | `0x630b03ff9eed4c4a468da9f481df23f542070aa4` | `0xefedc75ee88d2a4e8163506ed769ebbacaa788d5` |
| Delegation | `0xa16c34ed1c0601c0e749e17ebef19752a15faa01` | `0x2f29724dcf9e3fe2864db03c5310cb368aa96f09` |
| DelegationNFT | `0xe45adfcc739a0d10ce9462b58866c9a1a06035e2` | `0xaffe98bbd4c74ccc95d7d99a684bd83fecaece6c` |
| TokenCenter | `0x9113ba675aa8f2ef0c068cee2cdabab95b6437fb` | `0x0d01189be8dcab77b088a21353deff979dc83788` |
| NFTCenter | `0x443cc8ac24630be9257483a09c34ffa81608eace` | `0xced55da70f42ab101b4ce10aa9e767607cef5146` |
| Wrapped native | `0x1c5d8992da64c8d56ea413dd6f723061c29a7c0b` | No `getImpl()` result |

For the registry, `getImpl()` and the ERC1967 implementation storage slot agree.
The official explorer publishes flattened sources and metadata for all six
proxies and implementations above; wrapped native has no published source in
its explorer page. The Delegation implementation links `DelegationNFTLib` at
`0x940171137d5ffddd440a6fd17bbbd64a2001199b`; its flattened source includes
the library. A fresh deployment must deploy and relink this library.

An isolated local Solidity 0.8.20 compiler, using each artifact's published
settings, reproduced TokenCenter's complete on-chain runtime exactly. The
other proxies and registry, MasterValidator, Delegation and DelegationNFT
implementations matched executable runtime after removing the final CBOR
metadata trailer. Their metadata trailers differ, so full bytecode identity
has not been established. NFTCenter additionally differs in an embedded IPFS
metadata hash near byte 16526 of its 16686-byte runtime. Preserve this distinction
when deciding which artifacts are suitable for a reproducible genesis.

The setup runtime is 2706 bytes and its CBOR metadata identifies Solidity 0.8.20
with IPFS metadata CID `QmYmajmN1bdCBisPJ9ztfudvBm7HrbJ7q3CpgMDNVioiCU`.
Attempts to retrieve that metadata encountered HTTP 403, timeout and TLS errors
at public gateways. Certificate validation was retained. The official public
RPC returned `null` for the registry creation transaction hash exposed by the
explorer, so its historical input could not be independently retrieved there.
No expensive execution trace was requested.

Remaining external blockers are the setup source or verifiable deployment
recipe, wrapped native source/artifact and initialization, complete authority
initialization for each proxy, and an exact reproducible compilation manifest.
Known published sources materially narrow the investigation but do not resolve
the circular dependency between strict hooks and first deployment transactions.
