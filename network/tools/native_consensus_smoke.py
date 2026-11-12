"""Verify native voting changes on three disposable mcnd processes."""

import argparse
import base64
from dataclasses import dataclass
from functools import partial
import hashlib
import json
import pathlib
import re
import stat
import tempfile
import time

from configure_history import validate_fresh_home
from del_delegation_smoke import DELEGATED, GAS_LIMIT, GAS_PRICE, balance, native_positions, read_words, submit, write_config
from mcnd_process_smoke import ProcessOwner, SignalGuard, private_environment, private_namespace, run_command
from withdrawal_genesis_smoke import ADDRESS, CHAIN, EVM_CHAIN, MAX_BLOCKS, SHA256, WORD, bech32_payload, check, comet, native, private_bytes, private_write, safe_diagnostic, wait_height


ATOMS = 10**18
INITIAL = (2, 1, 0)
AFTER = (2, 0, 3)
DOWNTIME_INITIAL = (3, 3, 2)
DOWNTIME_AFTER = (3, 3, 0)
DOWNTIME_BURN = ATOMS // 50
DOWNTIME_WINDOW = 3


@dataclass
class Replica:
    label: str
    index: int
    home: pathlib.Path
    node_id: str
    child: object = None
    log: pathlib.Path = None

    @property
    def p2p(self):
        return 28656 + self.index * 1000

    @property
    def rpc(self):
        return partial(comet, base=f'http://127.0.0.1:{self.p2p + 1}')

    @property
    def grpc(self):
        return 9190 + self.index * 100

    @property
    def rest(self):
        return f'http://127.0.0.1:{1317 + self.index * 10}'

    @property
    def evm(self):
        return f'http://127.0.0.1:{8645 + self.index * 100}'

    def start(self, binary, env, owner, label):
        evm = 8645 + self.index * 100
        args = [
            'start', '--home', str(self.home), '--chain-id', CHAIN, '--evm.evm-chain-id', str(EVM_CHAIN),
            '--rpc.laddr', f'tcp://127.0.0.1:{self.p2p + 1}',
            '--p2p.laddr', f'tcp://127.0.0.1:{self.p2p}', '--grpc.address', f'127.0.0.1:{self.grpc}',
            '--api.enable=true', '--grpc-web.enable=false', '--json-rpc.enable=true',
            '--json-rpc.address', f'127.0.0.1:{evm}', '--json-rpc.ws-address', f'127.0.0.1:{evm + 1}',
            '--log_format', 'json', '--log_level', 'info',
        ]
        self.log = self.home.parent / f'{self.label}-{label}.log'
        self.child = owner.spawn([binary, *args], self.log, env)


def decode_pubkey(record):
    # Comet JSON RPC encodes ordinary validators and protobuf updates using
    # different registered representations of the same Ed25519 public key.
    if isinstance(record, dict) and record.get('type') == 'tendermint.crypto.PublicKey':
        record = record['value']
    if isinstance(record, dict) and 'Sum' in record:
        record = record['Sum']
    if isinstance(record, dict) and record.get('type') in (
        'tendermint/PubKeyEd25519', 'tendermint.crypto.PublicKey_Ed25519',
    ):
        record = record['value']
    if isinstance(record, dict):
        record = record['ed25519']
    check(isinstance(record, str) and re.fullmatch(r'[A-Za-z0-9+/]{43}=', record), 'invalid fixture Ed25519 public key')
    value = base64.b64decode(record, validate=True)
    check(len(value) == 32, 'invalid fixture Ed25519 key length')
    return value


def load_fixture(directory, fee_rewards, downtime):
    info = directory.lstat()
    check(stat.S_ISDIR(info.st_mode) and stat.S_IMODE(info.st_mode) & 0o077 == 0, 'private fixture directory required')
    expected = json.loads(private_bytes(directory / 'expectations.json', 8192))
    check(expected['chainId'] == CHAIN and expected['evmChainId'] == EVM_CHAIN, 'fixture signing domain mismatch')
    for field in ('gateway', 'wrapped', 'actor', 'feeCollector'):
        check(re.fullmatch(ADDRESS, expected[field]), f'invalid fixture {field}')
        expected[field] = expected[field].lower()
    for field in ('actor', 'wrapped', 'feeCollector'):
        check(bech32_payload(expected[field + 'Native']) == expected[field], 'fixture bank and EVM addresses differ')
    check(len({expected[field] for field in ('gateway', 'wrapped', 'actor', 'feeCollector')}) == 4, 'duplicate fixture roles')
    check(re.fullmatch(WORD, expected['stakeId']), 'invalid fixture stake ID')
    if downtime:
        expected['master'] = expected['master'].lower()
        check(re.fullmatch(ADDRESS, expected['master']) and set(expected['masterStateData']) == set('abc') and
              all(re.fullmatch(r'0x[0-9a-f]{72}', data) for data in expected['masterStateData'].values()) and
              re.fullmatch(r'0x[0-9a-f]{72}', expected['stakePenaltyData']), 'invalid downtime fixture EVM data')
        check(not any(route in expected for route in ('delegate', 'topUp')), 'downtime fixture contains an unrelated transaction')
    else:
        check(expected['gasLimit'] == GAS_LIMIT and expected['gasPrice'] == str(GAS_PRICE) and
              (expected['amount'], expected['topUpAmount']) == (str(3 * ATOMS), str(ATOMS // 4)),
              'fixture transaction value or gas differs')
        check(type(expected['nonce']) is int and 0 <= expected['nonce'] < 2**32 - 1, 'invalid fixture nonce')
        for route in ('delegate', 'topUp'):
            signed = expected[route]
            check(re.fullmatch(WORD, signed['hash']) and re.fullmatch(r'0x(?:[0-9a-f]{2}){100,512}', signed['raw']),
                  'invalid signed DEL fixture transaction')
        check(expected['delegate']['hash'] != expected['topUp']['hash'], 'duplicate signed fixture transaction')
    for name, words in (('supplyData', 0), ('escrowData', 1)):
        check(re.fullmatch(fr'0x[0-9a-f]{{{8 + words * 64}}}', expected[name]), 'invalid fixture WDEL calldata')
    validators = expected['validators']
    initial, after = (DOWNTIME_INITIAL, DOWNTIME_AFTER) if downtime else (INITIAL, AFTER)
    check([row['name'] for row in validators] == ['a', 'b', 'c'] and
          tuple(row['initialPower'] for row in validators) == initial and
          tuple(row['afterStakePower'] for row in validators) == after, 'unexpected fixture initial or changed voting power')
    check(expected['feeRewards'] is fee_rewards, 'fixture fee rewards mode differs')
    keys, reward_addresses = [], set()
    for row in validators:
        row['reward'] = row['reward'].lower()
        check(re.fullmatch(ADDRESS, row['reward']) and bech32_payload(row['rewardNative']) == row['reward'],
              'invalid fixture reward recipient')
        reward_addresses.add(row['reward'])
        key = decode_pubkey(row['publicKey'])
        check(re.fullmatch(r'0x[0-9a-f]{136}', row['reserveData']), 'invalid validator reserve calldata')
        address = hashlib.sha256(key).hexdigest()[:40].upper()
        check(row['consensusAddress'] == address and row['evm'].lower() == '0x' + address.lower() and
              bech32_payload(row['operator'], 'd0valoper') == row['evm'].lower(), 'fixture validator identities differ')
        private = private_bytes(directory / f'priv_validator_key_{row["name"]}.json', 8192)
        key_doc = json.loads(private)
        check(key_doc['address'] == address and decode_pubkey(key_doc['pub_key']) == key, 'fixture validator key identity differs')
        keys.append(private)
    check(len({row['consensusAddress'] for row in validators}) == 3, 'fixture validators require distinct consensus keys')
    check(len(reward_addresses) == 3 and not reward_addresses.intersection(
        expected[field] for field in ('actor', 'feeCollector', 'gateway', 'wrapped')), 'fixture reward recipients must be independent')
    genesis = private_bytes(directory / 'genesis.json', 64 * 1024 * 1024)
    doc = json.loads(genesis)
    profile = doc['app_state']['withdrawal_settlement']
    height = int(doc['initial_height'])
    check(doc['chain_id'] == CHAIN and 1 <= height < 2**63 - MAX_BLOCKS and not doc.get('validators'), 'invalid fixture genesis chain')
    check(all(profile.get(flag) is True for flag in ('enable_native_consensus', 'enable_del_delegation', 'settlement_only')) and
          profile['gateway'].lower() == expected['gateway'] and profile['wrapped_del'].lower() == expected['wrapped'],
          'fixture genesis native consensus and financial profile differ')
    check(profile.get('enable_native_fee_rewards', False) is fee_rewards, 'fixture genesis fee rewards mode differs')
    check(profile.get('enable_native_ordinary_del_downtime', False) is downtime, 'fixture genesis downtime mode differs')
    if downtime:
        check(profile['master_validator'].lower() == expected['master'] and
              not any(profile.get(flag, False) for flag in ('enable_del_hold', 'enable_nft_delegation', 'enable_native_ibc_v1')),
              'downtime fixture must contain only ordinary DEL')
    check(not doc['app_state']['staking'].get('validators') and not doc['app_state']['staking'].get('delegations'),
          'fixture requires empty SDK staking genesis')
    return expected, genesis, keys, height



def create_replicas(binary, root, genesis, keys, env):
    replicas = []
    for index, (label, key) in enumerate(zip(('a', 'b', 'c'), keys, strict=True)):
        home = root / label
        run_command(binary, home, ['init', f'native-consensus-{label}', '--chain-id', CHAIN, '--evm.evm-chain-id', str(EVM_CHAIN)], env, secret=True)
        validate_fresh_home(home)
        private_write(home / 'config/genesis.json', genesis)
        private_write(home / 'config/priv_validator_key.json', key)
        node_id = run_command(binary, home, ['comet', 'show-node-id'], env, secret=True).strip()
        check(re.fullmatch(r'[0-9a-f]{40}', node_id), 'invalid disposable P2P node identity')
        replica = Replica(label, index, home, node_id)
        write_config(home, 'app.toml', {('api', 'address'): json.dumps(replica.rest.replace('http:', 'tcp:'))})
        replicas.append(replica)
    check(len({node.node_id for node in replicas}) == 3, 'P2P node identities must be unique')
    source = replicas[0]
    for replica in replicas:
        changes = {('p2p', 'pex'): 'false'}
        if replica is source:
            # The two isolated peers share loopback but have distinct node keys.
            changes[('p2p', 'allow_duplicate_ip')] = 'true'
        else:
            changes[('p2p', 'persistent_peers')] = json.dumps(f'{source.node_id}@127.0.0.1:{source.p2p}')
        write_config(replica.home, 'config.toml', changes)
    return replicas


def wait_replicas(replicas, minimum, seconds, report):
    deadline = time.monotonic() + seconds
    heights = [wait_height(node.child, minimum, deadline, node.log, report, read_comet=node.rpc) for node in replicas]
    height = min(heights)
    check(height - report['initialHeight'] < MAX_BLOCKS, 'native consensus exceeded the disposable block budget')
    return height


def effective_set(replicas, validators, height, powers):
    expected = {row['consensusAddress']: (power, decode_pubkey(row['publicKey']))
                for row, power in zip(validators, powers, strict=True) if power}
    for node in replicas:
        result = node.rpc(f'validators?height={height}')
        rows = result['validators']
        check(int(result['block_height']) == height and int(result['total']) == int(result['count']) == len(rows) == len(expected),
              f'unexpected Comet validator count on {node.label} at height {height}')
        actual = {row['address']: (int(row['voting_power']), decode_pubkey(row['pub_key'])) for row in rows}
        check(actual == expected, f'unexpected Comet voting power on {node.label} at height {height}')
    return {row['name']: power for row, power in zip(validators, powers, strict=True) if power}


def committed_header(replicas, height):
    headers = []
    for node in replicas:
        response = node.rpc(f'block?height={height}')
        header = response['block']['header']
        fields = {key: header[key] for key in ('validators_hash', 'next_validators_hash', 'app_hash')}
        fields['block_hash'] = response['block_id']['hash']
        check(header['chain_id'] == CHAIN and int(header['height']) == height and
              all(re.fullmatch(r'[0-9A-Fa-f]{64}', value) for value in fields.values()) and
              re.fullmatch(r'[0-9A-F]{40}', header['proposer_address']), 'invalid Comet header identity')
        fields['proposer_address'] = header['proposer_address']
        headers.append(fields)
    check(all(header == headers[0] for header in headers), f'replicas disagree on block or application hash at {height}')
    return headers[0]


def check_signatures(replicas, validators, height, powers, *, missing=()):
    expected = {row['consensusAddress'] for row, power in zip(validators, powers, strict=True) if power}
    absent = {row['consensusAddress'] for row in validators if row['name'] in missing}
    check(absent <= expected, 'fixture marked a non-member absent')
    for node in replicas:
        result = node.rpc(f'commit?height={height}')
        commit = result['signed_header']['commit']
        check(result['canonical'] is True and int(commit['height']) == height, 'expected a canonical commit')
        signatures = commit['signatures']
        signers = {signature['validator_address'] for signature in signatures
                   if int(signature['block_id_flag']) == 2 and signature['signature']}
        skipped = sum(int(signature['block_id_flag']) == 1 for signature in signatures)
        check(len(signatures) == len(expected) and skipped == len(absent) and signers == expected - absent,
              f'unexpected commit signers on {node.label} at {height}')
    return [row['name'] for row in validators if row['consensusAddress'] in expected - absent]


def check_updates(replicas, validators, height):
    names = {decode_pubkey(row['publicKey']): row['name'] for row in validators}
    for node in replicas:
        result = node.rpc(f'block_results?height={height}')
        updates = result.get('validator_updates') or []
        check(int(result['height']) == height and len(updates) == 2, 'expected one removal and one addition from ABCI')
        actual = {names.get(decode_pubkey(row['pub_key'])): int(row.get('power', 0)) for row in updates}
        check(actual == {'b': 0, 'c': 3}, f'unexpected ABCI validator updates on {node.label}')
    return {'b': 0, 'c': 3}


def check_ledger(replicas, expected, height, c_reserve, *, wallet=None, collector=None, previous=None, rewards=None,
                 downtime=False, burned=0):
    reserves = (3 * ATOMS, 3 * ATOMS, c_reserve) if downtime else (2 * ATOMS, ATOMS, c_reserve)
    total = sum(reserves)
    wallet_value, collector_value = wallet, collector
    reward_values = {} if previous is None else {key: int(value) for key, value in previous['rewardAtomic'].items()}
    if rewards is not None:
        reward_values = rewards.copy()
    bank_supply = None if previous is None else int(previous['bankSupplyAtomic']) - burned
    for node in replicas:
        for validator, value in zip(expected['validators'], reserves, strict=True):
            native_positions({**expected, 'validatorNative': validator['operator']}, height, value, 0, None, node.rest)
            reserve = read_words(expected['gateway'], validator['reserveData'], height, 2, node.evm)[1]
            check(reserve == value, f'validator reserve differs on {node.label}')
        supply = read_words(expected['wrapped'], expected['supplyData'], height, 1, node.evm)[0]
        escrow = read_words(expected['wrapped'], expected['escrowData'], height, 1, node.evm)[0]
        backing = balance(expected['wrapped'], expected['wrappedNative'], height, node.evm, node.rest)
        check(supply == escrow == backing == total, f'wrapped reserve accounting differs on {node.label}')
        amount = balance(expected['actor'], expected['actorNative'], height, node.evm, node.rest)
        if wallet_value is None:
            wallet_value = amount
        check(amount == wallet_value, f'wallet balance differs on {node.label}')
        amount = balance(expected['feeCollector'], expected['feeCollectorNative'], height, node.evm, node.rest)
        if collector_value is None:
            collector_value = amount
        check(amount == collector_value, f'fee collector balance differs on {node.label}')
        for validator in expected['validators']:
            value = balance(validator['reward'], validator['rewardNative'], height, node.evm, node.rest)
            want = reward_values.setdefault(validator['name'], value)
            check(value == want, f'validator commission balance differs on {node.label}')
        supply = native('/cosmos/bank/v1beta1/supply/by_denom?denom=del', height, base=node.rest)['amount']
        check(supply['denom'] == 'del', 'unexpected native supply denomination')
        if bank_supply is None:
            bank_supply = int(supply['amount'])
        check(int(supply['amount']) == bank_supply, f'native bank supply differs on {node.label}')
    return {'height': height, 'walletAtomic': str(wallet_value), 'feeCollectorAtomic': str(collector_value),
            'wrappedAtomic': str(total), 'bankSupplyAtomic': str(bank_supply),
            'rewardAtomic': {name: str(value) for name, value in reward_values.items()},
            'activeReservesAtomic': {row['name']: str(value) for row, value in zip(expected['validators'], reserves, strict=True)}}


def settle_fees(replicas, expected, collected, powers, c_reserve, fee):
    check(int(collected['feeCollectorAtomic']) == fee, 'fee collector must contain exactly one transaction fee')
    total_power = sum(powers)
    commissions = {}
    balances = {}
    for validator, power in zip(expected['validators'], powers, strict=True):
        share, remainder = divmod(fee * power, total_power)
        check(remainder == 0, 'fixture fee must divide exactly across the effective signer powers')
        name = validator['name']
        commissions[name] = share // 4
        balances[name] = int(collected['rewardAtomic'][name]) + commissions[name]
    owner_reward = fee - sum(commissions.values())
    ledger = check_ledger(replicas, expected, collected['height'] + 1, c_reserve,
                          wallet=int(collected['walletAtomic']) + owner_reward, collector=0, previous=collected, rewards=balances)
    return {**ledger, 'commissionPaidAtomic': {name: str(value) for name, value in commissions.items()},
            'ownerRewardAtomic': str(owner_reward)}


def check_downtime_state(replicas, expected, height, jailed):
    count = int(jailed)
    for node in replicas:
        for row in expected['validators']:
            name = row['name']
            penalized = jailed and name == 'c'
            native_row = native('/decimal/validator/v1/validators/' + row['operator'], height, base=node.rest)['validator']
            check(native_row['operator_address'] == row['operator'] and native_row.get('jailed', False) is penalized,
                  f'unexpected native jail state for {name} on {node.label}')
            status, paused, penalties = read_words(expected['master'], expected['masterStateData'][name], height, 3, node.evm)
            check(status in (1, 2) and paused == penalties == int(penalized),
                  f'unexpected Master state for {name} on {node.label}')
        cursor = read_words(expected['gateway'], expected['stakePenaltyData'], height, 1, node.evm)[0]
        check(cursor == count, f'unexpected Gateway penalty cursor on {node.label}')
    return {'jailed': ['c'] if jailed else [], 'masterPenaltyCount': count, 'stakePenaltyCursor': count}


def downtime_events(result):
    return [event for event in result['finalize_block_events'] or [] if event['type'] == 'decimal_native_downtime_applied']


def run_downtime_gate(binary, expected, replicas, env, owner, report, first):
    validators = expected['validators']
    a, b, c = replicas
    quorum = (a, b)
    baseline = first - 1
    before = check_ledger(replicas, expected, baseline, 2 * ATOMS, downtime=True)
    report['before'] = {**before, **check_downtime_state(replicas, expected, baseline, False)}
    report['beforeSigners'] = check_signatures(replicas, validators, baseline, DOWNTIME_INITIAL)

    report['phase'] = 'stop-c-and-observe-downtime'
    check(c.child.poll() is None, 'validator C exited before the deliberate stop')
    last_seen = int(a.rpc('status')['sync_info']['latest_block_height'])
    owner.stop(c.child)
    # The surviving 6/8 voting power can advance blocks while C is offline.
    tip = wait_replicas(quorum, max(first + 9, last_seen + 8), 85, report)
    penalty_heights = [height for height in range(baseline + 1, tip)
                       if downtime_events(a.rpc(f'block_results?height={height}'))]
    check(len(penalty_heights) == 1, 'expected exactly one ordinary DEL downtime event while C was stopped')
    height = penalty_heights[0]
    infraction = height - 1
    check(height >= baseline + DOWNTIME_WINDOW + 1 and c.child.poll() is not None,
          'C must be absent for the full signing window')
    fields = {'operator': validators[2]['operator'], 'infraction_height': str(infraction), 'burned': str(DOWNTIME_BURN)}
    for node in quorum:
        result = node.rpc(f'block_results?height={height}')
        events = downtime_events(result)
        check(len(events) == 1 and len(events[0]['attributes']) == len(fields) and
              {item['key']: item['value'] for item in events[0]['attributes']} == fields,
              f'unexpected downtime event on {node.label}')
        updates = result.get('validator_updates') or []
        check(len(updates) == 1 and int(updates[0].get('power', 0)) == 0 and
              decode_pubkey(updates[0]['pub_key']) == decode_pubkey(validators[2]['publicKey']),
              f'ABCI must remove only C on {node.label}')
    missed = {}
    for observed in range(height - DOWNTIME_WINDOW, height):
        effective_set(quorum, validators, observed, DOWNTIME_INITIAL)
        missed[str(observed)] = check_signatures(quorum, validators, observed, DOWNTIME_INITIAL, missing=('c',))
    report['downtime'] = {'height': height, 'infractionHeight': infraction, 'burnedAtomic': str(DOWNTIME_BURN),
                          'missedCommits': missed, 'abciUpdates': {'c': 0}}

    report['phase'] = 'verify-penalty-and-effective-h-plus-two'
    wait_replicas(quorum, height + 3, 65, report)
    pre_penalty = check_ledger(quorum, expected, infraction, 2 * ATOMS, downtime=True, previous=before,
                               wallet=int(before['walletAtomic']), collector=int(before['feeCollectorAtomic']))
    check_downtime_state(quorum, expected, infraction, False)
    remaining = 2 * ATOMS - DOWNTIME_BURN
    punished = check_ledger(quorum, expected, height, remaining, downtime=True, previous=pre_penalty, burned=DOWNTIME_BURN,
                            wallet=int(before['walletAtomic']), collector=int(before['feeCollectorAtomic']))
    report['penalty'] = {**punished, **check_downtime_state(quorum, expected, height, True)}
    transition = {}
    for offset, powers in enumerate((DOWNTIME_INITIAL, DOWNTIME_INITIAL, DOWNTIME_AFTER)):
        current = height + offset
        transition[f'H+{offset}'] = {'height': current, 'powers': effective_set(quorum, validators, current, powers),
                                     'signers': check_signatures(quorum, validators, current, powers,
                                                                 missing=('c',) if offset < 2 else ()),
                                     **committed_header(quorum, current)}
    old, following, changed = (transition[f'H+{offset}'] for offset in range(3))
    check(old['validators_hash'] == old['next_validators_hash'] == following['validators_hash'] and
          following['next_validators_hash'] == changed['validators_hash'] != following['validators_hash'],
          'downtime removal must first become effective at H+2')
    report['transition'] = transition

    report['phase'] = 'restart-c-as-follower'
    checkpoint = height + 2
    checkpoint_header = committed_header(quorum, checkpoint)
    c.start(binary, env, owner, 'follower')
    tip = wait_replicas(replicas, checkpoint + 3, 95, report)
    check(committed_header(replicas, checkpoint) == checkpoint_header,
          'restarted C disagrees on the removal checkpoint')
    final_height = tip - 1
    report['follower'] = {'checkpointHeight': checkpoint, 'height': final_height,
                          'powers': effective_set(replicas, validators, final_height, DOWNTIME_AFTER),
                          'signers': check_signatures(replicas, validators, final_height, DOWNTIME_AFTER),
                          **committed_header(replicas, final_height)}
    final = check_ledger(replicas, expected, final_height, remaining, downtime=True, previous=punished,
                         wallet=int(before['walletAtomic']), collector=int(before['feeCollectorAtomic']))
    report['final'] = {**final, **check_downtime_state(replicas, expected, final_height, True)}


def run_gate(args, binary, root, expected, genesis, keys, env, owner, report):
    report['phase'] = 'create-replicas'
    replicas = create_replicas(binary, root, genesis, keys, env)
    source = replicas[0]
    version = json.loads(run_command(binary, source.home, ['version', '--long', '--output', 'json'], env))
    check(version['server_name'] == 'mcnd' and version['commit'] == args.source_commit, 'daemon version differs from checked source')
    report['phase'] = 'validate-genesis'
    try:
        run_command(binary, source.home, ['genesis', 'validate-genesis'], env)
    except RuntimeError as error:
        raise RuntimeError('native consensus genesis rejected: ' + safe_diagnostic(str(error))) from None
    validators = expected['validators']

    report['phase'] = 'initial-native-consensus'
    for node in replicas:
        node.start(binary, env, owner, 'initial')
    initial = report['initialHeight']
    first = wait_replicas(replicas, initial + (4 if args.downtime else 2), 85, report)
    for node, validator in zip(replicas, validators, strict=True):
        status = node.rpc('status')
        check(status['node_info']['id'] == node.node_id and status['validator_info']['address'] == validator['consensusAddress'],
              'replica did not start with its distinct configured signing key')
    initial_powers = DOWNTIME_INITIAL if args.downtime else INITIAL
    report['initialPower'] = effective_set(replicas, validators, initial, initial_powers)
    if args.downtime:
        run_downtime_gate(binary, expected, replicas, env, owner, report, first)
        return
    report['initialSigners'] = check_signatures(replicas, validators, initial, INITIAL)
    report['initialBlock'] = committed_header(replicas, initial + 1)
    before = check_ledger(replicas, expected, first, 0, collector=0 if args.fee_rewards else None)
    report['before'] = before

    report['phase'] = 'signed-del-delegation'
    signed = submit(source.child, source.log, report, expected, 'delegate', True, expected['nonce'], DELEGATED,
                    {'stake_id': expected['stakeId'], 'amount': expected['amount']})
    height = signed['height']
    check(height > first, 'signed delegation must follow the initial ledger check')
    report['delegation'] = signed

    report['phase'] = 'effective-h-plus-two'
    wait_replicas(replicas, height + 3, 65, report)
    report['abciUpdates'] = check_updates(replicas, validators, height)
    transition = {}
    for offset, powers in enumerate((INITIAL, INITIAL, AFTER)):
        current = height + offset
        transition[f'H+{offset}'] = {'height': current, 'powers': effective_set(replicas, validators, current, powers),
                                     'signers': check_signatures(replicas, validators, current, powers),
                                     **committed_header(replicas, current)}
    old, following, changed = (transition[f'H+{offset}'] for offset in range(3))
    check(old['validators_hash'] == old['next_validators_hash'] == following['validators_hash'] and
          following['next_validators_hash'] == changed['validators_hash'] != following['validators_hash'],
          'ABCI validator updates must first become effective at H+2')
    report['transition'] = transition
    deposit, fee = int(expected['amount']), int(signed['feeAtomic'])
    wallet = int(before['walletAtomic']) - deposit - fee
    collector = int(before['feeCollectorAtomic']) + fee
    collected = check_ledger(replicas, expected, height, deposit, wallet=wallet, collector=collector, previous=before)
    report['firstFeeCollected'] = collected
    if args.fee_rewards:
        paid = settle_fees(replicas, expected, collected, INITIAL, deposit, fee)
        report['firstFeeSettlement'] = paid
        wallet, collector, collected = int(paid['walletAtomic']), 0, paid
    report['funded'] = check_ledger(replicas, expected, height + 2, deposit, wallet=wallet, collector=collector, previous=collected)

    report['phase'] = 'post-transition-top-up'
    top_up = submit(source.child, source.log, report, expected, 'topUp', True, expected['nonce'] + 1, DELEGATED,
                    {'stake_id': expected['stakeId'], 'amount': expected['topUpAmount']})
    top_up_height = top_up['height']
    check(top_up_height > height + 2, 'top-up must occur after the new set became effective')
    report['topUp'] = top_up
    wait_replicas(replicas, top_up_height + 4, 65, report)
    top_up_block = committed_header(replicas, top_up_height)
    top_up['proposer'] = next((row['name'] for row in validators if row['consensusAddress'] == top_up_block['proposer_address']), None)
    check(top_up['proposer'] in ('a', 'c'), 'post-transition transaction must be in a block from the new set')
    for node in replicas:
        results = node.rpc(f'block_results?height={top_up_height}')
        check(int(results['height']) == top_up_height and not results.get('validator_updates'),
              'fractional DEL top-up changed the effective power schedule')
    for proposer_height in range(top_up_height, top_up_height + 4):
        header = committed_header(replicas, proposer_height)
        if header['proposer_address'] == validators[2]['consensusAddress']:
            report['cProposed'] = {'height': proposer_height, **header}
            break
    else:
        raise ValueError('new native validator C did not propose a committed block')
    effective_set(replicas, validators, report['cProposed']['height'], AFTER)
    check_signatures(replicas, validators, report['cProposed']['height'], AFTER)

    top_up_amount, top_up_fee = int(expected['topUpAmount']), int(top_up['feeAtomic'])
    wallet -= top_up_amount + top_up_fee
    collector += top_up_fee
    c_reserve = deposit + top_up_amount
    check_signatures(replicas, validators, top_up_height, AFTER)
    topped_up = check_ledger(replicas, expected, top_up_height, c_reserve, wallet=wallet, collector=collector, previous=report['funded'])
    report['toppedUp'] = topped_up
    if args.fee_rewards:
        paid = settle_fees(replicas, expected, topped_up, AFTER, c_reserve, top_up_fee)
        report['topUpFeeSettlement'] = paid
        wallet, collector, topped_up = int(paid['walletAtomic']), 0, paid

    report['phase'] = 'restart'
    checkpoint = wait_replicas(replicas, top_up_height + 4, 35, report)
    checkpoint_block = committed_header(replicas, checkpoint)
    for node in reversed(replicas):
        check(node.child.poll() is None, f'replica {node.label} exited before restart')
        owner.stop(node.child)
    for node in replicas:
        node.start(binary, env, owner, 'restart')
    tip = wait_replicas(replicas, checkpoint + 3, 95, report)
    check(committed_header(replicas, checkpoint) == checkpoint_block, 'restart changed the prior checkpoint')
    final_height = tip - 1
    report['restart'] = {'checkpointHeight': checkpoint, 'height': final_height,
                         'powers': effective_set(replicas, validators, final_height, AFTER),
                         'signers': check_signatures(replicas, validators, final_height, AFTER),
                         **committed_header(replicas, final_height)}
    report['final'] = check_ledger(replicas, expected, final_height, c_reserve, wallet=wallet, collector=collector, previous=topped_up)


def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('binary', 'binary-sha256', 'source-commit', 'fixture', 'home-parent', 'report'):
        parser.add_argument('--' + name, required=True)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--fee-rewards', action='store_true')
    mode.add_argument('--downtime', action='store_true')
    args = parser.parse_args(argv)
    report = {'status': 'FAIL', 'phase': 'fixture-provenance', 'scope': 'three loopback processes with native consensus and a signed DEL stake'}
    with pathlib.Path(args.report).open('x', encoding='utf8') as stream:
        try:
            private_namespace()
            check(re.fullmatch(SHA256, args.binary_sha256) and re.fullmatch(r'[0-9a-f]{40}', args.source_commit), 'invalid build provenance')
            binary = str(pathlib.Path(args.binary).resolve(strict=True))
            with open(binary, 'rb') as binary_stream:
                check(hashlib.file_digest(binary_stream, 'sha256').hexdigest() == args.binary_sha256, 'daemon SHA256 mismatch')
            expected, genesis, keys, initial = load_fixture(pathlib.Path(args.fixture), args.fee_rewards, args.downtime)
            report.update(sourceCommit=args.source_commit, binarySha256=args.binary_sha256, initialHeight=initial,
                          genesisSha256=hashlib.sha256(genesis).hexdigest(), chainID=CHAIN, evmChainID=EVM_CHAIN, feeRewards=args.fee_rewards)
            if args.downtime:
                report['scope'] = 'three loopback processes with an ordinary DEL downtime penalty and follower catch-up'
            with tempfile.TemporaryDirectory(prefix='mcnd-native-consensus-', dir=args.home_parent) as directory:
                root = pathlib.Path(directory)
                owner = ProcessOwner(signals)
                try:
                    run_gate(args, binary, root, expected, genesis, keys, private_environment(root), owner, report)
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
