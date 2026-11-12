"""Rehearse one native governance SDK migration across two binaries."""

import argparse
import base64
from datetime import datetime
import hashlib
import json
import pathlib
import re
import tempfile
import time

from del_delegation_smoke import GAS_LIMIT, GAS_PRICE, balance, native_positions, read_frozen, read_words
from mcnd_process_smoke import ProcessOwner, SignalGuard, private_environment, private_namespace, run_command
from native_consensus_smoke import ATOMS, INITIAL, check_signatures, committed_header, create_replicas, effective_set, load_fixture, wait_replicas
from withdrawal_genesis_smoke import CHAIN, EVM_CHAIN, MAX_BLOCKS, SHA256, WORD, check, ethereum, native, private_bytes, readiness_diagnostic, request, safe_diagnostic


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


def load_governance_fixture(directory, *, full_profile=False, pending_ibc=False):
    expected, genesis, keys, initial = load_fixture(
        directory, fee_rewards=full_profile, downtime=False, jail_recovery=False, held_downtime=False,
        nft_downtime=False, full_upgrade=full_profile)
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
              int(claim['activeAtomic']) == 7 * ATOMS // 4 and int(claim['pendingAtomic']) == ATOMS // 4 and
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


def check_proposal(replicas, proposal_id, height, manifest, accepted):
    status = 'PROPOSAL_STATUS_PASSED' if accepted else 'PROPOSAL_STATUS_REJECTED'
    expected = {'yes_count': VOTING_WEIGHT if accepted else '0', 'no_count': '0' if accepted else VOTING_WEIGHT,
                'abstain_count': '0', 'no_with_veto_count': '0'}
    for node in replicas:
        proposal = query_proposal(node, proposal_id, height)
        check(int(proposal['id']) == proposal_id and proposal['status'] == status and
              proposal['metadata'] == manifest and not proposal.get('messages'), 'unexpected native proposal identity or outcome')
        tally = proposal['final_tally_result']
        check({key: str(tally.get(key, '0')) for key in expected} == expected, 'final tally differs from the complete native DEL ownership')
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
        check(reserve == active and supply == escrow == backing == 3 * ATOMS,
              'Gateway reserve or exact DEL backing differs across the binary handoff')
        bank_supply = native('/cosmos/bank/v1beta1/supply/by_denom?denom=del', height, base=node.rest)['amount']
        check(bank_supply['denom'] == 'del' and int(bank_supply['amount']) >= backing, 'invalid native DEL supply')
        native_supply.add(bank_supply['amount'])
    check(len(native_supply) == 1, 'replicas disagree on native DEL supply')
    return {'height': height, 'pendingAtomic': str(pending), 'activeAtomic': str(active), 'dueAt': due,
            'wrappedBackingAtomic': str(3 * ATOMS), 'nativeSupplyAtomic': native_supply.pop(),
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


def run_gate(args, binary, replacement, root, expected, genesis, keys, actions, env, owner, report, ibc=None):
    report['phase'] = 'create-replicas'
    replicas = create_replicas(binary, root, genesis, keys, env)
    source = replicas[0]
    initial = report['initialHeight']
    if ibc:
        report['phase'] = 'fund-native-relayer'
        ibc.fund_relayer(binary, replicas, expected, genesis, initial)
    for executable in (binary, replacement):
        version = json.loads(run_command(executable, source.home, ['version', '--long', '--output', 'json'], env))
        check(version['server_name'] == 'mcnd' and version['commit'] == args.source_commit, 'daemon version differs from checked source')
    report['phase'] = 'validate-genesis'
    try:
        run_command(binary, source.home, ['genesis', 'validate-genesis'], env)
    except RuntimeError as error:
        raise RuntimeError('upgrade governance genesis rejected: ' + safe_diagnostic(str(error))) from None

    report['phase'] = 'initial-native-consensus'
    for node in replicas:
        node.start(binary, env, owner, 'upgrade-governance')
    first = wait_replicas(replicas, initial + 2, 85, report)
    report['initialPower'] = effective_set(replicas, expected['validators'], initial, INITIAL)
    report['initialSigners'] = check_signatures(replicas, expected['validators'], initial, INITIAL)
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
        report[label] = {**check_proposal(replicas, proposal_id, tally_height, action['metadata'], accepted),
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
    if args.full_profile:
        report['withdrawal']['before'] = check_pending_withdrawal(replicas, expected, target - 1, due)
    if ibc:
        ibc.assert_pending()
    for node in reversed(replicas):
        owner.stop(node.child)
        check(node.child.poll() is not None, 'predecessor is still running')

    report['phase'] = 'successor-replay-and-exact-preblock-application'
    for node in replicas:
        node.start(replacement, env, owner, 'upgrade-successor')
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
    report['application'] = {'name': PLAN, 'appliedHeight': target, 'sameBinary': False, 'processRestarted': True,
                             'before': before, 'block': boundary}
    report['progress'] = {'height': final, 'powers': effective_set(replicas, expected['validators'], final, powers),
                          'signers': check_signatures(replicas, expected['validators'], final, powers),
                          **committed_header(replicas, final)}
    if ibc:
        report['phase'] = 'hermes-post-upgrade-acknowledgement-and-return'
        ibc.settle_after_handoff(target)


def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('binary', 'binary-sha256', 'source-commit', 'fixture', 'home-parent', 'report'):
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--replacement-binary', required=True)
    parser.add_argument('--replacement-sha256', required=True)
    parser.add_argument('--full-profile', action='store_true', help='Use the fresh historical, IBC and fee-enabled fixture with a pending DEL claim')
    parser.add_argument('--pending-ibc', action='store_true', help='Carry a real ICS-20 packet across the private native handoff')
    for name in ('reference-binary', 'reference-sha256', 'hermes-binary'):
        parser.add_argument('--' + name)
    args = parser.parse_args(argv)
    report = {'status': 'FAIL', 'phase': 'fixture-provenance',
              'scope': 'three loopback replicas across a native governance SDK binary upgrade'}
    with pathlib.Path(args.report).open('x', encoding='utf8') as stream:
        try:
            private_namespace()
            check(re.fullmatch(SHA256, args.binary_sha256) and re.fullmatch(SHA256, args.replacement_sha256) and
                  args.binary_sha256 != args.replacement_sha256 and re.fullmatch(r'[0-9a-f]{40}', args.source_commit),
                  'invalid predecessor or successor build provenance')
            binary = str(pathlib.Path(args.binary).resolve(strict=True))
            replacement = str(pathlib.Path(args.replacement_binary).resolve(strict=True))
            for executable, expected_hash in ((binary, args.binary_sha256), (replacement, args.replacement_sha256)):
                with open(executable, 'rb') as binary_stream:
                    check(hashlib.file_digest(binary_stream, 'sha256').hexdigest() == expected_hash, 'daemon SHA256 mismatch')
            check(not args.pending_ibc or (args.full_profile and args.reference_binary and args.reference_sha256 and args.hermes_binary),
                  'pending IBC mode requires the full profile and both IBC tools')
            expected, genesis, keys, initial, actions = load_governance_fixture(
                pathlib.Path(args.fixture), full_profile=args.full_profile, pending_ibc=args.pending_ibc)
            report.update(sourceCommit=args.source_commit, binarySha256=args.binary_sha256,
                          replacementSha256=args.replacement_sha256, initialHeight=initial,
                          genesisSha256=hashlib.sha256(genesis).hexdigest(), chainID=CHAIN, evmChainID=EVM_CHAIN,
                          fullProfile=args.full_profile, planKind='same-source-handler-rehearsal')
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
