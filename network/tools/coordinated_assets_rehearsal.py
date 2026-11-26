"""Exercise a coordinated activation and signed liquidity operations on four disposable tMSP nodes."""
import argparse
import base64
import hashlib
import json
import math
import subprocess
from pathlib import Path
from tempfile import TemporaryDirectory
from urllib.error import HTTPError
from urllib.request import urlopen
from native_launch_media import commit_media_transaction

from del_delegation_smoke import read_words, write_config
from mcnd_process_smoke import ProcessOwner, SignalGuard, private_environment, private_namespace
from native_consensus_smoke import committed_header, create_replica, wait_replicas
from native_governance_upgrade_smoke import current_plan, committed_height, find_four_signature_commit
from native_launch_rehearsal import configure_peers, load_fixture
from testnet_reserve_trades import rehearse_trades
from testnet_atomic_convert import rehearse_atomic_convert, snapshot
from testnet_issuance_rehearsal import rehearse_issuance
from withdrawal_genesis_smoke import check, ethereum


def commit_inactive_market(nodes, fixture, report):
    source = nodes[0]
    start = committed_height(source)
    raw = bytes.fromhex(fixture['raw'][2:])
    tx_hash = ethereum('eth_sendRawTransaction', [fixture['raw']], base=source.evm)
    check(tx_hash == fixture['hash'], 'submitted Ethereum hash differs')
    for height in range(start + 1, min(start + 12, 80)):
        wait_replicas(nodes, height + 1, 30, report)
        block = source.rpc(f'block?height={height}')
        txs = [base64.b64decode(tx, validate=True) for tx in block['block']['data'].get('txs') or []]
        matches = [index for index, tx_bytes in enumerate(txs) if raw in tx_bytes]
        if not matches:
            continue
        check(len(matches) == 1, 'ambiguous signed transaction in block')
        index = matches[0]
        evidence = dict(height=height, hash=tx_hash, cometTxSha256=hashlib.sha256(txs[index]).hexdigest())
        header = committed_header(nodes, height)
        result = source.rpc(f'block_results?height={height}')['txs_results'][index]
        code = int(result.get('code', 0))
        evidence['abciCode'] = code
        if code:
            reason = result.get('log', '')
            check('failed to set account' in reason or 'failed to commit stateDB' in reason,
                  f'unexpected native failure on signed transaction at {height}: {reason[:400]}')
        else:
            receipt = ethereum('eth_getTransactionReceipt', [tx_hash], base=source.evm)
            check(receipt and receipt['transactionHash'] == tx_hash and int(receipt['status'], 16) == 0
                  and receipt['blockHash'][2:].lower() == header['block_hash'].lower(), 'expected failed EVM receipt')
            evidence['receiptStatus'] = 0
        return evidence
    raise ValueError('signed pre-activation transaction missing from committed blocks')


def inactive_balances(node, height, tokens, actor, center):
    def raw_call(token, data):
        return read_words(token, data, height, 1, node.evm)[0]
    def word(address):
        return address[2:].rjust(64, '0')
    return [(int(ethereum('eth_getBalance', [t, hex(height)], base=node.evm), 16),
             raw_call(t, '0x18160ddd'), raw_call(t, '0xcd3293de'),
             raw_call(t, '0x70a08231' + word(actor)), raw_call(t, '0x70a08231' + word(center)),
             raw_call(t, '0xdd62ed3e' + word(actor) + word(center))) for t in tokens]


def run(args, root, owner, report):
    raw, expected, keys = load_fixture(args.fixture, four_validators=True, empty_user_assets=True,
                                       assembly_report=args.assembly_report, network_profile='testnet')
    genesis = json.loads(raw)
    state = genesis['app_state']
    profile = state['withdrawal_settlement']
    env = private_environment(root)
    rows = expected['validators']
    report.update(activationHeight=80, chainId=expected['chainId'], genesisSha256=hashlib.sha256(raw).hexdigest(),
                  predecessorSHA256=hashlib.file_digest(args.predecessor.open('rb'), 'sha256').hexdigest(),
                  successorSHA256=hashlib.file_digest(args.successor.open('rb'), 'sha256').hexdigest(), transactions={})
    nodes = [create_replica(str(args.predecessor), root, r['name'], i, raw, key, env,
                            chain_id=expected['chainId'], evm_chain_id=990098)
             for i, (r, key) in enumerate(zip(rows, keys, strict=True))]
    for n in nodes:
        configure_peers(n, [peer for peer in nodes if peer is not n])
        write_config(n.home, 'config.toml', {('consensus', 'timeout_commit'): '"500ms"'})
    for n in nodes:
        n.start(str(args.predecessor), env, owner, 'admission')
    wait_replicas(nodes, 5, 100, report)
    source = nodes[0]
    wallet = expected['actor']
    wrapper = profile['wrapped_del']
    token = ''
    def tx(label, *, target_token=None, output_token='', status=1, minimum=''):
        nonce = int(ethereum('eth_getTransactionCount',[wallet,'latest'],base=source.evm), 16)
        path = root / (label+'.json')
        action = dict(Action=label, Nonce=nonce, Factory=profile['token_center'], Wrapper=wrapper, Token=token if target_token is None else target_token, Output=output_token, Minimum=minimum)
        signer_env = dict(env, MCND_POOL_ACTION=json.dumps(action), MCND_POOL_OUTPUT=str(path),
                          MCND_TESTNET_TARIFF_FOUNDER_FILE=str(root.parent / 'tariff-founder.key'))
        subprocess.run([str(args.signer), '-test.run=^TestSignCoordinatedPoolAction$'], check=True, timeout=30, env=signer_env)
        fixture = json.loads(path.read_text())
        if label.startswith('before-'):
            evidence = commit_inactive_market(nodes, fixture, report)
            height = evidence['height']
            reference = inactive_balances(source, height - 1, historical, wallet, profile['token_center'])
            for node in nodes:
                check(inactive_balances(node, height, historical, wallet, profile['token_center']) == reference,
                      'pre-activation trade moved reserve, supply, balance or allowance')
            report['transactions'][label] = evidence
            return None, fixture['pool']
        rc = commit_media_transaction(source, nodes, fixture, nonce, report, status=status)
        report['transactions'][label] = dict(height=int(rc['blockNumber'],16),hash=rc['transactionHash'],status=int(rc['status'],16))
        return rc, fixture['pool']
    rc, pool = tx('create')
    token = '0x' + [log for log in rc['logs'] if log['address'].lower()==profile['token_center'].lower()][0]['data'][26:66]
    report.update(issuer=wallet, pool=pool, wrapped=wrapper, cusdt=token)
    try:
        with urlopen(source.rest + '/mintcandy/issuance/v1/tariff', timeout=5):
            raise ValueError('tariff active before handoff')
    except HTTPError as exc:
        check(exc.code == 404,'unexpected inactive tariff response')
    historical = []
    for label in ('legacy-coin-a', 'legacy-coin-b'):
        rc, _ = tx(label)
        created = next(log for log in rc['logs'] if log['address'].lower() == profile['token_center'].lower())
        historical.append('0x' + created['data'][26:66])
    tx('legacy-approval', target_token=historical[0])
    handoff = committed_height(source)
    for n in nodes:
        owner.stop(n.child)
    for n in nodes:
        n.start(str(args.successor), env, owner, 'successor')
    wait_replicas(nodes, handoff + 1, 60, report)
    for label in ('buy', 'sell', 'convert'):
        tx('before-' + label, target_token=historical[0], output_token=historical[1], status=0)
    wait_replicas(nodes, 82, 150, report)
    for n in nodes:
        check(ethereum('eth_getCode',[pool,hex(79)],base=n.evm)=='0x' and
              ethereum('eth_getCode',[pool,hex(80)],base=n.evm)!='0x','wrong pool installation boundary')
        check(current_plan(n,80) is None,'unexpected SDK upgrade plan')
    report['activationHeader']=committed_header(nodes,81)
    unit=10**18
    def state_at(height):
        result=[]
        for n in nodes:
            def slot(i):
                return int(ethereum('eth_getStorageAt',[pool,hex(i),hex(height)],base=n.evm),16)
            packed=slot(0)
            result.append((packed%2**112,packed>>112,slot(1)))
        check(all(x==result[0] for x in result),'pool state differs between nodes')
        return result[0]
    check(state_at(80)==(0,0,0),'activation moved liquidity')
    for label in ['initialize','wrap','approve-wrapper','approve-token','deposit','swap','withdraw']:
        rc,_=tx(label)
        if label=='deposit':
            r0,r1=50000*unit,5000*unit
            shares=math.isqrt(r0*r1)
        elif label=='swap':
            net=9970*unit
            output=r1*net//(r0*10000+net)
            r0,r1=r0+unit,r1-output
        elif label=='withdraw':
            r0,r1,shares=r0-r0*unit//shares,r1-r1*unit//shares,shares-unit
        else:
            continue
        check(state_at(int(rc['blockNumber'],16))==(r0,r1,shares),'wrong final reserves or LP shares after '+label)
        report['transactions'][label].update(reserve0=str(r0),reserve1=str(r1),totalShares=str(shares))
    for label in ('buy', 'sell'):
        tx('after-' + label, target_token=historical[0])
    # The source has CRR 100 and the destination CRR 50. Price both legs from
    # one committed state. The curved mint must deliver less than half the
    # proportional linear estimate for a positive deposit (concavity).
    left, right = [asset.lower() for asset in historical]
    actor, center = wallet.lower(), profile['token_center'].lower()
    def curve_states(height):
        rows = [snapshot(node, height, [left, right], actor, actor, center, {right: 50})['tokens']
                for node in nodes]
        check(all(s == rows[0] for s in rows), 'historical curve states disagree')
        return rows[0]

    h = committed_height(source) - 1
    # The two direct trades must be reflected at the quotation height.
    h = max(h, report['transactions']['after-sell']['height'])
    initial = curve_states(h)
    middle = read_words(left, '0xc193d62e' + f'{unit:064x}', h, 1, source.evm)[0]
    output = read_words(right, '0x6cc94a2b' + f'{middle:064x}', h, 1, source.evm)[0]
    dst = initial[right]
    check(middle > 0 and 0 < 2 * output * dst['reserve'] < middle * dst['volume'],
          'CRR 50 conversion quote is inconsistent with a concave purchase curve')
    for name, minimum, success in [('nonlinear-min-out', output + 1, 0), ('after-convert', output, 1)]:
        rc, _ = tx(name, target_token=left, output_token=right, status=success, minimum=str(minimum))
        height = int(rc['blockNumber'], 16)
        states = curve_states(height)
        if success:
            initial[left]['balances'][actor] -= unit
            initial[left]['volume'] -= unit
            initial[left]['reserve'] -= middle
            initial[left]['allowance'] -= unit
            initial[right]['balances'][actor] += output
            initial[right]['volume'] += output
            initial[right]['reserve'] += middle
        check(states == initial, 'historical nonlinear conversion balances, allowance or reserves differ')
        report['transactions'][name].update(crrIn=100, crrOut=50, nativeAtoms=str(middle),
                                            outputAtoms=str(output), minOutAtoms=str(minimum), quoteHeight=h)
    factory, backed, other = rehearse_issuance(args.signer, root, nodes, expected, env, report)
    rehearse_trades(args.signer, root, nodes, expected, env, report, backed[0], other)
    rehearse_atomic_convert(args.signer, root, nodes, expected, env, report, factory, backed[:3], other)
    check(state_at(committed_height(source)-1) == (r0, r1, shares), 'reserve trades changed pinned pool')
    tip = committed_height(nodes[0])
    report['finalCommit'] = find_four_signature_commit(nodes, rows, args.commit_probe, tip, tip+8, report)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for arg in ('fixture', 'assembly-report', 'predecessor', 'successor', 'signer', 'commit-probe', 'home-parent', 'report'):
        parser.add_argument('--' + arg, required=True, type=Path)
    args = parser.parse_args()
    private_namespace()
    report = {'initialHeight': 1, 'disposableBlockBudget': 415, 'loopbackOnlyNamespace': True}
    with SignalGuard() as signals, TemporaryDirectory(dir=args.home_parent) as directory:
        owner = ProcessOwner(signals)
        try:
            run(args, Path(directory), owner, report)
        finally:
            owner.close()
    args.report.write_text(json.dumps(report, indent=2), encoding='utf-8')
