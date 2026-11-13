"""Rehearse one native governance SDK migration across two binaries."""

import argparse
import base64
from dataclasses import replace
from datetime import datetime
import hashlib
import json
import pathlib
import re
import subprocess
import tempfile
import time
from urllib.parse import urlencode

from del_delegation_smoke import GAS_LIMIT, GAS_PRICE, balance, native_positions, read_frozen, read_words, write_config
from mcnd_process_smoke import ProcessOwner, SignalGuard, private_environment, private_namespace, run_command
from native_consensus_smoke import ATOMS, FOUR_UPGRADE_POWERS, INITIAL, check_signatures, committed_header, create_replica, create_replicas, decode_pubkey, effective_set, load_fixture, wait_replicas
from withdrawal_genesis_smoke import CHAIN, EVM_CHAIN, MAX_BLOCKS, SHA256, WORD, check, ethereum, log_records, native, private_bytes, private_write, readiness_diagnostic, request, safe_diagnostic


PLAN = 'mintcandy-native-sdk-migrations-v1'
SCHEMA = 'mintcandy-sdk-upgrade/v1'
SCHEDULED = 'decimal_native_sdk_upgrade_scheduled'
UPGRADE_API = '/cosmos/upgrade/v1beta1'
PROPOSALS_API = '/cosmos/gov/v1/proposals'
VOTING = 'PROPOSAL_STATUS_VOTING_PERIOD'
VOTING_WEIGHT = '3000000000000000000'
UNBONDING_SECONDS = 28 * 24 * 60 * 60
TX_FAILURES = ('account sequence mismatch', 'incorrect account sequence', 'signature verification failed',
               'insufficient fee', 'gas prices too low', 'out of gas', 'proposal not found',
               'not in voting period', 'circuit breaker', 'native sdk upgrade proposal')


def load_governance_fixture(directory, *, full_profile=False, pending_ibc=False, four_upgrade=False):
    expected, genesis, keys, initial = load_fixture(
        directory, fee_rewards=full_profile, downtime=False, jail_recovery=False, held_downtime=False,
        nft_downtime=False, full_upgrade=full_profile, four_upgrade=four_upgrade)
    doc = json.loads(genesis)['app_state']
    profile = doc['withdrawal_settlement']
    check(profile.get('enable_native_governance') is True and profile.get('enable_native_upgrade_governance') is True,
          'fixture must explicitly enable native upgrade governance')
    check(not any(doc['gov'].get(name) for name in ('proposals', 'votes', 'deposits')),
          'fixture must begin with fresh governance state')
    payload = json.loads(private_bytes(directory / 'governance.json', 65536 if pending_ibc else 8192))
    actions = payload if pending_ibc else [payload]
    if pending_ibc:
        check([action['planHeight'] for action in actions] == list(range(48, 209, 8)), 'unexpected signed plan height grid')
    for action in actions:
        validate_signed_plan(action, expected['actorNative'], doc['gov'], initial)
        check(action['sequence'] == actions[0]['sequence'] and action['accountNumber'] == actions[0]['accountNumber'],
              'signed plans must share the same initial actor state')
    if full_profile:
        check(initial == 1 and all(profile.get(flag) is True for flag in (
            'enable_native_duplicate_vote', 'enable_native_ibc_v1', 'enable_del_hold', 'enable_del_hold_reset',
            'enable_nft_delegation',
        )), 'combined fixture requires a fresh historical profile with native IBC, hold and NFT admission')
        claim = expected['upgradeWithdrawal']
        first = expected['validators'][0]
        check(claim['validator'].lower() == first['evm'] and claim['validatorNative'] == first['operator'] and
              claim['nonce'] == actions[0]['sequence'] + 4 and
              int(claim['activeAtomic']) == (9 if four_upgrade else 7) * ATOMS // 4 and int(claim['pendingAtomic']) == ATOMS // 4 and
              len(claim['frozenData']) == 1 and re.fullmatch(r'0x[0-9a-f]{72}', claim['frozenData'][0]) and
              re.fullmatch(WORD, claim['hash']) and re.fullmatch(r'0x(?:[0-9a-f]{2}){100,512}', claim['raw']),
              'invalid signed full-profile withdrawal fixture')
        claim['validator'] = first['evm']
    else:
        check('upgradeWithdrawal' not in expected and not profile.get('enable_native_duplicate_vote'),
              'basic fixture must use its original profile')
    return expected, genesis, keys, initial, actions


def validate_signed_plan(action, actor, gov, initial):
    height = action['planHeight']
    check(action['planName'] == PLAN and type(height) is int and initial + 20 <= height < initial + MAX_BLOCKS - 4,
          'invalid fixture upgrade name or height')
    metadata = SCHEMA + ':' + json.dumps({'name': PLAN, 'height': height}, separators=(',', ':'))
    check(action['metadata'] == metadata and action['actorNative'] == actor,
          'fixture upgrade manifest or voter differs')
    rejected, passed = action['rejectedID'], action['passedID']
    check(type(rejected) is int and type(passed) is int and rejected > 0 and passed == rejected + 1 and
          rejected == int(gov['starting_proposal_id']), 'invalid fixture proposal IDs')
    check(action['depositAtomic'] == '1000000000000000000' and gov['params']['min_deposit'] ==
          [{'denom': 'del', 'amount': action['depositAtomic']}], 'fixture native governance deposit differs')
    for field in ('accountNumber', 'sequence'):
        check(type(action[field]) is int and 0 <= action[field] < 2**64 - 4, 'invalid fixture account sequence or number')
    hashes = set()
    for label in ('rejectedProposal', 'rejectedVote', 'passedProposal', 'passedVote'):
        signed = action[label]
        raw = base64.b64decode(signed['tx'], validate=True)
        check(100 <= len(raw) <= 1536 and re.fullmatch(SHA256, signed['hash']) and
              hashlib.sha256(raw).hexdigest() == signed['hash'], 'invalid signed Cosmos governance fixture transaction')
        hashes.add(signed['hash'])
    check(len(hashes) == 4, 'fixture signed Cosmos transactions must be distinct')


def check_tx_result(result, label, phase):
    code = int(result.get('code', 0))
    if code:
        log = str(result.get('log', ''))[:4096].lower()
        reason = next((phrase for phrase in TX_FAILURES if phrase in log), 'no allowlisted reason')
        raise ValueError(f'{label} {phase} failed with code {code}: {reason}')


def submit_governance_tx(node, signed, label, report):
    endpoint = f'http://127.0.0.1:{node.p2p + 1}'
    payload = {'jsonrpc': '2.0', 'id': 1, 'method': 'broadcast_tx_sync', 'params': {'tx': signed['tx']}}
    response, _ = request(endpoint, payload)
    check('error' not in response and 'result' in response, f'{label} broadcast RPC failed')
    result = response['result']
    check_tx_result(result, label, 'CheckTx')
    check(result['hash'].lower().removeprefix('0x') == signed['hash'], f'{label} returned an unexpected transaction hash')
    deadline = time.monotonic() + 40
    while time.monotonic() < deadline:
        if node.child.poll() is not None:
            raise RuntimeError(f'daemon stopped before {label} committed: ' + readiness_diagnostic(node.log, report))
        try:
            committed = node.rpc(f'tx?hash=0x{signed["hash"]}&prove=false')
        except (OSError, ValueError):
            time.sleep(0.25)
            continue
        check_tx_result(committed['tx_result'], label, 'DeliverTx')
        check(committed['hash'].lower().removeprefix('0x') == signed['hash'], f'{label} indexed a different transaction')
        return {'height': int(committed['height']), 'hash': signed['hash']}
    raise TimeoutError(f'daemon did not index {label}')


def committed_height(node):
    comet_height = int(node.rpc('status')['sync_info']['latest_block_height'])
    app_height = int(node.rpc('abci_info')['response'].get('last_block_height', '0'))
    return min(comet_height, app_height)


def query_proposal(node, proposal_id, height):
    return native(f'{PROPOSALS_API}/{proposal_id}', height, base=node.rest)['proposal']


def wait_tally(node, proposal_id, minimum, report):
    deadline = time.monotonic() + 45
    while time.monotonic() < deadline:
        if node.child.poll() is not None:
            raise RuntimeError('daemon stopped before native governance tally: ' + readiness_diagnostic(node.log, report))
        try:
            height = committed_height(node)
            if height >= minimum:
                proposal = query_proposal(node, proposal_id, height)
                if proposal['status'] != VOTING:
                    check(height - report['initialHeight'] < MAX_BLOCKS, 'native governance exceeded the disposable block budget')
                    return height
        except (OSError, json.JSONDecodeError):
            pass
        time.sleep(0.25)
    raise TimeoutError('proposal did not reach a final native tally')


def current_plan(node, height):
    return native(f'{UPGRADE_API}/current_plan', height, base=node.rest).get('plan')


def applied_height(node, height):
    return int(native(f'{UPGRADE_API}/applied_plan/{PLAN}', height, base=node.rest).get('height', '0'))


def check_proposal(replicas, proposal_id, height, manifest, accepted, voting_weight=VOTING_WEIGHT):
    status = 'PROPOSAL_STATUS_PASSED' if accepted else 'PROPOSAL_STATUS_REJECTED'
    expected = {'yes_count': voting_weight if accepted else '0', 'no_count': '0' if accepted else voting_weight,
                'abstain_count': '0', 'no_with_veto_count': '0'}
    for node in replicas:
        proposal = query_proposal(node, proposal_id, height)
        check(int(proposal['id']) == proposal_id and proposal['status'] == status and
              proposal['metadata'] == manifest and not proposal.get('messages'), 'unexpected native proposal identity or outcome')
        tally = {key: str(proposal['final_tally_result'].get(key, '0')) for key in expected}
        check(all(re.fullmatch(r'[0-9]{1,30}', count) for count in tally.values()), 'final native tally contains an invalid count')
        check(tally == expected, f'final tally on {node.label} differs from native voting weight: expected {expected}, got {tally}')
    return {'id': proposal_id, 'status': status, 'queryHeight': height, 'tally': expected}


def schedule_event(node, height):
    result = node.rpc(f'block_results?height={height}')
    check(int(result['height']) == height, 'unexpected Comet block result height')
    matches = [event for event in result.get('finalize_block_events') or [] if event['type'] == SCHEDULED]
    check(len(matches) <= 1, 'multiple SDK upgrade schedules in one block')
    if not matches:
        return None
    attributes = matches[0]['attributes']
    fields = {entry['key']: entry['value'] for entry in attributes}
    check(len(fields) == len(attributes), 'duplicate schedule event attribute')
    return {key: fields.get(key) for key in ('proposal_id', 'name', 'height')}


def check_schedule(replicas, action, height):
    expected_info = {'schema': SCHEMA, 'proposal_id': action['passedID']}
    for node in replicas:
        plan = current_plan(node, height)
        check(plan is not None and plan['name'] == PLAN and int(plan['height']) == action['planHeight'] and
              plan['info'] == json.dumps(expected_info, separators=(',', ':')) and not plan.get('upgraded_client_state'),
              'stored SDK plan differs from its exact approved governance manifest')
        check(applied_height(node, height) == 0, 'SDK migration executed before its scheduled height')


def wait_predecessor_halt(replicas, action, report):
    target = action['planHeight']
    expected_info = json.dumps({'schema': SCHEMA, 'proposal_id': action['passedID']}, separators=(',', ':'))
    started = time.monotonic()
    deadline = started + 180
    observed = {}
    while time.monotonic() < deadline:
        ready = True
        for node in replicas:
            if node.child.poll() is not None:
                raise RuntimeError('predecessor exited before its upgrade boundary was verified: ' +
                                   readiness_diagnostic(node.log, report))
            try:
                height = int(node.rpc('abci_info')['response'].get('last_block_height', '0'))
            except (OSError, json.JSONDecodeError):
                ready = False
                continue
            check(height <= target - 1, 'predecessor committed the upgrade height')
            prior = observed.get(node.label)
            last_advance = (prior['lastAdvanceSeconds'] if prior and height == prior['committedHeight']
                            else round(time.monotonic() - started, 1))
            state = {'committedHeight': height, 'upgradeHint': False, 'lastAdvanceSeconds': last_advance}
            observed[node.label] = state
            try:
                hint = json.loads(private_bytes(node.home / 'data/upgrade-info.json', 4096))
            except (OSError, json.JSONDecodeError):
                ready = False
                continue
            check(hint['name'] == PLAN and int(hint['height']) == target and hint['info'] == expected_info,
                  'SDK upgrade hint differs from the exact approved plan')
            state['upgradeHint'] = True
            ready &= height == target - 1
        if ready:
            return
        time.sleep(0.25)
    report['predecessorProgress'] = observed
    report['predecessorDiagnostics'] = {node.label: readiness_diagnostic(node.log, report) for node in replicas}
    raise TimeoutError('predecessors did not reach the exact SDK upgrade boundary')


def withdraw_for_handoff(node, expected):
    claim = expected['upgradeWithdrawal']

    def evm(method, params):
        return ethereum(method, params, base=node.evm)

    check(int(evm('eth_chainId', []), 16) == EVM_CHAIN and
          int(evm('eth_getTransactionCount', [expected['actor'], 'latest']), 16) == claim['nonce'],
          'signed withdrawal has the wrong EVM domain or account nonce')
    tx_hash = evm('eth_sendRawTransaction', [claim['raw']])
    check(tx_hash == claim['hash'], 'unexpected withdrawal transaction hash')
    deadline = time.monotonic() + 40
    while time.monotonic() < deadline:
        check(node.child.poll() is None, 'predecessor exited before withdrawal committed')
        try:
            receipt = evm('eth_getTransactionReceipt', [tx_hash])
        except OSError:
            receipt = None
        if receipt is not None:
            check(receipt['transactionHash'] == tx_hash and receipt['from'].lower() == expected['actor'] and
                  receipt['to'].lower() == expected['gateway'] and int(receipt['status'], 16) == 1 and
                  0 < int(receipt['gasUsed'], 16) <= GAS_LIMIT and int(receipt['effectiveGasPrice'], 16) == GAS_PRICE,
                  'predecessor withdrawal receipt differs from the successful signed fixture')
            return int(receipt['blockNumber'], 16), tx_hash
        time.sleep(0.25)
    raise TimeoutError('predecessor did not index the signed withdrawal')


def check_pending_withdrawal(replicas, expected, height, due):
    claim = expected['upgradeWithdrawal']
    asset = {**expected, **claim}
    active, pending = int(claim['activeAtomic']), int(claim['pendingAtomic'])
    backing_total = (10 if len(expected['validators']) == 4 else 3) * ATOMS
    native_supply = set()
    for node in replicas:
        check(read_frozen(asset, height, 0, pending, 1, node.evm) == due, 'EVM frozen claim deadline changed')
        native_positions(asset, height, active, pending, due, node.rest)
        sdk_params = native('/cosmos/staking/v1beta1/params', height, base=node.rest)['params']
        check(sdk_params['unbonding_time'] == f'{UNBONDING_SECONDS}s', 'IBC unbonding query no longer matches the 28-day Gateway duration')
        reserve = read_words(expected['gateway'], expected['validators'][0]['reserveData'], height, 2, node.evm)[1]
        supply = read_words(expected['wrapped'], expected['supplyData'], height, 1, node.evm)[0]
        escrow = read_words(expected['wrapped'], expected['escrowData'], height, 1, node.evm)[0]
        backing = balance(expected['wrapped'], expected['wrappedNative'], height, node.evm, node.rest)
        check(reserve == active and supply == escrow == backing == backing_total,
              'Gateway reserve or exact DEL backing differs across the binary handoff')
        bank_supply = native('/cosmos/bank/v1beta1/supply/by_denom?denom=del', height, base=node.rest)['amount']
        check(bank_supply['denom'] == 'del' and int(bank_supply['amount']) >= backing, 'invalid native DEL supply')
        native_supply.add(bank_supply['amount'])
    check(len(native_supply) == 1, 'replicas disagree on native DEL supply')
    return {'height': height, 'pendingAtomic': str(pending), 'activeAtomic': str(active), 'dueAt': due,
            'wrappedBackingAtomic': str(backing_total), 'nativeSupplyAtomic': native_supply.pop(),
            'appHash': committed_header(replicas, height + 1)['app_hash']}


def select_pending_plan(node, actions, report):
    height = committed_height(node)
    low = max(report['initialHeight'], height - 4)
    check(height > low, 'not enough committed blocks to schedule upgrade after the handshake')
    times = [datetime.fromisoformat(node.rpc(f'block?height={h}')['block']['header']['time'].replace('Z', '+00:00'))
             for h in (low, height)]
    seconds = (times[1] - times[0]).total_seconds() / (height - low)
    lead = 24 if seconds >= 2 else 32
    action = next((plan for plan in actions if plan['planHeight'] >= height + lead), None)
    check(action is not None, 'Hermes handshake consumed the private fixture height budget')
    report['ibcPacket']['planSelectionHeight'] = height
    return action


def read_coin_store(node, key, height):
    params = urlencode({'path': '"/store/coin/key"', 'data': '0x' + key.hex(), 'height': height, 'prove': 'false'})
    result = node.rpc(f'abci_query?{params}')['response']
    check(int(result.get('code', 0)) == 0 and int(result['height']) == height,
          'coin migration store query failed or used a different height')
    value = result.get('value')
    return base64.b64decode(value, validate=True) if value else None


def check_coin_module_migration(replicas, genesis, height, version):
    (coin,) = json.loads(genesis)['app_state']['coin']['coins']
    address, denom = coin['drc20_address'].lower(), coin['denom']
    check(re.fullmatch(r'0x[0-9a-f]{40}', address) and denom == 'genesis20', 'invalid migration DRC20 fixture')
    old_key, new_key = b'\x13' + address.encode('ascii'), b'\x14' + bytes.fromhex(address[2:])
    expected = (denom.encode(), None, None) if version == 1 else (None, denom.encode(), b'\x02')
    records = set()
    for node in replicas:
        versions = native(f'{UPGRADE_API}/module_versions?module_name=coin', height, base=node.rest)['module_versions']
        check([(entry['name'], int(entry['version'])) for entry in versions] == [('coin', version)],
              'persisted coin module version differs from the expected migration stage')
        check(tuple(read_coin_store(node, key, height) for key in (old_key, new_key, b'\x15')) == expected,
              'persisted DRC20 address index differs from the expected migration stage')
        record = read_coin_store(node, b'\x11' + denom.encode(), height)
        check(record is not None, 'indexed DRC20 coin record is missing')
        records.add(hashlib.sha256(record).hexdigest())
    check(len(records) == 1, 'replicas disagree on the DRC20 coin record')
    return {'height': height, 'module': 'coin', 'version': version, 'denom': denom,
            'legacyIndexPresent': version == 1, 'binaryIndexPresent': version == 2,
            'migrationMarkerPresent': version == 2, 'coinRecordSha256': records.pop()}


def canonical_commit(replicas, height):
    commits = [node.rpc(f'commit?height={height}') for node in replicas]
    signed = commits[0]['signed_header']
    check(all(entry['canonical'] is True and entry['signed_header'] == signed for entry in commits) and
          int(signed['commit']['height']) == height and int(signed['header']['height']) == height and
          signed['header']['chain_id'] == CHAIN and
          signed['commit']['block_id']['hash'] == committed_header(replicas, height)['block_hash'],
          'replicas disagree on a canonical signed commit')
    return signed['commit']


def verify_four_signatures(commit, validators, probe):
    height = int(commit['height'])
    payload = {'chainId': CHAIN, 'commit': commit,
               'publicKeys': [base64.b64encode(decode_pubkey(row['publicKey'])).decode('ascii') for row in validators]}
    result = subprocess.run([str(probe)], input=json.dumps(payload), capture_output=True, text=True, timeout=10)
    check(result.returncode == 0, 'Comet cryptographic commit verification failed: ' + result.stderr.strip()[:200])
    verified = json.loads(result.stdout)
    check(len(verified) == 4 and set(verified) == {row['consensusAddress'] for row in validators},
          'commit does not contain valid signatures from all four validators')
    return {'height': height, 'round': int(commit['round']), 'blockHash': commit['block_id']['hash'],
            'signers': [row['name'] for row in validators], 'verifiedEd25519Signatures': len(verified)}


def find_four_signature_commit(replicas, validators, probe, first, last, report):
    deadline = time.monotonic() + 30
    height = first
    while height <= last:
        tip = wait_replicas(replicas, height + 1, max(1, deadline - time.monotonic()), report)
        for height in range(height, min(tip - 1, last) + 1):
            commit = canonical_commit(replicas, height)
            if sum(int(row['block_id_flag']) == 2 for row in commit['signatures']) == 4:
                return verify_four_signatures(commit, validators, probe)
        height += 1
    raise ValueError('no canonical commit with all four validator signatures in the checked height window')


def wait_signing_boundary(replicas, boundary, report):
    deadline = time.monotonic() + 70
    while time.monotonic() < deadline:
        for node in replicas:
            if node.child.poll() is not None:
                raise RuntimeError('successor exited before saving the final signed block: ' +
                                   readiness_diagnostic(node.log, report))
        try:
            heights = [int(node.rpc('abci_info')['response']['last_block_height']) for node in replicas]
            check(max(heights) < boundary, 'a primary signer exceeded the application halt height')
            if all(height == boundary - 1 for height in heights):
                # The block RPC may lag until Comet persists the signed boundary.
                for node in replicas:
                    node.rpc(f'block?height={boundary}')
                return committed_header(replicas, boundary)
        except OSError:
            pass
        except ValueError as error:
            if 'Comet RPC GET /block' not in str(error):
                raise
        time.sleep(0.25)
    raise TimeoutError('successors did not reach the final signed boundary')


def checked_abci_state(node):
    info = node.rpc('abci_info')['response']
    digest = base64.b64decode(info['last_block_app_hash'], validate=True).hex().upper()
    check(re.fullmatch(r'[A-F0-9]{64}', digest), 'invalid persisted application hash')
    return int(info['last_block_height']), digest


def check_block_absent(node, boundary):
    status = node.rpc('status')
    check(status['node_info']['network'] == CHAIN and int(status['validator_info']['voting_power']) == 0,
          'expected a non-voting node on the isolated chain')
    tip = int(node.rpc('block')['block']['header']['height'])
    check(int(status['sync_info']['latest_block_height']) <= tip < boundary,
          'local blockstore already has the final signed block')
    # JSON-RPC POST preserves the structured application error in an HTTP 200
    # response. The URI handler turns the same error into HTTP 500.
    query = {'jsonrpc': '2.0', 'id': 1, 'method': 'block', 'params': [str(boundary)]}
    result, _ = request(f'http://127.0.0.1:{node.p2p + 1}', query)
    error = result.get('error', {})
    message = re.fullmatch(f'height {boundary} must be less than or equal to the current blockchain height ([0-9]+)',
                           str(error.get('data', '')))
    check(result.get('id') == 1 and 'result' not in result and error.get('code') == -32603 and
          message and tip <= int(message[1]) < boundary, 'Comet RPC did not reject the missing final block as out of range')
    return int(message[1])


def start_bounded_witness(replacement, root, source, index, genesis, env, owner, boundary, report):
    from native_launch_systemd import copy_offline_home

    bounded = create_replica(replacement, root, 'bounded-rollback-witness', index, genesis, None, env)
    write_config(bounded.home, 'config.toml', {
        ('p2p', 'pex'): 'false', ('p2p', 'allow_duplicate_ip'): 'true',
    })
    for database in ('application.db', 'blockstore.db', 'state.db'):
        copy_offline_home(source.home / 'data' / database, bounded.home / 'data' / database)
    run_command(replacement, bounded.home, ['rollback', '--hard', '--chain-id', CHAIN,
                                             '--evm.evm-chain-id', str(EVM_CHAIN)], env)
    bounded.start(replacement, env, owner, 'serve-prefix-without-final-block')
    wait_replicas([bounded], boundary - 1, 60, report)
    check(check_block_absent(bounded, boundary) == boundary - 1 and
          checked_abci_state(bounded) == (boundary - 1, report['signingBoundary']['app_hash']),
          'bounded witness must retain the canonical prefix without the final signed block')
    return bounded


def resume_validator_signing(fleet, replicas, witness, expected, env, owner, report, watermarks, boundary, probe,
                             *, bounded=None):
    from native_launch_systemd import copy_offline_home, home_digest, home_records

    report['phase'] = 'replay-last-signed-block-without-validator-keys'
    wait_replicas(replicas, boundary - (3 if bounded else 2), 100, report)
    witness_state = checked_abci_state(witness)
    check(witness_state[0] == boundary, 'canonical witness did not persist the final signed height')
    # Block sync can hand off two heights behind the frozen witness. Consensus
    # gossip can also deliver the final signed block before we close a replica.
    first = boundary - (3 if bounded else 2)
    canonical_prefix = {height: committed_header([witness], height + 1)['app_hash']
                        for height in range(first, boundary)}
    if not bounded:
        canonical_prefix[boundary] = witness_state[1]
    canonical_hashes = {**canonical_prefix, boundary: witness_state[1]}
    absence = {}
    for node in replicas:
        height, app_hash = checked_abci_state(node)
        check(canonical_prefix.get(height) == app_hash,
              'restored follower did not reach the canonical application state near the last signed block')
        if bounded:
            absence[node.label] = check_block_absent(node, boundary)
    signed_boundary = witness.rpc(f'commit?height={boundary}')['signed_header']
    check(int(signed_boundary['commit']['height']) == boundary and signed_boundary['header']['chain_id'] == CHAIN and
          signed_boundary['commit']['block_id']['hash'] == report['signingBoundary']['block_hash'],
          'witness did not preserve the last signed block')
    old_commit = signed_boundary['commit']
    boundary_signers = {signature['validator_address'] for signature in old_commit['signatures']
                        if int(signature['block_id_flag']) == 2 and signature['signature']}
    check(len(boundary_signers) >= 3 and all(
        (row['height'], row['round'], row['step']) ==
        (boundary, int(old_commit['round']), 3) for row in watermarks.values()
        if row['consensusAddress'] in boundary_signers), 'retained signer state does not cover the last canonical commit')

    for node in reversed(replicas):
        fleet.stop(node.child)
    # Once all restored services are closed, hand off the validated Comet stores.
    # Each restored application.db remains in place and executes the final block
    # through Comet's startup replay before its original key may be reinstalled.
    report['phase'] = 'install-closed-canonical-comet-stores'
    if bounded:
        owner.stop(bounded.child)
        check(bounded.child.returncode == 0, 'bounded witness did not close its databases')
    owner.stop(witness.child)
    check(witness.child.returncode == 0, 'canonical witness did not close its databases')
    application_before = {}
    for node in replicas:
        if bounded:
            application_db = node.home / 'data' / 'application.db'
            application_before[node.label] = home_digest(home_records(application_db))
            write_config(node.home, 'config.toml', {
                ('p2p', 'persistent_peers'): '""', ('p2p', 'seeds'): '""',
            })
        for database in ('blockstore.db', 'state.db'):
            current = node.home / 'data' / database
            current.rename(node.home / f'before-last-block-{database}')
            copy_offline_home(witness.home / 'data' / database, current)
        if bounded:
            app_db_at_startup = home_digest(home_records(application_db))
            check(app_db_at_startup == application_before[node.label],
                  'installing Comet stores modified the restored application database')
            application_before[node.label] = (application_before[node.label], app_db_at_startup)
        node.start(fleet.binary_for(node.home), env, fleet, 'replay-last-signed-block')
    wait_replicas(replicas, boundary, 70, report)
    replayed = {}
    handshake_heights = {}
    replay_proof = {}
    for node in replicas:
        check(checked_abci_state(node) == witness_state and int(node.rpc('status')['validator_info']['voting_power']) == 0,
              'restored service did not replay and verify the last signed block while non-voting')
        (handshake,) = log_records(node.log, 'ABCI Handshake App Info')
        handshake_height = int(handshake['height'])
        check(canonical_prefix.get(handshake_height) == handshake['hash'].upper(),
              'last signed block replay did not open the previously verified restored application')
        handshake_heights[node.label] = handshake_height
        replayed[node.home] = witness_state[0]
        if bounded:
            (startup,) = log_records(node.log, 'ABCI Replay Blocks')
            executed = log_records(node.log, 'executed block')
            expected_heights = list(range(handshake_height + 1, boundary + 1))
            peers = int(node.rpc('net_info')['n_peers'])
            check((int(startup['appHeight']), int(startup['storeHeight']), int(startup['stateHeight'])) ==
                  (handshake_height, boundary, boundary) and expected_heights and
                  [int(row['height']) for row in executed] == expected_heights and
                  all(row['app_hash'].upper() == canonical_hashes[height]
                      for row, height in zip(executed, expected_heights, strict=True)) and peers == 0 and
                  committed_header([node], boundary) == report['signingBoundary'],
                  'startup did not execute the missing final block without P2P')
            before, at_startup = application_before[node.label]
            replay_proof[node.label] = {
                'blockstoreHeightBefore': absence[node.label], 'missingBlockHeight': boundary,
                'applicationHeightBefore': handshake_height, 'applicationAppHashBefore': handshake['hash'].upper(),
                'applicationDbSha256Before': before, 'applicationDbSha256AtStartup': at_startup,
                'executedHeights': expected_heights, 'committedHeight': witness_state[0],
                'appHash': witness_state[1], 'peersAtStartup': peers,
            }
    if bounded:
        report['forcedStartupReplay'] = {
            'height': boundary, 'blockHash': report['signingBoundary']['block_hash'], 'appHash': witness_state[1],
            'boundedWitnessHeight': boundary - 1, 'replicas': replay_proof,
        }
    for node in reversed(replicas):
        fleet.stop(node.child)
    retained = fleet.reactivate_signers(replayed)
    check(retained == watermarks, 'restored signer high-watermarks differ from the closed primary fleet')
    report['signingResumption'] = {'watermarks': watermarks, 'verifiedBeforeSigningHeight': boundary,
                                   'verifiedBeforeSigningAppHash': witness_state[1],
                                   'nonVotingHandshakeHeights': handshake_heights,
                                   'retainedOriginalState': True}
    report['phase'] = 'resume-original-validators-after-verified-replay'
    for node in replicas:
        write_config(node.home, 'config.toml', {
            ('p2p', 'persistent_peers'): json.dumps(','.join(f'{peer.node_id}@127.0.0.1:{peer.p2p}' for peer in replicas if peer is not node)),
        })
        node.start(fleet.binary_for(node.home), env, fleet, 'resume-original-signing')
    wait_replicas(replicas, boundary + 5, 90, report)
    check(committed_header(replicas, boundary) == report['signingBoundary'] and
          canonical_commit(replicas, boundary) == signed_boundary['commit'] and
          committed_header(replicas, boundary + 1)['app_hash'] == witness_state[1],
          'resumed validators changed the prior signed boundary or its application hash')
    signatures = find_four_signature_commit(replicas, expected['validators'], probe, boundary + 1, boundary + 12, report)
    new_height = signatures['height']
    final = committed_header(replicas, new_height + 1)
    powers = effective_set(replicas, expected['validators'], new_height, FOUR_UPGRADE_POWERS)
    check(new_height > max(row['height'] for row in watermarks.values()) and
          all(not node.rpc('status')['sync_info']['catching_up'] for node in replicas),
          'fresh commit must follow every old signature and all validators must be synchronized')
    report['signingResumption'].update(verifiedHeight=new_height, appHash=final['app_hash'], powers=powers,
                                       commit=signatures, sameSignedHistory=True, services=fleet.evidence())


def restore_validator_checkpoint(fleet, replicas, replacement, root, genesis, expected, action, env, owner, report,
                                 *, signing_boundary=None, commit_probe=None, force_startup_replay=False):
    from native_launch_systemd import copy_offline_home

    target = action['planHeight']
    final = report['progress']['height']
    report['phase'] = 'stop-all-signers-and-retain-canonical-signed-blocks'
    # A block-sync peer needs the following header to validate each block.
    # Leave enough history above the shared verification height.
    wait_replicas(replicas, final + 3, 40, report)
    primary_final = committed_header(replicas, final + 1)
    report['coinMigration']['verifiedHeight'] = final
    report['coinMigration']['verifiedAppHash'] = primary_final['app_hash']
    if signing_boundary is not None:
        report['phase'] = 'primary-signers-final-signed-boundary'
        report['signingBoundary'] = wait_signing_boundary(replicas, signing_boundary, report)
    for node in reversed(replicas):
        fleet.stop(node.child)
    check(all(node.child.poll() is not None for node in replicas), 'a primary signer is still running')
    if signing_boundary is not None:
        watermarks = fleet.capture_signing_watermarks()
        check(max(row['height'] for row in watermarks.values()) == signing_boundary,
              'primary validators have an unexpected signing high-watermark')

    bounded = None
    if force_startup_replay:
        report['phase'] = 'prepare-witness-without-final-signed-block'
        bounded = start_bounded_witness(replacement, root, replicas[0], len(replicas) + 1, genesis,
                                        env, owner, signing_boundary, report)
    witness = create_replica(replacement, root, 'rollback-witness', len(replicas), genesis, None, env)
    write_config(witness.home, 'config.toml', {('p2p', 'pex'): 'false', ('p2p', 'allow_duplicate_ip'): 'true'})
    # Copy only the closed application and Comet stores. The new witness keeps
    # its own identities and never opens a validator's WAL or signing state.
    for database in ('application.db', 'blockstore.db', 'state.db'):
        copy_offline_home(replicas[0].home / 'data' / database, witness.home / 'data' / database)
    witness.start(replacement, env, owner, 'retain-canonical-blocks')
    wait_replicas([witness], signing_boundary if signing_boundary is not None else final + 3, 60, report)
    check(committed_header([witness], final + 1) == primary_final and
          int(witness.rpc('status')['validator_info']['voting_power']) == 0,
          'rollback witness must retain the canonical history without voting')

    report['phase'] = 'restore-closed-validator-homes'
    restored_hashes = fleet.restore()
    check(restored_hashes == report['systemd']['backupHomeSha256'], 'restored homes differ from closed backups')
    replay_peer = bounded or witness
    for node in replicas:
        # Replay on restored copies without the original signing identities.
        keys = create_replica(replacement, root, f'nonvoting-{node.label}', node.index, genesis, None, env)
        for relative in ('config/priv_validator_key.json', 'data/priv_validator_state.json'):
            private_write(node.home / relative, private_bytes(keys.home / relative, 8192))
        write_config(node.home, 'config.toml', {
            ('p2p', 'persistent_peers'): json.dumps(f'{replay_peer.node_id}@127.0.0.1:{replay_peer.p2p}'),
        })
    fleet.replace_binaries(replacement, report['replacementSha256'])
    report['phase'] = 'replay-governed-migration-from-restored-homes'
    for node in replicas:
        node.start(fleet.binary_for(node.home), env, fleet, 'restore-replay')
    wait_replicas([*replicas, witness], final + 1, 125, report)
    restored_app_hash = report['predecessor']['boundary']['app_hash']
    for node in replicas:
        (handshake,) = log_records(node.log, 'ABCI Handshake App Info')
        check((int(handshake['height']), handshake['hash'].upper()) == (target - 1, restored_app_hash.upper()),
              'restored service did not load the exact pre-upgrade application checkpoint')
    deadline = time.monotonic() + 25
    while True:
        statuses = [node.rpc('status') for node in replicas]
        check(all(status['node_info']['id'] == node.node_id and int(status['validator_info']['voting_power']) == 0
                  for node, status in zip(replicas, statuses, strict=True)), 'restored services must use fresh non-voting signing identities')
        if all(not status['sync_info']['catching_up'] for status in statuses):
            break
        check(time.monotonic() < deadline, 'restored services did not finish synchronization')
        time.sleep(0.25)
    participants = [*replicas, witness]
    check(committed_header(participants, target) == report['predecessor']['boundary'],
          'restored checkpoint changed the canonical upgrade block')
    migration_app_hash = committed_header(participants, target + 1)['app_hash']
    check(migration_app_hash == report['coinMigration']['appHash'], 'restored migration produced a different AppHash')
    final_header = committed_header(participants, final + 1)
    check(final_header == primary_final, 'restored services did not replay the original signed history')
    check_schedule(replicas, action, target - 1)
    for node in replicas:
        check(applied_height(node, target) == target and current_plan(node, target) is None,
              'restored service did not reapply the exact approved migration')
    report['rollback'] = {'restoredHomeSha256': restored_hashes, 'restoredHeight': target - 1,
                          'restoredAppHash': restored_app_hash,
                          'replayedHeight': target, 'migrationAppHash': migration_app_hash,
                          'verifiedHeight': final, 'appHash': final_header['app_hash'],
                          'primaryAppHashAtVerifiedHeight': primary_final['app_hash'], 'catchingUp': False,
                          'sameCanonicalHistory': True, 'votingPower': 0, 'witnessVotingPower': 0,
                          'coinBefore': check_coin_module_migration(replicas, genesis, target - 1, 1),
                          'coinAfter': check_coin_module_migration(replicas, genesis, target, 2),
                          'services': fleet.evidence()}
    report['rollback']['pendingWithdrawal'] = check_pending_withdrawal(replicas, expected, final, report['withdrawal']['before']['dueAt'])
    check(report['rollback']['coinBefore'] == report['coinMigration']['before'] and
          report['rollback']['coinAfter'] == report['coinMigration']['after'],
          'restored module records differ before or after replaying the migration')
    if signing_boundary is not None:
        resume_validator_signing(fleet, replicas, witness, expected, env, owner, report, watermarks,
                                 signing_boundary, commit_probe, bounded=bounded)


def run_gate(args, binary, replacement, root, expected, genesis, keys, actions, env, owner, report, ibc=None, fleet=None):
    report['phase'] = 'create-replicas'
    replicas = create_replicas(fleet.root_binary if fleet else binary, fleet.root if fleet else root, genesis, keys, env,
                              full_mesh=fleet is not None)
    source = replicas[0]
    initial = report['initialHeight']
    if ibc:
        report['phase'] = 'fund-native-relayer'
        ibc.fund_relayer(binary, replicas, expected, genesis, initial)
    for executable, revision in ((binary, report['predecessorSourceCommit']), (replacement, report['replacementSourceCommit'])):
        version = json.loads(run_command(executable, source.home, ['version', '--long', '--output', 'json'], env))
        check(version['server_name'] == 'mcnd' and version['commit'] == revision, 'daemon version differs from its checked source')
    report['phase'] = 'validate-genesis'
    try:
        run_command(binary, source.home, ['genesis', 'validate-genesis'], env)
    except RuntimeError as error:
        raise RuntimeError('upgrade governance genesis rejected: ' + safe_diagnostic(str(error))) from None

    report['phase'] = 'initial-native-consensus'
    for node in replicas:
        node.start(fleet.install(node.home) if fleet else binary, env, fleet or owner, 'upgrade-governance')
    first = wait_replicas(replicas, initial + 2, 85, report)
    initial_powers = FOUR_UPGRADE_POWERS if args.resume_signing else INITIAL
    report['initialPower'] = effective_set(replicas, expected['validators'], initial, initial_powers)
    if args.resume_signing:
        report['initialCommit'] = find_four_signature_commit(
            replicas, expected['validators'], args.commit_probe, initial, initial + 6, report)
        report['initialSigners'] = report['initialCommit']['signers']
    else:
        report['initialSigners'] = check_signatures(replicas, expected['validators'], initial, initial_powers)
    action = actions[0]
    account = native('/cosmos/auth/v1beta1/accounts/' + action['actorNative'], first, base=source.rest)['account']
    check(account['address'] == action['actorNative'] and int(account['sequence']) == action['sequence'] and
          int(account['account_number']) == action['accountNumber'], 'fixture signed Cosmos account did not survive genesis import')
    for node in replicas:
        check(current_plan(node, first) is None and applied_height(node, first) == 0, 'unexpected upgrade before any proposal')

    if ibc:
        report['phase'] = 'hermes-handshake-and-pending-packet'
        ibc.start_packet(binary)
        action = select_pending_plan(source, actions, report)

    for accepted, label in ((False, 'rejected'), (True, 'passed')):
        report['phase'] = label + '-signed-proposal'
        proposal_tx = submit_governance_tx(source, action[label + 'Proposal'], label + ' proposal', report)
        proposal_id = action[label + 'ID']
        wait_replicas(replicas, proposal_tx['height'], 40, report)
        for node in replicas:
            proposal = query_proposal(node, proposal_id, proposal_tx['height'])
            check(proposal['status'] == VOTING and proposal['metadata'] == action['metadata'] and
                  current_plan(node, proposal_tx['height']) is None, 'proposal scheduled an action before its vote')
        report['phase'] = label + '-signed-vote'
        vote_tx = submit_governance_tx(source, action[label + 'Vote'], label + ' vote', report)
        check(vote_tx['height'] > proposal_tx['height'], 'vote must be committed after its proposal')
        tally_height = wait_tally(source, proposal_id, vote_tx['height'], report)
        wait_replicas(replicas, tally_height, 40, report)
        weight = str(sum(initial_powers) * ATOMS) if args.resume_signing else VOTING_WEIGHT
        report[label] = {**check_proposal(replicas, proposal_id, tally_height, action['metadata'], accepted, weight),
                         'proposalTx': proposal_tx, 'voteTx': vote_tx}
        if not accepted:
            for node in replicas:
                check(current_plan(node, tally_height) is None and applied_height(node, tally_height) == 0,
                      'rejected native proposal changed SDK upgrade state')

    report['phase'] = 'exact-schedule'
    target = action['planHeight']
    check(tally_height < target, 'passed tally arrived after the fixture upgrade height')
    schedule_blocks = [height for height in range(vote_tx['height'], tally_height + 1) if schedule_event(source, height)]
    check(len(schedule_blocks) == 1, 'expected exactly one scheduling event after the approving vote')
    scheduled_at = schedule_blocks[0]
    check(scheduled_at < target, 'upgrade was not scheduled for a strictly future height')
    for node in replicas:
        check(schedule_event(node, scheduled_at) == {'proposal_id': str(proposal_id), 'name': PLAN, 'height': str(target)},
              'schedule event does not bind the exact approved proposal and upgrade height')
        check(current_plan(node, scheduled_at - 1) is None, 'plan was present before the passed tally')
    check_schedule(replicas, action, scheduled_at)
    report['schedule'] = {'proposalID': proposal_id, 'name': PLAN, 'scheduledAt': scheduled_at, 'targetHeight': target}

    if args.full_profile:
        report['phase'] = 'predecessor-signed-withdrawal'
        withdrawn_at, withdrawal_hash = withdraw_for_handoff(source, expected)
        check(tally_height < withdrawn_at and withdrawn_at + 2 <= target - 1, 'withdrawal missed the predecessor boundary')
        header = source.rpc(f'block?height={withdrawn_at}')['block']['header']
        due = int(datetime.fromisoformat(header['time'].replace('Z', '+00:00')).timestamp()) + UNBONDING_SECONDS
        report['withdrawal'] = {'height': withdrawn_at, 'txHash': withdrawal_hash, 'unbondingSeconds': UNBONDING_SECONDS}

    report['phase'] = 'predecessor-halt-at-upgrade'
    wait_predecessor_halt(replicas, action, report)
    check_schedule(replicas, action, target - 1)
    before = committed_header(replicas, target - 1)
    boundary = committed_header(replicas, target)
    report['predecessor'] = {'committedHeight': target - 1, 'boundaryHeight': target, 'boundary': boundary}
    if fleet:
        report['coinMigration'] = {'before': check_coin_module_migration(replicas, genesis, target - 1, 1)}
        report['systemd'] = {'predecessors': fleet.evidence()}
    if args.full_profile:
        report['withdrawal']['before'] = check_pending_withdrawal(replicas, expected, target - 1, due)
    if ibc:
        ibc.mark_checkpoint()
    for node in reversed(replicas):
        (fleet or owner).stop(node.child)
        check(node.child.poll() is not None, 'predecessor is still running')
    if fleet:
        report['phase'] = 'backup-closed-pre-upgrade-homes'
        report['systemd']['backupHomeSha256'] = fleet.checkpoint()
        fleet.replace_binaries(replacement, report['replacementSha256'])
    if ibc:
        report['phase'] = 'backup-closed-pre-upgrade-homes'
        ibc.backup_closed_homes()
        ibc.start_reference('reference-primary-branch')

    report['phase'] = 'successor-replay-and-exact-preblock-application'
    signing_boundary = target + 30 if args.resume_signing else None
    for node in replicas:
        if signing_boundary is not None:
            write_config(node.home, 'app.toml', {(None, 'halt-height'): str(signing_boundary)})
        node.start(fleet.binary_for(node.home) if fleet else replacement, env, fleet or owner, 'upgrade-successor')
    # Observe continuing full-profile consensus past the earlier CI frontier.
    tip = wait_replicas(replicas, target + (16 if args.full_profile else 4), 125, report)
    final = tip - 1
    check(committed_header(replicas, target - 1) == before and committed_header(replicas, target) == boundary,
          'successor changed the predecessor checkpoint or signed boundary block')
    check_schedule(replicas, action, target - 1)
    for node in replicas:
        check(applied_height(node, target) == target and current_plan(node, target) is None and
              applied_height(node, final) == target and current_plan(node, final) is None,
              'SDK handler did not finish at its exact PreBlock height')
    check(all(node.child.poll() is None for node in replicas), 'one successor did not remain running')
    powers = INITIAL
    if args.full_profile:
        report['withdrawal']['after'] = check_pending_withdrawal(replicas, expected, final, due)
        powers = (1, 1, 0)
    if args.resume_signing:
        powers = FOUR_UPGRADE_POWERS
        report['successorCommit'] = find_four_signature_commit(
            replicas, expected['validators'], args.commit_probe, target + 1, final, report)
        signers = report['successorCommit']['signers']
    else:
        signers = check_signatures(replicas, expected['validators'], final, powers)
    report['application'] = {'name': PLAN, 'appliedHeight': target, 'sameBinary': False, 'processRestarted': True,
                             'before': before, 'block': boundary}
    report['progress'] = {'height': final, 'powers': effective_set(replicas, expected['validators'], final, powers),
                          'signers': signers,
                          **committed_header(replicas, final)}
    if fleet:
        report['systemd']['successors'] = fleet.evidence()
        report['progress']['stateAppHash'] = committed_header(replicas, final + 1)['app_hash']
        report['coinMigration'].update(after=check_coin_module_migration(replicas, genesis, target, 2),
                                       appHash=committed_header(replicas, target + 1)['app_hash'])
        check(report['coinMigration']['before']['coinRecordSha256'] == report['coinMigration']['after']['coinRecordSha256'],
              'DRC20 address index migration changed the indexed coin record')
        restore_validator_checkpoint(fleet, replicas, replacement, root, genesis, expected, action, env, owner, report,
                                     signing_boundary=signing_boundary, commit_probe=args.commit_probe,
                                     force_startup_replay=args.force_startup_replay)
    if ibc:
        applied_app_hash = committed_header(replicas, target + 1)['app_hash']
        report['phase'] = 'hermes-post-upgrade-acknowledgement-and-return'
        ibc.settle_after_handoff(target)
        report['phase'] = 'restore-closed-private-checkpoint'
        for node in reversed(replicas):
            owner.stop(node.child)
        restored = ibc.restore_closed_homes(replicas)
        restored_replicas = [replace(node, home=restored / node.label, child=None, log=None) for node in replicas]
        for node in restored_replicas:
            node.start(replacement, env, owner, 'upgrade-restored-replay')
        ibc.start_reference('reference-restored-copy')
        wait_replicas(restored_replicas, target + 2, 125, report)
        check(committed_header(restored_replicas, target - 1) == before and
              committed_header(restored_replicas, target) == boundary, 'restored native copy changed the signed pre-upgrade boundary')
        for node in restored_replicas:
            check(applied_height(node, target) == target and current_plan(node, target) is None,
                  'restored native copy did not replay the approved plan')
        restored_claim = check_pending_withdrawal(restored_replicas, expected, target, due)
        check(restored_claim['appHash'] == applied_app_hash, 'restored replay produced a different application hash')
        report['rollback'] = {**ibc.verify_restored_ledger(restored_replicas), 'predecessorHeight': target - 1,
                              'replayedHeight': target, 'boundaryBlockHash': boundary['block_hash'],
                              'primaryAppliedAppHash': applied_app_hash, 'restoredClaim': restored_claim}


def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('binary', 'binary-sha256', 'source-commit', 'fixture', 'home-parent', 'report'):
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--replacement-binary', required=True)
    parser.add_argument('--replacement-sha256', required=True)
    parser.add_argument('--predecessor-source-commit',
                        help='Require a distinct predecessor source revision. --source-commit identifies the replacement.')
    parser.add_argument('--full-profile', action='store_true', help='Use the fresh historical, IBC and fee-enabled fixture with a pending DEL claim')
    parser.add_argument('--pending-ibc', action='store_true', help='Carry a real ICS-20 packet across the private native handoff')
    parser.add_argument('--systemd-migration', action='store_true', help='Run and restore the coordinated module migration in systemd')
    parser.add_argument('--resume-signing', action='store_true', help='Resume the four original signers after verifying the restored state')
    parser.add_argument('--force-startup-replay', action='store_true', help='Require a missing block to execute at startup on all four restored nodes')
    parser.add_argument('--commit-probe', type=pathlib.Path, help='Canonical Comet Ed25519 commit verifier')
    for name in ('reference-binary', 'reference-sha256', 'hermes-binary'):
        parser.add_argument('--' + name)
    args = parser.parse_args(argv)
    if args.systemd_migration and (not args.full_profile or args.pending_ibc):
        parser.error('--systemd-migration requires --full-profile without --pending-ibc')
    if args.resume_signing != bool(args.commit_probe) or (args.resume_signing and not args.systemd_migration):
        parser.error('--resume-signing and --commit-probe require --systemd-migration and each other')
    if args.force_startup_replay and not args.resume_signing:
        parser.error('--force-startup-replay requires --resume-signing')
    report = {'status': 'FAIL', 'phase': 'fixture-provenance',
              'scope': 'three loopback replicas across a native governance SDK binary upgrade'}
    if args.resume_signing:
        report['scope'] = 'four loopback validators across a native governance SDK binary upgrade and rollback'
    with pathlib.Path(args.report).open('x', encoding='utf8') as stream:
        try:
            private_namespace()
            check(re.fullmatch(SHA256, args.binary_sha256) and re.fullmatch(SHA256, args.replacement_sha256) and
                  args.binary_sha256 != args.replacement_sha256 and re.fullmatch(r'[0-9a-f]{40}', args.source_commit),
                  'invalid predecessor or successor build provenance')
            predecessor_commit = args.source_commit
            plan_kind = 'same-source-handler-rehearsal'
            if args.predecessor_source_commit is not None:
                predecessor_commit = args.predecessor_source_commit
                check(args.pending_ibc and re.fullmatch(r'[0-9a-f]{40}', predecessor_commit) and predecessor_commit != args.source_commit,
                      'cross-version handoff requires the pending IBC profile and distinct full source revisions')
                plan_kind = 'distinct-source-same-family-handler-rehearsal'
            if args.systemd_migration:
                plan_kind = 'systemd-coordinated-module-migration'
            binary = str(pathlib.Path(args.binary).resolve(strict=True))
            replacement = str(pathlib.Path(args.replacement_binary).resolve(strict=True))
            for executable, expected_hash in ((binary, args.binary_sha256), (replacement, args.replacement_sha256)):
                with open(executable, 'rb') as binary_stream:
                    check(hashlib.file_digest(binary_stream, 'sha256').hexdigest() == expected_hash, 'daemon SHA256 mismatch')
            check(not args.pending_ibc or (args.full_profile and args.reference_binary and args.reference_sha256 and args.hermes_binary),
                  'pending IBC mode requires the full profile and both IBC tools')
            expected, genesis, keys, initial, actions = load_governance_fixture(
                pathlib.Path(args.fixture), full_profile=args.full_profile, pending_ibc=args.pending_ibc,
                four_upgrade=args.resume_signing)
            report.update(sourceCommit=args.source_commit, predecessorSourceCommit=predecessor_commit,
                          replacementSourceCommit=args.source_commit, binarySha256=args.binary_sha256,
                          replacementSha256=args.replacement_sha256, initialHeight=initial,
                          genesisSha256=hashlib.sha256(genesis).hexdigest(), chainID=CHAIN, evmChainID=EVM_CHAIN,
                          fullProfile=args.full_profile, planKind=plan_kind)
            if args.full_profile:
                report['ibcCoverage'] = 'pending-packet-after-client-update' if args.pending_ibc else 'unbonding-state-only'
            with tempfile.TemporaryDirectory(prefix='mcnd-native-governance-upgrade-', dir=args.home_parent) as directory:
                root = pathlib.Path(directory)
                owner = ProcessOwner(signals)
                env = private_environment(root)
                ibc = None
                try:
                    if args.pending_ibc:
                        from native_upgrade_ibc import NativeUpgradeIBC
                        ibc = NativeUpgradeIBC(args, root, env, report, signals)
                    if args.systemd_migration:
                        from native_launch_systemd import SystemdValidatorFleet

                        with SystemdValidatorFleet(binary, args.binary_sha256, signals=signals) as fleet:
                            run_gate(args, binary, replacement, root, expected, genesis, keys, actions, env, owner, report, fleet=fleet)
                    else:
                        run_gate(args, binary, replacement, root, expected, genesis, keys, actions, env, owner, report, ibc)
                finally:
                    try:
                        if ibc:
                            ibc.lab.close()
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
