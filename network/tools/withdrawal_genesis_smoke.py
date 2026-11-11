"""Verify a generated financial genesis with a disposable mcnd process."""

import argparse
import datetime
import hashlib
import json
import pathlib
import re
import stat
import tempfile
import time
import urllib.parse
import urllib.request

from mcnd_process_smoke import HTTP, ProcessOwner, SignalGuard, private_environment, private_namespace, run_command


CHAIN = 'mintcandy_990001-1'
EVM_CHAIN = 990001
COMET = 'http://127.0.0.1:28657'
REST = 'http://127.0.0.1:1319'
EVM = 'http://127.0.0.1:8645'
ADDRESS = r'0x[0-9a-f]{40}'
WORD = r'0x[0-9a-f]{64}'
SHA256 = r'[0-9a-f]{64}'
ALPHABET = 'qpzry9x8gf2tvdw0s3jn54khce6mua7l'
HEIGHT_HEADER = 'x-cosmos-block-height'
COMPLETION = 'decimal_withdrawal_completed'
MAX_BLOCKS = 240


def check(condition, reason):
    if not condition:
        raise ValueError(reason)


def private_bytes(path, limit):
    info = path.lstat()
    check(stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode) & 0o077 == 0,
          'fixture must be a private regular file: ' + path.name)
    check(0 < info.st_size <= limit, 'fixture size is invalid: ' + path.name)
    return path.read_bytes()


def expectations(path):
    expected = json.loads(private_bytes(path, 8192))
    check(expected['chainId'] == CHAIN and expected['evmChainId'] == EVM_CHAIN, 'fixture chain domain mismatch')
    check(type(expected['dueAt']) is int and 15 <= expected['dueAt'] - time.time() <= 150,
          'fixture deadline must allow startup and bounded settlement')
    check(re.fullmatch(ADDRESS, expected['gateway']), 'invalid Gateway address')
    check(re.fullmatch(r'0x[0-9a-f]{72}', expected['delegateCallData']), 'invalid delegateDEL fixture calldata')
    check(isinstance(expected['invalidError'], str) and 8 <= len(expected['invalidError']) <= 240,
          'fixture must identify the expected authentication error')
    nft = expected['nft']
    check(re.fullmatch(WORD, nft['id']) and re.fullmatch(WORD, nft['tokenId']), 'NFT and frozen IDs must be full uint256 words')
    check(re.fullmatch(ADDRESS, nft['vault']) and re.fullmatch(ADDRESS, nft['beneficiary']), 'invalid NFT address')
    check(len({expected['gateway'], nft['vault'], nft['beneficiary']}) == 3, 'fixture Gateway, vault and recipient must differ')
    check(re.fullmatch(r'[A-Za-z0-9_-]{1,128}', nft['nativeTokenId']), 'invalid native NFT ID')
    check(type(nft['nativeSubTokenId']) is int and 0 < nft['nativeSubTokenId'] < 2**32, 'invalid native NFT unit ID')
    check(nft['originQuantity'] == 1024 and nft['reserveAtomic'] == '2000000000000000000', 'fixture must exercise the 1024-unit backed receipt')
    data = nft['positionCallData']
    check(re.fullmatch(r'0x[0-9a-f]{72}', data) and data[10:] == nft['tokenId'][2:], 'invalid position getter calldata')
    return expected


def private_write(path, data):
    # The mktemp root is private. Explicit mode also protects files copied out
    # by an operator while investigating a disposable CI failure.
    path.unlink(missing_ok=True)
    with path.open('xb') as stream:
        path.chmod(0o600)
        stream.write(data)


def bech32_payload(owner):
    hrp, payload = owner.rsplit('1', 1)
    check(hrp == 'd0' and len(payload) == 38 and set(payload) <= set(ALPHABET), 'invalid native NFT owner encoding')
    # The SDK query returns an already validated address. Decode its complete
    # 160-bit account payload to compare it to the actual EVM address.
    address = 0
    for character in payload[:-6]:
        address = (address << 5) | ALPHABET.index(character)
    return '0x' + address.to_bytes(20, 'big').hex()


def request(url, payload=None, headers=None):
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(url, data=data, headers=headers or {})
    if data is not None:
        req.add_header('Content-Type', 'application/json')
    with HTTP.open(req, timeout=4) as response:
        raw = response.read(2 * 1024 * 1024 + 1)
        check(len(raw) <= 2 * 1024 * 1024, 'disposable RPC response exceeds limit')
        return json.loads(raw), response.headers


def comet(method):
    response, _ = request(COMET + '/' + method)
    check('error' not in response and 'result' in response, 'Comet RPC failed: ' + method)
    return response['result']


def ethereum(method, params):
    response, _ = request(EVM, {'jsonrpc': '2.0', 'id': 1, 'method': method, 'params': params})
    check('error' not in response and 'result' in response, 'EVM RPC failed: ' + method)
    return response['result']


def native(path, height):
    body, headers = request(REST + path, headers={HEIGHT_HEADER: str(height)})
    check(headers.get(HEIGHT_HEADER) == str(height), 'native query did not use the requested committed height')
    return body


def wait_height(child, minimum, deadline):
    while time.monotonic() < deadline:
        check(child.poll() is None, 'daemon exited before reaching the expected committed height')
        try:
            status = comet('status')
        except (OSError, json.JSONDecodeError):
            time.sleep(0.25)
            continue
        check(status['node_info']['network'] == CHAIN, 'Comet RPC chain mismatch')
        height = int(status['sync_info']['latest_block_height'])
        if height >= minimum:
            return height
        time.sleep(0.25)
    raise TimeoutError('daemon did not reach the expected committed height')


def block_time(height):
    header = comet(f'block?height={height}')['block']['header']
    check(int(header['height']) == height and header['chain_id'] == CHAIN, 'unexpected committed block identity')
    stamp = datetime.datetime.fromisoformat(header['time'].replace('Z', '+00:00'))
    check(stamp.tzinfo is not None, 'committed block time lacks a timezone')
    return stamp


def completion_count(result, height, gateway, frozen_id):
    check(int(result['height']) == height, 'block results height mismatch')
    count = 0
    for event in result.get('finalize_block_events') or []:
        if event['type'] != COMPLETION:
            continue
        attributes = event['attributes']
        fields = {item['key']: item['value'] for item in attributes}
        check(len(fields) == len(attributes), 'duplicate withdrawal event attribute')
        check(fields == {'gateway': gateway, 'frozen_index': frozen_id, 'mode': 'EndBlock'},
              'unexpected withdrawal completion event or execution mode')
        count += 1
    return count


def check_nft(height, nft, expected_owner):
    query = '/decimal/nft/v1/tokens/' + urllib.parse.quote(nft['nativeTokenId'], safe='') + '/' + str(nft['nativeSubTokenId'])
    unit = native(query, height)['sub_token']
    check(int(unit['id']) == nft['nativeSubTokenId'], 'native query returned the wrong NFT unit')
    native_owner = bech32_payload(unit['owner'])
    check(native_owner == expected_owner, 'native NFT owner differs from the expected withdrawal state')
    reserve = unit['reserve']
    check(reserve['denom'] == 'del' and reserve['amount'] == nft['reserveAtomic'], 'native receipt reserve changed')
    owner_of = {'to': nft['vault'], 'data': '0x6352211e' + nft['tokenId'][2:], 'gas': '0x3d090'}
    owner_word = ethereum('eth_call', [owner_of, hex(height)])
    check(owner_word == '0x' + '00' * 12 + expected_owner[2:], 'EVM ownerOf differs from the expected withdrawal state')
    backing = int(ethereum('eth_getBalance', [nft['vault'], hex(height)]), 16)
    check(backing >= int(nft['reserveAtomic']), 'vault no longer holds the declared native reserve')
    call = {'to': nft['vault'], 'data': nft['positionCallData'], 'gas': '0x3d090'}
    position = ethereum('eth_call', [call, hex(height)])
    check(re.fullmatch(r'0x[0-9a-f]{768}', position), 'unexpected isolated position result')
    words = [int(position[i:i + 64], 16) for i in range(2, len(position), 64)]
    check(words[2] == 1 and words[3] == nft['originQuantity'] and words[4] == 0 and words[5] == int(nft['reserveAtomic']) and words[6] == 1,
          'actual isolated origin quantity or native reserve differs')
    check(words[8] == int(expected_owner, 16) and words[9] == 1, 'actual isolated position owner or live status differs')
    return {'height': height, 'nativeOwner': native_owner, 'evmOwner': expected_owner, 'reserveAtomic': nft['reserveAtomic'], 'vaultBackingAtomic': str(backing)}


def check_admission(height, expected):
    transaction = {'from': expected['nft']['beneficiary'], 'to': expected['gateway'], 'value': '0x1',
                   'data': expected['delegateCallData'], 'gas': '0x7a1200'}
    body, _ = request(EVM, {'jsonrpc': '2.0', 'id': 1, 'method': 'eth_estimateGas', 'params': [transaction, hex(height)]})
    error = body.get('error') or {}
    check('result' not in body and 'withdrawal genesis Gateway admits only completion' in error.get('message', ''),
          'new Gateway delegation estimate did not fail for the expected admission reason')


def scan_blocks(first, last, expected):
    check(1 <= first <= last + 1 <= MAX_BLOCKS + 1, 'unexpected block range in disposable process')
    completions = []
    for height in range(first, last + 1):
        count = completion_count(comet(f'block_results?height={height}'), height, expected['gateway'], expected['nft']['id'])
        completions.extend([height] * count)
    return completions


def log_records(path, message):
    matches = []
    with path.open(encoding='utf8') as stream:
        for line in stream:
            try:
                record = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(record, dict) and record.get('message', record.get('msg')) == message:
                matches.append(record)
    return matches


def run_gate(args, binary, root, expected, genesis, invalid, validator, env, owner, report):
    def prepare(home, state):
        run_command(binary, home, ['init', 'withdrawal-genesis-fixture', '--chain-id', CHAIN, '--evm.evm-chain-id', str(EVM_CHAIN)], env)
        private_write(home / 'config/genesis.json', state)
        private_write(home / 'config/priv_validator_key.json', validator)

    start = ['start', '--rpc.laddr', 'tcp://127.0.0.1:28657', '--p2p.laddr', 'tcp://127.0.0.1:28656',
             '--grpc.address', '127.0.0.1:9190', '--api.enable=true', '--api.address', 'tcp://127.0.0.1:1319',
             '--grpc-web.enable=false', '--json-rpc.enable=true', '--json-rpc.address', '127.0.0.1:8645',
             '--json-rpc.ws-address', '127.0.0.1:8646', '--log_format', 'json', '--log_level', 'info']
    domain = ['--chain-id', CHAIN, '--evm.evm-chain-id', str(EVM_CHAIN)]

    def launch(home, label, domain_flags):
        log_path = root / (label + '.log')
        child = owner.spawn([binary, *start, *domain_flags, '--home', str(home)], log_path, env)
        return child, log_path

    good = root / 'good'
    prepare(good, genesis)
    version = json.loads(run_command(binary, good, ['version', '--long', '--output', 'json'], env))
    check(version['server_name'] == 'mcnd' and version['commit'] == args.source_commit, 'daemon version differs from checked source')
    run_command(binary, good, ['genesis', 'validate-genesis'], env)

    child, log_path = launch(good, 'start', domain)
    start_height = wait_height(child, 3, time.monotonic() + 45)
    check(int(ethereum('eth_chainId', []), 16) == EVM_CHAIN, 'EVM RPC chain mismatch')
    start_time = block_time(start_height)
    check(start_time.timestamp() < expected['dueAt'], 'first committed state did not precede the deadline')
    check(not scan_blocks(1, start_height, expected), 'withdrawal completed before it was due')
    report['before'] = check_nft(start_height, expected['nft'], expected['gateway'])
    report['before']['blockTime'] = start_time.isoformat()
    check_admission(start_height, expected)
    report['newDelegationEstimate'] = 'REJECTED'

    until = time.monotonic() + max(0, expected['dueAt'] - time.time()) + 45
    height = start_height
    completed = []
    while not completed:
        last = wait_height(child, height + 1, until)
        completed = scan_blocks(height + 1, last, expected)
        height = last
    check(len(completed) == 1, 'withdrawal completed more than once')
    completed_at = completed[0]
    settled_time = block_time(completed_at)
    check(settled_time.timestamp() >= expected['dueAt'], 'completed withdrawal predates its deadline')
    report['settlement'] = check_nft(completed_at, expected['nft'], expected['nft']['beneficiary'])
    report['settlement']['blockTime'] = settled_time.isoformat()
    check(report['settlement']['vaultBackingAtomic'] == report['before']['vaultBackingAtomic'], 'NFT transfer altered actual vault backing')
    owner.stop(child)

    admissions = log_records(log_path, 'authenticated withdrawal genesis admitted')
    check(len(admissions) == 1, 'expected one authenticated genesis admission diagnostic')
    work = log_records(log_path, 'withdrawal block settlement work')
    check(len(work) == 1 and work[0].get('settled') == 1 and work[0].get('deferred') == 0 and work[0].get('gas', 0) > 0,
          'unexpected automatic withdrawal work diagnostic')
    report['settlement']['sdkGasCharged'] = work[0]['gas']
    check(type(admissions[0].get('gas')) is int and admissions[0]['gas'] > 0, 'missing authenticated genesis work gas')
    report['genesisSDKGasCharged'] = admissions[0]['gas']

    child, restart_log = launch(good, 'restart', [])
    restarted_height = wait_height(child, height + 2, time.monotonic() + 45)
    check(int(ethereum('eth_chainId', []), 16) == EVM_CHAIN, 'restarted EVM RPC chain mismatch')
    check(not scan_blocks(completed_at + 1, restarted_height, expected), 'withdrawal repeated before or after disk restart')
    report['restart'] = check_nft(restarted_height, expected['nft'], expected['nft']['beneficiary'])
    check(report['restart']['vaultBackingAtomic'] == report['before']['vaultBackingAtomic'], 'vault backing changed after restart')
    owner.stop(child)
    check(not log_records(restart_log, 'authenticated withdrawal genesis admitted'), 'restart unexpectedly reinitialized genesis')
    check(not log_records(restart_log, 'withdrawal block settlement work'), 'restart attempted to settle an already completed withdrawal')

    bad = root / 'invalid'
    prepare(bad, invalid)
    child, invalid_log = launch(bad, 'invalid', domain)
    code = child.wait(timeout=30)
    check(code != 0 and expected['invalidError'] in invalid_log.read_text(encoding='utf8'),
          'tampered genesis did not fail for the expected frozen-state authentication reason')
    report['invalidGenesis'] = {'status': 'REJECTED', 'exitCode': code, 'expectedAuthenticationFailure': True}


def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--binary-sha256', required=True)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--fixture', required=True)
    parser.add_argument('--home-parent', required=True)
    parser.add_argument('--report', required=True)
    args = parser.parse_args(argv)
    report = {'status': 'FAIL', 'scope': 'disposable authenticated withdrawal genesis only'}
    with pathlib.Path(args.report).open('x', encoding='utf8') as stream:
        try:
            private_namespace()
            check(re.fullmatch(SHA256, args.binary_sha256) and re.fullmatch(r'[0-9a-f]{40}', args.source_commit), 'invalid build provenance')
            binary = str(pathlib.Path(args.binary).resolve(strict=True))
            with open(binary, 'rb') as binary_stream:
                check(hashlib.file_digest(binary_stream, 'sha256').hexdigest() == args.binary_sha256, 'daemon SHA256 mismatch')
            fixture = pathlib.Path(args.fixture)
            fixture_info = fixture.lstat()
            check(stat.S_ISDIR(fixture_info.st_mode) and stat.S_IMODE(fixture_info.st_mode) & 0o077 == 0, 'private fixture directory required')
            expected = expectations(fixture / 'expectations.json')
            genesis = private_bytes(fixture / 'genesis.json', 64 * 1024 * 1024)
            invalid = private_bytes(fixture / 'invalid-genesis.json', 64 * 1024 * 1024)
            validator = private_bytes(fixture / 'priv_validator_key.json', 8192)
            check(genesis != invalid, 'negative control genesis must differ')
            for state in (genesis, invalid):
                document = json.loads(state)
                check(document['chain_id'] == CHAIN and int(document.get('initial_height') or 1) == 1, 'fixture genesis domain or initial height mismatch')
            report.update(sourceCommit=args.source_commit, binarySha256=args.binary_sha256,
                          genesisSha256=hashlib.sha256(genesis).hexdigest(), invalidGenesisSha256=hashlib.sha256(invalid).hexdigest(),
                          chainID=CHAIN, evmChainID=EVM_CHAIN, gateway=expected['gateway'], frozenIndex=expected['nft']['id'],
                          positionID=expected['nft']['tokenId'], originQuantity=expected['nft']['originQuantity'], dueAt=expected['dueAt'])
            with tempfile.TemporaryDirectory(prefix='mcnd-withdrawal-', dir=args.home_parent) as directory:
                root = pathlib.Path(directory)
                env = private_environment(root)
                owner = ProcessOwner(signals)
                try:
                    run_gate(args, binary, root, expected, genesis, invalid, validator, env, owner, report)
                finally:
                    owner.close()
            report['status'] = 'PASS'
        except Exception as error:
            report['error'] = str(error)
        finally:
            json.dump(report, stream, indent=2)
    print(json.dumps(report, indent=2))
    return 0 if report['status'] == 'PASS' else 1


if __name__ == '__main__':
    with SignalGuard() as guard:
        raise SystemExit(main(signals=guard))
