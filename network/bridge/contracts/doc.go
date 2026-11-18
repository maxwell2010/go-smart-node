// Package contracts contains a CI-only deposit escrow source. There is no
// production artifact, release path, trusted verifier or deployment command.
// Tests tagged bridgecontracts compile the source with an explicitly supplied,
// pinned Solidity compiler, then execute it in an isolated London EVM.
package contracts
