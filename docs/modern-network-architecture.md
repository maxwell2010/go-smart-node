# Separate modern network and legacy replay

Decision recorded 2026-11-02: build a separate network on a maintained dependency family. Keep Decimal mainnet historical replay as a separate legacy build. A successful legacy replay is a reference result, not certification of the new network.

## Repository and build boundaries

- The root Go module retains the historical Decimal application and its frozen replay reference. Do not upgrade its SDK, IBC, store layout or historical handlers to make a new network compile.
- The modern application belongs in the independent `network/` Go module, with its own dependency graph, command, genesis, tests, artifacts and installation instructions. A root `go test ./...` does not test this nested module; it requires a dedicated CI job.
- Select a coherent released Cosmos EVM family, then test compatible patch updates. Record effective module replacements, archive checksums and scanner coverage. A required Geth version is not the actual version when the manifest replaces it with a Cosmos fork.
- Keep ordinary legacy replay artifacts, the old isolated IBC prototype and modern network artifacts separately named. None is a mainnet validator upgrade instruction.

## Required behavior before a full release

| Area | Required evidence in the modern application |
| --- | --- |
| Network identity | New chain and EVM IDs, explicit operator genesis, distinct homes and peer configuration. Reject Decimal production identities and accidental reuse of legacy validator signing state. |
| EVM bootstrap | Fresh deterministic genesis supports signed native transfers and contract creation through the real ante, fees and EVM. System contract addresses, creation/runtime hashes, initialization order and authorities are explicit and verified. |
| Decimal functionality | Inventory coin issuance/reserves, validator operations, native and NFT delegation, holds, commission and rewards. Implement and test the selected equivalents; importing a generic EVM example does not demonstrate these features. Any changed API or economic behavior must be stated explicitly. |
| Financial events | Real contract events update the correct native state; malformed financial events abort execution and roll back value, contract storage and native state while charging only the correct fee. No dependency may overwrite the actual hook error with a placeholder. |
| Auto-unbond | Deterministic bounded progress, persistent retry state, no duplicate queue entries, a defined policy for invalid entries and reconciliation of prior partial work. Prove conservation and restart behavior. |
| IBC core and ICS20 | Clients, connections and channels are genuinely wired into the running application. Signed send/receive/acknowledgement/return, timeout and error refunds preserve native and EVM balances. Validate proof rejection, client expiry, relayer restart, node restart and upgrade with a packet pending. |
| Interoperability | Test against an independent supported counterparty and an external relayer. Two applications of the same implementation alone do not establish broad interoperability. State which IBC applications and asset types are supported. |
| Security and authority | Completed source and binary scans of the effective graph, reviewed findings and fork coverage, explicit upgrade/emergency authority and asset limits. A clean scanner count alone is insufficient when advisories do not identify a replacement fork. |
| Release operations | Linux archives, checksums, SBOM and source provenance; clean installation; state export/restart; upgrade and rollback rehearsals; operator documentation and independent review. |

## Genesis and keys

New network authorities and distribution are operator inputs. Do not copy mainnet contract owners, balances or production signing keys into a fresh network and call it bootstrap. Disposable test keys stay in temporary fixtures and never become published production genesis credentials. A source-derived contract setup for the new network may differ from Decimal's unverified setup, but its source, storage effects, permissions and one-time initialization must be independently tested.

Existing working Decimal nodes remain outside this deployment. Historical data collection is read-only and bounded; restore, replay, fault injection and all transactions run on isolated copies or dedicated test networks.

## Current status

The modern module is being prepared. The legacy isolated ICS20 prototype is tested but still uses the old family; it is not the modern release. Dependency selection, application compilation and complete feature validation remain release gates. No stable release is announced by this decision.
