# Native validator schema foundation

This package preserves Decimal validator, custom-coin/NFT stake, hold, queue,
reward, signing and historical-state protobuf fields under the separate network
module. Native power is the hydrated integer `Validator.Stake`; it is not SDK
staking shares and is not divided by another token power reduction.

The package provides explicit `d0`/`d0valoper` validation, parameter/genesis
checks and deterministic historical ordering. Financial values are copied by
protobuf serialization, including nonempty NFT reserves and holds.

The schema, transactional keeper and guarded read-only application module are
present. Native financial message routes, emergency authority, emission schedule
and consensus activation remain incomplete. The existing SDK staking scaffold must be
replaced atomically with a native ledger and genuine consensus/index/history
adapter before this schema controls consensus or IBC trusting periods. A keeper
must hydrate the separate `ValidatorRS` row before computing native power.
The mounted module is described in [its scope](module/README.md); it must not
be mistaken for activation of that native financial/consensus controller.

Genesis validation checks structural references and bounded native financial
fields. Bank escrow, conversion-price, supply and NFT ownership reconciliation
remain cross-module keeper responsibilities.

Generation from the `network` module uses the existing pinned tools:

```sh
bash scripts/generate-native-proto.sh validator
go test -mod=readonly -p 1 -count=1 ./decimal/validator/...
```

The protobuf package and wire type URLs remain `decimal.validator.v1`; the Go
module relocation does not rename messages or change field numbers. Operator
signer fields declare `cosmos.ValidatorAddressString`, so modern descriptor
signing decodes them with the validator codec. Delegator and admin signer fields
use the account codec. Declaring admin message schemas grants no authority and
does not register their handlers.

The schema regression suite checks serialization of nonempty native financial
fields and held NFT reserves, native power/bond status, deterministic history
ordering and independent copies, malformed genesis/parameters, and all 18
descriptor-based message signers. Passing these checks does not certify a
validator state machine, slashing, reward accounting or an IBC staking adapter.
