"""Exercise reserve-token fees against both native profiles in the loopback launch rehearsal."""

import json
import re
import subprocess

from del_delegation_smoke import read_words
from native_launch_media import commit_media_transaction
from reserve_fee_smoke import encode_operation, submit_envelope
from withdrawal_genesis_smoke import check, ethereum, native, private_bytes, request


def rehearse_profile_fees(binary, signer, root, nodes, expected, env, report):
    source, witness = nodes[0], nodes[-1]
    chain, evm_id, denom = expected['chainId'], expected['evmChainId'], expected['nativeDenom']
    refund = 'native ' + denom
    directory = root / 'profile-fees'
    directory.mkdir(mode=0o700)

    def sign(token=''):
        subprocess.run([str(signer), '-test.run=^TestSignNativeFeeRehearsal$'], check=True, timeout=35,
                       env=dict(env, MCND_PROFILE_FEE_FIXTURE=str(directory), MCND_PROFILE_FEE_TOKEN=token,
                                MCND_PROFILE_FEE_GENESIS=str(source.home / 'config/genesis.json')))

    report['phase'] = 'create-reserve-token'
    sign()
    create = json.loads(private_bytes(directory / 'create.json', 8192))
    payer = create['creator'].lower()
    receipt = commit_media_transaction(source, nodes, create, 0, report)
    events = [row['data'] for row in receipt['logs'] if row['address'].lower() == create['target'].lower()
              and row['topics'] == [create['event']]]
    check(len(events) == 1 and re.fullmatch(r'0x0{24}[0-9a-f]{40}(?:[0-9a-f]{64})+', events[0]),
          'missing canonical reserve-token deployment event')
    token = '0x' + events[0][26:66]
    sign(token)
    fixture = json.loads(private_bytes(directory / 'fee.json', 8192))
    check((fixture['chain_id'], fixture['evm_id'], fixture['payer'].lower(), fixture['token'].lower()) ==
          (chain, evm_id, payer, token), 'fee signer used different network or asset')
    unit = int(fixture['exact_input'])
    payer_word = payer[2:].rjust(64, '0')

    def snapshot(node, height):
        def coins(address):
            return int(ethereum('eth_getBalance', [address, hex(height)], base=node.evm), 16)
        supply = native('/cosmos/bank/v1beta1/supply/by_denom?denom=' + denom, height, base=node.rest)['amount']
        check(supply['denom'] == denom, 'bank returned a foreign supply')
        tokens = read_words(token, '0x70a08231' + payer_word, height, 1, node.evm)[0]
        return coins(payer), coins(token), tokens, int(supply['amount'])

    results = []
    for op in fixture['operations']:
        idx = op['fee_nonce']
        report['phase'] = 'profile-fee-' + op['name']
        quote_args = {key: fixture[key] for key in ('token', 'payer', 'exact_input', 'min_native_output', 'native_fee_budget')}
        quote_args['deadline'] = str(fixture['deadline'])
        for node in (source, witness):
            policy, _ = request(node.rest + '/mintcandy/fees/v1/policy?payer=' + payer)
            quote, _ = request(node.rest + '/mintcandy/fees/v1/quote', quote_args)
            for response in (policy, quote):
                check(response['enabled'] and response['refund_asset'] == refund and response['fee_nonce'] == str(idx) and
                      response['cosmos_chain_id'] == chain and response['evm_chain_id'] == str(evm_id),
                      'policy or quote has the wrong fee asset, nonce or domain')
            check(int(quote['native_output']) >= int(fixture['native_fee_budget']), 'reserve cannot cover fee')
        raw = encode_operation(binary, source.home, env, directory, fixture, op, directory)
        receipt, height, _ = submit_envelope(nodes, raw, op, report)
        fee = receipt['mintcandyFee']
        status = int(receipt['status'], 16)
        check(status == 1-idx and fee['refundAsset'] == refund and fee['payer'].lower() == payer and
              fee['token'].lower() == token and fee['feeNonce'] == str(idx) and int(fee['tokenInput']) == unit and
              receipt['transactionHash'] == op['ethereum_hash'], 'wrong indexed fee receipt')
        converted, charge = int(fee['nativeOutput']), int(fee['conversionFee'])
        price = int(receipt['effectiveGasPrice'], 16)
        conversion_gas = int(fee['conversionGas'], 16)
        check(0 < conversion_gas <= fixture['conversion_gas'] and charge == conversion_gas * price and
              converted >= int(fixture['min_native_output']), 'indexed conversion violates the signed fee bounds')
        charge += int(receipt['gasUsed'], 16) * price
        check(ethereum('eth_getTransactionReceipt', [op['ethereum_hash']], base=witness.evm) == receipt,
              'observer indexed a different fee receipt')
        before, after = (snapshot(witness, h) for h in (height-1, height))
        check(after == (before[0] + converted - charge, before[1] - converted, before[2] - unit, before[3]),
              'payer refund, reserve, tokens or native supply differ from the indexed sale')
        check(snapshot(source, height) == after, 'validator and observer disagree on settlement')
        policy, _ = request(witness.rest + '/mintcandy/fees/v1/policy?payer=' + payer)
        check(policy['fee_nonce'] == str(idx+1), 'fee nonce did not advance exactly once')
        results.append({'height': height, 'status': status, 'hash': op['ethereum_hash'],
                        'nativeOutputAtomic': str(converted), 'chargedAtomic': str(charge)})
    report['reserveFees'] = {'refundAsset': refund, 'operations': results, 'observerMatched': True,
                             'nativeSupplyUnchanged': True}
