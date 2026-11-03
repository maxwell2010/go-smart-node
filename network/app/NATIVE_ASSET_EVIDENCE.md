# Native asset store and query integration evidence

Runtime source: `be6274ded4625d6849c579008d0a15be797d6851`.
Default-build Linux amd64 `mcnd` SHA256:
`2b26dfb6d170ed493b487a97fd1a5a505825ace0103db593a09bcf5d579ce8fd`.
Built with Go1.26.10, GCC/CGO and the selected SDK53 graph at that source.
Later dependency changes require their own combined validation.

The bounded process test ran in a private Linux network namespace and a new
owned home, without public transactions or existing node homes. It explicitly
reconciled native allocation after funding genesis, signed the validator gentx,
reached height3, queried the real native coin Params and empty NFT collections,
sent a native bank transfer with exact value/fee/shared SDK-EVM nonce checks,
then reopened the same disk home to height5 and repeated native queries.
A mismatched signing-domain restart failed with home bytes/metadata unchanged.
The `rpc-ibc` development history profile was enabled; its million-block
retention boundary was not exercised. All owned processes exited afterward.

Additional default-build application tests cover real mounted native stores,
read-only gRPC routes, forward/reverse key pagination, complete NFT metadata,
inherited versus explicit-zero reserves, cache discard and metadata
export/reimport. Nonempty records in that test are explicitly isolated cache
fixtures before application initialization. Full application admission rejects
such nonempty asset genesis until authenticated contract bootstrap and backing
reconciliation exist. No financial native Msg handlers were installed.

Explicit allocation tests reject mismatched bank/native declarations without
startup repair; the standalone builder preserves SDK AppGenesis metadata,
writes a protected backup, is idempotent at the same amount, and rejects
existing chain data. Validation checks an explicit supplied file as well as the
default home. Full application export reconciles allocation in the exported
copy without changing runtime native parameters; mismatched subset exports fail.

The App, CLI, native module, actual system-contract and bounded EVM view suites
passed. Independent relayer lab guards passed4/4, operator tools passed8/8
on Linux. Whole module verification and both owned-source verifiers passed
before building this daemon. This does not certify native contract-event
financial transitions, NFT authority/binding, full production governance,
historical-header monetary queries, major upgrades or production resource bounds.
