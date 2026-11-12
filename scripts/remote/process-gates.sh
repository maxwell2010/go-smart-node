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
ibc_client_upgrade="${4:-off}"
[[ "$ibc_client_upgrade" == off || "$ibc_client_upgrade" == ibc-client-upgrade ]] || { echo 'Unsupported IBC client upgrade option.' >&2; exit 2; }
native_consensus="${5:-off}"
[[ "$native_consensus" == off || "$native_consensus" == native-consensus || "$native_consensus" == native-consensus-fee-rewards ]] || { echo 'Unsupported native consensus option.' >&2; exit 2; }
native_ibc_v1="${6:-off}"
[[ "$native_ibc_v1" == off || "$native_ibc_v1" == native-ibc-v1 ]] || { echo 'Unsupported native IBC v1 option.' >&2; exit 2; }
native_del_downtime="${7:-off}"
[[ "$native_del_downtime" == off || "$native_del_downtime" == native-del-downtime ]] || { echo 'Unsupported native DEL downtime option.' >&2; exit 2; }
native_del_jail_recovery="${8:-off}"
[[ "$native_del_jail_recovery" == off || "$native_del_jail_recovery" == native-del-jail-recovery ]] || { echo 'Unsupported native DEL jail recovery option.' >&2; exit 2; }
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
  for file in "$output/daemon.json" "$output/hermes.json" "$output/withdrawal-genesis.json" "$output/del-delegation.json" "$output/ibc-client-upgrade.json" "$output/native-consensus.json" "$output/native-hermes.json" "$output/native-del-downtime.json" "$output/native-del-jail-recovery.json"; do
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

if [[ "$ibc_client_upgrade" == ibc-client-upgrade ]]; then
  overlay_handler="$root/network/internal/ibclab/rehearsal_handler.go.tmpl"
  "$python" - "$root/network/app/upgrades.go" "$overlay_handler" "$work/ibc-upgrade-overlay.json" <<'PY'
import json,sys
from pathlib import Path
original,replacement,output=map(Path,sys.argv[1:])
with output.open('x') as stream:
    json.dump({'Replace': {str(original.resolve(strict=True)):str(replacement.resolve(strict=True))}},stream)
PY
  go build -mod=readonly -tags= -buildvcs=true -p 2 -trimpath -overlay "$work/ibc-upgrade-overlay.json" \
    -ldflags "-X main.buildCommit=$revision" -o "$work/mcnd-ibc-upgrade-rehearsal" ./cmd/mcnd
  go version -m "$work/mcnd-ibc-upgrade-rehearsal" > "$output/ibc-upgrade-rehearsal-build.log"
  replacement_sha="$(sha256sum "$work/mcnd-ibc-upgrade-rehearsal" | awk '{print $1}')"
  overlay_sha="$(sha256sum "$overlay_handler" | awk '{print $1}')"
  git diff --exit-code -- app/upgrades.go
  sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
    env TMPDIR="$work" "$python" "$root/network/internal/ibclab/hermes_lab.py" --mode client-upgrade \
    --reference-binary "$work/ibcreference" --reference-sha256 "$reference_sha" --reference-source-commit "$revision" \
    --mcnd-binary "$work/mcnd" --mcnd-sha256 "$mcnd_sha" --mcnd-source-commit "$revision" \
    --replacement-binary "$work/mcnd-ibc-upgrade-rehearsal" --replacement-sha256 "$replacement_sha" \
    --hermes-binary "$work/hermes" --report "$output/ibc-client-upgrade.json" |& tee "$output/ibc-client-upgrade.log"
  preserve_public_reports
  "$python" - "$output" "$root" "$revision" "$replacement_sha" "$overlay_sha" <<'PY'
import json,sys
from pathlib import Path
out,root=map(Path,sys.argv[1:3]);revision,replacement_sha,overlay_sha=sys.argv[3:]
sys.path.insert(0,str(root/'network/tools'))
from binary_provenance import parse_build_info
default=parse_build_info((out/'mcnd-build.log').read_text())
replacement=parse_build_info((out/'ibc-upgrade-rehearsal-build.log').read_text())
for key in ('path','module','dependencies','goVersion'):
    assert replacement[key]==default[key], key
assert replacement['settings']['vcs.revision']==revision
report=json.loads((out/'ibc-client-upgrade.json').read_text())
assert report['status']=='HERMES_CLIENT_UPGRADE_PASS' and report['upgrade_phase']=='complete',report
assert report['release_gate'] is False and 'error' not in report,report
assert report['binaries']['replacement']['sha256']==replacement_sha,report
required={'client_connection_channel_handshake','pending_packet_client_upgrade',
          'post_upgrade_voucher_return','post_upgrade_error_ack_refund'}
assert set(report['phases'])==required and all(phase['status']=='PASS' for phase in report['phases'].values()),report
(out/'ibc-upgrade-rehearsal-provenance.json').write_text(json.dumps({
    'sourceCommit':revision,'replacementSHA256':replacement_sha,'handlerOverlaySHA256':overlay_sha,
    'sameModuleGraph':True,'scope':'Isolated rehearsal executable only'
},indent=2)+'\n')
PY
fi

private_fixture_diagnostic() {
  local checkout_free fixture_free
  checkout_free="$(df -Pk "$root" | awk 'END {print $4}')" || checkout_free=unknown
  fixture_free="$(df -Pk "$work_parent" | awk 'END {print $4}')" || fixture_free=unknown
  printf 'Available worker space after fixture failure (KiB): checkout=%s fixture=%s\n' "$checkout_free" "$fixture_free"
  "$python" - "$1" <<'PY'
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
}

if [[ "$withdrawal_genesis" == withdrawal-genesis ]]; then
  # No fixture document, key or raw generator/daemon log is uploaded. The
  # generator runs on the same verified checkout immediately before the lab.
  mkdir -m 700 -- "$work/withdrawal-fixture" "$work/del-fixture"
  if ! MCND_WITHDRAWAL_GENESIS_FIXTURE_DIR="$work/withdrawal-fixture" \
      MCND_DEL_DELEGATION_FIXTURE_DIR="$work/del-fixture" \
      go test -mod=readonly -tags= -trimpath -p 2 -count=1 -timeout 8m -json -run '^TestOwnedAppSignedRegistryBootstrap$' ./systemcontracts \
      > "$work/withdrawal-fixture-build.log" 2>&1; then
    private_fixture_diagnostic "$work/withdrawal-fixture-build.log"
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


if [[ "$native_consensus" != off ]]; then
  mkdir -m 700 -- "$work/native-consensus-fixture"
  fee_rewards=0
  fee_args=()
  if [[ "$native_consensus" == native-consensus-fee-rewards ]]; then
    fee_rewards=1
    fee_args+=(--fee-rewards)
  fi
  if ! MCND_NATIVE_CONSENSUS_FIXTURE_DIR="$work/native-consensus-fixture" MCND_NATIVE_CONSENSUS_FEE_REWARDS="$fee_rewards" \
      go test -mod=readonly -tags= -trimpath -p 2 -count=1 -timeout 8m -json \
      -run '^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$' ./systemcontracts \
      > "$work/native-consensus-fixture.log" 2>&1; then
    private_fixture_diagnostic "$work/native-consensus-fixture.log"
    exit 1
  fi
  sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
    env TMPDIR="$work" "$python" -B "$root/network/tools/native_consensus_smoke.py" \
    --binary "$work/mcnd" --binary-sha256 "$mcnd_sha" --source-commit "$revision" \
    --fixture "$work/native-consensus-fixture" --home-parent "$work" --report "$output/native-consensus.json" "${fee_args[@]}" \
    |& tee "$output/native-consensus.log"
  preserve_public_reports
  "$python" - "$output/native-consensus.json" "$revision" "$mcnd_sha" "$fee_rewards" <<'PY'
import json,sys
from pathlib import Path
report=json.loads(Path(sys.argv[1]).read_text())
assert report['status']=='PASS' and 'error' not in report,report
assert report['sourceCommit']==sys.argv[2] and report['binarySha256']==sys.argv[3],report
assert report['initialPower']=={'a':2,'b':1} and report['initialSigners']==['a','b'],report
assert report['delegation']['success'] and report['abciUpdates']=={'b':0,'c':3},report
for label,powers in (('H+0',{'a':2,'b':1}),('H+1',{'a':2,'b':1}),('H+2',{'a':2,'c':3})):
    assert report['transition'][label]['powers']==powers,report
assert report['transition']['H+2']['signers']==['a','c'],report
assert report['restart']['powers']=={'a':2,'c':3} and report['restart']['signers']==['a','c'],report
assert report['restart']['height']>report['restart']['checkpointHeight'],report
assert report['before']['wrappedAtomic']=='3000000000000000000' and report['funded']['wrappedAtomic']=='6000000000000000000',report
assert report['topUp']['success'] and report['topUp']['height']>report['transition']['H+2']['height'],report
assert report['cProposed']['height']>=report['topUp']['height'] and report['final']['wrappedAtomic']=='6250000000000000000',report
assert report['feeRewards'] is (sys.argv[4]=='1'),report
assert report['before']['bankSupplyAtomic']==report['final']['bankSupplyAtomic'],report
if report['feeRewards']:
    assert report['before']['feeCollectorAtomic']==report['final']['feeCollectorAtomic']=='0',report
    for tx,settlement,powers in (('delegation','firstFeeSettlement',(2,1,0)), ('topUp','topUpFeeSettlement',(2,0,3))):
        fee=int(report[tx]['feeAtomic'])
        paid=report[settlement]
        assert paid['height']==report[tx]['height']+1 and paid['feeCollectorAtomic']=='0',report
        expected={name:str(fee*power//sum(powers)//4) for name,power in zip('abc',powers,strict=True)}
        assert paid['commissionPaidAtomic']==expected and int(paid['ownerRewardAtomic'])==fee-sum(map(int,expected.values())),report
else:
    assert int(report['funded']['feeCollectorAtomic'])-int(report['before']['feeCollectorAtomic'])==int(report['delegation']['feeAtomic']),report
    assert int(report['final']['feeCollectorAtomic'])-int(report['funded']['feeCollectorAtomic'])==int(report['topUp']['feeAtomic']),report
print('Three replicas agree on the signed DEL validator replacement at H+2 and on restart.')
PY
fi

run_native_del_downtime() {
  local gate="$1" option="$2" recovery="$3"
  local fixture="$work/$gate-fixture"
  mkdir -m 700 -- "$fixture"
  if ! MCND_NATIVE_CONSENSUS_FIXTURE_DIR="$fixture" MCND_NATIVE_CONSENSUS_DEL_DOWNTIME=1 MCND_NATIVE_CONSENSUS_DEL_JAIL_RECOVERY="$recovery" \
      go test -mod=readonly -tags= -trimpath -p 2 -count=1 -timeout 8m -json \
      -run '^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$' ./systemcontracts \
      > "$fixture.log" 2>&1; then
    private_fixture_diagnostic "$fixture.log"
    exit 1
  fi
  sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
    env TMPDIR="$work" "$python" -B "$root/network/tools/native_consensus_smoke.py" \
    --binary "$work/mcnd" --binary-sha256 "$mcnd_sha" --source-commit "$revision" \
    --fixture "$fixture" --home-parent "$work" --report "$output/$gate.json" "$option" \
    |& tee "$output/$gate.log"
  preserve_public_reports
  "$python" - "$output/$gate.json" "$revision" "$mcnd_sha" "$recovery" <<'PY'
import json,sys
from datetime import datetime
from pathlib import Path
report=json.loads(Path(sys.argv[1]).read_text())
assert report['status']=='PASS' and 'error' not in report,report
assert report['sourceCommit']==sys.argv[2] and report['binarySha256']==sys.argv[3],report
assert report['initialPower']=={'a':3,'b':3,'c':2} and report['beforeSigners']==['a','b','c'],report
penalty=report['downtime']
assert penalty['height']==penalty['infractionHeight']+1 and penalty['abciUpdates']=={'c':0},report
assert penalty['burnedAtomic']=='20000000000000000' and len(penalty['missedCommits'])==3,report
assert report['transition']['H+2']['height']==penalty['height']+2,report
assert report['follower']['powers']=={'a':3,'b':3} and report['follower']['signers']==['a','b'],report
assert report['final']['activeReservesAtomic']=={'a':'3000000000000000000','b':'3000000000000000000','c':'1980000000000000000'},report
assert report['final']['masterPenaltyCount']==report['final']['stakePenaltyCursor']==1,report
assert int(report['before']['bankSupplyAtomic'])-int(report['final']['bankSupplyAtomic'])==int(penalty['burnedAtomic']),report
print('Three isolated nodes proved one ordinary DEL downtime burn, H+2 removal and follower catch-up.')
if sys.argv[4]=='1':
    early,success=report['earlyUnpause'],report['unpause']
    timestamp=datetime.fromisoformat
    assert not early['success'] and success['success'],report
    assert timestamp(early['blockTime'])<timestamp(report['jailUntil'])<=timestamp(success['blockTime']),report
    assert early['jailed']==['c'] and success['jailed']==report['final']['jailed']==[],report
    reentry=report['reentry']
    assert reentry['abciUpdates']=={'c':1} and reentry['signers']==['a','b','c'],report
    assert reentry['transition']['H+2']['height']==success['height']+2,report
    assert reentry['transition']['H+2']['powers']=={'a':3,'b':3,'c':1},report
    assert reentry['cSignedHeight']>=success['height']+2 and report['final']['appHashHeight']==report['final']['height']+1,report
    assert report['final']['app_hash'] and len(report['final']['app_hash'])==64,report
    print('Signed early unpause rolled back; matured unpause restored C at H+2 and all replicas agreed without another burn.')
PY
}
if [[ "$native_del_downtime" == native-del-downtime ]]; then
  run_native_del_downtime native-del-downtime --downtime 0
fi
if [[ "$native_del_jail_recovery" == native-del-jail-recovery ]]; then
  run_native_del_downtime native-del-jail-recovery --jail-recovery 1
fi

if [[ "$native_ibc_v1" == native-ibc-v1 ]]; then
  mkdir -m 700 -- "$work/native-hermes-fixture"
  if ! MCND_NATIVE_CONSENSUS_FIXTURE_DIR="$work/native-hermes-fixture" MCND_NATIVE_CONSENSUS_FEE_REWARDS=1 MCND_NATIVE_CONSENSUS_IBC_V1=1 \
      go test -mod=readonly -tags= -trimpath -p 2 -count=1 -timeout 8m -json \
      -run '^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$' ./systemcontracts \
      > "$work/native-hermes-fixture.log" 2>&1; then
    private_fixture_diagnostic "$work/native-hermes-fixture.log"
    exit 1
  fi
  sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
    env TMPDIR="$work" "$python" -B "$root/network/internal/ibclab/hermes_lab.py" --mode native-full \
    --mcnd-binary "$work/mcnd" --mcnd-sha256 "$mcnd_sha" --mcnd-source-commit "$revision" \
    --reference-binary "$work/ibcreference" --reference-sha256 "$reference_sha" --reference-source-commit "$revision" \
    --hermes-binary "$work/hermes" --native-fixture "$work/native-hermes-fixture" --report "$output/native-hermes.json" \
    |& tee "$output/native-hermes.log"
  preserve_public_reports
  "$python" - "$output/native-hermes.json" "$revision" "$mcnd_sha" "$reference_sha" <<'PY'
import json,sys
from pathlib import Path
report=json.loads(Path(sys.argv[1]).read_text())
assert report['status']=='NATIVE_CONSENSUS_HERMES_PASS' and report['release_gate'] is True and 'error' not in report,report
assert report['binaries']['mcnd']=={'source_commit':sys.argv[2],'sha256':sys.argv[3]},report
assert report['binaries']['reference']=={'source_commit':sys.argv[2],'sha256':sys.argv[4]},report
probe=report['owned_probe']
assert probe['replicas']==3 and probe['unbonding_seconds']==28*24*60*60 and probe['consensus']['signers']==['a','b'],report
phases=report['phases']
assert set(phases)=={'client_connection_channel_handshake','native_send','voucher_return','foreign_bank_voucher',
                     'foreign_bank_voucher_return','pending_packet_restart','error_ack_refund','timeout_refund'},report
assert all(phase['status']=='PASS' and (phase['owned_fees'] or phase['counterparty_fees']) for phase in phases.values()),report
restart=phases['pending_packet_restart']
assert len(restart['preserved_packets'])==1 and restart['native_after_restart']['height']>restart['native_before_restart']['height'],report
final=report['native_final_consensus']
assert final['height']>restart['native_after_restart']['height'] and final['power']=={'a':2,'b':1},report
print('Native consensus bank ICS-20 v1, both voucher directions, disk restart and refunds passed with Hermes.')
PY
fi
