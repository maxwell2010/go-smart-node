# Native operator address in EVM COINBASE

The canonical Cosmos EVM 0.6.3
[proposer lookup](https://github.com/cosmos/evm/blob/v0.6.3/x/vm/keeper/block_proposer.go)
converts `[]byte(validator.GetOperator())` directly to an Ethereum address.
SDK53 returns the operator's Bech32 string, so this truncates its final twenty
text bytes instead of decoding the underlying address. This concerns the new
network dependency, not the preserved legacy Decimal replay build.

The default-production owned-app test now deploys a genuine nine-byte EVM
program that returns the `COINBASE` opcode. Deployment and execution are signed
Ethereum transactions through the real ante, FinalizeBlock and Commit. Each
checks exact fee debit and nonce advancement. The expected address is decoded
from the actual SDK validator using its configured address codec.

The canonical source failed this test with an ASCII-tail beneficiary; its
existing financial rollback control still passed. The owned correction decodes
Bech32 through `ValidatorAddressCodec` and rejects non-20-byte results. The same
opcode test then passed, together with the signed transfer, domain and financial
rollback checks. Validator records remain ordinary honest Bech32 strings.

`third_party/cosmos-evm` contains the complete authenticated canonical module
source with its Apache license. `UPSTREAM.json` records the upstream tag,
commit, Go module checksum, all 1,236 original file hashes and explicitly changed
or added files. No authenticated module-cache file is changed. Normal Go builds select
this correction through the explicit local replacement. The patch identifier
`v0.6.3-mintcandy.2` is an owned-source label, not a published upstream release.
It also includes the separately documented [native work gas policy](ru/NATIVE_WORK_GAS.md).

Run `python3 scripts/verify_owned_evm.py` before builds. The verifier detects
undeclared changes, missing files and extra files in that local replacement;
`go mod verify` alone skips local replacements. This manifest is a consistency
record, not independent authentication against a maliciously changed manifest.
Upstream vulnerability coverage and actual binary/source scans must continue
to identify the owned replacement and review the original component versions.

This is a new-network EVM behavior correction. It is not retroactively applied
to historical Decimal blocks, and its tests do not certify native staking or
governance integration that remains under development.
