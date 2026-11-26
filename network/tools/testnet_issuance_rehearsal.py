"""Verify committed issuance debits on disposable tMSP validators and their observer."""
import concurrent.futures
import json
import subprocess
import time
from collections import defaultdict

from native_consensus_smoke import wait_replicas
from native_launch_media import commit_media_transaction
from withdrawal_genesis_smoke import bech32_payload, check, ethereum, native, private_bytes, request

UNIT = 10**18
ZERO = '0x' + '00' * 20
FEES = (100000, 10000, 1000, 100, 100, 100, 100, 10)


def rehearse_issuance(signer, root, nodes, expected, env, report, *, mainnet=False):
    source, observer = nodes[0], nodes[-1]
    chain, denom, recipient = (('mintcandy_9905401-1', 'msp', '0x40900a48273644768c09183e00e43528c17a29f6')
                               if mainnet else ('mintcandy_990098-1', 'tmsp', '0x00000000000000000000000000000000cafe9900'))
    check(expected['chainId'] == chain and expected['nativeDenom'] == denom
          and report['loopbackOnlyNamespace'], 'issuance requires the matching isolated profile')
    directory = root / 'testnet-issuance'
    directory.mkdir(mode=0o700)
    path = '/mintcandy/issuance/v1/tariff'
    policy, _ = request(observer.rest + path)
    check(policy['chain_id'] == expected['chainId'] and policy['denom'] == denom and policy['treasury'].lower() == recipient
          and policy['treasury_percent'] == 80 and policy['burn_percent'] == 20, 'unexpected tariff domain or split')
    for length, row in enumerate(policy['tariffs'], 3):
        check(row['length'] == length and int(row['fee_atoms']) == FEES[length-3]*UNIT, 'wrong quote')
    check(len(policy['tariffs']) == 8, 'incomplete tariff')
    treasury = policy['treasury']
    subprocess.run([str(signer), '-test.run=^TestSignTestnetIssuance$'], check=True, timeout=35,
                   env=dict(env, MCND_TESTNET_TARIFF_FOUNDER_FILE=str(root.parent / 'tariff-founder.key'),
                            MCND_TARIFF_FACTORY=policy['token_center'], MCND_TARIFF_FIXTURE=str(directory)))
    fixtures = json.loads(private_bytes(directory / 'issues.json', 160000))
    check(fixtures[0]['creator'].lower() == expected['actor'].lower(), 'funded founder differs from test signer')
    outcomes = []

    def verify(batch):
        height = int(batch[0][1]['blockNumber'], 16)
        totals = defaultdict(int)
        burned = 0
        for op, rc in batch:
            ok = int(rc['status'], 16)
            payer = op['creator'].lower()
            gas = int(rc['gasUsed'], 16)*int(rc['effectiveGasPrice'], 16)
            totals[payer] -= gas
            if ok:
                fee = int(op['fee'])
                totals[payer] -= fee
                totals[treasury.lower()] += fee*4//5
                burned += fee//5
                logs = [x for x in rc['logs'] if x['address'].lower() == op['target'].lower()]
                check(len(logs) == 1, 'expected exactly one factory deployment')
                token = '0x' + logs[0]['data'][26:66]
                reserve = UNIT if op['backed'] else 0
                totals[payer] -= reserve
                totals[token] += reserve
                check(ethereum('eth_getCode', [token, hex(height)], base=observer.evm) != '0x', 'missing token code')
            else:
                check(not rc['logs'], 'rejected issuance leaked deployment logs')
            outcomes.append({'case': op['label'], 'height': height, 'status': ok, 'tx': op['hash']})
        totals[ZERO] += 0
        totals[treasury.lower()] += 0
        for event in observer.rpc(f'block_results?height={height}').get('finalize_block_events') or []:
            if event['type'] == 'decimal_native_fee_reward':
                attrs = {a['key']: a['value'] for a in event['attributes']}
                addr = bech32_payload(attrs['recipient'])
                if addr in totals:
                    check(attrs['amount'].endswith(denom), 'foreign payout')
                    totals[addr] += int(attrs['amount'].removesuffix(denom))
        for node in (source, observer):
            for addr, delta in totals.items():
                amounts = [int(ethereum('eth_getBalance', [addr, hex(h)], base=node.evm), 16) for h in (height-1, height)]
                check(amounts[1]-amounts[0] == delta, f'wrong {addr} balance delta at {height}: {amounts[1]-amounts[0]} != {delta}')
            supplies = [int(native('/cosmos/bank/v1beta1/supply/by_denom?denom=' + denom, h, base=node.rest)['amount']['amount'])
                        for h in (height-1, height)]
            check(supplies[0]-supplies[1] == burned, 'bank supply change differs from burn')
    for op in fixtures[:-2]:
        report['phase'] = 'tariff-' + op['label']
        rc = commit_media_transaction(source, nodes, op, op['nonce'], report, status=int(op['success']))
        verify([(op, rc)])
    report['phase'] = 'tariff-race'
    race = fixtures[-2:]
    def submit(op):
        return ethereum('eth_sendRawTransaction', [op['raw']], base=source.evm)
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as clients:
        check(list(clients.map(submit, race)) == [op['hash'] for op in race], 'missing competing issuance')
    groups = defaultdict(list)
    statuses = []
    for op in race:
        deadline = time.monotonic()+45
        rc = None
        while time.monotonic() < deadline and rc is None:
            rc = ethereum('eth_getTransactionReceipt', [op['hash']], base=source.evm)
            if rc is None:
                time.sleep(0.25)
        check(rc is not None, 'missing race receipt')
        statuses.append(int(rc['status'], 16))
        groups[int(rc['blockNumber'], 16)].append((op, rc))
    check(sorted(statuses) == [0, 1], 'both competing issues had the same outcome')
    wait_replicas(nodes, max(groups)+2, 65, report)
    for batch in groups.values():
        verify(batch)
    report['mainnetIssuance' if mainnet else 'testnetIssuance'] = {'denom': denom, 'treasury': treasury, 'outcomes': outcomes,
                                'observerMatched': True, 'treasuryAndBurnVerified': True}

