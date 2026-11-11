"""Exercise signed DEL delegation, top-up and withdrawals on a disposable daemon."""

import argparse
import datetime
import hashlib
import json
import pathlib
import re
import stat
import tempfile
import time
from functools import partial

from configure_history import render, validate_fresh_home
from mcnd_process_smoke import ProcessOwner, SignalGuard, private_environment, private_namespace, run_command
from withdrawal_genesis_smoke import (
    ADDRESS, CHAIN, COMET, EVM_CHAIN, EVM, MAX_BLOCKS, REST, SHA256, WORD, bech32_payload, block_time, check, comet,
    ethereum, log_records, native, private_bytes, private_write, safe_diagnostic, scan_blocks, wait_height,
)

DELEGATED = 'decimal_del_delegated'
WITHDRAWAL_REQUESTED = 'decimal_del_withdrawal_requested'
AMOUNT = 3_000_000_000_000_000_000
TOP_UP = 2_000_000_000_000_000_000
PARTIAL = 1_000_000_000_000_000_000
REMAINDER = AMOUNT + TOP_UP - PARTIAL
GAS_PRICE = 1_000_000_000_000
GAS_LIMIT = 24_000_000
SOURCE_P2P = '127.0.0.1:28656'
WITNESS_COMET = 'http://127.0.0.1:30657'
SYNC_COMET = 'http://127.0.0.1:29657'
SYNC_REST = 'http://127.0.0.1:1327'
SYNC_EVM = 'http://127.0.0.1:8745'
# Leave time for the witness to reach H+2 before the next snapshot is advertised.
SNAPSHOT_INTERVAL = 20


def load_expectations(path):
    expected = json.loads(private_bytes(path, 8192))
    check(expected['chainId'] == CHAIN and expected['evmChainId'] == EVM_CHAIN, 'fixture signing domain mismatch')
    for name in ('gateway', 'wrapped', 'actor', 'validator'):
        check(re.fullmatch(ADDRESS, expected[name]), f'invalid {name} address')
        expected[name] = expected[name].lower()
    check(len({expected[name] for name in ('gateway', 'wrapped', 'actor', 'validator')}) == 4, 'fixture roles must be distinct')
    for name in ('actor', 'wrapped'):
        check(bech32_payload(expected[name + 'Native']) == expected[name], 'fixture native address does not match its EVM address')
    check(bech32_payload(expected['validatorNative'], 'd0valoper') == expected['validator'], 'fixture operator address differs from its EVM address')
    check((expected['amount'], expected['topUpAmount'], expected['partialAmount']) == (str(AMOUNT), str(TOP_UP), str(PARTIAL)) and
          expected['gasPrice'] == str(GAS_PRICE) and expected['gasLimit'] == GAS_LIMIT,
          'fixture transaction value or gas differs')
    check(type(expected['nonce']) is int and 0 <= expected['nonce'] < 2**32, 'invalid fixture nonce')
    check(re.fullmatch(WORD, expected['stakeId']) and expected['frozenIds'] == [f'0x{index:064x}' for index in (0, 1)],
          'fixture must start before its first withdrawal')
    check(len(expected['frozenData']) == 2 and all(re.fullmatch(r'0x[0-9a-f]{72}', data) for data in expected['frozenData']),
          'invalid frozen claim getter calldata')
    for name, words in (('stakeData', 3), ('reserveData', 2), ('progressData', 0), ('supplyData', 0), ('escrowData', 1)):
        check(re.fullmatch(fr'0x[0-9a-f]{{{8 + words * 64}}}', expected[name]), f'invalid {name} calldata')
    for name in ('delegate', 'topUp', 'partialWithdraw', 'withdraw', 'repeatComplete'):
        signed = expected[name]
        check(re.fullmatch(WORD, signed['hash']) and re.fullmatch(r'0x(?:[0-9a-f]{2}){100,512}', signed['raw']),
              f'invalid {name} fixture transaction')
    return expected


def read_words(address, data, height, count, evm):
    response = ethereum('eth_call', [{'to': address, 'data': data, 'gas': '0x7a120'}, hex(height)], base=evm)
    check(re.fullmatch(fr'0x[0-9a-f]{{{count * 64}}}', response), 'unexpected fixed-size EVM return')
    return [int(response[i:i + 64], 16) for i in range(2, len(response), 64)]


def balance(account, native_account, height, evm, rest):
    value = int(ethereum('eth_getBalance', [account, hex(height)], base=evm), 16)
    native_balance = native(f'/cosmos/bank/v1beta1/balances/{native_account}/by_denom?denom=del', height, base=rest)['balance']
    check(native_balance['denom'] == 'del' and value == int(native_balance['amount']), 'EVM and native bank DEL balances differ')
    return value


def check_native_del_stake(stake, amount):
    check(stake['id'] == 'del' and stake['stake'] == {'denom': 'del', 'amount': str(amount)} and
          not stake.get('holds') and not stake.get('sub_token_ids'), 'native position does not match the exact ordinary DEL amount')


def native_positions(expected, height, active, pending, due, rest):
    operator, actor = expected['validatorNative'], expected['actorNative']
    pair = f'/decimal/validator/v1/validators/{operator}/delegations/{actor}'
    rows = native(pair, height, base=rest).get('delegations') or []
    check(len(rows) == int(active > 0), 'unexpected active native delegation count')
    if rows:
        check((rows[0]['validator'], rows[0]['delegator']) == (operator, actor), 'unexpected native delegation pair')
        check_native_del_stake(rows[0]['stake'], active)
    queue = pair + '/undelegation'
    if not pending:
        try:
            native(queue, height, base=rest)
        except OSError as error:
            check(str(error).startswith('HTTP 404 '), 'expected missing native queue, received another query error')
        else:
            raise ValueError('unexpected native undelegation after completion')
        return
    row = native(queue, height, base=rest)['undelegation']
    check((row['validator'], row['delegator']) == (operator, actor) and len(row['entries']) == 1, 'unexpected native undelegation pair or entry count')
    entry = row['entries'][0]
    check_native_del_stake(entry['stake'], pending)
    stamp = datetime.datetime.fromisoformat(entry['completion_time'].replace('Z', '+00:00'))
    check(stamp.tzinfo is not None and stamp.timestamp() == due, 'native and EVM withdrawal deadlines differ')


def read_state(expected, height, *, active, pending, claims, wallet, due=None, evm=EVM, rest=REST):
    gateway, wrapped, actor = (expected[name] for name in ('gateway', 'wrapped', 'actor'))
    stake = read_words(gateway, expected['stakeData'], height, 8, evm)
    if active:
        check(stake == [int(expected['validator'], 16), int(actor, 16), int(wrapped, 16), active, 0, 4, 0, 0],
              'active Gateway DEL stake fields differ')
    else:
        check(stake[3] == 0, 'Gateway retains an unexpected active stake')
    reserve = read_words(gateway, expected['reserveData'], height, 2, evm)[1]
    check(reserve == active, 'active Gateway validator reserve differs')
    check(read_words(gateway, expected['progressData'], height, 2, evm) == [claims, 0],
          'unexpected Gateway frozen queue length or legacy state')
    supply = read_words(wrapped, expected['supplyData'], height, 1, evm)[0]
    escrow = read_words(wrapped, expected['escrowData'], height, 1, evm)[0]
    backing = balance(wrapped, expected['wrappedNative'], height, evm, rest)
    check(supply == escrow == backing == active + pending, 'wrapped supply, Gateway custody and bank backing must reconcile exactly')
    actor_balance = balance(actor, expected['actorNative'], height, evm, rest)
    check(wallet is None or actor_balance == wallet, 'actor balance differs from exact value, fee and payout accounting')
    native_positions(expected, height, active, pending, due, rest)
    return {'height': height, 'walletAtomic': str(actor_balance), 'wrappedSupplyAtomic': str(supply),
            'gatewayEscrowAtomic': str(escrow), 'wrappedBackingAtomic': str(backing), 'activeReserveAtomic': str(reserve),
            'activeStakeAtomic': str(stake[3]), 'pendingAtomic': str(pending), 'nativePositionsVerified': True}


def read_frozen(expected, height, index, amount, status, evm=EVM):
    words = read_words(expected['gateway'], expected['frozenData'][index], height, 11, evm)
    want = [int(expected[name], 16) for name in ('validator', 'actor', 'wrapped')] + [amount, 0, 4, 0, 0, status, 1]
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


def withdraw_and_payout(child, log, report, expected, *, name, index, amount, active, nonce, wallet):
    target = expected['gateway'], expected['frozenIds'][index]
    report['phase'] = 'signed-' + name
    receipt = submit(child, log, report, expected, name, True, nonce, WITHDRAWAL_REQUESTED,
                     {'stake_id': expected['stakeId'], 'amount': str(amount), 'frozen_index': target[1]})
    wallet -= int(receipt['feeAtomic'])
    height = receipt['height']
    due = read_frozen(expected, height, index, amount, 1)
    check(0 < due - int(block_time(height).timestamp()) <= 30, 'unexpected DEL withdrawal freeze period')
    report[name] = {**receipt, **read_state(expected, height, active=active, pending=amount, claims=index + 1, wallet=wallet, due=due),
                    'dueAt': due, 'frozenIndex': target[1]}
    check(not scan_blocks(height, height, *target), 'withdrawal completed in the request block')

    report['phase'] = 'automatic-' + name
    deadline = time.monotonic() + max(0, due - time.time()) + 45
    completed = []
    while not completed:
        last = wait_height(child, height + 1, deadline, log, report)
        check(last - report['initialHeight'] < MAX_BLOCKS, 'DEL lifecycle exceeded the disposable block budget')
        completed = scan_blocks(height + 1, last, *target)
        height = last
    check(len(completed) == 1 and block_time(completed[0]).timestamp() >= due, 'DEL completion repeated or executed before maturity')
    check(read_frozen(expected, completed[0], index, amount, 0) == due, 'completed Gateway claim changed its original deadline')
    wallet += amount
    payout = read_state(expected, completed[0], active=active, pending=0, claims=index + 1, wallet=wallet)
    return {**payout, 'amountAtomic': str(amount), 'frozenIndex': target[1]}, wallet


def settlement_gas(log):
    work = log_records(log, 'withdrawal block settlement work')
    check(len(work) == 1 and work[0].get('settled') == 1 and work[0].get('deferred') == 0, 'unexpected EndBlock settlement work')
    gas = work[0].get('gas')
    check(type(gas) is int and 0 < gas <= GAS_LIMIT, 'EndBlock DEL settlement gas outside budget')
    return gas


def committed_hash(height, follower_comet):
    # Block H+1 contains the application hash committed by block H.
    method = f'block?height={height + 1}'
    source, follower = (read(method) for read in (comet, follower_comet))
    source_hash, observed_hash = (block['block']['header']['app_hash'] for block in (source, follower))
    check(re.fullmatch(r'[0-9A-Fa-f]{64}', source_hash) and source_hash == observed_hash and
          source['block_id']['hash'] == follower['block_id']['hash'], 'source and follower committed hashes differ')
    return source_hash


def observe_state_sync(log, observer_comet):
    markers = {
        'discovering snapshots': 'discovery',
        'discovered new snapshot': 'discovered',
        'offering snapshot to abci app': 'offered',
        'snapshot accepted, restoring': 'accepted',
        'applied snapshot chunk to abci app': 'chunkApplied',
        'verified abci app': 'verified',
        'snapshot restored': 'restored',
        'state sync failed': 'syncFailed',
        'snapshot rejected': 'rejected',
        'timed out waiting for snapshot chunks': 'chunksTimedOut',
        'no valid peers found for snapshot': 'noSnapshotPeers',
        'failed to fetch and verify app hash': 'trustedHashFailed',
        'failed to fetch and verify cometbft state': 'trustedStateFailed',
        'failed to fetch and verify commit': 'trustedCommitFailed',
        'state sync requires an empty application': 'nonemptyApplication',
        'received unexpected state sync snapshot chunk': 'unexpectedChunk',
        'state sync snapshot does not match the configured application': 'applicationMismatch',
        'restored application height or hash differs': 'applicationHeightOrHashMismatch',
        'restored withdrawal profile was not prepared': 'profileNotPrepared',
        'restored withdrawal profile differs': 'profileMismatch',
        'app version mismatch': 'applicationVersionMismatch',
        'apphash verification failed': 'appHashVerificationFailed',
        'abci app reported unexpected last block height': 'abciHeightMismatch',
        'failed to bootstrap node with new state': 'bootstrapFailed',
        'failed to store last seen commit': 'storeCommitFailed',
        'failed to switch to block sync': 'blockSyncStartFailed',
        'failed to apply block; error': 'blockApplicationFailed',
        'time to switch to consensus reactor!': 'blockSyncFinished',
    }
    with log.open('rb') as stream:
        head = stream.read(16384)
        stream.seek(max(len(head), log.stat().st_size - 65536))
        sample = (head + b'\n' + stream.read(65536)).decode('utf8', errors='replace')
    observed = {}
    for line in sample.lower().splitlines():
        for marker, name in markers.items():
            if marker in line:
                observed[name] = observed.get(name, 0) + 1

    # Persist only recognized categories, counts and bounded numeric RPC fields.
    result = {'logMarkers': observed}
    for method, fields in (
        ('status', {'cometHeight': ('sync_info', 'latest_block_height')}),
        ('abci_info', {'appHeight': ('response', 'last_block_height'), 'appVersion': ('response', 'app_version')}),
        ('net_info', {'peerCount': ('n_peers',)}),
    ):
        try:
            response = observer_comet(method)
            for name, path in fields.items():
                value = response
                for key in path:
                    value = value[key]
                if re.fullmatch(r'[0-9]{1,19}', str(value)):
                    result[name] = int(value)
        except (OSError, ValueError, KeyError, RuntimeError):
            continue
    return result


def write_config(home, name, changes):
    path = home / 'config' / name
    private_write(path, render(path.read_text(encoding='utf8'), changes).encode())


def new_follower_home(binary, root, genesis, env, label, domain):
    home = root / label
    run_command(binary, home, ['init', 'del-state-sync-' + label, *domain], env)
    validate_fresh_home(home)
    private_write(home / 'config/genesis.json', genesis)
    return home


def consensus_identity(read_comet, *, votes):
    status = read_comet('status')
    node_id = status['node_info']['id']
    power = int(status['validator_info']['voting_power'])
    check(re.fullmatch(r'[0-9a-f]{40}', node_id) and (power > 0 if votes else power == 0),
          'unexpected Comet peer identity or voting power')
    return node_id, power


def start_witness(binary, root, genesis, env, owner, domain):
    home = new_follower_home(binary, root, genesis, env, 'witness', domain)
    source_id, _ = consensus_identity(comet, votes=True)
    write_config(home, 'config.toml', {
        ('p2p', 'persistent_peers'): json.dumps(f'{source_id}@{SOURCE_P2P}'),
        ('p2p', 'pex'): 'false',
        ('statesync', 'enable'): 'false',
    })
    start = ['start', '--rpc.laddr', 'tcp://127.0.0.1:30657', '--p2p.laddr', 'tcp://127.0.0.1:30656',
             '--api.enable=false', '--grpc.enable=false', '--grpc-web.enable=false', '--json-rpc.enable=false',
             '--log_format', 'json', '--log_level', 'info']
    log = root / 'witness.log'
    return owner.spawn([binary, *start, *domain, '--home', str(home)], log, env), log, partial(comet, base=WITNESS_COMET)


def start_state_sync(binary, root, genesis, env, owner, source_child, source_log, minimum_height, witness, report):
    report['phase'] = 'snapshot-source'
    domain = ['--chain-id', CHAIN, '--evm.evm-chain-id', str(EVM_CHAIN)]
    home = new_follower_home(binary, root, genesis, env, 'observer', domain)
    deadline = time.monotonic() + 70
    while time.monotonic() < deadline:
        snapshots = [int(row['height']) for row in log_records(source_log, 'completed state snapshot')]
        if snapshots and max(snapshots) >= minimum_height:
            break
        check(source_child.poll() is None, 'source stopped before producing the financial snapshot')
        time.sleep(0.5)
    else:
        raise TimeoutError('source did not produce an active DEL snapshot')
    source_snapshot_height = max(snapshots)
    # Comet verifies the snapshot, the next header carrying its AppHash and
    # the subsequent validator set. Let the separate witness catch up first.
    trust_height = source_snapshot_height + 2
    wait_height(source_child, trust_height, deadline, source_log, report)
    witness_child, witness_log, witness_comet = witness
    report['phase'] = 'snapshot-witness'
    wait_height(witness_child, trust_height, time.monotonic() + 45, witness_log, report, read_comet=witness_comet)
    source_id, source_power = consensus_identity(comet, votes=True)
    witness_id, witness_power = consensus_identity(witness_comet, votes=False)
    check(source_id != witness_id and int(witness_comet('status')['sync_info']['earliest_block_height']) == report['initialHeight'],
          'witness must replay from genesis as a separate peer')
    trust_hash = ''
    for height in range(source_snapshot_height, trust_height + 1):
        method = f'block?height={height}'
        trust_hash = comet(method)['block_id']['hash']
        check(re.fullmatch(r'[0-9A-Fa-f]{64}', trust_hash) and trust_hash == witness_comet(method)['block_id']['hash'],
              'source and witness trust-window block hashes differ')
    report['stateSync'] = {
        'sourceSnapshotHeight': source_snapshot_height, 'sourceSnapshotAppHash': committed_hash(source_snapshot_height, witness_comet),
        'trustHeight': trust_height, 'trustHash': trust_hash,
        'witnessVerifiedThrough': trust_height, 'sourceNodeID': source_id, 'sourceVotingPower': source_power,
        'witnessNodeID': witness_id, 'witnessVotingPower': witness_power,
        'witnessIsSeparateProcess': True, 'independentFailureDomains': False,
    }

    report['phase'] = 'snapshot-restore'
    write_config(home, 'config.toml', {
        ('p2p', 'persistent_peers'): json.dumps(f'{source_id}@{SOURCE_P2P}'),
        ('p2p', 'pex'): 'false',
        ('statesync', 'enable'): 'true',
        ('statesync', 'rpc_servers'): json.dumps(COMET + ',' + WITNESS_COMET),
        ('statesync', 'trust_height'): str(trust_height),
        ('statesync', 'trust_hash'): json.dumps(trust_hash),
        ('statesync', 'trust_period'): '"1h"',
        ('statesync', 'discovery_time'): '"5s"',
    })
    write_config(home, 'app.toml', {('api', 'address'): '"tcp://127.0.0.1:1327"'})
    start = ['start', '--rpc.laddr', 'tcp://127.0.0.1:29657', '--p2p.laddr', 'tcp://127.0.0.1:29656',
             '--grpc.address', '127.0.0.1:9290', '--api.enable=true', '--grpc-web.enable=false',
             '--json-rpc.enable=true', '--json-rpc.address', '127.0.0.1:8745', '--json-rpc.ws-address', '127.0.0.1:8746',
             '--log_format', 'json', '--log_level', 'info']
    log = root / 'observer.log'
    observer = owner.spawn([binary, *start, *domain, '--home', str(home)], log, env)
    observer_comet = partial(comet, base=SYNC_COMET)
    try:
        wait_height(observer, trust_height + 1, time.monotonic() + 120, log, report, read_comet=observer_comet)
    except (TimeoutError, RuntimeError):
        report['stateSync']['observerDiagnostics'] = observe_state_sync(log, observer_comet)
        raise
    restored = log_records(log, 'Snapshot restored')
    check(len(restored) == 1 and int(restored[0]['height']) == source_snapshot_height,
          'observer did not restore the witnessed active DEL snapshot')
    observer_id, observer_power = consensus_identity(observer_comet, votes=False)
    check(observer_id not in (source_id, witness_id), 'observer must use its own peer identity')
    check(int(ethereum('eth_chainId', [], base=SYNC_EVM), 16) == EVM_CHAIN, 'observer EVM signing domain mismatch')
    snapshot_height = int(restored[0]['height'])
    height = wait_height(observer, snapshot_height + 2, time.monotonic() + 45, log, report, read_comet=observer_comet)
    wait_height(observer, height + 1, time.monotonic() + 45, log, report, read_comet=observer_comet)
    check(int(observer_comet('status')['sync_info']['earliest_block_height']) >= snapshot_height,
          'observer retained block history older than the restored snapshot')
    report['stateSync'].update(snapshotHeight=snapshot_height, activeStateHeight=height,
                               observerNodeID=observer_id, observerVotingPower=observer_power,
                               activeStateAppHash=committed_hash(height, observer_comet))
    return observer, log, observer_comet


def run_gate(args, binary, root, expected, genesis, validator, env, owner, report):
    home = root / 'node'
    domain = ['--chain-id', CHAIN, '--evm.evm-chain-id', str(EVM_CHAIN)]
    start = ['start', '--rpc.laddr', 'tcp://127.0.0.1:28657', '--p2p.laddr', 'tcp://127.0.0.1:28656',
             '--grpc.address', '127.0.0.1:9190', '--api.enable=true', '--grpc-web.enable=false',
             '--json-rpc.enable=true', '--json-rpc.address', '127.0.0.1:8645', '--json-rpc.ws-address', '127.0.0.1:8646',
             '--log_format', 'json', '--log_level', 'info']
    run_command(binary, home, ['init', 'del-delegation-fixture', *domain], env)
    if args.state_sync:
        # All disposable peers are loopback processes in one network namespace.
        write_config(home, 'config.toml', {('p2p', 'allow_duplicate_ip'): 'true', ('p2p', 'pex'): 'false'})
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
    if args.state_sync:
        witness = start_witness(binary, root, genesis, env, owner, domain)
    report['before'] = read_state(expected, height, active=0, pending=0, claims=0, wallet=None)
    wallet = int(report['before']['walletAtomic'])
    nonce = expected['nonce']
    active = 0
    for offset, (name, key, amount) in enumerate((('delegate', 'delegation', AMOUNT), ('topUp', 'topUp', TOP_UP))):
        report['phase'] = 'signed-' + name
        receipt = submit(child, log, report, expected, name, True, nonce + offset, DELEGATED,
                         {'stake_id': expected['stakeId'], 'amount': str(amount)})
        wallet -= amount + int(receipt['feeAtomic'])
        active += amount
        report[key] = {**receipt, **read_state(expected, receipt['height'], active=active, pending=0, claims=0, wallet=wallet)}

    report['partialPayout'], wallet = withdraw_and_payout(
        child, log, report, expected, name='partialWithdraw', index=0, amount=PARTIAL, active=REMAINDER, nonce=nonce + 2, wallet=wallet)

    report['phase'] = 'repeat-complete'
    repeated = submit(child, log, report, expected, 'repeatComplete', False, nonce + 3, 'decimal_withdrawal_completed', {})
    wallet -= int(repeated['feeAtomic'])
    report['repeatComplete'] = {**repeated, **read_state(expected, repeated['height'], active=REMAINDER, pending=0, claims=1, wallet=wallet)}
    owner.stop(child)
    report['partialPayout']['sdkGasCharged'] = settlement_gas(log)

    report['phase'] = 'restart'
    restart_log = root / 'restart.log'
    snapshots = ['--state-sync.snapshot-interval', str(SNAPSHOT_INTERVAL), '--state-sync.snapshot-keep-recent', '4'] if args.state_sync else []
    child = owner.spawn([binary, *start, *snapshots, '--home', str(home)], restart_log, env)
    restarted = wait_height(child, repeated['height'] + 2, time.monotonic() + 45, restart_log, report)
    check(not scan_blocks(report['partialPayout']['height'] + 1, restarted, expected['gateway'], expected['frozenIds'][0]),
          'DEL payout repeated before or after restart')
    due = report['partialWithdraw']['dueAt']
    check(read_frozen(expected, restarted, 0, PARTIAL, 0) == due, 'completed claim differs after restart')
    report['restart'] = read_state(expected, restarted, active=REMAINDER, pending=0, claims=1, wallet=wallet)

    if args.state_sync:
        observer, observer_log, observer_comet = start_state_sync(
            binary, root, genesis, env, owner, child, restart_log, restarted, witness, report)
        active_height = report['stateSync']['activeStateHeight']
        report['stateSync']['restored'] = read_state(expected, active_height, active=REMAINDER, pending=0, claims=1, wallet=wallet,
                                                     evm=SYNC_EVM, rest=SYNC_REST)

    report['payout'], wallet = withdraw_and_payout(
        child, restart_log, report, expected, name='withdraw', index=1, amount=REMAINDER, active=0, nonce=nonce + 4, wallet=wallet)
    report['phase'] = 'final-state'
    payout_height = report['payout']['height']
    final_height = wait_height(child, payout_height + 2, time.monotonic() + 45, restart_log, report)
    check(not scan_blocks(payout_height + 1, final_height, expected['gateway'], expected['frozenIds'][1]), 'final DEL payout repeated')
    check(read_frozen(expected, final_height, 0, PARTIAL, 0) == due, 'first claim changed while completing the remainder')
    report['final'] = read_state(expected, final_height, active=0, pending=0, claims=2, wallet=wallet)
    if args.state_sync:
        report['phase'] = 'snapshot-replay'
        pending_height = report['withdraw']['height']
        wait_height(observer, final_height + 1, time.monotonic() + 90, observer_log, report, read_comet=observer_comet)
        report['stateSync']['pending'] = read_state(expected, pending_height, active=0, pending=REMAINDER, claims=2,
                                                   wallet=int(report['withdraw']['walletAtomic']), due=report['withdraw']['dueAt'],
                                                   evm=SYNC_EVM, rest=SYNC_REST)
        report['stateSync']['replayed'] = read_state(expected, final_height, active=0, pending=0, claims=2, wallet=wallet,
                                                    evm=SYNC_EVM, rest=SYNC_REST)
        check(read_frozen(expected, final_height, 1, REMAINDER, 0, SYNC_EVM) == report['withdraw']['dueAt'],
              'consumer did not replay final DEL completion')
        report['stateSync']['finalAppHash'] = committed_hash(final_height, observer_comet)
        witness_child, witness_log, witness_comet = witness
        wait_height(witness_child, final_height + 1, time.monotonic() + 45, witness_log, report, read_comet=witness_comet)
        report['stateSync']['witnessFinalAppHash'] = committed_hash(final_height, witness_comet)
        owner.stop(observer)
        owner.stop(witness_child)
    owner.stop(child)
    report['payout']['sdkGasCharged'] = settlement_gas(restart_log)


def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('binary', 'binary-sha256', 'source-commit', 'fixture', 'home-parent', 'report'):
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--state-sync', action='store_true')
    args = parser.parse_args(argv)
    report = {'status': 'FAIL', 'phase': 'fixture-provenance', 'scope': 'disposable DEL top-up and partial/full withdrawal'}
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
                          frozenIndices=expected['frozenIds'])
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
