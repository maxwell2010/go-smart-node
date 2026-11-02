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
