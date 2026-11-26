"""Rehearse the MSP tariff with disposable validators and signed transactions."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
from tempfile import TemporaryDirectory
from urllib.error import HTTPError

from check_operator_applications import native_address_from_evm
from del_delegation_smoke import write_config
from mcnd_process_smoke import ProcessOwner, SignalGuard, private_environment, private_namespace
from native_consensus_smoke import ATOMS, committed_header, create_replica, decode_pubkey, wait_replicas
from native_governance_upgrade_smoke import (SCHEMA, applied_height, check_proposal, check_schedule, committed_height,
                                            current_plan, find_four_signature_commit, wait_predecessor_halt, wait_tally)
from native_launch_nft_refund import governance_tx
from native_launch_rehearsal import configure_peers, load_fixture
from testnet_issuance_rehearsal import rehearse_issuance
from withdrawal_genesis_smoke import bech32_payload, check, native


def run(args, root, owner, report):
    raw, expected, keys = load_fixture(args.fixture, four_validators=True, empty_user_assets=True,
                                       assembly_report=args.assembly_report, network_profile='mainnet')
    genesis = json.loads(raw)
    state = genesis['app_state']
    # Only the isolated clock is compressed; deposits and threshold remain as assembled.
    state['gov']['params'].update(voting_period='32s', expedited_voting_period='24s')
    raw = json.dumps(genesis, separators=(',', ':')).encode()
    report['genesisSha256'] = hashlib.sha256(raw).hexdigest()
    env = private_environment(root)
    rows = expected['validators']
    addrs = {r['consensus_pubkey']['key']: bech32_payload(r['operator_address'], 'd0valoper')
             for r in state['validator']['validators']}
    signers = [native_address_from_evm(addrs[base64.b64encode(decode_pubkey(r['publicKey'])).decode()]) for r in rows]
    nodes = [create_replica(str(args.predecessor), root, r['name'], i, raw, key, env,
                            chain_id=expected['chainId'], evm_chain_id=9905401)
             for i, (r, key) in enumerate(zip(rows, keys, strict=True))]
    for n in nodes:
        configure_peers(n, [peer for peer in nodes if peer is not n])
        write_config(n.home, 'config.toml', {('consensus', 'timeout_commit'): '"1s"'})
    for n in nodes:
        n.start(str(args.predecessor), env, owner, 'admission')
    wait_replicas(nodes, 5, 100, report)
    from urllib.request import urlopen
    try:
        with urlopen(nodes[0].rest + '/mintcandy/issuance/v1/tariff', timeout=5):
            raise ValueError('tariff active before governance')
    except HTTPError as exc:
        check(exc.code == 404, 'unexpected inactive tariff response')
    target = committed_height(nodes[0]) + 42
    plan = 'mintcandy-drc20-issuance-v1'
    action = {'planHeight': target, 'passedID': 1}
    metadata = SCHEMA + ':' + json.dumps({'name': plan, 'height': target}, separators=(',', ':'))
    submit = governance_tx(args, nodes[0], root, env, report, {'height': target, 'issuance': True}, 0, signers[0], 'submit')
    p = native('/cosmos/gov/v1/proposals/1', submit['height'], base=nodes[0].rest)['proposal']
    check(p['expedited'] and p['total_deposit'] == [{'denom': 'msp', 'amount': str(50*ATOMS)}], 'incorrect expedited proposal')
    votes = [governance_tx(args, nodes[0], root, env, report, {'proposalId': 1}, i, voter, f'vote-{i}')
             for i, voter in enumerate(signers)]
    closed = wait_tally(nodes[0], 1, votes[-1]['height'], report)
    wait_replicas(nodes, closed + 1, 40, report)
    check_proposal(nodes, 1, closed, metadata, True, str(sum(row['initialPower'] for row in rows)*ATOMS))
    check_schedule(nodes, action, closed, plan)
    wait_predecessor_halt(nodes, action, report, plan)
    report.update(activationHeight=target, submission=submit, votes=votes, talliedHeight=closed,
                  priorHeader=committed_header(nodes, target-1))
    for n in nodes:
        owner.stop(n.child)
    for n in nodes:
        n.start(str(args.successor), env, owner, 'successor')
    wait_replicas(nodes, target+2, 120, report)
    check(all(applied_height(n, target, plan) == target and current_plan(n, target) is None for n in nodes),
          'plan did not execute exactly at its height')
    report['postUpgradeHeader'] = committed_header(nodes, target+1)
    rehearse_issuance(args.signer, root, nodes, expected, env, report, mainnet=True)
    tip = committed_height(nodes[0])
    report['finalCommit'] = find_four_signature_commit(nodes, rows, args.commit_probe, tip, tip+8, report)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for arg in ('fixture', 'assembly-report', 'predecessor', 'successor', 'signer', 'commit-probe', 'home-parent', 'report'):
        parser.add_argument('--' + arg, required=True, type=Path)
    args = parser.parse_args()
    private_namespace()
    report = {'initialHeight': 1, 'loopbackOnlyNamespace': True}
    with SignalGuard() as signals, TemporaryDirectory(dir=args.home_parent) as directory:
        owner = ProcessOwner(signals)
        try:
            run(args, Path(directory), owner, report)
        finally:
            owner.close()
    args.report.write_text(json.dumps(report, indent=2), encoding='utf-8')
