# STUN v2 short-attribute backport

The SDK53 candidate actually selects `github.com/pion/stun/v2 v2.0.0`
through the Cosmos Geth NAT dependency. The prior SDK54 regression imported
STUN v3, which was absent from the selected family. Testing the real v2
dependency confirmed the same unchecked short-attribute panic.

The owned v2 replacement backports the upstream
[short-attribute guard](https://github.com/pion/stun/pull/278): check length
before reading the address-family header. It preserves the v2 module/API and
does not substitute a different major version. The complete authenticated
module source and MIT license are retained, with canonical hashes and the
single changed file in `third_party/pion-stun-v2/UPSTREAM.json`.

The actual imported package regression fails with the canonical source and
passes with this replacement. Valid IPv4/IPv6 round trips remain tested.
This establishes the parser behavior, not reachability from every daemon
configuration. The upstream advisory is
[GHSA-34rh-wp3j-6cxc](https://github.com/pion/stun/security/advisories/GHSA-34rh-wp3j-6cxc).

Run `python3 scripts/verify_owned_stun.py` before builds. As for the EVM
replacement, ordinary `go mod verify` skips local replacement sources. The
manifest is a consistency record; independent review and advisory tracking
must continue to identify the actual owned source rather than suppressing
all upstream findings.
