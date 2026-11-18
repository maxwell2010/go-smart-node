# Canonical SP1 v6.0.0 compatibility fixture

Repository: https://github.com/succinctlabs/sp1-contracts

Pinned commit: `d3629729c3216eb51bd4859d027a8eb729399fa4`.

The three verifier source files and `UPSTREAM_TEST.sol` are unchanged upstream
UTF-8 source bytes. Their SHA256, Git blob identifiers, original paths and byte
lengths are recorded in `UPSTREAM.json`. All four originals declare
`SPDX-License-Identifier: MIT`; source attribution and notices are preserved.
The pinned upstream tree has no standalone LICENSE file. `LICENSE.MIT`
reproduces the standard MIT permission terms without adding a new copyright
claim over the upstream sources.

`proof.json` copies PROGRAM_VKEY, PUBLIC_VALUES and PROOF_BYTES from the pinned
`contracts/test/SP1VerifierGroth16V6.sol`, with provenance. Its proof matches
the v6.0.0 verifier imported by that test, not v6.1.0. No proof bytes, verifier
mathematics or verification keys have been modified. `UPSTREAM_TEST.sol` is
retained for provenance, not compiled: its Foundry dependencies are not vendored.

This sample proves compatibility of a known upstream proof with the standalone
v6.0.0 verifier under a London EVM. It is not a Tendermint/Decimal state proof,
bridge authorization, deployment instruction, audit, or selection of the
production prover/verifier version. The pinned tree has no corresponding
v6.1.0 positive fixture; a v6.1.0 success claim remains unsupported.

The tagged test requires both `bridgecontracts` and `sp1contracts` plus the
CI-supplied pinned `MCND_BRIDGE_SOLC` compiler. No prover, ELF, package install,
external RPC, key or transaction broadcast is used.
