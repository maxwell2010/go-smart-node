# Same-context coin view

`Reader` implements `GetCoin(sdk.Context, string)` and `GetParams(sdk.Context)`
for valuation consumers. Native coin records remain validated metadata with
monetary limits. The returned custom coin is hydrated using actual EVM
`totalSupply()` and the SDK bank DEL balance of that token address in the same
context. Direct native transfers or forced EVM credits can change that balance
without a ReserveUpdated event, so cached metadata is not an authoritative
reserve for governance valuation.

The bank is the same 18-decimal DEL ledger used by this application's EVM.
Reserves are not minted, wrapped, mirrored or added to native supply here.
Base DEL is rejected: its ownership uses native amounts directly. Actual values
must still satisfy the stored reviewed coin limits; the reader never silently
loosens limits following an upgrade or substitutes an RPC quote.

Application configuration must provide an approved `BindingSource`, based on
authenticated chain deployment/governance state. Each binding specifies the
native token address and expected runtime hash. EIP1967 proxies also require
their exact current implementation address/hash. An occupied implementation
slot without that approval fails closed. Other proxy/forwarding schemes require
separate review; an arbitrary runtime must not be admitted just because its
hash can be read. This interface is not an authentication mechanism for an
untrusted binding provider.

Construct with the compiler-pinned `systemcontracts.CompiledABI("DRC20")`.
The selector is copied; no mutable ABI map or financial result is cached.
Execution uses an actual bounded `core.Message.GasLimit` and `ApplyMessage`
with `commit=false`, in a discarded child SDK context with its own finite gas
meter. It does not call upstream `CallEVM` with an ineffective gas-cap argument.
Both native-store work and EVM gas, including failed EVM calls, are charged to
the caller's SDK meter. SDK out-of-gas in the view is returned as an error;
exhaustion of the caller's own meter retains normal SDK panic semantics.
Gas caps are explicit reviewed policy, not user-controlled query options.

The supplied context must contain genuine current block metadata, including
the proposer required by upstream EVM configuration. A missing/unknown proposer
fails; no fabricated zero coinbase is used. Query wiring must preserve the
real header too. Callers must include hydrated values in their own tally-time
state fingerprint; the reader does not make a long-lived snapshot immutable.

The owned-App regression uses one default-production App in its test binary,
genuine signed deploy/call transactions, the compiler-pinned DRC20 and proxy,
real FinalizeBlock/Commit and exact fee checks. Metadata/admission records are
explicit fixtures because native coin storage is not yet mounted in the owned
App. Same-context SDK bank credits demonstrate stale recorded reserves and
parent/child isolation. Explicitly admitted adversarial fixture bytecode tests
revert, malformed output, EVM gas exhaustion and discarded state writes; it is
not a production runtime allowlist. Full application wiring and deployment
authority remain separate integration requirements.

The bounded loop case uses an EVM gas cap of 200,000 and charged 226,619 SDK
gas including native reads in the verified run. A one-unit child SDK budget
fails closed. The entire view suite and native coin keeper regressions pass
on the selected SDK53 family, with whole-module verification and both owned
source verifiers passing before/after the grouped checks. This is not an
aggregate block/proposal-work bound; callers must also bound assets, positions
and repeated fingerprint reads across a tally.
