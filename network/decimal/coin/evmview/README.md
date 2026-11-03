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
authenticated deployment/governance state. `Binding.Kind` must explicitly be
`DirectRuntime`, `ERC1967Proxy`, `BeaconProxy` or `ReadableBeaconProxy`; zero is invalid. Each binding
pins the native token address and runtime hash. An EIP1967 implementation proxy
also pins its current implementation address and code hash. A direct binding
rejects occupied implementation/beacon slots and proxy-specific approval fields.

Canonical factory-created BeaconProxy tokens require `NewWithBeacon` with the
separate compiler-pinned `DecimalTokenCenter` ABI. Approval pins the token
runtime, beacon/factory address and runtime, the factory proxy's own EIP1967
implementation address/hash, and the underlying token implementation address/hash.
The actual bounded `implementation()` getter must return that approved token
logic. Wrong kinds, missing approval, changed factory/logic, recursive beacon
schemes and malformed/zero results fail closed. A pinned proxy runtime alone
never admits an unverified beacon. These checks authenticate only a correctly
configured reviewed source; an untrusted provider cannot authenticate itself
by reading arbitrary code hashes from the same chain.

The canonical factory returns zero from `implementation()` while paused. This
reader deliberately rejects that state without a stale metadata fallback.
The separate fresh backed-token profile uses `NewWithReadableBeacon` and
`ReadableBeaconProxy`. It authenticates the factory/proxy/logic approvals as
above, and additionally derives the exact family0 proxy runtime from compiler
immutable references and requires the pinned `NewNetworkTokenCenter` runtime.
Only this explicit profile reads `readImplementation(uint8(0))`; canonical
paused beacons retain their rejection behavior. A reserve-less family cannot
be admitted as a backed family merely by supplying its observed code hash.
The normal token getter remains readable while paused, and financial transfers
remain blocked. Changes to the compiled factory profile require a reviewed
reader update. Other proxy/forwarding schemes require separate review.

Construct with the compiler-pinned `systemcontracts.CompiledABI("DRC20")`.
The selector is copied; no mutable ABI map or financial result is cached.
For a beacon, resolving the factory and reading token supply are two separately
bounded EVM calls. Each has a discarded child cache, so speculative native
precompile writes cannot influence the other. Backing is captured before either
getter. Execution uses an actual bounded `core.Message.GasLimit` and `ApplyMessage`
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
explicit fixtures; native coin storage is mounted, but financial deployment
admission is not activated in the owned App. Same-context SDK bank credits demonstrate stale recorded reserves and
parent/child isolation. Explicitly admitted adversarial fixture bytecode tests
revert, malformed output, EVM gas exhaustion and discarded state writes; it is
not a production runtime allowlist. Full application wiring and deployment
authority remain separate integration requirements.

The bounded loop case uses an EVM gas cap of 200,000 and charged 227,778 SDK
gas including native reads in the verified run. A one-unit child SDK budget
fails closed. The entire view suite and native coin keeper regressions pass
on the selected SDK53 family, with whole-module verification and both owned
source verifiers passing before/after the grouped checks. This is not an
aggregate block/proposal-work bound; callers must also bound assets, positions
and repeated fingerprint reads across a tally.

The actual factory-created-token case deploys the compiler-pinned TokenCenter
proxy, authorizes DRC20 logic, calls `createToken` with5DEL reserve plus the
actual0.25DEL symbol commission, and reconciles exact signed transaction fees.
It verifies supply/reserve, wrong-kind/code/binding rejection, canonical pause
rejection, successful recovery after unpause, and logic-upgrade rejection until
its binding is explicitly updated. Factory and native metadata admission rows
remain fixtures, not a completed production bootstrap/receipt pipeline.
The fresh-profile control additionally verifies compiler-derived actual proxy
code, authoritative supply/reserve reads during pause, rejected financial
transfer, recovery after unpause, and explicit approval after logic replacement.
These reader controls do not establish the still-pending complete native
validator/delegator governance tally and unpause-proposal integration.
