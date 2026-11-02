#!/usr/bin/env bash
set -euo pipefail

# Build a candidate only. This script does not install or start a node.
version=${1:?Usage: build-linux-candidate.sh VERSION OUTPUT_DIRECTORY [ordinary|experimental_ibc]}
out=${2:?Usage: build-linux-candidate.sh VERSION OUTPUT_DIRECTORY [ordinary|experimental_ibc]}
flavor=${3:-ordinary}
case "$flavor" in
  ordinary) tags=netgo ;;
  experimental_ibc) tags=netgo,experimental_ibc ;;
  *) echo "Unknown candidate flavor" >&2; exit 2 ;;
esac
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+-alpha\.[0-9]+$ ]] || { echo 'Use a prerelease version such as 0.1.0-alpha.1' >&2; exit 2; }
[[ "$(go env GOOS)/$(go env GOARCH)" == linux/amd64 ]] || { echo 'Requires native Linux amd64' >&2; exit 2; }
[[ "$(go env CGO_ENABLED)" == 1 ]] || { echo 'CGO must be enabled' >&2; exit 2; }
[[ "$(go env GOVERSION)" == go1.24.4 ]] || { echo 'Requires the pinned Go 1.24.4 toolchain' >&2; exit 2; }
[[ -z "$(git status --porcelain --untracked-files=no)" ]] || { echo 'Tracked source tree must be clean' >&2; exit 2; }
revision=$(git rev-parse HEAD)
epoch=$(git show -s --format=%ct HEAD)
mkdir -p "$out"
out=$(cd "$out" && pwd)
stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT
flags="-buildid= -s -w -X github.com/cosmos/cosmos-sdk/version.Name=MintCandy -X github.com/cosmos/cosmos-sdk/version.AppName=dscd -X github.com/cosmos/cosmos-sdk/version.Version=$version -X github.com/cosmos/cosmos-sdk/version.Commit=$revision -X github.com/cosmos/cosmos-sdk/version.BuildTags=$tags"
for pass in first second; do
  go build -mod=readonly -p 2 -trimpath -buildvcs=false -tags "$tags" -ldflags "$flags" -o "$stage/dscd-$pass" ./cmd/dscd
done
cmp "$stage/dscd-first" "$stage/dscd-second"
mv "$stage/dscd-first" "$stage/dscd"
rm "$stage/dscd-second"
cp LICENSE "$stage/LICENSE"
export CANDIDATE_VERSION="$version" CANDIDATE_REVISION="$revision" CANDIDATE_EPOCH="$epoch"
export CANDIDATE_FLAVOR="$flavor" CANDIDATE_TAGS="$tags"
python3 - "$stage/BUILD.json" <<'PY'
import json, os, subprocess, sys
from pathlib import Path
run = lambda *args: subprocess.check_output(args, text=True).strip()
Path(sys.argv[1]).write_text(json.dumps({
    'version': os.environ['CANDIDATE_VERSION'],
    'revision': os.environ['CANDIDATE_REVISION'],
    'source_date_epoch': int(os.environ['CANDIDATE_EPOCH']),
    'platform': 'linux/amd64', 'go': run('go', 'version'),
    'c_compiler': run('gcc', '--version').splitlines()[0],
    'cgo_enabled': True, 'build_tags': os.environ['CANDIDATE_TAGS'].split(','),
    'flavor': os.environ['CANDIDATE_FLAVOR'],
    'allowed_chain_ids': ['decimal_900001-1', 'decimal_900002-1'] if os.environ['CANDIDATE_FLAVOR'] == 'experimental_ibc' else [],
    'chain_id_restriction_enforced': os.environ['CANDIDATE_FLAVOR'] == 'experimental_ibc',
    'isolated_reliability_activation_heights': {'decimal_900001-1': 1, 'decimal_900002-1': 1},
    'production_reliability_activation_heights': {},
    'journal_replay_fix': 'unconditional',
    'same_environment_double_build_identical': True,
    'mainnet_certified': False, 'production_ibc': False,
}, indent=2) + '\n')
flavor = os.environ['CANDIDATE_FLAVOR']
notice = '''Experimental integration candidate. Not certified for mainnet validator use.
Reliability rules activate at height 1 only on decimal_900001-1 and decimal_900002-1.
Production chain IDs remain unset. The journal replay correction is unconditional.
Historical mainnet replay and deployed-contract rehearsal remain release gates.
This archive does not install services or contain a genesis, node data or keys.
'''
if flavor == 'experimental_ibc':
    notice += '''Flavor: experimental_ibc. Build tags: netgo,experimental_ibc.
ICS-20 is wired into the experimental DSC application. This binary accepts only
exact chain IDs decimal_900001-1 and decimal_900002-1. Production IBC is not certified.
'''
else:
    notice += '''Flavor: ordinary. Build tags: netgo. No transfer module is enabled.
The ordinary binary is not restricted to the isolated chain IDs, but that does
not establish mainnet compatibility or authorize its use on production validators.
'''
Path(sys.argv[1]).with_name('README.release.txt').write_text(notice)
PY
# Version inspection uses a fresh disposable home and never starts consensus.
mkdir "$stage/smoke-home"
HOME="$stage/smoke-home" "$stage/dscd" version --long > "$out/version.txt"
rm -rf -- "$stage/smoke-home"
name="mintcandy-node-${version}-${flavor}-linux-amd64-${revision:0:12}.tar.gz"
tar --sort=name --mtime="@$epoch" --owner=0 --group=0 --numeric-owner -C "$stage" -cf - dscd LICENSE README.release.txt BUILD.json | gzip -n > "$out/$name"
(cd "$out" && sha256sum "$name" version.txt > SHA256SUMS)
echo "$out/$name"
