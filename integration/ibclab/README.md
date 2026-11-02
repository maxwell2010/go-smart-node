# Isolated ICS-20 protocol baseline

Run `CGO_ENABLED=1 go test -mod=readonly -count=1 -v ./integration/ibclab`.

The lab creates two independent in-memory SDK SimApp instances using the repository's pinned IBC-Go v6.2.1. The coordinator constructs light clients, connections and unordered ICS-20 channels. Its relay helper submits real messages and proofs through the applications, rather than mocking packet callbacks. No external endpoint or production account is used.

Tests verify escrow/voucher creation, send/return conservation, voucher supply after return, duplicate relay behavior, error acknowledgement refund and timeout refund without double payment. This IBC version accepts a repeated timeout as a no-op; the test checks the balance rather than requiring rejection.

Limits: these are reference SDK applications, not Decimal's DSC application. They do not validate Decimal ValidatorKeeper, its ante routing, native/EVM asset conservation, process restarts, operator relayers or upgraded dependencies. This is a protocol baseline for subsequent DSC integration, not a production IBC implementation or live-network rehearsal.
