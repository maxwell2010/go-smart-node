"""Exercise a fresh signed DEL delegation and real EndBlock payout on a disposable daemon."""

import argparse
import hashlib
import json
import pathlib
import re
import stat
import tempfile
import time

from mcnd_process_smoke import ProcessOwner, SignalGuard, private_environment, private_namespace, run_command
from withdrawal_genesis_smoke import (
    ADDRESS, CHAIN, EVM_CHAIN, EVM, MAX_BLOCKS, SHA256, WORD, bech32_payload, block_time, check, comet,
    ethereum, log_records, native, private_bytes, private_write, safe_diagnostic, scan_blocks, wait_height,
)

DELEGATED = 'decimal_del_delegated'
WITHDRAWAL_REQUESTED = 'decimal_del_withdrawal_requested'
AMOUNT = 3_000_000_000_000_000_000
GAS_PRICE = 1_000_000_000_000
GAS_LIMIT = 24_000_000


def load_expectations(path):
    expected = json.loads(private_bytes(path, 8192))
    check(expected['chainId'] == CHAIN and expected['evmChainId'] == EVM_CHAIN, 'fixture signing domain mismatch')
    for name in ('gateway', 'wrapped', 'actor', 'validator'):
        check(re.fullmatch(ADDRESS, expected[name]), f'invalid {name} address')
        expected[name] = expected[name].lower()
    check(len({expected[name] for name in ('gateway', 'wrapped', 'actor', 'validator')}) == 4, 'fixture roles must be distinct')
    for name in ('actor', 'wrapped'):
        check(bech32_payload(expected[name + 'Native']) == expected[name], 'fixture native address does not match its EVM address')
    check(expected['amount'] == str(AMOUNT) and expected['gasPrice'] == str(GAS_PRICE) and expected['gasLimit'] == GAS_LIMIT,
          'fixture transaction value or gas differs')
    check(type(expected['nonce']) is int and 0 <= expected['nonce'] < 2**32, 'invalid fixture nonce')
    check(re.fullmatch(WORD, expected['stakeId']) and expected['frozenId'] == '0x' + '00' * 32, 'fixture must start before its first withdrawal')
    for name, words in (('stakeData', 3), ('reserveData', 2), ('frozenData', 1), ('progressData', 0), ('supplyData', 0), ('escrowData', 1)):
        check(re.fullmatch(fr'0x[0-9a-f]{{{8 + words * 64}}}', expected[name]), f'invalid {name} calldata')
    for name in ('delegate', 'withdraw', 'repeatComplete'):
        signed = expected[name]
        check(re.fullmatch(WORD, signed['hash']) and re.fullmatch(r'0x(?:[0-9a-f]{2}){100,512}', signed['raw']),
              f'invalid {name} fixture transaction')
    return expected


def read_words(address, data, height, count):
    response = ethereum('eth_call', [{'to': address, 'data': data, 'gas': '0x7a120'}, hex(height)])
    check(re.fullmatch(fr'0x[0-9a-f]{{{count * 64}}}', response), 'unexpected fixed-size EVM return')
    return [int(response[i:i + 64], 16) for i in range(2, len(response), 64)]


def balance(account, native_account, height):
    evm = int(ethereum('eth_getBalance', [account, hex(height)]), 16)
    native_balance = native(f'/cosmos/bank/v1beta1/balances/{native_account}/by_denom?denom=del', height)['balance']
    check(native_balance['denom'] == 'del' and evm == int(native_balance['amount']), 'EVM and native bank DEL balances differ')
    return evm


def read_state(expected, height, phase, wallet):
    delegated = phase == 'delegated'
    frozen = phase == 'frozen'
    amount = AMOUNT if delegated or frozen else 0
    gateway, wrapped, actor = (expected[name] for name in ('gateway', 'wrapped', 'actor'))
    stake = read_words(gateway, expected['stakeData'], height, 8)
    if delegated:
        check(stake == [int(expected['validator'], 16), int(actor, 16), int(wrapped, 16), AMOUNT, 0, 4, 0, 0],
              'active Gateway DEL stake fields differ')
    else:
        check(stake[3] == 0, 'Gateway retains an unexpected active stake')
    reserve = read_words(gateway, expected['reserveData'], height, 2)[1]
    check(reserve == (AMOUNT if delegated else 0), 'active Gateway validator reserve differs')
    check(read_words(gateway, expected['progressData'], height, 2) == [int(phase not in ('genesis', 'delegated')), 0],
          'unexpected Gateway frozen queue length or legacy state')
    supply = read_words(wrapped, expected['supplyData'], height, 1)[0]
    escrow = read_words(wrapped, expected['escrowData'], height, 1)[0]
    backing = balance(wrapped, expected['wrappedNative'], height)
    check(supply == escrow == backing == amount, 'wrapped supply, Gateway custody and bank backing must reconcile exactly')
    actor_balance = balance(actor, expected['actorNative'], height)
    check(wallet is None or actor_balance == wallet, 'actor balance differs from exact value, fee and payout accounting')
    return {'height': height, 'walletAtomic': str(actor_balance), 'wrappedSupplyAtomic': str(supply),
            'gatewayEscrowAtomic': str(escrow), 'wrappedBackingAtomic': str(backing), 'activeReserveAtomic': str(reserve)}


def read_frozen(expected, height, status):
    words = read_words(expected['gateway'], expected['frozenData'], height, 11)
    want = [int(expected[name], 16) for name in ('validator', 'actor', 'wrapped')] + [AMOUNT, 0, 4, 0, 0, status, 1]
    check(words[:10] == want and words[10] > 0, 'Gateway frozen DEL claim fields differ')
    return words[10]


def event_matches(events, name, expected):
    matched = [event for event in events if event['type'] == name]
    if not expected:
        check(not matched, f'unexpected native {name} event')
        return
    check(len(matched) == 1, f'expected exactly one native {name} event')
    attrs = matched[0]['attributes']
    fields = {field['key']: field['value'] for field in attrs}
    check(len(fields) == len(attrs) and all(fields.get(k) == v for k, v in expected.items()), f'native {name} event fields differ')


def submit(child, log, report, expected, name, success, nonce, event, fields):
    check(int(ethereum('eth_chainId', []), 16) == EVM_CHAIN, 'daemon EVM signing domain mismatch')
    check(int(ethereum('eth_getTransactionCount', [expected['actor'], 'latest']), 16) == nonce, 'fixture nonce differs from committed account')
    signed = expected[name]
    result = ethereum('eth_sendRawTransaction', [signed['raw']])
    check(result == signed['hash'], 'daemon returned an unexpected transaction hash')
    deadline = time.monotonic() + 40
    while time.monotonic() < deadline:
        check(child.poll() is None, f'daemon stopped while waiting for the {name} receipt')
        try:
            receipt = ethereum('eth_getTransactionReceipt', [result])
        except OSError:
            receipt = None
        if receipt is not None:
            break
        time.sleep(0.25)
    else:
        raise TimeoutError(f'daemon did not index the {name} receipt')
    height = int(receipt['blockNumber'], 16)
    wait_height(child, height, deadline, log, report)
    gas, price = int(receipt['gasUsed'], 16), int(receipt['effectiveGasPrice'], 16)
    check(receipt['transactionHash'] == result and receipt['from'].lower() == expected['actor'] and receipt['to'].lower() == expected['gateway'],
          'receipt identity differs from the signed fixture')
    check(int(receipt['status'], 16) == int(success) and 0 < gas <= GAS_LIMIT and price == GAS_PRICE,
          f'{name} status, gas or effective price differs')
    txs = comet(f'block_results?height={height}')['txs_results']
    check(len(txs) == 1, 'fixture block must contain exactly one signed transaction')
    events = txs[0].get('events') or []
    event_matches(events, event, fields)
    return {'height': height, 'transactionHash': result, 'success': success, 'gasUsed': gas, 'feeAtomic': str(gas * price)}


def run_gate(args, binary, root, expected, genesis, validator, env, owner, report):
    home = root / 'node'
    domain = ['--chain-id', CHAIN, '--evm.evm-chain-id', str(EVM_CHAIN)]
    start = ['start', '--rpc.laddr', 'tcp://127.0.0.1:28657', '--p2p.laddr', 'tcp://127.0.0.1:28656',
             '--grpc.address', '127.0.0.1:9190', '--api.enable=true', '--grpc-web.enable=false',
             '--json-rpc.enable=true', '--json-rpc.address', '127.0.0.1:8645', '--json-rpc.ws-address', '127.0.0.1:8646',
             '--log_format', 'json', '--log_level', 'info']
    run_command(binary, home, ['init', 'del-delegation-fixture', *domain], env)
    private_write(home / 'config/genesis.json', genesis)
    private_write(home / 'config/priv_validator_key.json', validator)
    version = json.loads(run_command(binary, home, ['version', '--long', '--output', 'json'], env))
    check(version['server_name'] == 'mcnd' and version['commit'] == args.source_commit, 'daemon version differs from checked source')
    try:
        run_command(binary, home, ['genesis', 'validate-genesis'], env)
    except RuntimeError as error:
        raise RuntimeError('genesis validation rejected fresh DEL fixture: ' + safe_diagnostic(str(error))) from None

    report['phase'] = 'positive-start'
    log = root / 'start.log'
    child = owner.spawn([binary, *start, *domain, '--home', str(home)], log, env)
    height = wait_height(child, report['initialHeight'] + 2, time.monotonic() + 45, log, report)
    check(len(log_records(log, 'authenticated withdrawal genesis admitted')) == 1, 'fresh DEL genesis admission not observed')
    report['before'] = read_state(expected, height, 'genesis', None)
    initial_wallet = int(report['before']['walletAtomic'])
    nonce = expected['nonce']
    completion_target = expected['gateway'], expected['frozenId']
    fields = {'stake_id': expected['stakeId'], 'amount': str(AMOUNT)}

    report['phase'] = 'signed-delegate'
    delegated = submit(child, log, report, expected, 'delegate', True, nonce, DELEGATED, fields)
    wallet = initial_wallet - AMOUNT - int(delegated['feeAtomic'])
    report['delegation'] = {**delegated, **read_state(expected, delegated['height'], 'delegated', wallet)}

    report['phase'] = 'signed-withdraw'
    withdrawn = submit(child, log, report, expected, 'withdraw', True, nonce + 1, WITHDRAWAL_REQUESTED,
                       {**fields, 'frozen_index': expected['frozenId']})
    wallet -= int(withdrawn['feeAtomic'])
    height = withdrawn['height']
    due = read_frozen(expected, height, 1)
    check(0 < due - int(block_time(height).timestamp()) <= 30, 'unexpected DEL withdrawal freeze period')
    report['withdrawal'] = {**withdrawn, **read_state(expected, height, 'frozen', wallet), 'dueAt': due}
    check(not scan_blocks(report['initialHeight'], height, *completion_target), 'withdrawal completed before maturity')

    report['phase'] = 'automatic-complete'
    deadline = time.monotonic() + max(0, due - time.time()) + 45
    completed = []
    while not completed:
        last = wait_height(child, height + 1, deadline, log, report)
        check(last - report['initialHeight'] < MAX_BLOCKS, 'DEL lifecycle exceeded the disposable block budget')
        completed = scan_blocks(height + 1, last, *completion_target)
        height = last
    check(len(completed) == 1 and block_time(completed[0]).timestamp() >= due, 'DEL completion repeated or executed before maturity')
    check(read_frozen(expected, completed[0], 0) == due, 'completed Gateway claim changed its original deadline')
    wallet += AMOUNT
    report['payout'] = {**read_state(expected, completed[0], 'complete', wallet), 'amountAtomic': str(AMOUNT)}

    report['phase'] = 'repeat-complete'
    repeated = submit(child, log, report, expected, 'repeatComplete', False, nonce + 2, 'decimal_withdrawal_completed', {})
    wallet -= int(repeated['feeAtomic'])
    report['repeatComplete'] = {**repeated, **read_state(expected, repeated['height'], 'complete', wallet)}
    owner.stop(child)
    work = log_records(log, 'withdrawal block settlement work')
    check(len(work) == 1 and work[0].get('settled') == 1 and work[0].get('deferred') == 0, 'unexpected EndBlock settlement work')
    report['payout']['sdkGasCharged'] = work[0]['gas']

    report['phase'] = 'restart'
    restart_log = root / 'restart.log'
    child = owner.spawn([binary, *start, '--home', str(home)], restart_log, env)
    restarted = wait_height(child, repeated['height'] + 2, time.monotonic() + 45, restart_log, report)
    check(not scan_blocks(completed[0] + 1, restarted, *completion_target), 'DEL payout repeated before or after restart')
    check(read_frozen(expected, restarted, 0) == due, 'completed claim differs after restart')
    report['restart'] = read_state(expected, restarted, 'complete', wallet)
    owner.stop(child)


def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('binary', 'binary-sha256', 'source-commit', 'fixture', 'home-parent', 'report'):
        parser.add_argument('--' + name, required=True)
    args = parser.parse_args(argv)
    report = {'status': 'FAIL', 'phase': 'fixture-provenance', 'scope': 'disposable new DEL delegation and complete'}
    with pathlib.Path(args.report).open('x', encoding='utf8') as stream:
        try:
            private_namespace()
            check(re.fullmatch(SHA256, args.binary_sha256) and re.fullmatch(r'[0-9a-f]{40}', args.source_commit), 'invalid build provenance')
            binary = str(pathlib.Path(args.binary).resolve(strict=True))
            with open(binary, 'rb') as binary_stream:
                check(hashlib.file_digest(binary_stream, 'sha256').hexdigest() == args.binary_sha256, 'daemon SHA256 mismatch')
            fixture = pathlib.Path(args.fixture)
            info = fixture.lstat()
            check(stat.S_ISDIR(info.st_mode) and stat.S_IMODE(info.st_mode) & 0o077 == 0, 'private fixture directory required')
            expected = load_expectations(fixture / 'expectations.json')
            genesis = private_bytes(fixture / 'genesis.json', 64 * 1024 * 1024)
            validator = private_bytes(fixture / 'priv_validator_key.json', 8192)
            doc = json.loads(genesis)
            profile = doc['app_state']['withdrawal_settlement']
            height = int(doc['initial_height'])
            check(doc['chain_id'] == CHAIN and 1 <= height < 2**63 - MAX_BLOCKS, 'fixture genesis domain or height mismatch')
            check(profile['enable_del_delegation'] is True and profile['settlement_only'] is True and
                  profile['gateway'].lower() == expected['gateway'] and profile['wrapped_del'].lower() == expected['wrapped'],
                  'fixture must explicitly admit only DEL plus settlement')
            report.update(sourceCommit=args.source_commit, binarySha256=args.binary_sha256, genesisSha256=hashlib.sha256(genesis).hexdigest(),
                          chainID=CHAIN, evmChainID=EVM_CHAIN, initialHeight=height, gateway=expected['gateway'], stakeID=expected['stakeId'],
                          frozenIndex=expected['frozenId'])
            with tempfile.TemporaryDirectory(prefix='mcnd-del-delegation-', dir=args.home_parent) as directory:
                root = pathlib.Path(directory)
                owner = ProcessOwner(signals)
                try:
                    run_gate(args, binary, root, expected, genesis, validator, private_environment(root), owner, report)
                finally:
                    owner.close()
            report.update(status='PASS', phase='complete')
        except Exception as error:
            report['error'] = ' '.join(str(error).split())[:520]
        finally:
            json.dump(report, stream, indent=2)
    print(json.dumps(report, indent=2))
    return 0 if report['status'] == 'PASS' else 1


if __name__ == '__main__':
    with SignalGuard() as guard:
        raise SystemExit(main(signals=guard))
