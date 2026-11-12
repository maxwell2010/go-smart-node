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
import urllib.error
import urllib.parse
import urllib.request

from mcnd_process_smoke import HTTP, ProcessOwner, SignalGuard, private_environment, private_namespace, run_command


CHAIN = 'mintcandy_990001-1'
EVM_CHAIN = 990001
COMET = 'http://127.0.0.1:28657'
REST = 'http://127.0.0.1:1317'
EVM = 'http://127.0.0.1:8645'
ADDRESS = r'0x[0-9a-fA-F]{40}'
WORD = r'0x[0-9a-f]{64}'
SHA256 = r'[0-9a-f]{64}'
ALPHABET = 'qpzry9x8gf2tvdw0s3jn54khce6mua7l'
HEIGHT_HEADER = 'x-cosmos-block-height'
COMPLETION = 'decimal_withdrawal_completed'
MAX_BLOCKS = 240
RESERVE_ATOMIC = '2048000000000000000000'
# Keep the engine limit aligned with withdrawalAttemptGas in app. The lower
# regression limit leaves room for the measured 1024-unit EndBlock work.
WITHDRAWAL_ATTEMPT_GAS_LIMIT = 24_000_000
MAX_1024_SETTLEMENT_GAS = 23_000_000


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
    expected['gateway'] = expected['gateway'].lower()
    check(re.fullmatch(r'0x[0-9a-f]{72}', expected['delegateCallData']), 'invalid delegateDEL fixture calldata')
    check(isinstance(expected['invalidError'], str) and 8 <= len(expected['invalidError']) <= 240,
          'fixture must identify the expected authentication error')
    nft = expected['nft']
    check(re.fullmatch(WORD, nft['id']) and re.fullmatch(WORD, nft['tokenId']), 'NFT and frozen IDs must be full uint256 words')
    check(re.fullmatch(ADDRESS, nft['vault']) and re.fullmatch(ADDRESS, nft['beneficiary']), 'invalid NFT address')
    nft['vault'], nft['beneficiary'] = nft['vault'].lower(), nft['beneficiary'].lower()
    check(len({expected['gateway'], nft['vault'], nft['beneficiary']}) == 3, 'fixture Gateway, vault and recipient must differ')
    check(re.fullmatch(r'[A-Za-z0-9_-]{1,128}', nft['nativeTokenId']), 'invalid native NFT ID')
    check(type(nft['nativeSubTokenId']) is int and 0 < nft['nativeSubTokenId'] < 2**32, 'invalid native NFT unit ID')
    check(nft['originQuantity'] == 1024 and nft['reserveAtomic'] == RESERVE_ATOMIC, 'fixture must exercise the 1024-unit backed receipt')
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


def bech32_payload(owner, prefix='d0'):
    hrp, payload = owner.rsplit('1', 1)
    check(hrp == prefix and len(payload) == 38 and set(payload) <= set(ALPHABET), 'invalid native address encoding')
    # The SDK query returns an already validated address. Decode its complete
    # 160-bit account payload to compare it to the actual EVM address.
    address = 0
    for character in payload[:-6]:
        address = (address << 5) | ALPHABET.index(character)
    return '0x' + address.to_bytes(20, 'big').hex()


def rpc_context(url, payload=None, headers=None):
    parsed = urllib.parse.urlsplit(url)
    height = (headers or {}).get(HEIGHT_HEADER)
    if payload is None:
        endpoint = 'GET ' + parsed.path
        height = height or urllib.parse.parse_qs(parsed.query).get('height', [None])[0]
    else:
        endpoint = 'POST ' + payload['method']
        params = payload.get('params') or []
        if params and isinstance(params[-1], str) and re.fullmatch(r'0x[0-9a-f]{1,16}', params[-1]):
            height = str(int(params[-1], 16))
    return endpoint + (f' at height {height}' if height else '')


def rpc_reason(text):
    phrases = ('cannot query with height in the future', 'version does not exist', 'could not load state',
               'could not find results', 'block results are not available', 'height is not available',
               'header not found', 'please wait for first block', 'context did not contain latest block height')
    sample = text[:4096].lower()
    return ', '.join(phrase for phrase in phrases if phrase in sample) or 'no allowlisted upstream reason'


def request(url, payload=None, headers=None):
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(url, data=data, headers=headers or {})
    if data is not None:
        req.add_header('Content-Type', 'application/json')
    try:
        with HTTP.open(req, timeout=4) as response:
            raw = response.read(2 * 1024 * 1024 + 1)
            check(len(raw) <= 2 * 1024 * 1024, 'disposable RPC response exceeds limit')
            return json.loads(raw), response.headers
    except urllib.error.HTTPError as error:
        with error:
            reason = rpc_reason(error.read(4096).decode('utf8', errors='replace'))
        raise OSError(f'HTTP {error.code} {rpc_context(url, payload, headers)}: {reason}') from None
    except OSError as error:
        raise OSError(f'{rpc_context(url, payload, headers)}: {type(error).__name__}') from None


def comet(method, *, base=COMET):
    url = base + '/' + method
    response, _ = request(url)
    check('error' not in response and 'result' in response,
          f'Comet RPC {rpc_context(url)} failed: ' + rpc_reason(str(response.get('error', ''))))
    return response['result']


def ethereum(method, params, *, base=EVM):
    payload = {'jsonrpc': '2.0', 'id': 1, 'method': method, 'params': params}
    response, _ = request(base, payload)
    check('error' not in response and 'result' in response,
          f'EVM RPC {rpc_context(base, payload)} failed: ' + rpc_reason(str(response.get('error', ''))))
    return response['result']


def native(path, height, *, base=REST):
    body, headers = request(base + path, headers={HEIGHT_HEADER: str(height)})
    check(headers.get(HEIGHT_HEADER) == str(height), 'native query did not use the requested committed height')
    return body


def safe_diagnostic(text):
    categories = ('authenticate withdrawal genesis', 'genesis signing domain', 'failed to unmarshal',
                  'withdrawal genesis profile exceeds size limit', 'decode withdrawal genesis profile',
                  'consensus genesis cannot be nil', 'cosmos and evm chain ids do not match',
                  'failed to initialize', 'validator set', 'priv-validator', 'no space left on device',
                  'failed init node', 'failed start cometbft server', 'invalid cachemergeiterator',
                  'native fee rewards require an empty genesis del fee collector',
                  'native fee reward positions differ from the indexed active del reserve',
                  'native fee reward commission address is not admitted',
                  'native fee reward owner address is not admitted', 'invalid native fee reward recipient',
                  'native delegation index requires initialization or explicit migration',
                  'distribute native fees', 'record native prior commit', 'authenticate native consensus genesis position',
                  'native ordinary del downtime requires native consensus', 'native ordinary downtime requires',
                  'native ordinary downtime exceeds its per-validator position budget',
                  'native ordinary downtime encountered an unsupported', 'native ordinary del downtime deferred',
                  'native validator frozen index is unavailable', 'invalid native validator binding index',
                  'native commit observation is replayed or skipped', 'invalid native signing window',
                  'native signing window size or height differs', 'native downtime planning requires the recorded prior commit',
                  'native downtime window differs')
    lower = text.lower()
    notes = [category for category in categories if category in lower]
    reason = consensus_reason(text)
    if reason:
        notes.insert(0, 'consensus block application: ' + reason)
    flags = re.findall(r'unknown (?:shorthand )?flag: ([-A-Za-z0-9._]{2,80})', text, re.IGNORECASE)
    notes += ['unknown flag ' + flag for flag in list(dict.fromkeys(flags))[:4]]
    notes += list(dict.fromkeys(re.findall(r'unable to resolve type URL (/[A-Za-z0-9_.]{1,140})', text)))[:4]
    notes += list(dict.fromkeys(re.findall(r'\b[A-Za-z0-9_-]+\.go:\d{1,6}(?::\d{1,5})?', text)))[:8]
    return ', '.join(notes) if notes else 'no allowlisted diagnostic'


def consensus_reason(text):
    marker = 'failed to apply block; error'
    for line in text.splitlines():
        if marker not in line.lower():
            continue
        try:
            record = json.loads(line)
        except json.JSONDecodeError:
            values = (line,)
        else:
            values = (record.get('err'), record.get('error'), record.get('message'), record.get('msg')) if isinstance(record, dict) else ()
        for value in values:
            if not isinstance(value, str) or marker not in value.lower():
                continue
            reason = value[value.lower().index(marker) + len(marker):].lstrip(' :')
            if re.search(r'mnemonic|private[ _-]?key|seed[ _-]?phrase|password|authorization|bearer|api[ _-]?token', reason, re.IGNORECASE):
                return 'sensitive reason suppressed'
            reason = re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', reason)
            reason = re.sub(r'(?<![A-Za-z0-9_.])(?:[A-Za-z]:)?[/\\][^\s"\'<>]+', '[path]', reason)
            reason = re.sub(r'(?<![A-Za-z0-9])(?:0[xX])?[A-Fa-f0-9]{24,}(?![A-Za-z0-9])', '[hex]', reason)
            reason = re.sub(r'[A-Za-z0-9_+/=-]{24,}', '[identifier]', reason)
            reason = re.sub(r'\[(?:\s*\d{1,3}\s*,?){8,}\]', '[byte array]', reason)
            reason = ' '.join(re.sub(r'[^\x20-\x7e]', ' ', reason).split())
            if reason:
                return reason[:240]
    return None


def log_tail(path):
    with path.open('rb') as stream:
        stream.seek(max(0, path.stat().st_size - 65536))
        return stream.read(65536).decode('utf8', errors='replace')


def readiness_diagnostic(path, report):
    with path.open('rb') as stream:
        text = stream.read(65536).decode('utf8', errors='replace')
    if path.stat().st_size > 65536:
        text += '\n' + log_tail(path)
    if report['phase'] in ('positive-start', 'initial-native-consensus'):
        report['genesisAdmissionObserved'] = 'authenticated withdrawal genesis admitted' in text
    return safe_diagnostic(text)


def wait_height(child, minimum, deadline, log_path, report, *, read_comet=None, chain_id=CHAIN):
    read_comet = comet if read_comet is None else read_comet
    last_heights = ''
    while time.monotonic() < deadline:
        code = child.poll()
        if code is not None:
            raise RuntimeError(f'daemon exited ({code}) before committed height {minimum}: ' + readiness_diagnostic(log_path, report))
        try:
            status = read_comet('status')
            check(status['node_info']['network'] == chain_id, 'Comet RPC chain mismatch')
            advertised = int(status['sync_info']['latest_block_height'])
            committed = int(read_comet('abci_info')['response'].get('last_block_height', '0'))
        except (OSError, json.JSONDecodeError):
            time.sleep(0.25)
            continue
        # Comet can advertise a block before Commit makes its SDK version
        # queryable. Block results and historical state must both be ready.
        height = min(advertised, committed)
        if height >= minimum:
            return height
        last_heights = f' (Comet advertised {advertised}, application committed {committed})'
        time.sleep(0.25)
    raise TimeoutError(f'daemon did not reach committed height {minimum}{last_heights}: ' + readiness_diagnostic(log_path, report))


def block_time(height):
    header = comet(f'block?height={height}')['block']['header']
    check(int(header['height']) == height and header['chain_id'] == CHAIN, 'unexpected committed block identity')
    stamp = datetime.datetime.fromisoformat(header['time'].replace('Z', '+00:00'))
    check(stamp.tzinfo is not None, 'committed block time lacks a timezone')
    return stamp


def completion_count(result, height, gateway, frozen_id):
    check(int(result['height']) == height, 'block results height mismatch')
    check('finalize_block_events' in result, 'block results lack committed EndBlock events')
    count = 0
    for event in result['finalize_block_events'] or []:
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
    token_id = urllib.parse.quote(nft['nativeTokenId'], safe='')
    query = '/decimal/nft/v1/tokens/' + token_id + '/' + str(nft['nativeSubTokenId'])
    unit = native(query, height)['sub_token']
    check(int(unit['id']) == nft['nativeSubTokenId'], 'native query returned the wrong NFT unit')
    native_owner = bech32_payload(unit['owner'])
    check(native_owner == expected_owner, 'native NFT owner differs from the expected withdrawal state')
    token = native('/decimal/nft/v1/token/' + token_id, height)['token']
    check(token['id'] == nft['nativeTokenId'] and token['isolated_receipt'] is True, 'native query returned the wrong receipt token')
    reserve = token['reserve']
    check(reserve['denom'] == 'del' and reserve['amount'] == nft['reserveAtomic'], 'native receipt reserve changed')
    check(unit.get('reserve') is None or unit['reserve'] == reserve, 'native receipt unit reserve differs from its token')
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


def scan_blocks(first, last, gateway, frozen_id):
    check(1 <= first <= last + 1 and last - first + 1 <= MAX_BLOCKS, 'unexpected block range in disposable process')
    completions = []
    for height in range(first, last + 1):
        count = completion_count(comet(f'block_results?height={height}'), height, gateway, frozen_id)
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


def check_settlement_gas(result, charged):
    check(type(charged) is int and charged > 0, 'missing automatic settlement SDK gas')
    result.update(sdkGasCharged=charged, sdkGasLimit=WITHDRAWAL_ATTEMPT_GAS_LIMIT,
                  sdkGasHeadroom=WITHDRAWAL_ATTEMPT_GAS_LIMIT - charged,
                  sdkGasRegressionLimit=MAX_1024_SETTLEMENT_GAS)
    check(charged <= MAX_1024_SETTLEMENT_GAS,
          f'1024-unit settlement charged {charged} SDK gas, above the {MAX_1024_SETTLEMENT_GAS} regression limit')


def run_gate(args, binary, root, expected, genesis, invalid, validator, env, owner, report):
    def prepare(home, state):
        run_command(binary, home, ['init', 'withdrawal-genesis-fixture', '--chain-id', CHAIN, '--evm.evm-chain-id', str(EVM_CHAIN)], env)
        private_write(home / 'config/genesis.json', state)
        private_write(home / 'config/priv_validator_key.json', validator)

    start = ['start', '--rpc.laddr', 'tcp://127.0.0.1:28657', '--p2p.laddr', 'tcp://127.0.0.1:28656',
             '--grpc.address', '127.0.0.1:9190', '--api.enable=true',
             '--grpc-web.enable=false', '--json-rpc.enable=true', '--json-rpc.address', '127.0.0.1:8645',
             '--json-rpc.ws-address', '127.0.0.1:8646', '--log_format', 'json', '--log_level', 'info']
    domain = ['--chain-id', CHAIN, '--evm.evm-chain-id', str(EVM_CHAIN)]

    def launch(home, label, domain_flags):
        log_path = root / (label + '.log')
        child = owner.spawn([binary, *start, *domain_flags, '--home', str(home)], log_path, env)
        return child, log_path

    good = root / 'good'
    report['phase'] = 'positive-cli-validation'
    prepare(good, genesis)
    version = json.loads(run_command(binary, good, ['version', '--long', '--output', 'json'], env))
    check(version['server_name'] == 'mcnd' and version['commit'] == args.source_commit, 'daemon version differs from checked source')
    try:
        run_command(binary, good, ['genesis', 'validate-genesis'], env)
    except RuntimeError as error:
        raise RuntimeError('genesis validation rejected the positive fixture: ' + safe_diagnostic(str(error))) from None

    report['phase'] = 'positive-start'
    child, log_path = launch(good, 'start', domain)
    initial_height = report['initialHeight']
    start_height = wait_height(child, initial_height + 2, time.monotonic() + 45, log_path, report)
    report['genesisAdmissionObserved'] = bool(log_records(log_path, 'authenticated withdrawal genesis admitted'))
    report['phase'] = 'pre-deadline-state'
    check(int(ethereum('eth_chainId', []), 16) == EVM_CHAIN, 'EVM RPC chain mismatch')
    start_time = block_time(start_height)
    check(start_time.timestamp() < expected['dueAt'], 'first committed state did not precede the deadline')
    check(not scan_blocks(initial_height, start_height, expected['gateway'], expected['nft']['id']), 'withdrawal completed before it was due')
    report['before'] = check_nft(start_height, expected['nft'], expected['gateway'])
    report['before']['blockTime'] = start_time.isoformat()
    check_admission(start_height, expected)
    report['newDelegationEstimate'] = 'REJECTED'

    report['phase'] = 'automatic-settlement'
    until = time.monotonic() + max(0, expected['dueAt'] - time.time()) + 45
    height = start_height
    completed = []
    while not completed:
        last = wait_height(child, height + 1, until, log_path, report)
        check(last - initial_height < MAX_BLOCKS, 'withdrawal exceeded the disposable block budget')
        completed = scan_blocks(height + 1, last, expected['gateway'], expected['nft']['id'])
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
    check(len(work) == 1 and work[0].get('settled') == 1 and work[0].get('deferred') == 0,
          'unexpected automatic withdrawal work diagnostic')
    check_settlement_gas(report['settlement'], work[0].get('gas'))
    check(type(admissions[0].get('gas')) is int and admissions[0]['gas'] > 0, 'missing authenticated genesis work gas')
    report['genesisSDKGasCharged'] = admissions[0]['gas']

    report['phase'] = 'restart'
    child, restart_log = launch(good, 'restart', [])
    restarted_height = wait_height(child, height + 2, time.monotonic() + 45, restart_log, report)
    check(restarted_height - initial_height < MAX_BLOCKS, 'restart exceeded the disposable block budget')
    check(int(ethereum('eth_chainId', []), 16) == EVM_CHAIN, 'restarted EVM RPC chain mismatch')
    check(not scan_blocks(completed_at + 1, restarted_height, expected['gateway'], expected['nft']['id']), 'withdrawal repeated before or after disk restart')
    report['restart'] = check_nft(restarted_height, expected['nft'], expected['nft']['beneficiary'])
    check(report['restart']['vaultBackingAtomic'] == report['before']['vaultBackingAtomic'], 'vault backing changed after restart')
    owner.stop(child)
    check(not log_records(restart_log, 'authenticated withdrawal genesis admitted'), 'restart unexpectedly reinitialized genesis')
    check(not log_records(restart_log, 'withdrawal block settlement work'), 'restart attempted to settle an already completed withdrawal')

    report['phase'] = 'negative-genesis'
    bad = root / 'invalid'
    prepare(bad, invalid)
    child, invalid_log = launch(bad, 'invalid', domain)
    code = child.wait(timeout=30)
    diagnostic = log_tail(invalid_log)
    check(code != 0 and expected['invalidError'] in diagnostic,
          f'tampered genesis did not fail for the expected authentication reason ({code}): ' + safe_diagnostic(diagnostic))
    report['invalidGenesis'] = {'status': 'REJECTED', 'exitCode': code, 'expectedAuthenticationFailure': True}


def initial_height(genesis, invalid):
    heights = []
    for state in (genesis, invalid):
        document = json.loads(state)
        height = int(document['initial_height'])
        check(document['chain_id'] == CHAIN and 1 <= height < 2**63 - MAX_BLOCKS, 'fixture genesis domain or initial height mismatch')
        heights.append(height)
    check(heights[0] == heights[1], 'negative control changed the initial height')
    return heights[0]


def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--binary-sha256', required=True)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--fixture', required=True)
    parser.add_argument('--home-parent', required=True)
    parser.add_argument('--report', required=True)
    args = parser.parse_args(argv)
    report = {'status': 'FAIL', 'phase': 'fixture-provenance', 'scope': 'disposable authenticated withdrawal genesis only'}
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
            height = initial_height(genesis, invalid)
            report.update(sourceCommit=args.source_commit, binarySha256=args.binary_sha256,
                          genesisSha256=hashlib.sha256(genesis).hexdigest(), invalidGenesisSha256=hashlib.sha256(invalid).hexdigest(),
                          chainID=CHAIN, evmChainID=EVM_CHAIN, gateway=expected['gateway'], frozenIndex=expected['nft']['id'],
                          positionID=expected['nft']['tokenId'], originQuantity=expected['nft']['originQuantity'], dueAt=expected['dueAt'],
                          initialHeight=height)
            with tempfile.TemporaryDirectory(prefix='mcnd-withdrawal-', dir=args.home_parent) as directory:
                root = pathlib.Path(directory)
                env = private_environment(root)
                owner = ProcessOwner(signals)
                try:
                    run_gate(args, binary, root, expected, genesis, invalid, validator, env, owner, report)
                    report['phase'] = 'cleanup'
                finally:
                    owner.close()
            report['status'] = 'PASS'
            report['phase'] = 'complete'
        except Exception as error:
            report['error'] = ' '.join(str(error).split())[:520]
        finally:
            json.dump(report, stream, indent=2)
    print(json.dumps(report, indent=2))
    return 0 if report['status'] == 'PASS' else 1


if __name__ == '__main__':
    with SignalGuard() as guard:
        raise SystemExit(main(signals=guard))
