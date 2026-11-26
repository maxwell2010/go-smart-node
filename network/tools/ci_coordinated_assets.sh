#!/usr/bin/env bash
set -euo pipefail
umask 077
work=$(mktemp -d "$RUNNER_TEMP/msp-issuance.XXXXXXXX")
out="$RUNNER_TEMP/coordinated-assets-results"
mkdir -p "$out" "$work/source" "$work/assembled/public"
for role in predecessor successor; do
  tags=mintcandy_testnet,mintcandy_assets_rehearsal_predecessor
  if [[ $role == successor ]]; then tags=mintcandy_testnet,mintcandy_coordinated_assets; fi
  go build -mod=readonly -p 2 -trimpath -tags "$tags" -ldflags "-X main.buildCommit=$GITHUB_SHA" \
    -o "$out/mcnd-$role-linux-amd64" ./cmd/mcnd
done
tags=mintcandy_testnet
go build -mod=readonly -p 2 -tags "$tags" -o "$work/genesis" ./cmd/mcnd-genesis
go build -mod=readonly -p 2 -tags "$tags" -o "$work/probe" ./tools/comet_commit_probe
go test -mod=readonly -p 2 -tags "$tags" -c -o "$work/signer" ./systemcontracts
export MCND_TESTNET_TARIFF_FOUNDER_FILE="$work/tariff-founder.key"
printf '%064d' 1 > "$MCND_TESTNET_TARIFF_FOUNDER_FILE"
MCND_NATIVE_CONSENSUS_FIXTURE_DIR="$work/source" MCND_NATIVE_LAUNCH_REHEARSAL=4-empty-sponsored \
  go test -mod=readonly -p 2 -count=1 -timeout 8m \
    -run '^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$' ./systemcontracts
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
     "$work/source/expectations.json" > "$work/assembled/expectations.json"
sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
  python3 -B tools/coordinated_assets_rehearsal.py --fixture "$work/assembled" --assembly-report "$work/assembly.json" \
  --predecessor "$out/mcnd-predecessor-linux-amd64" --successor "$out/mcnd-successor-linux-amd64" --signer "$work/signer" \
  --commit-probe "$work/probe" --home-parent "$work" --report "$work/result.json"
sudo -n install -m 644 "$work/result.json" "$out/rehearsal.json"
jq -e '.activationHeight == 80 and .finalCommit.verifiedEd25519Signatures == 4' "$out/rehearsal.json"
