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
native_held_del_downtime="${9:-off}"
[[ "$native_held_del_downtime" == off || "$native_held_del_downtime" == native-held-del-downtime ]] || { echo 'Unsupported native held DEL downtime option.' >&2; exit 2; }
native_upgrade_governance="${10:-off}"
[[ "$native_upgrade_governance" == off || "$native_upgrade_governance" == native-upgrade-governance ||
   "$native_upgrade_governance" == native-full-profile-upgrade || "$native_upgrade_governance" == native-full-ibc-upgrade ||
   "$native_upgrade_governance" == native-cross-version-ibc-upgrade || "$native_upgrade_governance" == native-systemd-module-upgrade ]] || { echo 'Unsupported native upgrade governance option.' >&2; exit 2; }
native_nft_downtime="${11:-off}"
[[ "$native_nft_downtime" == off || "$native_nft_downtime" == native-nft-downtime ]] || { echo 'Unsupported native NFT downtime option.' >&2; exit 2; }
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
  for file in "$output/daemon.json" "$output/hermes.json" "$output/withdrawal-genesis.json" "$output/del-delegation.json" "$output/ibc-client-upgrade.json" "$output/native-consensus.json" "$output/native-hermes.json" "$output/native-del-downtime.json" "$output/native-del-jail-recovery.json" "$output/native-held-del-downtime.json" "$output/native-governance-upgrade.json" "$output/native-nft-downtime.json"; do
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
  local gate="$1" option="$2" recovery="$3" held="$4" nft="$5"
  local fixture="$work/$gate-fixture"
  mkdir -m 700 -- "$fixture"
  if ! MCND_NATIVE_CONSENSUS_FIXTURE_DIR="$fixture" MCND_NATIVE_CONSENSUS_DEL_DOWNTIME=1 MCND_NATIVE_CONSENSUS_DEL_JAIL_RECOVERY="$recovery" MCND_NATIVE_CONSENSUS_HELD_DEL_DOWNTIME="$held" MCND_NATIVE_CONSENSUS_NFT_DOWNTIME="$nft" \
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
  "$python" - "$output/$gate.json" "$revision" "$mcnd_sha" "$recovery" "$held" "$nft" <<'PY'
import json,sys
from datetime import datetime
from pathlib import Path
report=json.loads(Path(sys.argv[1]).read_text())
assert report['status']=='PASS' and 'error' not in report,report
assert report['sourceCommit']==sys.argv[2] and report['binarySha256']==sys.argv[3],report
expected_initial={'a':3,'b':3} if '1' in sys.argv[5:7] else {'a':3,'b':3,'c':2}
assert report['initialPower']==expected_initial and report['beforeSigners']==['a','b','c'],report
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
    topup=report['postJailTopUp']
    assert topup['success'] and topup['height']>report['final']['height'] and topup['effectiveHeight']==topup['height']+2,report
    assert topup['amountAtomic']=='250000000000000000' and topup['activeReservesAtomic']['c']=='2230000000000000000',report
    assert topup['jailed']==[] and topup['masterPenaltyCount']==topup['stakePenaltyCursor']==1,report
    assert topup['powers']=={'a':3,'b':3,'c':2} and topup['signers']==['a','b','c'],report
    assert topup['effectiveHeight']<=topup['cSignedHeight']<topup['effectiveHeight']+3,report
    assert topup['bankSupplyAtomic']==report['final']['bankSupplyAtomic'] and topup['appHashHeight']==topup['height']+1,report
    assert len(topup['app_hash'])==64,report
    print('Matured unpause restored C at H+2, then a signed DEL top-up raised C to power 2 without another burn.')
if sys.argv[5]=='1':
    funding,request,payout=(report[name] for name in ('heldFunding','heldWithdrawal','heldPayout'))
    assert funding['success'] and funding['effectiveHeight']==funding['height']+2,report
    assert funding['holdStart']<funding['holdDeadline'] and request['success'],report
    assert request['amountAtomic']==payout['amountAtomic']=='1980000000000000000',report
    assert payout['wrappedAtomic']=='6000000000000000000' and payout['height']>request['height'],report
    assert payout['activeReservesAtomic']=={'a':'3000000000000000000','b':'3000000000000000000','c':'0'},report
    assert payout['bankSupplyAtomic']==report['final']['bankSupplyAtomic'] and len(payout['app_hash'])==64,report
    print('Mature held DEL withdrawal paid the exact post-slash amount on all three replicas.')
if sys.argv[6]=='1':
    approval,funding,penalty=(report[name] for name in ('nftApproval','nftFunding','penalty'))
    assert approval['success'] and funding['success'] and funding['effectiveHeight']==funding['height']+2,report
    assert approval['nftCollateralAtomic']=='0' and funding['nftCollateralAtomic']=='2000000000000000000',report
    assert penalty['nftCollateralAtomic']==report['final']['nftCollateralAtomic']=='1980000000000000000',report
    assert approval['wrappedAtomic']==funding['wrappedAtomic']==penalty['wrappedAtomic']==report['final']['wrappedAtomic']=='6000000000000000000',report
    assert len(report['follower']['app_hash'])==64,report
    restored=report['stateSync']
    assert restored['restoredHeight']>=restored['sourceSnapshotHeight']>=report['final']['height'],report
    assert restored['verifiedHeight']>restored['restoredHeight'] and len(restored['app_hash'])==64,report
    assert restored['powers']=={'a':3,'b':3} and restored['signers']==['a','b'],report
    assert restored['jailed']==['c'] and restored['masterPenaltyCount']==restored['stakePenaltyCursor']==1,report
    assert restored['nftCollateralAtomic']==report['final']['nftCollateralAtomic'],report
    assert restored['bankSupplyAtomic']==report['final']['bankSupplyAtomic'],report
    print('NFT receipt custody, backing and penalty survived a fresh observer P2P snapshot restore.')
PY
}
if [[ "$native_del_downtime" == native-del-downtime ]]; then
  run_native_del_downtime native-del-downtime --downtime 0 0 0
fi
if [[ "$native_del_jail_recovery" == native-del-jail-recovery ]]; then
  run_native_del_downtime native-del-jail-recovery --jail-recovery 1 0 0
fi
if [[ "$native_held_del_downtime" == native-held-del-downtime ]]; then
  run_native_del_downtime native-held-del-downtime --held-downtime 0 1 0
fi
if [[ "$native_nft_downtime" == native-nft-downtime ]]; then
  run_native_del_downtime native-nft-downtime --nft-downtime 0 0 1
fi

if [[ "$native_upgrade_governance" != off ]]; then
  fixture="$work/native-governance-upgrade-fixture"
  full_profile=0
  pending_ibc=0
  predecessor_revision="$revision"
  predecessor_source="$root"
  predecessor="$work/mcnd"
  predecessor_sha="$mcnd_sha"
  profile_args=()
  source_args=()
  if [[ "$native_upgrade_governance" != native-upgrade-governance ]]; then
    full_profile=1
    profile_args=(--full-profile)
  fi
  if [[ "$native_upgrade_governance" == native-full-ibc-upgrade || "$native_upgrade_governance" == native-cross-version-ibc-upgrade ]]; then
    pending_ibc=1
    profile_args+=(--pending-ibc --reference-binary "$work/ibcreference" --reference-sha256 "$reference_sha" --hermes-binary "$work/hermes")
  fi
  if [[ "$native_upgrade_governance" == native-cross-version-ibc-upgrade ]]; then
    predecessor_revision=de3851c5d45102398244d21c78f57f129fff712d
    [[ "$predecessor_revision" != "$revision" ]] || { echo 'Cross-version rehearsal requires two source revisions.' >&2; exit 1; }
    git -C "$root" cat-file -e "$predecessor_revision^{commit}"
    predecessor_source="$work/predecessor-source"
    predecessor="$work/mcnd-predecessor"
    git clone --quiet --shared --no-checkout -- "$root" "$predecessor_source"
    git -C "$predecessor_source" checkout --quiet --detach "$predecessor_revision"
    [[ "$(git -C "$predecessor_source" rev-parse HEAD)" == "$predecessor_revision" ]]
    (
      cd "$predecessor_source/network"
      go mod download
      go mod verify |& tee "$output/native-governance-upgrade-predecessor-modules.log"
      git diff --exit-code -- go.mod go.sum
      "$python" scripts/verify_owned_evm.py
      "$python" scripts/verify_owned_geth.py
      "$python" scripts/verify_owned_sdk.py --compiled
      "$python" scripts/verify_pion_transport.py
      "$python" "$root/scripts/remote/build-inputs.py" --source "$predecessor_source" --target mcnd \
        --output "$output/native-governance-upgrade-predecessor-inputs-before.json"
      "$python" - "$output" "$predecessor_revision" "$revision" <<'PY'
import json,sys
from pathlib import Path
out=Path(sys.argv[1])
previous=json.loads((out/'native-governance-upgrade-predecessor-inputs-before.json').read_text())
current=json.loads((out/'mcnd-inputs-before.json').read_text())
assert previous['sourceRevision']==sys.argv[2] and current['sourceRevision']==sys.argv[3]
assert previous['buildPolicy']==current['buildPolicy'], 'cross-version toolchain policy differs'
old,new=previous['files'],current['files']
changed=sorted(path for path in old.keys()|new.keys() if old.get(path)!=new.get(path))
assert any(not path.endswith(('/go.mod','/go.sum')) for path in changed), 'cross-version rehearsal requires changed compiled source or embedded inputs'
(out/'native-governance-upgrade-source-comparison.json').write_text(json.dumps(
    {'predecessorSourceCommit':sys.argv[2],'replacementSourceCommit':sys.argv[3],'changedBuildInputs':changed},
    indent=2)+'\n')
PY
      go build -mod=readonly -tags= -buildvcs=true -p 2 -trimpath \
        -ldflags "-X main.buildCommit=$predecessor_revision" -o "$predecessor" ./cmd/mcnd
      "$python" "$root/scripts/remote/build-inputs.py" --source "$predecessor_source" --target mcnd \
        --output "$output/native-governance-upgrade-predecessor-inputs-after.json"
      cmp -- "$output/native-governance-upgrade-predecessor-inputs-before.json" "$output/native-governance-upgrade-predecessor-inputs-after.json"
      go version -m "$predecessor" > "$output/native-governance-upgrade-predecessor-build.log"
      "$python" - "$output" "$root" "$predecessor_source" "$predecessor_revision" <<'PY'
import json,sys
from pathlib import Path
out,root,source=map(Path,sys.argv[1:4])
sys.path.insert(0,str(root/'network/tools'))
from binary_provenance import verify_binary_build
verified=verify_binary_build((out/'native-governance-upgrade-predecessor-build.log').read_text(),
                             source,sys.argv[4],'go1.26.11')
(out/'native-governance-upgrade-predecessor-metadata-verified.json').write_text(json.dumps(verified,indent=2)+'\n')
PY
    )
    predecessor_sha="$(sha256sum "$predecessor" | awk '{print $1}')"
    source_args=(--predecessor-source-commit "$predecessor_revision")
  fi
  successor="$work/mcnd-native-sdk-migration-v1"
  successor_tags=mcnd_native_sdk_migration_v1
  coin_index_migration=0
  if [[ "$native_upgrade_governance" == native-systemd-module-upgrade ]]; then
    [[ "${GITHUB_ACTIONS:-}" == true && "${RUNNER_ENVIRONMENT:-}" == github-hosted ]]
    successor_tags+=,mcnd_native_coin_index_migration_ci
    profile_args+=(--systemd-migration)
    coin_index_migration=1
    go test -mod=readonly -tags="$successor_tags" -p 2 -count=1 \
      -run '^TestAddressIndexMigrationPreservesCoinState$' ./decimal/coin/keeper
  fi
  go build -mod=readonly -tags="$successor_tags" -buildvcs=true -p 2 -trimpath \
    -ldflags "-X main.buildCommit=$revision" -o "$successor" ./cmd/mcnd
  go version -m "$successor" > "$output/native-governance-upgrade-successor-build.log"
  successor_sha="$(sha256sum "$successor" | awk '{print $1}')"
  mkdir -m 700 -- "$fixture"
  if ! (cd "$predecessor_source/network" &&
      MCND_NATIVE_CONSENSUS_FIXTURE_DIR="$fixture" MCND_NATIVE_CONSENSUS_UPGRADE_GOVERNANCE=1 \
      MCND_NATIVE_CONSENSUS_FULL_PROFILE_UPGRADE="$full_profile" \
      MCND_NATIVE_CONSENSUS_UPGRADE_PENDING_IBC="$pending_ibc" \
      MCND_NATIVE_COIN_INDEX_MIGRATION_CI="$coin_index_migration" \
      go test -mod=readonly -tags= -trimpath -p 2 -count=1 -timeout 8m -json \
      -run '^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$' ./systemcontracts \
      > "$work/native-governance-upgrade-fixture.log" 2>&1); then
    private_fixture_diagnostic "$work/native-governance-upgrade-fixture.log"
    exit 1
  fi
  if [[ "$native_upgrade_governance" == native-systemd-module-upgrade ]]; then
    signer_home=$(mktemp -d "$work/upgrade-signer.XXXXXXXX")
    gpg --homedir "$signer_home" --batch --no-tty --pinentry-mode loopback --passphrase '' \
      --quick-generate-key "MintCandy module migration CI ${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}" ed25519 sign 0
    signer=$(gpg --homedir "$signer_home" --batch --with-colons --list-keys | awk -F: '$1 == "fpr" {print $10}')
    [[ "$signer" =~ ^[A-F0-9]{40}$ ]]
    gpg --homedir "$signer_home" --batch --export "$signer" > "$work/upgrade-trusted-keyring.gpg"
    for phase in predecessor successor; do
      executable=$predecessor
      binary_sha=$predecessor_sha
      if [[ "$phase" == successor ]]; then
        executable=$successor
        binary_sha=$successor_sha
      fi
      archive="$work/$phase.tar.gz"
      extracted="$work/$phase-verified"
      package_report="$output/native-governance-$phase-package.json"
      "$python" -B "$root/network/tools/native_launch_package.py" build --binary "$executable" \
        --genesis "$fixture/genesis.json" --source-commit "$revision" --output "$archive" \
        --signer-home "$signer_home" --signer-fingerprint "$signer"
      "$python" -B "$root/network/tools/native_launch_package.py" verify --archive "$archive" \
        --trusted-keyring "$work/upgrade-trusted-keyring.gpg" --signer-fingerprint "$signer" \
        --output-dir "$extracted" > "$package_report"
      [[ "$(sha256sum "$extracted/bin/mcnd" | awk '{print $1}')" == "$binary_sha" ]]
      cmp -- "$extracted/genesis.json" "$fixture/genesis.json"
    done
    predecessor="$work/predecessor-verified/bin/mcnd"
    successor="$work/successor-verified/bin/mcnd"
  fi
  run_native_upgrade() {
    sudo -n unshare --net -- bash -euc 'ip link set lo up; exec "$@"' _ \
      env TMPDIR="$work" "$python" -B "$root/network/tools/native_governance_upgrade_smoke.py" \
      --binary "$predecessor" --binary-sha256 "$predecessor_sha" --source-commit "$revision" \
      --replacement-binary "$successor" --replacement-sha256 "$successor_sha" \
      --fixture "$fixture" --home-parent "$work" "${profile_args[@]}" "$@"
  }
  if [[ "$predecessor_revision" != "$revision" ]]; then
    if run_native_upgrade --predecessor-source-commit "$revision" --report "$work/invalid-same-source.json" \
        > "$work/invalid-same-source.log" 2>&1; then
      echo 'Cross-version runner accepted the same source twice.' >&2
      exit 1
    fi
    "$python" - "$work/invalid-same-source.json" <<'PY'
import json,sys
from pathlib import Path
report=json.loads(Path(sys.argv[1]).read_text())
assert report['status']=='FAIL' and report['phase']=='fixture-provenance'
assert report['error']=='cross-version handoff requires the pending IBC profile and distinct full source revisions'
print('Cross-version runner rejected the same source before opening a private fixture.')
PY
  fi
  run_native_upgrade "${source_args[@]}" --report "$output/native-governance-upgrade.json" |& tee "$output/native-governance-upgrade.log"
  preserve_public_reports
  "$python" - "$output" "$root" "$revision" "$predecessor_revision" "$predecessor_sha" "$successor_sha" "$full_profile" "$pending_ibc" "$successor_tags" <<'PY'
import json,sys
from pathlib import Path
out,root=map(Path,sys.argv[1:3])
revision,predecessor_revision,predecessor_sha,successor_sha,full_profile,pending_ibc,successor_tags=sys.argv[3:]
sys.path.insert(0,str(root/'network/tools'))
from binary_provenance import parse_build_info
default_build=parse_build_info((out/'mcnd-build.log').read_text())
successor=parse_build_info((out/'native-governance-upgrade-successor-build.log').read_text())
assert successor['settings'].pop('-tags') == successor_tags
assert successor==default_build, 'successor build differs from its source default beyond the planned build tag'
cross_version=predecessor_revision!=revision
if cross_version:
    predecessor=json.loads((out/'native-governance-upgrade-predecessor-metadata-verified.json').read_text())
    assert predecessor['settings']['vcs.revision']==predecessor_revision
    assert predecessor['settings']['vcs.revision']!=successor['settings']['vcs.revision']
report=json.loads((out/'native-governance-upgrade.json').read_text())
assert report['status']=='PASS' and 'error' not in report,report
assert report['sourceCommit']==revision and report['binarySha256']==predecessor_sha,report
assert report['replacementSha256']==successor_sha and successor_sha!=predecessor_sha,report
assert report['predecessorSourceCommit']==predecessor_revision and report['replacementSourceCommit']==revision,report
assert report['fullProfile'] is (full_profile == '1'),report
systemd_migration='mcnd_native_coin_index_migration_ci' in successor_tags.split(',')
expected_kind=('systemd-coordinated-module-migration' if systemd_migration else
               'distinct-source-same-family-handler-rehearsal' if cross_version else 'same-source-handler-rehearsal')
assert report['planKind']==expected_kind,report
assert report['rejected']['status']=='PROPOSAL_STATUS_REJECTED' and report['passed']['status']=='PROPOSAL_STATUS_PASSED',report
plan,application,progress=report['schedule'],report['application'],report['progress']
assert plan['scheduledAt'] < plan['targetHeight'] == application['appliedHeight'] <= progress['height']-3,report
assert report['predecessor']['committedHeight']+1==plan['targetHeight']==report['predecessor']['boundaryHeight'],report
assert application['sameBinary'] is False and application['processRestarted'] is True,report
if full_profile == '1':
    assert report['ibcCoverage']==('pending-packet-after-client-update' if pending_ibc=='1' else 'unbonding-state-only'),report
    claim=report['withdrawal']
    before,after=claim['before'],claim['after']
    assert claim['unbondingSeconds']==28*24*60*60,report
    assert claim['height']<before['height']==plan['targetHeight']-1<after['height']==progress['height'],report
    assert before['appHash']==application['block']['app_hash'],report
    for field,value in {'pendingAtomic':'250000000000000000','activeAtomic':'1750000000000000000',
                        'wrappedBackingAtomic':'3000000000000000000','dueAt':before['dueAt'],
                        'nativeSupplyAtomic':before['nativeSupplyAtomic']}.items():
        assert before[field]==after[field]==value,report
    assert progress['powers']=={'a':1,'b':1} and progress['signers']==['a','b'],report
if systemd_migration:
    migration,rollback,systemd=report['coinMigration'],report['rollback'],report['systemd']
    assert (migration['before']['version'],migration['after']['version'])==(1,2),report
    assert migration['before']['height']==plan['targetHeight']-1 and migration['after']['height']==plan['targetHeight'],report
    assert rollback['coinBefore']==migration['before'] and rollback['coinAfter']==migration['after'],report
    assert rollback['restoredHomeSha256']==systemd['backupHomeSha256'] and len(rollback['restoredHomeSha256'])==3,report
    assert rollback['restoredAppHash']==application['block']['app_hash'],report
    assert rollback['migrationAppHash']==migration['appHash'] and rollback['sameCanonicalHistory'] is True,report
    assert rollback['verifiedHeight']==migration['verifiedHeight'] and rollback['appHash']==migration['verifiedAppHash'],report
    assert rollback['votingPower']==rollback['witnessVotingPower']==0 and rollback['catchingUp'] is False,report
    for phase,expected_sha in [('predecessor',predecessor_sha),('successor',successor_sha)]:
        package=json.loads((out/f'native-governance-{phase}-package.json').read_text())
        assert package['binarySha256']==expected_sha and package['genesisSha256']==report['genesisSha256'],report
if pending_ibc == '1':
    packet=report['ibcPacket']
    assert len(packet['packets'])==1 and packet['escrowAtomic']=='1000000000000000000',report
    assert packet['clientHeightBefore']<plan['targetHeight']<packet['clientHeightAfter'],report
    phases=report['phases']
    assert set(phases)=={'client_connection_channel_handshake','pending_packet_submit_before_native_upgrade',
                         'pending_packet_across_native_upgrade',
                         'post_native_upgrade_voucher_return'},report
    assert all(phase['status']=='PASS' and (phase['owned_fees'] or phase['counterparty_fees']) for phase in phases.values()),report
    rollback=report['rollback']
    assert rollback['mode']=='coordinated-private-copy-restore',report
    assert rollback['predecessorHeight']==plan['targetHeight']-1 and rollback['replayedHeight']==plan['targetHeight'],report
    assert rollback['boundaryBlockHash']==application['block']['block_hash'],report
    restored=rollback['restoredClaim']
    assert restored['height']==plan['targetHeight'] and restored['appHash']==rollback['primaryAppliedAppHash'],report
    for field in ('pendingAtomic','activeAtomic','dueAt','wrappedBackingAtomic','nativeSupplyAtomic'):
        assert restored[field]==before[field],report
    ledger=rollback['pendingLedger']
    assert ledger['packets']==packet['packets'] and ledger['escrowAtomic']==packet['escrowAtomic'],report
    assert ledger['referenceVoucherAtomic']=='0' and ledger['referenceClientHeight']<plan['targetHeight'],report
print('Native governance tally, exact predecessor halt, successor migration and continued three-node consensus passed.')
PY
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
