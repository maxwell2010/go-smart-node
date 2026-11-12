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
from withdrawal_genesis_smoke import ADDRESS, CHAIN, EVM_CHAIN, MAX_BLOCKS, SHA256, WORD, bech32_payload, check, comet, private_bytes, private_write, safe_diagnostic, wait_height


ATOMS = 10**18
INITIAL = (2, 1, 0)
AFTER = (2, 0, 3)


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
            '--p2p.laddr', f'tcp://127.0.0.1:{self.p2p}', '--grpc.address', f'127.0.0.1:{9190 + self.index * 100}',
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


def load_fixture(directory):
    info = directory.lstat()
    check(stat.S_ISDIR(info.st_mode) and stat.S_IMODE(info.st_mode) & 0o077 == 0, 'private fixture directory required')
    expected = json.loads(private_bytes(directory / 'expectations.json', 8192))
    check(expected['chainId'] == CHAIN and expected['evmChainId'] == EVM_CHAIN, 'fixture signing domain mismatch')
    for field in ('gateway', 'wrapped', 'actor'):
        check(re.fullmatch(ADDRESS, expected[field]), f'invalid fixture {field}')
        expected[field] = expected[field].lower()
    for field in ('actor', 'wrapped'):
        check(bech32_payload(expected[field + 'Native']) == expected[field], 'fixture bank and EVM addresses differ')
    check(len({expected[field] for field in ('gateway', 'wrapped', 'actor')}) == 3, 'duplicate fixture roles')
    check(expected['gasLimit'] == GAS_LIMIT and expected['gasPrice'] == str(GAS_PRICE) and expected['amount'] == str(3 * ATOMS),
          'fixture transaction value or gas differs')
    check(type(expected['nonce']) is int and 0 <= expected['nonce'] < 2**32, 'invalid fixture nonce')
    check(re.fullmatch(WORD, expected['stakeId']) and re.fullmatch(WORD, expected['delegate']['hash']) and
          re.fullmatch(r'0x(?:[0-9a-f]{2}){100,512}', expected['delegate']['raw']), 'invalid signed DEL fixture transaction')
    for name, words in (('supplyData', 0), ('escrowData', 1)):
        check(re.fullmatch(fr'0x[0-9a-f]{{{8 + words * 64}}}', expected[name]), 'invalid fixture WDEL calldata')
    validators = expected['validators']
    check([row['name'] for row in validators] == ['a', 'b', 'c'] and
          tuple(row['initialPower'] for row in validators) == INITIAL and
          tuple(row['afterStakePower'] for row in validators) == AFTER, 'unexpected fixture initial or changed voting power')
    keys = []
    for row in validators:
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
    genesis = private_bytes(directory / 'genesis.json', 64 * 1024 * 1024)
    doc = json.loads(genesis)
    profile = doc['app_state']['withdrawal_settlement']
    height = int(doc['initial_height'])
    check(doc['chain_id'] == CHAIN and 1 <= height < 2**63 - MAX_BLOCKS and not doc.get('validators'), 'invalid fixture genesis chain')
    check(all(profile.get(flag) is True for flag in ('enable_native_consensus', 'enable_del_delegation', 'settlement_only')) and
          profile['gateway'].lower() == expected['gateway'] and profile['wrapped_del'].lower() == expected['wrapped'],
          'fixture genesis native consensus and financial profile differ')
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
              all(re.fullmatch(r'[0-9A-Fa-f]{64}', value) for value in fields.values()), 'invalid Comet header identity')
        headers.append(fields)
    check(all(header == headers[0] for header in headers), f'replicas disagree on block or application hash at {height}')
    return headers[0]


def check_signatures(replicas, validators, height, powers):
    expected = {row['consensusAddress'] for row, power in zip(validators, powers, strict=True) if power}
    for node in replicas:
        result = node.rpc(f'commit?height={height}')
        commit = result['signed_header']['commit']
        check(result['canonical'] is True and int(commit['height']) == height, 'expected a canonical commit')
        signatures = commit['signatures']
        signers = {signature['validator_address'] for signature in signatures
                   if int(signature['block_id_flag']) == 2 and signature['signature']}
        check(len(signatures) == len(expected) and signers == expected, f'unexpected commit signers on {node.label} at {height}')
    return [row['name'] for row in validators if row['consensusAddress'] in expected]


def check_updates(replicas, validators, height):
    names = {decode_pubkey(row['publicKey']): row['name'] for row in validators}
    for node in replicas:
        result = node.rpc(f'block_results?height={height}')
        updates = result.get('validator_updates') or []
        check(int(result['height']) == height and len(updates) == 2, 'expected one removal and one addition from ABCI')
        actual = {names.get(decode_pubkey(row['pub_key'])): int(row.get('power', 0)) for row in updates}
        check(actual == {'b': 0, 'c': 3}, f'unexpected ABCI validator updates on {node.label}')
    return {'b': 0, 'c': 3}


def check_ledger(replicas, expected, height, funded, wallet=None):
    reserves = (2 * ATOMS, ATOMS, 3 * ATOMS if funded else 0)
    total = sum(reserves)
    wallet_value = wallet
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
    return {'height': height, 'walletAtomic': str(wallet_value), 'wrappedAtomic': str(total),
            'activeReservesAtomic': {row['name']: str(value) for row, value in zip(expected['validators'], reserves, strict=True)}}



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
    first = wait_replicas(replicas, initial + 2, 85, report)
    for node, validator in zip(replicas, validators, strict=True):
        status = node.rpc('status')
        check(status['node_info']['id'] == node.node_id and status['validator_info']['address'] == validator['consensusAddress'],
              'replica did not start with its distinct configured signing key')
    report['initialPower'] = effective_set(replicas, validators, initial, INITIAL)
    report['initialSigners'] = check_signatures(replicas, validators, initial, INITIAL)
    report['initialBlock'] = committed_header(replicas, initial + 1)
    before = check_ledger(replicas, expected, first, False)
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
    wallet = int(before['walletAtomic']) - int(expected['amount']) - int(signed['feeAtomic'])
    report['funded'] = check_ledger(replicas, expected, height + 2, True, wallet)

    report['phase'] = 'restart'
    checkpoint = wait_replicas(replicas, height + 3, 35, report)
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
    report['final'] = check_ledger(replicas, expected, final_height, True, wallet)


def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('binary', 'binary-sha256', 'source-commit', 'fixture', 'home-parent', 'report'):
        parser.add_argument('--' + name, required=True)
    args = parser.parse_args(argv)
    report = {'status': 'FAIL', 'phase': 'fixture-provenance', 'scope': 'three loopback processes with native consensus and a signed DEL stake'}
    with pathlib.Path(args.report).open('x', encoding='utf8') as stream:
        try:
            private_namespace()
            check(re.fullmatch(SHA256, args.binary_sha256) and re.fullmatch(r'[0-9a-f]{40}', args.source_commit), 'invalid build provenance')
            binary = str(pathlib.Path(args.binary).resolve(strict=True))
            with open(binary, 'rb') as binary_stream:
                check(hashlib.file_digest(binary_stream, 'sha256').hexdigest() == args.binary_sha256, 'daemon SHA256 mismatch')
            expected, genesis, keys, initial = load_fixture(pathlib.Path(args.fixture))
            report.update(sourceCommit=args.source_commit, binarySha256=args.binary_sha256, initialHeight=initial,
                          genesisSha256=hashlib.sha256(genesis).hexdigest(), chainID=CHAIN, evmChainID=EVM_CHAIN)
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
