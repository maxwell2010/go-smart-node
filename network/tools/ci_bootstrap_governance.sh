#!/usr/bin/env bash
# Isolated validator homes and deterministic test signing material stay in RUNNER_TEMP.
set -euo pipefail
umask 077
work=$(mktemp -d "$RUNNER_TEMP/bootstrap-governance.XXXXXXXX")
mkdir -p "$work/source" "$work/assembled/public"
tags=mintcandy_testnet
for variant in predecessor successor; do
  build_tags=$tags
  if [[ $variant == successor ]]; then build_tags+=,mintcandy_bootstrap_unanimity; fi
  go build -mod=readonly -p 2 -tags "$build_tags" -trimpath -ldflags "-X main.buildCommit=$GITHUB_SHA" -o "$work/$variant" ./cmd/mcnd
done
go build -mod=readonly -p 2 -tags "$tags" -o "$work/genesis" ./cmd/mcnd-genesis
go build -mod=readonly -p 2 -tags "$tags" -o "$work/probe" ./tools/comet_commit_probe
go test -mod=readonly -p 2 -tags "$tags" -c -o "$work/signer" ./systemcontracts
MCND_NATIVE_CONSENSUS_FIXTURE_DIR="$work/source" MCND_NATIVE_LAUNCH_REHEARSAL=4-empty-sponsored \
  go test -mod=readonly -p 2 -count=1 -timeout 8m \
    -run '^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$' ./systemcontracts > "$work/source.log"
# The default sponsored beneficiary is intentionally address-only. A disposable
# fifth key must own the fixture positions in order to exercise explicit consent.
MCND_BOOTSTRAP_IDENTITY="$work/founder.json" "$work/signer" -test.run '^TestExportBootstrapFixtureIdentity$'
jq -s '.[0] * .[1]' "$work/source/expectations.json" "$work/founder.json" > "$work/expectations.json"
install -m 600 "$work/expectations.json" "$work/source/expectations.json"
"$work/genesis" addresses --chain-id mintcandy_990098-1 --evm-chain-id 990098 > "$work/roles.json"
python3 -B tools/test_founding_ledger_fixture.py --source "$work/source" --roles "$work/roles.json" \
  --network-profile testnet --output "$work/ledger.json"
opts=(--ledger "$work/ledger.json" --parameters tools/fixtures/founding-genesis-ci-parameters.json)
genesis="$work/assembled/public/genesis.json"
"$work/genesis" build "${opts[@]}" --genesis "$genesis"
"$work/genesis" verify "${opts[@]}" --genesis "$genesis" > "$work/assembly.json"
install -m 600 "$genesis" "$work/assembled/genesis.json"
install -m 600 "$work/source/"priv_validator_key_{a,b,c,d}.json "$work/assembled/"
jq '{chainId:"mintcandy_990098-1", evmChainId:990098, actor, actorNative,
     validators:[.validators[]|{name,initialPower,consensusAddress,publicKey}]}' \
     "$work/expectations.json" > "$work/assembled/expectations.json"
sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
  python3 -B tools/bootstrap_governance_rehearsal.py --fixture "$work/assembled" --assembly-report "$work/assembly.json" \
  --predecessor "$work/predecessor" --successor "$work/successor" --signer "$work/signer" \
  --commit-probe "$work/probe" --home-parent "$work" --report "$work/result.json"
sudo -n install -m 644 "$work/result.json" "$RUNNER_TEMP/bootstrap-governance-result.json"
