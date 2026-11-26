"""Exercise bootstrap governance with two tMSP binaries and four loopback validators."""

import argparse
import base64
from contextlib import ExitStack
from datetime import datetime
import hashlib
import json
from pathlib import Path
import tempfile

from check_operator_applications import native_address_from_evm
from del_delegation_smoke import write_config
from mcnd_process_smoke import ProcessOwner, SignalGuard, private_environment, private_namespace
from native_consensus_smoke import ATOMS, committed_header, create_replica, decode_pubkey, effective_set, wait_replicas
from native_governance_upgrade_smoke import (
    SCHEMA, VOTING, applied_height, check_proposal, check_schedule, committed_height, current_plan,
    find_four_signature_commit, query_proposal, wait_predecessor_halt, wait_tally,
)
from native_launch_nft_refund import governance_tx
from native_launch_rehearsal import configure_peers, load_fixture
from withdrawal_genesis_smoke import bech32_payload, check

PLAN = 'mintcandy-bootstrap-unanimity-v1'
CHAIN = 'mintcandy_990098-1'


def rehearsal(args, root, owner, report):
    raw, expected, keys = load_fixture(args.fixture, four_validators=True, empty_user_assets=True,
                                       assembly_report=args.assembly_report, network_profile='testnet')
    genesis = json.loads(raw)
    state = genesis['app_state']
    state['gov']['params'].update(voting_period='70s', expedited_voting_period='20s')
    genesis = json.dumps(genesis, separators=(',', ':')).encode()
    report.update(genesisSha256=hashlib.sha256(genesis).hexdigest(), governancePeriod='70s', matchingValidators=4)
    env = private_environment(root)
    rows = expected['validators']
    operators = {r['consensus_pubkey']['key']: bech32_payload(r['operator_address'], 'd0valoper')
                 for r in state['validator']['validators']}
    signers = [native_address_from_evm(operators[base64.b64encode(decode_pubkey(r['publicKey'])).decode()])
               for r in rows] + [expected['actorNative']]
    check(len(set(signers)) == 5, 'four separate operators and one founder required')
    nodes = [create_replica(str(args.predecessor), root, row['name'], i, genesis, key, env,
                            chain_id=CHAIN, evm_chain_id=990098)
             for i, (row, key) in enumerate(zip(rows, keys, strict=True))]
    for n in nodes:
        configure_peers(n, [other for other in nodes if other is not n])
        write_config(n.home, 'config.toml', {('consensus', 'timeout_commit'): '"2s"'})
    for n in nodes:
        n.start(str(args.predecessor), env, owner, 'predecessor')
    wait_replicas(nodes, 5, 105, report)
    powers = [row['initialPower'] for row in rows]
    effective_set(nodes, rows, 4, powers)
    source = nodes[0]
    weight = str(sum(powers) * ATOMS)

    def tx(action, index, label):
        receipt = governance_tx(args, source, root, env, report, action, index, signers[index], label,
                                fixture_test='TestExportBootstrapAction')
        report['transactions'][label] = receipt
        return receipt['height']

    target = committed_height(source) + 55
    report['activationHeight'] = target
    plan = {'planHeight': target, 'passedID': 1}
    manifest = SCHEMA + ':' + json.dumps({'name': PLAN, 'height': target}, separators=(',', ':'))
    tx({'height': target}, 4, 'submit-upgrade')
    for index in range(5):
        last = tx({'proposalId': 1}, index, f'vote-upgrade-{index}')
    check(query_proposal(source, 1, last)['status'] == VOTING, 'activation proposal closed before old deadline')
    height = wait_tally(source, 1, last, report, timeout=90)
    wait_replicas(nodes, height + 1, 40, report)
    report['upgradeProposal'] = check_proposal(nodes, 1, height, manifest, True, weight)
    check_schedule(nodes, plan, height, PLAN)
    wait_predecessor_halt(nodes, plan, report, PLAN)
    report['priorHeader'] = committed_header(nodes, target - 1)
    for n in nodes:
        owner.stop(n.child)
    for n in nodes:
        n.start(str(args.successor), env, owner, 'successor')
    wait_replicas(nodes, target + 2, 120, report)
    check(all(applied_height(n, target, PLAN) == target and current_plan(n, target) is None for n in nodes),
          'successor did not apply the approved upgrade once')
    report['afterUpgradeHeader'] = committed_header(nodes, target + 1)
    submitted = tx({'signal': True}, 4, 'submit-signal')
    deadline = query_proposal(source, 2, submitted)['voting_end_time']
    for index in range(4):
        last = tx({'proposalId': 2}, index, f'vote-signal-{index}')
    check(query_proposal(source, 2, last)['status'] == VOTING, 'operators closed proposal without founder consent')
    last = tx({'proposalId': 2}, 4, 'vote-signal-founder')
    wait_replicas(nodes, last + 1, 40, report)
    report['signalProposal'] = check_proposal(nodes, 2, last, 'bootstrap-unanimity-signal', True, weight)
    events = source.rpc(f'block_results?height={last}').get('finalize_block_events') or []
    check(any(e['type'] == 'mintcandy_unanimous_voting_closed' and
              any(a['key'] == 'proposal_id' and a['value'] == '2' for a in e['attributes']) for e in events),
          'missing early-close event on the final signed vote block')
    closed = query_proposal(source, 2, last)['voting_end_time']
    parse = datetime.fromisoformat
    check(parse(closed) < parse(deadline), 'proposal closed at the ordinary deadline')
    report.update(originalDeadline=deadline, closed=closed, afterVoteHeader=committed_header(nodes, last + 1),
                  finalCommit=find_four_signature_commit(nodes, rows, args.commit_probe, last + 1, last + 8, report))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ('fixture', 'assembly-report', 'predecessor', 'successor', 'signer', 'commit-probe', 'home-parent', 'report'):
        parser.add_argument('--' + flag, type=Path, required=True)
    args = parser.parse_args()
    private_namespace()
    report = {'initialHeight': 1, 'chainId': CHAIN, 'transactions': {}, 'status': 'RUNNING'}
    for role in ('predecessor', 'successor'):
        with getattr(args, role).open('rb') as stream:
            report[role + 'SHA256'] = hashlib.file_digest(stream, 'sha256').hexdigest()
    check(report['predecessorSHA256'] != report['successorSHA256'], 'identical daemons')
    with args.report.open('x') as output, SignalGuard() as signals, ExitStack() as stack:
        root = Path(stack.enter_context(tempfile.TemporaryDirectory(dir=args.home_parent)))
        owner = ProcessOwner(signals)
        stack.callback(owner.close)
        try:
            rehearsal(args, root, owner, report)
            report['status'] = 'PASS'
        finally:
            if report['status'] != 'PASS':
                report['status'] = 'FAIL'
            public = ('status', 'chainId', 'genesisSha256', 'governancePeriod', 'matchingValidators',
                      'predecessorSHA256', 'successorSHA256', 'transactions', 'activationHeight',
                      'upgradeProposal', 'priorHeader', 'afterUpgradeHeader', 'signalProposal',
                      'originalDeadline', 'closed', 'afterVoteHeader', 'finalCommit')
            json.dump({k: report[k] for k in public if k in report}, output, indent=2)
