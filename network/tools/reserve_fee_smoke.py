"""Verify DRC20 fee envelopes on three disposable Linux daemon replicas."""

import argparse
import hashlib
import json
import pathlib
import re
import tempfile
import time

from del_delegation_smoke import balance, read_words, write_config
from mcnd_process_smoke import ProcessOwner, SignalGuard, private_environment, private_namespace, run_command
from native_consensus_smoke import committed_header, create_replicas, decode_pubkey, wait_replicas
from withdrawal_genesis_smoke import ADDRESS, CHAIN, EVM_CHAIN, SHA256, WORD, bech32_payload, check, ethereum, native, private_bytes, request, safe_diagnostic


def load_fixture(directory):
    check(directory.is_dir() and directory.stat().st_mode & 0o077 == 0, 'private fixture directory required')
    fixture = json.loads(private_bytes(directory / 'reserve-fee-daemon.json', 16_384))
    genesis = private_bytes(directory / 'genesis.json', 64 * 1024 * 1024)
    doc = json.loads(genesis)
    check(fixture['schema'] == 1 and fixture['chain_id'] == doc['chain_id'] == CHAIN and fixture['evm_id'] == EVM_CHAIN,
          'fixture signing domain differs')
    check(int(doc['consensus_params']['block']['max_gas']) == 24_000_000, 'fee daemon requires the finite 24m block cap')
    profile = doc['app_state']['withdrawal_settlement']
    check(profile['enable_native_consensus'] and not profile.get('enable_native_fee_rewards', False), 'unexpected fee daemon profile')
    for name in ('payer', 'token', 'actor', 'converter', 'verifying_module', 'fee_collector'):
        check(re.fullmatch(ADDRESS, fixture[name]), 'invalid fixture role: ' + name)
        fixture[name] = fixture[name].lower()
    for name in ('payer', 'token', 'actor', 'fee_collector'):
        check(bech32_payload(fixture[name + '_native']) == fixture[name], 'fixture native address differs: ' + name)
    check(len({fixture[name] for name in ('payer', 'token', 'actor', 'converter')}) == 4, 'duplicate fee fixture roles')
    check(0 < fixture['deadline'] - int(time.time()) <= 3600, 'fixture fee signatures expired or exceed one hour')
    check(fixture['ethereum_gas'] == 1_000_000 and fixture['conversion_gas'] == 5_000_000 and fixture['gas_price'] == '1000000000',
          'unexpected signed fee budget')
    check(int(fixture['native_fee_budget']) == (fixture['ethereum_gas'] + fixture['conversion_gas']) * int(fixture['gas_price']) ==
          int(fixture['min_native_output']), 'fixture authorization budget differs')
    check([row['name'] for row in fixture['operations']] == ['approve', 'revert'], 'unexpected fixture operations')
    for index, operation in enumerate(fixture['operations']):
        check(operation['fee_nonce'] == index and operation['status'] == 1 - index and
              re.fullmatch(WORD, operation['digest']) and re.fullmatch(WORD, operation['ethereum_hash']), 'invalid signed operation binding')
        for field in ('raw_file', 'signature_file'):
            name = operation[field]
            check(pathlib.PurePath(name).name == name and re.fullmatch(r'[a-z.-]{1,80}', name), 'invalid fixture input path')
            private_bytes(directory / name, 256 * 1024)
    expected = json.loads(private_bytes(directory / 'expectations.json', 8192))
    keys = load_validator_keys(directory, expected['validators'])
    return fixture, genesis, keys, int(doc['initial_height'])


def load_validator_keys(directory, validators):
    check([row['name'] for row in validators] == list('abc'), 'fee gate requires the three named native validators')
    keys = []
    identities = set()
    for row in validators:
        key = private_bytes(directory / f'priv_validator_key_{row["name"]}.json', 8192)
        private = json.loads(key)
        public_key = decode_pubkey(row['publicKey'])
        identity = hashlib.sha256(public_key).hexdigest()[:40].upper()
        check(identity not in identities and decode_pubkey(private['pub_key']) == public_key and
              private['address'] == row['consensusAddress'] == identity,
              'fixture validator signing identity differs')
        identities.add(identity)
        keys.append(key)
    return keys


def fee_policy(node, fixture, nonce):
    value, _ = request(node.rest + '/mintcandy/fees/v1/policy?payer=' + fixture['payer'])
    check(value['enabled'] is True and value['cosmos_chain_id'] == CHAIN and value['evm_chain_id'] == str(EVM_CHAIN) and
          value['verifying_module'].lower() == fixture['verifying_module'] and value['fee_nonce'] == str(nonce) and
          value['refund_asset'] == 'native del' and value['submits_transactions'] is False, 'live DRC20 fee policy differs')
    return value


def state_at(replicas, fixture, height):
    states = []
    for node in replicas:
        supply = native('/cosmos/bank/v1beta1/supply/by_denom?denom=del', height, base=node.rest)['amount']
        check(supply['denom'] == 'del', 'unexpected native supply denomination')
        states.append({
            'payer_native': balance(fixture['payer'], fixture['payer_native'], height, node.evm, node.rest),
            'reserve_native': balance(fixture['token'], fixture['token_native'], height, node.evm, node.rest),
            'fee_collector': balance(fixture['fee_collector'], fixture['fee_collector_native'], height, node.evm, node.rest),
            'native_supply': int(supply['amount']),
            'payer_tokens': read_words(fixture['token'], fixture['payer_balance_data'], height, 1, node.evm)[0],
            'actor_tokens': read_words(fixture['token'], fixture['actor_balance_data'], height, 1, node.evm)[0],
            'token_supply': read_words(fixture['token'], fixture['supply_data'], height, 1, node.evm)[0],
            'allowance': read_words(fixture['token'], fixture['allowance_data'], height, 1, node.evm)[0],
        })
    check(all(state == states[0] for state in states), 'replicas disagree on exact fee balances')
    return states[0]


def encode_operation(binary, home, env, directory, fixture, operation, root):
    flags = ['--tx-file', str(directory / operation['raw_file']), '--cosmos-chain-id', fixture['chain_id'], '--evm-id', str(fixture['evm_id']),
             '--token', fixture['token'], '--exact-input', fixture['exact_input'], '--min-native-output', fixture['min_native_output'],
             '--conversion-gas', str(fixture['conversion_gas']), '--deadline', str(fixture['deadline']), '--fee-nonce', str(operation['fee_nonce'])]
    payload = json.loads(run_command(binary, home, ['fee-envelope', 'payload', *flags], env))
    check(payload['digest'].lower() == operation['digest'].lower() and
          payload['ethereum_tx_hash'].lower() == operation['ethereum_hash'].lower() and
          payload['native_fee_budget'] == fixture['native_fee_budget'], 'offline payload does not match independently signed fixture')
    output = root / (operation['name'] + '.envelope')
    run_command(binary, home, ['fee-envelope', 'encode', *flags, '--fee-signature-file', str(directory / operation['signature_file']),
                              '--out', str(output)], env)
    return private_bytes(output, 256 * 1024)


def submit_envelope(replicas, raw, operation, report):
    source = replicas[0]
    comet_hash = hashlib.sha256(raw).hexdigest().upper()
    result = source.rpc('broadcast_tx_sync?tx=0x' + raw.hex())
    check(int(result['code']) == 0 and result['hash'].upper() == comet_hash, 'Comet CheckTx rejected the signed fee envelope')
    deadline = time.monotonic() + 45
    while time.monotonic() < deadline:
        check(all(node.child.poll() is None for node in replicas), 'a disposable fee daemon exited')
        try:
            receipt = ethereum('eth_getTransactionReceipt', [operation['ethereum_hash']], base=source.evm)
        except OSError:
            receipt = None
        if receipt is not None:
            break
        time.sleep(0.25)
    else:
        raise TimeoutError('daemon did not index the DRC20 fee envelope receipt')
    height = int(receipt['blockNumber'], 16)
    wait_replicas(replicas, height + 1, max(0, deadline - time.monotonic()), report)
    return receipt, height, comet_hash


def verify_receipt(replicas, fixture, operation, receipt, height, before):
    price = int(fixture['gas_price'])
    gas = int(receipt['gasUsed'], 16)
    check(receipt['transactionHash'].lower() == operation['ethereum_hash'].lower() and receipt['from'].lower() == fixture['payer'] and
          receipt['to'].lower() == fixture['token'] and int(receipt['status'], 16) == operation['status'] and
          0 < gas <= fixture['ethereum_gas'] and int(receipt['effectiveGasPrice'], 16) == price, 'Ethereum receipt identity, status or gas differs')
    fee = receipt['mintcandyFee']
    converted = int(fee['nativeOutput'])
    conversion_fee, conversion_gas = int(fee['conversionFee']), int(fee['conversionGas'], 16)
    check(fee['payer'].lower() == fixture['payer'] and fee['token'].lower() == fixture['token'] and
          fee['feeNonce'] == str(operation['fee_nonce']) and fee['tokenInput'] == fixture['exact_input'] and fee['refundAsset'] == 'native del',
          'mintcandyFee does not describe the exact signed conversion')
    check(converted >= int(fixture['min_native_output']) and 0 < conversion_gas <= fixture['conversion_gas'] and
          conversion_fee == conversion_gas * price, 'conversion charge or minOut differs')
    for node in replicas:
        indexed = ethereum('eth_getTransactionReceipt', [operation['ethereum_hash']], base=node.evm)
        check(indexed == receipt, 'replicas disagree on the Ethereum fee receipt')
        result = node.rpc(f'block_results?height={height}')
        rows = result['txs_results']
        check(len(rows) == 1 and int(rows[0]['code']) == 0 and int(rows[0]['gas_used']) == gas + conversion_gas,
              'Comet composite gas must include conversion work beyond the EVM-only receipt')
        events = [event for event in rows[0].get('events') or [] if event['type'] == 'mintcandy_drc20_fee']
        check(len(events) == 1, 'exactly one durable conversion fee event is required')
        fields = {item['key']: item['value'] for item in events[0]['attributes']}
        check(fields['transaction'].lower() == operation['ethereum_hash'].lower() and fields['fee_nonce'] == str(operation['fee_nonce']) and
              int(fields['native_output']) == converted and int(fields['conversion_fee']) == conversion_fee and
              int(fields['conversion_gas']) == conversion_gas, 'durable fee event differs from indexed receipt')
    if operation['status'] == 0:
        check(not receipt['logs'], 'the reverted user operation retained an EVM log')
    else:
        check(len(receipt['logs']) == 1 and receipt['logs'][0]['address'].lower() == fixture['token'], 'approval receipt logs differ')
    after = state_at(replicas, fixture, height)
    check(after['native_supply'] == before['native_supply'] == int(fixture['native_supply']), 'paying fees changed native DEL supply')
    check(after['reserve_native'] == before['reserve_native'] - converted and
          after['payer_native'] == before['payer_native'] + converted - conversion_fee - gas * price and
          after['fee_collector'] == before['fee_collector'] + conversion_fee + gas * price,
          'native sale surplus or gas refund was not returned exactly to the payer')
    check(after['payer_tokens'] == before['payer_tokens'] - int(fixture['exact_input']) and
          after['token_supply'] == before['token_supply'] - int(fixture['exact_input']) and
          after['actor_tokens'] == before['actor_tokens'] and after['allowance'] == 1,
          'fee burn or user-operation rollback changed unexpected ERC20 state')
    for node in replicas:
        fee_policy(node, fixture, operation['fee_nonce'] + 1)
        check(int(ethereum('eth_getTransactionCount', [fixture['payer'], hex(height)], base=node.evm), 16) == operation['fee_nonce'] + 1,
              'EVM nonce must advance once even for user REVERT')
    return after, {'height': height, 'status': operation['status'], 'evmGas': gas, 'conversionGas': conversion_gas,
                   'nativeFeeAtomic': str(conversion_fee + gas * price), 'nativeOutputAtomic': str(converted),
                   'ethereumHash': operation['ethereum_hash']}


def run_gate(args, binary, root, directory, fixture, genesis, keys, owner, report):
    env = private_environment(root)
    replicas = create_replicas(binary, root, genesis, keys, env, full_mesh=True)
    for node in replicas:
        write_config(node.home, 'config.toml', {('consensus', 'timeout_commit'): '"500ms"'})
    source = replicas[0]
    version = json.loads(run_command(binary, source.home, ['version', '--long', '--output', 'json'], env))
    check(version['server_name'] == 'mcnd' and version['commit'] == args.source_commit, 'daemon build differs from recorded source')
    run_command(binary, source.home, ['genesis', 'validate-genesis'], env)
    for node in replicas:
        node.start(binary, env, owner, 'fee-start')
    first = wait_replicas(replicas, report['initialHeight'] + 2, 85, report)
    before = state_at(replicas, fixture, first)
    check(before['payer_native'] == 0 and before['payer_tokens'] == int(fixture['payer_tokens']) and
          before['reserve_native'] == int(fixture['token_reserve']) and before['token_supply'] == int(fixture['token_supply']),
          'fee payer or canonical token genesis differs')
    first_raw = None
    report['operations'] = []
    for operation in fixture['operations']:
        report['phase'] = 'drc20-fee-' + operation['name']
        for node in replicas:
            fee_policy(node, fixture, operation['fee_nonce'])
            quote, _ = request(node.rest + '/mintcandy/fees/v1/quote', {
                'payer': fixture['payer'], 'token': fixture['token'], 'exact_input': fixture['exact_input'],
                'min_native_output': fixture['min_native_output'], 'native_fee_budget': fixture['native_fee_budget'], 'deadline': str(fixture['deadline']),
            })
            check(quote['fee_nonce'] == str(operation['fee_nonce']) and quote['liquidity_reserved'] is False and
                  int(quote['native_output']) >= int(fixture['min_native_output']), 'live exact-input fee quote differs')
        raw = encode_operation(binary, source.home, env, directory, fixture, operation, root)
        first_raw = raw if first_raw is None else first_raw
        receipt, height, comet_hash = submit_envelope(replicas, raw, operation, report)
        before, result = verify_receipt(replicas, fixture, operation, receipt, height, before)
        report['operations'].append({**result, 'cometHash': comet_hash})
    report['phase'] = 'fee-daemon-restart-and-replay'
    checkpoint = wait_replicas(replicas, report['operations'][-1]['height'] + 2, 35, report)
    header = committed_header(replicas, checkpoint)
    for node in reversed(replicas):
        owner.stop(node.child)
    for node in replicas:
        node.start(binary, env, owner, 'fee-restart')
    tip = wait_replicas(replicas, checkpoint + 3, 85, report)
    check(committed_header(replicas, checkpoint) == header, 'restart changed committed AppHash or block hash')
    check(state_at(replicas, fixture, tip - 1) == before, 'restart changed finalized fee balances or allowances')
    for node in replicas:
        fee_policy(node, fixture, 2)
        duplicate = node.rpc('broadcast_tx_sync?tx=0x' + first_raw.hex())
        check(int(duplicate['code']) != 0, 'restart accepted an old fee authorization replay')
    tip = wait_replicas(replicas, tip + 2, 25, report)
    check(state_at(replicas, fixture, tip - 1) == before, 'rejected replay spent funds')
    report['restart'] = {'checkpointHeight': checkpoint, 'height': tip - 1, **committed_header(replicas, tip - 1)}
    report['final'] = {key: str(value) for key, value in before.items()}


def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('binary', 'binary-sha256', 'source-commit', 'fixture', 'home-parent', 'report'):
        parser.add_argument('--' + name, required=True)
    args = parser.parse_args(argv)
    report = {'status': 'FAIL', 'phase': 'fee-daemon-fixture', 'scope': 'three loopback daemon replicas, live fee API, offline envelope CLI and Comet broadcast'}
    with pathlib.Path(args.report).open('x', encoding='utf8') as output:
        try:
            private_namespace()
            check(re.fullmatch(SHA256, args.binary_sha256) and re.fullmatch(r'[0-9a-f]{40}', args.source_commit), 'invalid binary provenance')
            binary = str(pathlib.Path(args.binary).resolve(strict=True))
            with open(binary, 'rb') as stream:
                check(hashlib.file_digest(stream, 'sha256').hexdigest() == args.binary_sha256, 'daemon SHA256 differs')
            directory = pathlib.Path(args.fixture)
            fixture, genesis, keys, initial = load_fixture(directory)
            report.update(initialHeight=initial, sourceCommit=args.source_commit, binarySha256=args.binary_sha256,
                          genesisSha256=hashlib.sha256(genesis).hexdigest(), chainID=CHAIN, evmChainID=EVM_CHAIN)
            with tempfile.TemporaryDirectory(prefix='mcnd-reserve-fee-', dir=args.home_parent) as temporary:
                root = pathlib.Path(temporary)
                owner = ProcessOwner(signals)
                try:
                    run_gate(args, binary, root, directory, fixture, genesis, keys, owner, report)
                finally:
                    owner.close()
            report.update(status='PASS', phase='complete')
        except Exception as error:
            report['error'] = safe_diagnostic(str(error))
            raise
        finally:
            json.dump(report, output, ensure_ascii=False, indent=2)
            output.write('\n')


if __name__ == '__main__':
    with SignalGuard() as guard:
        main(signals=guard)
