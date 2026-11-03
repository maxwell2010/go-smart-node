#!/usr/bin/env bash
# Only publish a complete regeneration from the pinned compiler on a remote worker.
set -euo pipefail
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && -z "${WSL_DISTRO_NAME:-}" ]] || { echo 'Disposable Linux amd64 worker required.' >&2; exit 2; }
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
output="${1:?Pass a new output directory}"
[[ ! -e "$output" ]] || { echo 'Refuse existing output directory.' >&2; exit 2; }
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
export SOLC="$work/solc"
curl --fail --location --proto '=https' --tlsv1.2 --retry 3 \
  'https://raw.githubusercontent.com/ethereum/solc-bin/gh-pages/linux-amd64/solc-linux-amd64-v0.8.20+commit.a1b79de6' --output "$SOLC"
printf '%s  %s\n' 0479d44fdf9c501c25337fdc540419f1593b884a87b47f023da4f1c700fda782 "$SOLC" | sha256sum --check
chmod 755 "$SOLC"
cd "$root/network"
python3 systemcontracts/build.py |& tee "$work/system-contracts.log"
python3 systemcontracts/build_owned_delegation.py |& tee "$work/owned-delegation.log"
mkdir -p "$output/artifacts"
cp -- systemcontracts/artifacts/*.json "$output/artifacts/"
cp -- "$work/system-contracts.log" "$work/owned-delegation.log" "$output/"
python3 - "$output" "$(git rev-parse HEAD)" <<'PY'
import hashlib,json,sys
from pathlib import Path
out=Path(sys.argv[1])
files={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted((out/'artifacts').glob('*.json'))}
(out/'manifest.json').write_text(json.dumps(dict(sourceCommit=sys.argv[2],compilerSHA256='0479d44fdf9c501c25337fdc540419f1593b884a87b47f023da4f1c700fda782',artifacts=files),indent=2)+'\n')
PY
echo 'Complete pinned regeneration ready. This is not an EVM test result.'
