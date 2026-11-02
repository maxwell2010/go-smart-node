# Native Decimal feature port

This directory is a staged source port for the separate modern network. It is not a claim that the application has coin issuance, NFT delegation or validator integration wired and tested.

The initial `bigfloat` and `formulas` packages retain the legacy calculation behavior and existing vectors, with imports moved into this module. The third-party bigfloat license is preserved. They compile and pass their existing tests with the candidate modern dependency graph and Go1.26.10.

Keep historical behavior in the root application unchanged. The modern feature port must separately verify keeper state, real EVM contract execution, fees, supply/reserve conservation, event rollback and SDK query/transaction routes. Formula tests alone do not establish those properties.

Financial review remains required: legacy inverse calculations truncate to integers and do not establish a guaranteed executable minimum-input quote. Inputs also have legacy preconditions. Validate bounds and compare quotes against the actual new-network contract implementation before exposing them as guaranteed swap or fee quotations. Do not silently change monetary rounding while calling the behavior identical.

Coin, NFT, validator and fee integration remain incomplete until their keepers and actual application tests are present.

Coin/NFT data and transaction schemas are now staged under `proto/decimal`, with their original wire type URLs and signer annotations. Generated files use Cosmos gogoproto v1.7.2 and protoc3.21.12; `scripts/generate-native-proto.sh` reproduces them. Modern descriptor-based signing is tested with a real SDK address codec, including invalid-sender rejection. These schema checks do not enable legacy native write handlers or deploy system contracts.

The new coin default has zero allocation; this is not a mint instruction. Application genesis must reconcile metadata with the operator's bank distribution. Parameter validation rejects missing/negative amounts, and the real SDK CacheContext tests demonstrate that discarded changes cannot alter keeper reads. The complete-export NFT supply checker now detects counter mismatches and duplicate/missing records; callers must hydrate counters and sub-tokens rather than pass shallow query records. Native bank/reserve conservation and event-backed keeper wiring are still outstanding.
