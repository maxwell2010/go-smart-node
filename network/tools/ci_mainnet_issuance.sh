#!/usr/bin/env bash
set -euo pipefail
umask 077
work=$(mktemp -d "$RUNNER_TEMP/msp-issuance.XXXXXXXX")
out="$RUNNER_TEMP/mainnet-issuance-candidate"
mkdir -p "$out" "$work/source" "$work/assembled/public"
for role in admission activation; do
  tags=mintcandy_mainnet
  if [[ $role == activation ]]; then tags+=,mintcandy_drc20_issuance; fi
  go build -mod=readonly -p 2 -trimpath -tags "$tags" -ldflags "-X main.buildCommit=$GITHUB_SHA" \
    -o "$out/mcnd-$role-linux-amd64" ./cmd/mcnd
  for test in TestDRC20IssuanceGovernance TestDRC20IssuanceBinaryRole TestMainnetDRC20IssuanceTariff; do
    go test -mod=readonly -p 2 -count=1 -tags "$tags" -run "^$test$" -json ./app > "$work/$role-$test.jsonl" || {
      jq -r '.Output // empty' "$work/$role-$test.jsonl"; exit 1;
    }
    jq -se --arg test "$test" 'any(.[]; .Action == "pass" and .Test == $test)' "$work/$role-$test.jsonl"
  done
done
tags=mintcandy_mainnet
go build -mod=readonly -p 2 -tags "$tags" -o "$work/genesis" ./cmd/mcnd-genesis
go build -mod=readonly -p 2 -tags "$tags" -o "$work/probe" ./tools/comet_commit_probe
go test -mod=readonly -p 2 -tags "$tags" -c -o "$work/signer" ./systemcontracts
export MCND_TESTNET_TARIFF_FOUNDER_FILE="$work/tariff-founder.key"
openssl rand -hex 32 | tr -d '\n' > "$MCND_TESTNET_TARIFF_FOUNDER_FILE"
MCND_NATIVE_CONSENSUS_FIXTURE_DIR="$work/source" MCND_NATIVE_LAUNCH_REHEARSAL=4-empty-sponsored \
  go test -mod=readonly -p 2 -count=1 -timeout 8m \
    -run '^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$' ./systemcontracts
"$work/genesis" addresses --chain-id mintcandy_9905401-1 --evm-chain-id 9905401 > "$work/roles.json"
python3 -B tools/test_founding_ledger_fixture.py --source "$work/source" --roles "$work/roles.json" \
  --network-profile mainnet --output "$work/ledger.json"
opts=(--ledger "$work/ledger.json" --parameters tools/fixtures/founding-genesis-ci-parameters.json)
genesis="$work/assembled/public/genesis.json"
"$work/genesis" build "${opts[@]}" --genesis "$genesis"
"$work/genesis" verify "${opts[@]}" --genesis "$genesis" > "$work/assembly.json"
install -m 600 "$genesis" "$work/assembled/genesis.json"
install -m 600 "$work/source/"priv_validator_key_{a,b,c,d}.json "$work/assembled/"
jq '{chainId:"mintcandy_9905401-1", evmChainId:9905401, actor, actorNative,
     validators:[.validators[]|{name,initialPower,consensusAddress,publicKey}]}' \
     "$work/source/expectations.json" > "$work/assembled/expectations.json"
sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
  python3 -B tools/mainnet_issuance_upgrade.py --fixture "$work/assembled" --assembly-report "$work/assembly.json" \
  --predecessor "$out/mcnd-admission-linux-amd64" --successor "$out/mcnd-activation-linux-amd64" --signer "$work/signer" \
  --commit-probe "$work/probe" --home-parent "$work" --report "$work/result.json"
sudo -n install -m 644 "$work/result.json" "$out/rehearsal.json"
cd "$out"
echo "$GITHUB_SHA" > SOURCE_COMMIT
sha256sum mcnd-admission-linux-amd64 mcnd-activation-linux-amd64 rehearsal.json SOURCE_COMMIT > SHA256SUMS
tar -czf "$RUNNER_TEMP/mintcandy-mainnet-issuance-linux-amd64.tar.gz" mcnd-admission-linux-amd64 mcnd-activation-linux-amd64 rehearsal.json SOURCE_COMMIT SHA256SUMS
