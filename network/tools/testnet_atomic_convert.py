"""Replay signed TokenCenter conversions against a private four-validator tMSP chain."""
import json
import subprocess

from del_delegation_smoke import read_words
from native_launch_media import commit_media_transaction
from withdrawal_genesis_smoke import bech32_payload, check, ethereum, native, private_bytes, request


UNIT = 10**18
TRANSFER_TOPIC = '0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef'
ZERO = '0x' + '00' * 20


def word(address):
    return address[2:].rjust(64, '0')


def snapshot(node, height, tokens, actor, receiver, center, expected_crr=None):
    accounts = (actor, receiver, center)
    def balance(address):
        return int(ethereum('eth_getBalance', [address, hex(height)], base=node.evm), 16)

    snapshot = dict(bank={a: balance(a) for a in accounts}, tokens={})
    snapshot['native_supply'] = int(native('/cosmos/bank/v1beta1/supply/by_denom?denom=tmsp', height,
                                           base=node.rest)['amount']['amount'])
    for token in tokens:
        def read(data):
            return read_words(token, data, height, 1, node.evm)[0]
        market, _ = request(node.rest + f'/mintcandy/markets/v1/reserve?token={token}&height={height}')
        volume, reserve = read('0x18160ddd'), read('0xcd3293de')
        check(market['chain_id'] == 'mintcandy_990098-1' and market['native_denom'] == 'tmsp'
              and market['height'] == height and market['token'].lower() == token
              and market['token_center'].lower() == center and market['atomic_convert_version'] == 1,
              'unexpected registry market identity or atomic conversion version')
        check(market['supply_atoms'] == str(volume) and market['reserve_atoms'] == str(reserve)
              and balance(token) == reserve and market['crr'] == (expected_crr or {}).get(token, 100), 'reserve or supply differs across ledgers')
        snapshot['tokens'][token] = {
            'volume': volume, 'reserve': reserve, 'allowance': read('0xdd62ed3e' + word(actor) + word(center)),
            'balances': {a: read('0x70a08231' + word(a)) for a in accounts},
        }
    return snapshot


def transfer_logs(receipt):
    movements = []
    for item in receipt['logs']:
        topics = item['topics']
        if topics[0].lower() == TRANSFER_TOPIC:
            check(len(topics) == 3 and len(item['data']) == 66, 'invalid transfer log')
            movements.append((item['address'].lower(), '0x' + topics[1][-40:].lower(),
                              '0x' + topics[2][-40:].lower(), int(item['data'], 16)))
    return movements


def rehearse_atomic_convert(signer, root, nodes, expected, env, report, center, funded_tokens, other):
    check(expected['chainId'] == 'mintcandy_990098-1' and expected['nativeDenom'] == 'tmsp'
          and report['loopbackOnlyNamespace'], 'isolated tMSP is required')
    actor, receiver, center = expected['actor'].lower(), other.lower(), center.lower()
    capped, token_in, token_out = tokens = [a.lower() for a in funded_tokens]
    check(len(set([actor, receiver, center, *tokens])) == 6, 'expected two distinct wallets, factory and three markets')
    validator, observer = nodes[0], nodes[-1]
    folder = root / 'atomic-convert'
    folder.mkdir(mode=0o700)
    nonce = int(ethereum('eth_getTransactionCount', [actor, 'latest'], base=validator.evm), 16)
    subprocess.run([str(signer), '-test.run=^TestSignTestnetAtomicConvert$'], timeout=35, check=True,
                   env=dict(env, MCND_CONVERT_DIR=str(folder), MCND_CONVERT_CAPPED=capped,
                            MCND_CONVERT_IN=token_in, MCND_CONVERT_OUT=token_out, MCND_CONVERT_CENTER=center,
                            MCND_CONVERT_RECEIVER=receiver, MCND_CONVERT_NONCE=str(nonce),
                            MCND_TESTNET_TARIFF_FOUNDER_FILE=str(root.parent / 'tariff-founder.key')))
    rows = json.loads(private_bytes(folder / 'conversions.json', 60000))
    evidence = []
    report['atomicConvert'] = {'outcomes': evidence, 'center': center, 'observerMatched': False}
    for row in rows:
        label, action = row['label'], row['action']
        report['phase'] = 'atomic-convert-' + label
        call = {'to': row['target'], 'from': row['creator'], 'data': row['data'], 'value': row['value'], 'gas': hex(8_000_000)}
        estimate, _ = request(validator.evm, {'jsonrpc': '2.0', 'id': 1, 'method': 'eth_estimateGas', 'params': [call]})
        expected_success = row['success']
        if expected_success:
            check('result' in estimate and 21000 < int(estimate['result'], 16) <= 8_000_000,
                  f'{label} estimate failed: {str(estimate.get("error"))[:220]}')
        else:
            check('error' in estimate and 'result' not in estimate, f'{label} returned a successful estimate')
        receipt = commit_media_transaction(validator, nodes, row, row['nonce'], report, status=int(expected_success))
        height = int(receipt['blockNumber'], 16)
        before = snapshot(observer, height-1, tokens, actor, receiver, center)
        after = snapshot(observer, height, tokens, actor, receiver, center)
        check(after == snapshot(validator, height, tokens, actor, receiver, center), 'observer disagrees with validator')
        before['bank'][actor] -= int(receipt['gasUsed'], 16) * int(receipt['effectiveGasPrice'], 16)
        block = observer.rpc(f'block_results?height={height}')
        for event in block.get('finalize_block_events') or []:
            if event['type'] == 'decimal_native_fee_reward':
                fields = {a['key']: a['value'] for a in event['attributes']}
                address = bech32_payload(fields['recipient'])
                if address in before['bank']:
                    check(fields['amount'].endswith('tmsp'), 'unexpected reward denom')
                    before['bank'][address] += int(fields['amount'][:-4])

        amount, src, dst = int(row['amount']), row['input'].lower(), row['output'].lower()
        quoted = {}
        if action == 'convert' and src in tokens and dst in tokens and src != dst:
            bridge = read_words(src, '0xc193d62e' + f'{amount:064x}', height-1, 1, observer.evm)[0]
            output = read_words(dst, '0x6cc94a2b' + f'{bridge:064x}', height-1, 1, observer.evm)[0]
            check(bridge * 10 == amount and output == amount, 'fixture quote differs from pinned curve')
            quoted = {'native_atoms': str(bridge), 'output_atoms': str(output), 'quote_height': height-1}
        if expected_success:
            debit = before['tokens'][src]
            if action == 'approve':
                debit['allowance'] = amount
            elif action == 'buy':
                debit['volume'] += 10*amount
                debit['reserve'] += amount
                debit['balances'][actor] += 10*amount
                before['bank'][actor] -= amount
            else:
                check(output >= int(row['minimum']), 'conversion output below minimum')
                debit['allowance'] -= amount
                debit['balances'][actor] -= amount
                debit['volume'] -= amount
                debit['reserve'] -= bridge
                credit = before['tokens'][dst]
                credit['volume'] += output
                credit['reserve'] += bridge
                credit['balances'][receiver] += output
                check(transfer_logs(receipt) == [(src, actor, center, amount), (src, center, ZERO, amount),
                                                 (dst, ZERO, receiver, output)], 'unexpected conversion transfer events')
        else:
            check(not receipt['logs'], 'failed conversion retained EVM effects')
        check(before == after, f'{label} balance, escrow, allowance or native state delta differs at {height}')
        events = [e for tx in block['txs_results'] for e in tx['events'] if e['type'] == 'mintcandy_drc20_convert']
        if action == 'convert' and expected_success:
            check(len(events) == 1, 'missing native conversion event')
            fields = {attr['key']: attr['value'].lower() for attr in events[0]['attributes']}
            required = {'payer': actor, 'recipient': receiver, 'token_in': src, 'token_out': dst,
                        'input_atoms': str(amount), 'native_atoms': str(bridge), 'output_atoms': str(output),
                        'min_output_atoms': row['minimum'], 'in_volume': str(debit['volume']),
                        'in_reserve': str(debit['reserve']), 'out_volume': str(credit['volume']),
                        'out_reserve': str(credit['reserve'])}
            check(all(fields.get(k) == v for k, v in required.items()), 'native conversion event differs from settled balances')
        else:
            check(not events, 'unexpected native conversion event')
        if label == 'exact-cap':
            check(after['tokens'][dst]['volume'] == 1000*UNIT, 'exact-output-cap fixture missed the boundary')
        evidence.append({'case': label, 'height': height, 'hash': receipt['transactionHash'],
                         'status': int(receipt['status'],16), **quoted})
    expected_cases = {'fund-input', 'missing-allowance', 'approve', 'convert', 'min-out', 'input-reserve-floor',
                      'output-cap', 'same-token', 'unregistered-in', 'unregistered-out',
                      'seed-output-near-cap', 'exact-cap', 'above-cap'}
    check({r['case'] for r in evidence} == expected_cases and len(evidence) == len(expected_cases),
          'incomplete conversion scenarios')
    report['atomicConvert']['observerMatched'] = True
