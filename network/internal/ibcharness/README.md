# Owned IBC protocol test harness

Derived from the Apache-2.0 licensed `github.com/cosmos/ibc-go/v11` v11.2.0 `testing/` helpers. `LICENSE` preserves the upstream license. The canonical signed headers, proof queries, Tendermint client updates, connection/channel handshake messages and packet relay are retained.

The local subset has no default application constructor, concrete SimApp import, mock application, governance fixture, solo-machine fixture or IBC v2 fixture. Callers supply the application, genesis and funded signing accounts. Native transactions are encoded and signed, then executed by the application's actual `FinalizeBlock`; its ante handler deducts the declared fee. No keeper protocol check is bypassed. Transfer is the default channel configuration.

This package is internal interoperability test support, not consensus code or an operator relayer. The tagged own-app test verifies ICS20 escrow, vouchers, return, error acknowledgements, timeout refund, idempotent duplicate timeout and rejection of an altered packet proof. It does not replace the separate production-binary, independent SDK counterparty and external Hermes gate.

Validation: the complete tagged own-app ICS20 test passes on the original SDK 0.54 reference family. The owned harness also compiles against the SDK 0.55 / Comet 0.40 candidate aa83e9e; the full own-app test on that family is a separate gate; the upstream testing/simapp package is deliberately not imported.
