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
