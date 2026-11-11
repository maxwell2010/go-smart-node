#!/usr/bin/env bash
# All processes, data and secrets belong to this disposable worker only.
set -euo pipefail
umask 077
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && -z "${WSL_DISTRO_NAME:-}" ]] || { echo 'Disposable Linux amd64 worker required.' >&2; exit 2; }
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
output="$(realpath -m -- "${1:?Pass a new output directory}")"
[[ ! -e "$output" && ! -L "$output" ]] || { echo 'Refuse existing output directory.' >&2; exit 2; }
withdrawal_genesis="${2:-off}"
[[ "$withdrawal_genesis" == off || "$withdrawal_genesis" == withdrawal-genesis ]] || { echo 'Unsupported process gate option.' >&2; exit 2; }
del_state_sync="${3:-off}"
[[ "$del_state_sync" == off || ( "$del_state_sync" == del-state-sync && "$withdrawal_genesis" == withdrawal-genesis ) ]] || { echo 'Unsupported DEL state sync option.' >&2; exit 2; }
for tool in sudo unshare ip gcc python3 go; do command -v "$tool" >/dev/null; done
python="$(command -v python3)"
"$python" -c 'import sys,tomllib; assert sys.version_info >= (3,11), "Python >=3.11 required"'
[[ "$(go version)" == 'go version go1.26.11 linux/amd64' ]] || { echo 'Wrong Go toolchain.' >&2; exit 2; }
work_parent="$(realpath -e -- "${RUNNER_TEMP:-${TMPDIR:-/tmp}}")"
for volume in "$root" "$work_parent"; do
  (( $(df -Pk "$volume" | awk 'END {print $4}') >= 20 * 1024 * 1024 )) || { echo 'Less than 20 GiB free on worker volume.' >&2; exit 1; }
done
mkdir -- "$output"
evidence_owner="$(id -u):$(id -g)"
preserve_public_reports() {
  local file
  for file in "$output/daemon.json" "$output/hermes.json" "$output/withdrawal-genesis.json" "$output/del-delegation.json"; do
    if [[ -L "$file" ]]; then echo 'Refuse symbolic report file.' >&2; return 1; fi
    if [[ -f "$file" ]]; then
      sudo -n chown -- "$evidence_owner" "$file" || return 1
      sudo -n chmod 600 -- "$file" || return 1
    fi
  done
}
work="$(mktemp -d "$work_parent/mcnd-process.XXXXXXXX")"
work_identity="$(stat -c '%d:%i' -- "$work")"
cleanup() {
  local status=$?
  trap - EXIT
  preserve_public_reports || status=1
  if [[ ! -L "$work" && "$(dirname -- "$work")" == "$work_parent" && "$(basename -- "$work")" == mcnd-process.* && "$(stat -c '%d:%i' -- "$work")" == "$work_identity" ]]; then
    sudo -n rm -rf -- "$work" || status=1
  else
    echo 'Refuse cleanup: temporary directory identity changed.' >&2
    status=1
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export TMPDIR="$work" GOTMPDIR="$work"
mkdir -p "$work/lifecycle-tool-home"
export GOENV=off GOWORK=off GOFLAGS= GOTOOLCHAIN=local CGO_ENABLED=1 CC=gcc GOMAXPROCS=2 GOAMD64=v1
unset GOEXPERIMENT CGO_CFLAGS CGO_CPPFLAGS CGO_CXXFLAGS CGO_FFLAGS CGO_LDFLAGS
revision="$(git -C "$root" rev-parse HEAD)"
[[ -z "${GITHUB_SHA:-}" || "$revision" == "$GITHUB_SHA" ]] || { echo 'Checkout differs from workflow source SHA.' >&2; exit 1; }
cd "$root/network"
go mod download
go mod verify |& tee "$output/module-integrity.log"
git diff --exit-code -- go.mod go.sum
"$python" scripts/verify_owned_evm.py
"$python" scripts/verify_owned_geth.py
"$python" scripts/verify_owned_sdk.py --compiled
"$python" scripts/verify_pion_transport.py
for target in mcnd reference; do
  "$python" "$root/scripts/remote/build-inputs.py" --source "$root" --target "$target" --output "$output/$target-inputs-before.json"
done
go build -mod=readonly -tags= -buildvcs=true -p 2 -trimpath -ldflags "-X main.buildCommit=$revision" -o "$work/mcnd" ./cmd/mcnd
go build -mod=readonly -tags= -buildvcs=true -p 2 -trimpath -o "$work/ibcreference" ./internal/ibcreference
for target in mcnd reference; do
  "$python" "$root/scripts/remote/build-inputs.py" --source "$root" --target "$target" --output "$output/$target-inputs-after.json"
  cmp -- "$output/$target-inputs-before.json" "$output/$target-inputs-after.json"
done
go version -m "$work/mcnd" > "$output/mcnd-build.log"
go version -m "$work/ibcreference" > "$output/reference-build.log"
mcnd_sha="$(sha256sum "$work/mcnd" | awk '{print $1}')"
reference_sha="$(sha256sum "$work/ibcreference" | awk '{print $1}')"
"$python" - "$output" "$revision" "$mcnd_sha" "$reference_sha" <<'PY'
import json,sys
from pathlib import Path
with (Path(sys.argv[1])/'manifest.json').open('x') as stream:
    json.dump(dict(sourceCommit=sys.argv[2],mcndSHA256=sys.argv[3],referenceSHA256=sys.argv[4],
        provenanceVerified=False),stream,indent=2)
PY
"$work/mcnd" version --long --output json > "$output/mcnd-version.json"
"$python" - "$output" "$revision" "$mcnd_sha" "$reference_sha" "$root" <<'PY'
import json,subprocess,sys
from pathlib import Path
out=Path(sys.argv[1]);revision=sys.argv[2]
root=Path(sys.argv[5]);sys.path.insert(0,str(root/'network/tools'))
from binary_provenance import module_identity,parse_build_info,verify_binary_build
from source_inventory import json_records,production_environment
for name in ('mcnd','reference'):
    metadata=(out/(name+'-build.log')).read_text()
    parsed=parse_build_info(metadata)
    assert parsed['goVersion']=='go1.26.11', name+' toolchain mismatch'
    assert parsed['settings']['vcs.revision']==revision, name+' embedded source revision mismatch'
    assert parsed['settings']['vcs.modified']=='false', name+' modified sources'
    if name=='mcnd':
        verified=verify_binary_build(metadata,root,revision,'go1.26.11')
        (out/'mcnd-metadata-verified.json').write_text(json.dumps(verified,indent=2)+'\n')
    else:
        assert parsed['path']=='github.com/maxwell2010/go-smart-node/network/internal/ibcreference'
        expected={};main=None
        raw=subprocess.check_output(['go','list','-mod=readonly','-tags=','-deps','-json','./internal/ibcreference'],
            cwd=root/'network',env=production_environment(),text=True)
        for package in json_records(raw):
            if package.get('Error') or package.get('Incomplete'):
                raise ValueError('Incomplete reference dependency inventory')
            module=package.get('Module')
            if not module: continue
            identity=module_identity(module)
            if module.get('Main'): main=identity
            elif expected.setdefault(module['Path'],identity)!=identity:
                raise ValueError('Inconsistent reference module identity')
        assert main==parsed['module'] and expected==parsed['dependencies'], 'Reference module graph differs from binary'
        assert parsed['settings']==verified['settings'], 'Reference default build settings differ from mcnd'
assert json.loads((out/'mcnd-version.json').read_text())['commit']==revision
with (out/'manifest.json').open('w') as stream:
    json.dump(dict(sourceCommit=revision,mcndSHA256=sys.argv[3],referenceSHA256=sys.argv[4],
        provenanceVerified=True,
        scope='Default production builds; both source inventories checked before/after; Go VCS metadata checked'),stream,indent=2)
PY
curl --fail --location --proto '=https' --tlsv1.2 --retry 3 \
  'https://github.com/informalsystems/hermes/releases/download/v1.13.3/hermes-v1.13.3-x86_64-unknown-linux-gnu.tar.gz' --output "$work/hermes.tar.gz"
printf '%s  %s\n' 3582136824ac1984c8884b310c63940a1489f091fa45faca38ed80e5dfe7d51b "$work/hermes.tar.gz" | sha256sum --check
tar -xzf "$work/hermes.tar.gz" -C "$work"
printf '%s  %s\n' ed8d57781f57a93e74259c12c8feee661b9633c882307f3a6236e41dbf5ffe35 "$work/hermes" | sha256sum --check
chmod 755 "$work/hermes"

# Namespace creation and loopback setup affect only the new namespace.
sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
  env HOME="$work/lifecycle-tool-home" TMPDIR="$work" NO_PROXY=127.0.0.1,localhost \
  "$python" "$root/network/tools/mcnd_process_smoke.py" --binary "$work/mcnd" \
  --home-parent "$work/lifecycle" --history-role rpc-ibc --report "$output/daemon.json" \
  |& tee "$output/daemon.log"
preserve_public_reports
sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
  env TMPDIR="$work" "$python" "$root/network/internal/ibclab/hermes_lab.py" --mode full \
  --reference-binary "$work/ibcreference" --reference-sha256 "$reference_sha" --reference-source-commit "$revision" \
  --mcnd-binary "$work/mcnd" --mcnd-sha256 "$mcnd_sha" --mcnd-source-commit "$revision" \
  --hermes-binary "$work/hermes" --report "$output/hermes.json" |& tee "$output/hermes.log"
preserve_public_reports
"$python" - "$output" <<'PY'
import json,sys
from pathlib import Path
out=Path(sys.argv[1]);daemon=json.loads((out/'daemon.json').read_text());hermes=json.loads((out/'hermes.json').read_text())
assert daemon['status']=='PASS',daemon
assert hermes['status']=='TWO_PROCESS_HERMES_PASS' and hermes['release_gate'] is True,hermes
assert 'error' not in daemon and 'error' not in hermes,(daemon,hermes)
required={'client_connection_channel_handshake','native_send','voucher_return','pending_packet_restart','error_ack_refund','timeout_refund'}
assert set(hermes['phases'])==required,hermes['phases']
assert all(phase['status']=='PASS' for phase in hermes['phases'].values()),hermes['phases']
print('Default daemon lifecycle and all six genuine Hermes phases passed.')
PY

if [[ "$withdrawal_genesis" == withdrawal-genesis ]]; then
  # No fixture document, key or raw generator/daemon log is uploaded. The
  # generator runs on the same verified checkout immediately before the lab.
  mkdir -m 700 -- "$work/withdrawal-fixture" "$work/del-fixture"
  if ! MCND_WITHDRAWAL_GENESIS_FIXTURE_DIR="$work/withdrawal-fixture" \
      MCND_DEL_DELEGATION_FIXTURE_DIR="$work/del-fixture" \
      go test -mod=readonly -tags= -trimpath -p 2 -count=1 -timeout 8m -json -run '^TestOwnedAppSignedRegistryBootstrap$' ./systemcontracts \
      > "$work/withdrawal-fixture-build.log" 2>&1; then
    checkout_free="$(df -Pk "$root" | awk 'END {print $4}')" || checkout_free=unknown
    fixture_free="$(df -Pk "$work_parent" | awk 'END {print $4}')" || fixture_free=unknown
    printf 'Available worker space after fixture failure (KiB): checkout=%s fixture=%s\n' "$checkout_free" "$fixture_free"
    "$python" - "$work/withdrawal-fixture-build.log" <<'PY'
import json,re,sys
location=re.compile(r'\b[A-Za-z0-9_-]+\.go:\d+(?::\d+)?')
diagnostics=[]
categories=set()
with open(sys.argv[1], encoding='utf8') as stream:
    for line in stream:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            event={'Action':'build-output','Output':line}
        if event.get('Action') == 'fail':
            print('Private fixture generator failed:', event.get('Package'), event.get('Test', '(build or package)'))
        output=event.get('Output','')
        for category in ('no space left on device', 'signal: killed', 'test timed out'):
            if category in output.lower():
                categories.add(category)
        if event.get('Action')=='build-output' or 'Error Trace:' in output:
            for match in location.findall(output):
                if match not in diagnostics and len(diagnostics)<12:
                    diagnostics.append(match)
        for label in ('Received unexpected error', 'Should be true', 'Not equal', 'Should be empty'):
            if 'Error:' in output and label in output and label not in diagnostics and len(diagnostics)<12:
                diagnostics.append(label)
if diagnostics:
    print('Private fixture failure locations or assertions:', ', '.join(diagnostics))
if categories:
    print('Private fixture failure categories:', ', '.join(sorted(categories)))
PY
    exit 1
  fi
  sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
    env TMPDIR="$work" "$python" "$root/network/tools/withdrawal_genesis_smoke.py" \
    --binary "$work/mcnd" --binary-sha256 "$mcnd_sha" --source-commit "$revision" \
    --fixture "$work/withdrawal-fixture" --home-parent "$work" --report "$output/withdrawal-genesis.json" \
    |& tee "$output/withdrawal-genesis.log"
  preserve_public_reports
  "$python" - "$output/withdrawal-genesis.json" "$revision" "$mcnd_sha" <<'PY'
import json,sys
from pathlib import Path
report=json.loads(Path(sys.argv[1]).read_text())
assert report['status']=='PASS' and 'error' not in report,report
assert report['sourceCommit']==sys.argv[2] and report['binarySha256']==sys.argv[3],report
assert report['invalidGenesis']['status']=='REJECTED',report
print('Authenticated withdrawal genesis, completion, rejection and restart passed.')
PY
  sync_args=()
  if [[ "$del_state_sync" == del-state-sync ]]; then sync_args+=(--state-sync); fi
  sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
    env TMPDIR="$work" "$python" "$root/network/tools/del_delegation_smoke.py" \
    --binary "$work/mcnd" --binary-sha256 "$mcnd_sha" --source-commit "$revision" \
    --fixture "$work/del-fixture" --home-parent "$work" --report "$output/del-delegation.json" \
    "${sync_args[@]}" \
    |& tee "$output/del-delegation.log"
  preserve_public_reports
  "$python" - "$output/del-delegation.json" "$revision" "$mcnd_sha" "$del_state_sync" <<'PY'
import json,sys
from pathlib import Path
report=json.loads(Path(sys.argv[1]).read_text())
assert report['status']=='PASS' and 'error' not in report,report
assert report['sourceCommit']==sys.argv[2] and report['binarySha256']==sys.argv[3],report
assert all(report[step]['success'] for step in ('delegation','topUp','partialWithdraw','withdraw')),report
assert not report['repeatComplete']['success'],report
assert report['partialPayout']['amountAtomic']=='1000000000000000000' and report['partialPayout']['sdkGasCharged']>0,report
assert all(report[step]['nativePositionsVerified'] for step in ('before','delegation','topUp','partialWithdraw','partialPayout','restart','withdraw','payout','final')),report
assert report['restart']['activeStakeAtomic']=='4000000000000000000' and report['restart']['pendingAtomic']=='0',report
assert report['payout']['amountAtomic']=='4000000000000000000' and report['payout']['sdkGasCharged']>0,report
assert report['final']['activeStakeAtomic']==report['final']['wrappedSupplyAtomic']=='0',report
if sys.argv[4]=='del-state-sync':
    sync=report['stateSync']
    assert sync['snapshotHeight']==sync['sourceSnapshotHeight']>report['initialHeight'],report
    assert sync['trustHeight']==sync['witnessVerifiedThrough']==sync['snapshotHeight']+2 and len(sync['trustHash'])==64,report
    assert len(sync['sourceSnapshotAppHash'])==64 and sync['witnessIsSeparateProcess'] is True and sync['independentFailureDomains'] is False,report
    assert len({sync['sourceNodeID'],sync['witnessNodeID'],sync['observerNodeID']})==3,report
    assert sync['sourceVotingPower']>0 and sync['witnessVotingPower']==sync['observerVotingPower']==0,report
    assert sync['restored']['activeStakeAtomic']=='4000000000000000000',report
    assert sync['pending']['pendingAtomic']=='4000000000000000000',report
    assert sync['replayed']==report['final'] and sync['finalAppHash']==sync['witnessFinalAppHash'] and len(sync['finalAppHash'])==64,report
    print('Isolated P2P snapshot restore, separate RPC witness, financial replay and committed AppHash checks passed.')
print('Fresh DEL delegation, top-up, partial payout, repeat rejection, restart and remainder payout passed.')
PY
fi
