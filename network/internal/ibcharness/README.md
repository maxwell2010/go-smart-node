# Owned IBC protocol test harness

This branch uses an owned subset of the Apache-2.0 licensed `cosmos/ibc-go` v10.7.0 testing helpers, commit `40c9dbe397c8bb385dc03cd2cfc8e36c4023a2ba`. `LICENSE` retains the upstream license. The canonical signed headers, committed proof queries, Tendermint client updates, connection/channel handshakes and packet relay are retained.

The subset has no default application constructor, concrete SimApp import, mock application, governance fixture, solo-machine fixture or IBC v2 fixture. Callers supply the application, genesis and funded signing accounts. Transactions are encoded and signed, then executed by the actual `FinalizeBlock`; the application ante handler deducts the declared fee. No keeper or protocol verification is bypassed. Transfer is the default channel configuration.

The tagged own-app test exercises actual ICS20 escrow, vouchers, return, error acknowledgement, timeout refund, duplicate-timeout handling and rejection of an altered packet with a genuine original commitment proof. This internal harness does not replace the production-binary, independent SDK counterparty and external-relayer gate.

SDK 0.55/IBC11 work is preserved separately. The current SDK0.53/Comet0.38/IBC10 validation is recorded below; prior SDK54/55 passes are not counted as evidence for this branch.

The maintained53 tagged own-app scenario passed with a genuine expired-client rejection case: an actual committed packet proof first verifies, block time advances beyond its Tendermint client's trusting period, and signed reception then fails with the exact client-not-active code. No receipt or voucher is created; source escrow/commitment and latest client height remain intact. No client status flag is manually set. This unit clock progression and same-binary restart work are not SDK major-upgrade evidence.
