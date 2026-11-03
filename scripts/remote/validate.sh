#!/usr/bin/env bash
# Reuse one checkout and cache. Never select an existing node home or public RPC.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
remote_require_tools
profile="${1:-modern-smoke}"
case "$profile" in modern-smoke|modern-full|legacy) ;; *) echo 'Usage: validate.sh [modern-smoke|modern-full|legacy]' >&2; exit 2 ;; esac
exec 9>"$remote_base/worker.lock"
flock -n 9 || { echo 'Another build/setup owns this worker cache.' >&2; exit 1; }
remote_budget
run_tmp="$(mktemp -d "$remote_base/tmp/test.XXXXXXXX")"
export TMPDIR="$run_tmp" GOTMPDIR="$run_tmp"
result_dir="$(mktemp -d "$remote_base/results/${profile}.XXXXXXXX")"
revision="$(git -C "$remote_root" rev-parse HEAD)"
started="$(date -u +%FT%TZ)"
finish() {
  local status=$?
  trap - EXIT
  python3 - "$result_dir/result.json" "$revision" "$profile" "$started" "$status" <<'PY'
import datetime,json,sys
from pathlib import Path
out,revision,profile,started,status=sys.argv[1:]
Path(out).write_text(json.dumps(dict(revision=revision,profile=profile,started=started,
    finished=datetime.datetime.now(datetime.timezone.utc).isoformat(),exitCode=int(status)),indent=2)+'\n')
PY
  if [[ "$(dirname -- "$run_tmp")" == "$remote_base/tmp" && "$(basename -- "$run_tmp")" == test.* && ! -L "$run_tmp" ]]; then
    rm -rf -- "$run_tmp"
  fi
  echo "Validation exit=$status; results=$result_dir"
  exit "$status"
}
trap finish EXIT
step() {
  local name="$1"; shift
  remote_budget
  "$@" 2>&1 | tee "$result_dir/$name.log"
  remote_budget
}

if [[ "$profile" == legacy ]]; then
  remote_select_go legacy
  cd "$remote_root"
else
  remote_select_go modern
  cd "$remote_root/network"
fi
[[ "$(go version)" == "go version go$remote_go_version linux/amd64" ]] || { echo 'Run prepare.sh with the matching profile first.' >&2; exit 1; }
{
  printf 'revision=%s\nprofile=%s\n' "$revision" "$profile"
  go version
  go env GOOS GOARCH CGO_ENABLED CC GOTOOLCHAIN GOMODCACHE GOCACHE
  gcc --version
  df -Pk "$remote_base"
} > "$result_dir/environment.log"
step module-integrity go mod verify
git diff --exit-code -- go.mod go.sum

if [[ "$profile" == legacy ]]; then
  step real-evm go test -mod=readonly -p 2 -count=1 -v -timeout 10m -run '^TestIntegration.*RealEVM$' ./app
  for test in TestIntegrationUpgradeCrashRealEVM TestIntegrationAutoUnbondRealEVM TestIntegrationEventRollbackRealEVM; do
    grep -q -- "^--- PASS: ${test} " "$result_dir/real-evm.log"
  done
  step affected-packages go test -mod=readonly -p 2 -count=1 -v -timeout 10m ./app ./app/ante ./integrationconfig ./x/validator/keeper ./x/upgrade ./cmd/config
  step ibc-protocol go test -mod=readonly -p 2 -count=1 -v -timeout 10m ./integration/ibclab
else
  step owned-evm python3 scripts/verify_owned_evm.py
  step owned-geth python3 scripts/verify_owned_geth.py
  step owned-sdk python3 scripts/verify_owned_sdk.py --compiled
  step pion python3 scripts/verify_pion_transport.py
  step python-guards python3 -m unittest discover -s scripts -p 'test_*.py'
  if [[ "$profile" == modern-full ]]; then
    step modern-packages go test -mod=readonly -p 2 -count=1 -v -timeout 10m ./...
    step system-contracts python3 systemcontracts/build.py
    step owned-delegation python3 systemcontracts/build_owned_delegation.py
    git diff --exit-code -- systemcontracts/artifacts
    step governance go test -mod=readonly -p 2 -count=1 -v -timeout 10m -tags sdk53 ./decimal/validator/governance
  else
    step real-evm go test -mod=readonly -p 2 -count=1 -v -timeout 10m ./app ./authority/... ./systemcontracts ./nativeexec ./decimal/coin/... ./decimal/nft/... ./decimal/validator/adapter
    step ibc-protocol go test -mod=readonly -p 2 -count=1 -v -timeout 10m ./internal/ibcharness ./internal/ibcreference/...
  fi
  step ics20-two-chains go test -mod=readonly -p 2 -count=1 -v -timeout 10m -tags test -run '^TestOwnApplicationICS20TwoChains$' ./app
  grep -q -- '^--- PASS: TestOwnApplicationICS20TwoChains ' "$result_dir/ics20-two-chains.log"
  step relayer-guards python3 -m unittest discover -s internal/ibclab -p 'test_*.py'
  step build-daemon go build -mod=readonly -tags= -p 2 -trimpath -ldflags "-X main.buildCommit=$revision" -o "$run_tmp/mcnd" ./cmd/mcnd
  step daemon-version "$run_tmp/mcnd" version
  step build-family-probe go build -mod=readonly -p 2 -trimpath -o "$run_tmp/family-check" ./cmd/family-check
  step family-probe "$run_tmp/family-check"
fi
git diff --exit-code -- go.mod go.sum
echo 'Requested checks passed. This is validation evidence, not a production release approval.'
