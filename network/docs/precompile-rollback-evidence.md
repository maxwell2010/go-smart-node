# Precompile rollback evidence for the tagged reference

The Go report [GO-2025-3684](https://pkg.go.dev/vuln/GO-2025-3684)
has an unreviewed all-versions range. The
[maintainer advisory](https://github.com/cosmos/evm/security/advisories/GHSA-mjfq-3qr2-6g84)
identifies affected version 0.1.0 and a fix in 0.6.0. Preserve this metadata
conflict rather than equating a symbol finding with demonstrated exposure in
the selected version 0.7.3.

The [exact ancestry comparison](https://github.com/cosmos/evm/compare/0fff8c144b24effbcb3addd666150ba5989d631c...c3ae9067a7eadcdea55a5192226916deb44c71c4)
places fix 0fff8c144b24effbcb3addd666150ba5989d631c in the tagged source's
ancestry, with zero commits behind and 454 ahead. In that source,
`precompiles/common/precompile.go` takes the SDK multistore snapshot and records
the precompile journal entry before flushing or invoking the native action.
The journal is therefore present when the enclosing EVM scope reverts.

The owned application's ordinary production-build test
`TestFreshOwnApplicationSignedNativeEVMTransfer` invokes
`checkBoundedFinancialRollback` in `app/financial_rollback_test.go`. It executes
the pinned upstream `StakingReverter` fixture through signed Ethereum
transactions and actual FinalizeBlock/Commit calls. Two successful control
delegations first establish that financial native calls really execute. A
caught child failure and a reverted parent then preserve delegation shares,
validator tokens, bank/EVM balances and the previously committed contract
counter. Reverted financial SDK events and EVM logs do not survive. Each outer
transaction is checked against its exact gas fee; the successful outer caught
case still commits its own counter.

This is bounded regression evidence for the current application and temporary
canonical SDK staking. It does not establish correctness of future Decimal
native hooks, every precompile or a different dependency family. Repeat it
after the supported-family migration and native validator port. The original
scanner captures remain unchanged, and the manifest-bound vulnerability gate
still fails while findings await individual reviewed disposition. No blanket
scanner exception is introduced by this document.
