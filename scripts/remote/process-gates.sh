#!/usr/bin/env bash
# All processes, data and secrets belong to this disposable worker only.
set -euo pipefail
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && -z "${WSL_DISTRO_NAME:-}" ]] || { echo 'Disposable Linux amd64 worker required.' >&2; exit 2; }
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
output="${1:?Pass a new output directory}"
[[ ! -e "$output" ]] || { echo 'Refuse existing output directory.' >&2; exit 2; }
for tool in sudo unshare ip gcc python3 go; do command -v "$tool" >/dev/null; done
[[ "$(go version)" == 'go version go1.26.10 linux/amd64' ]] || { echo 'Wrong Go toolchain.' >&2; exit 2; }
(( $(df -Pk "$root" | awk 'END {print $4}') >= 20 * 1024 * 1024 )) || { echo 'Less than 20 GiB free on worker.' >&2; exit 1; }
mkdir -p "$output"
work="$(mktemp -d)"
cleanup() { local status=$?; sudo -n rm -rf -- "$work"; exit "$status"; }
trap cleanup EXIT
export TMPDIR="$work" GOTMPDIR="$work"
export GOENV=off GOWORK=off GOFLAGS= GOTOOLCHAIN=local CGO_ENABLED=1 CC=gcc GOMAXPROCS=2 GOAMD64=v1
unset GOEXPERIMENT CGO_CFLAGS CGO_CPPFLAGS CGO_CXXFLAGS CGO_FFLAGS CGO_LDFLAGS
revision="$(git -C "$root" rev-parse HEAD)"
cd "$root/network"
go mod download
go mod verify |& tee "$output/module-integrity.log"
git diff --exit-code -- go.mod go.sum
python3 scripts/verify_owned_evm.py
python3 scripts/verify_owned_geth.py
python3 scripts/verify_owned_sdk.py --compiled
python3 scripts/verify_pion_transport.py
go build -mod=readonly -tags= -p 2 -trimpath -ldflags "-X main.buildCommit=$revision" -o "$work/mcnd" ./cmd/mcnd
go build -mod=readonly -tags= -p 2 -trimpath -o "$work/ibcreference" ./internal/ibcreference
go version -m "$work/mcnd" > "$output/mcnd-build.log"
go version -m "$work/ibcreference" > "$output/reference-build.log"
mcnd_sha="$(sha256sum "$work/mcnd" | awk '{print $1}')"
reference_sha="$(sha256sum "$work/ibcreference" | awk '{print $1}')"
curl --fail --location --proto '=https' --tlsv1.2 --retry 3 \
  'https://github.com/informalsystems/hermes/releases/download/v1.13.3/hermes-v1.13.3-x86_64-unknown-linux-gnu.tar.gz' --output "$work/hermes.tar.gz"
printf '%s  %s\n' 3582136824ac1984c8884b310c63940a1489f091fa45faca38ed80e5dfe7d51b "$work/hermes.tar.gz" | sha256sum --check
tar -xzf "$work/hermes.tar.gz" -C "$work"
printf '%s  %s\n' ed8d57781f57a93e74259c12c8feee661b9633c882307f3a6236e41dbf5ffe35 "$work/hermes" | sha256sum --check
chmod 755 "$work/hermes"

# Namespace creation and loopback setup affect only the new namespace.
sudo -n unshare --net -- bash -c 'ip link set lo up; exec "$@"' _ \
  python3 "$root/network/tools/mcnd_process_smoke.py" --binary "$work/mcnd" \
  --home-parent "$work/lifecycle" --history-role rpc-ibc --report "$output/daemon.json" \
  |& tee "$output/daemon.log"
sudo -n unshare --net -- bash -c 'ip link set lo up; exec "$@"' _ \
  env TMPDIR="$work" python3 "$root/network/internal/ibclab/hermes_lab.py" --mode full \
  --reference-binary "$work/ibcreference" --reference-sha256 "$reference_sha" --reference-source-commit "$revision" \
  --mcnd-binary "$work/mcnd" --mcnd-sha256 "$mcnd_sha" --mcnd-source-commit "$revision" \
  --hermes-binary "$work/hermes" --report "$output/hermes.json" |& tee "$output/hermes.log"
python3 - "$output" <<'PY'
import json,sys
from pathlib import Path
out=Path(sys.argv[1]);daemon=json.loads((out/'daemon.json').read_text());hermes=json.loads((out/'hermes.json').read_text())
assert daemon['status']=='PASS',daemon
assert hermes['status']=='TWO_PROCESS_HERMES_PASS' and hermes['release_gate'] is True,hermes
required={'client_connection_channel_handshake','native_send','voucher_return','pending_packet_restart','error_ack_refund','timeout_refund'}
assert set(hermes['phases'])==required,hermes['phases']
print('Default daemon lifecycle and all six genuine Hermes phases passed.')
PY
