"""Rehearse a governed NFT refund upgrade from the published MSP binary."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile

from check_operator_applications import native_address_from_evm
from del_delegation_smoke import balance, read_words, write_config
from mcnd_process_smoke import ProcessOwner, SignalGuard, private_environment, private_namespace, run_command
from native_consensus_smoke import ATOMS, committed_header, create_replica, decode_pubkey, effective_set, wait_replicas
from native_governance_upgrade_smoke import (
    PLAN, SCHEMA, applied_height, check_proposal, check_schedule, committed_height, current_plan,
    find_four_signature_commit, query_proposal, submit_governance_tx, wait_predecessor_halt, wait_tally,
)
from native_launch_media import commit_media_transaction, read_media_fixture
from native_launch_rehearsal import configure_peers, load_fixture
from withdrawal_genesis_smoke import SHA256, bech32_payload, check, native, private_bytes, safe_diagnostic


CHAIN, EVM_ID = 'mintcandy_9905401-1', 9905401
UNIT = 2 * ATOMS
MEDIA_URI = 'ipfs://bafkrei' + 'a' * 52


def sign_fixture(args, root, env, test, name, **values):
    subprocess.run([str(args.signer), '-test.run', f'^{test}$', '-test.timeout', '35s'],
                   cwd=root, env=dict(env, **values), check=True, timeout=40)
    return root / name


def nft_call(args, root, env, nodes, report, nft, nonce, *, action='', status=1):
    source = nodes[0]
    name = f'nft-{nonce}.json'
    path = sign_fixture(args, root, env, 'TestExportLaunchMediaCreation', name,
                        MCND_MEDIA_GENESIS=str(source.home / 'config/genesis.json'),
                        MCND_MEDIA_FIXTURE=str(root / name), MCND_MEDIA_URI=MEDIA_URI,
                        MCND_MEDIA_KIND=nft['family'] + '-backed', MCND_MEDIA_NONCE=str(nonce),
                        MCND_MEDIA_COLLECTION=nft.get('contract', ''), MCND_MEDIA_REFUND=action)
    fixture = read_media_fixture(path, report)
    if action or 'contract' in nft:
        check(fixture['target'] == nft['contract'] and fixture['creator'] == nft['wallet'],
              'fixture action differs from the signed collection')
    receipt = commit_media_transaction(source, nodes, fixture, nonce, report, status=status)
    return fixture, receipt


def nft_state(node, height, nft):
    contract, owner = nft['contract'], nft['wallet']
    wallet = balance(owner, native_address_from_evm(owner), height, node.evm, node.rest, denom='msp')
    custody = balance(contract, native_address_from_evm(contract), height, node.evm, node.rest, denom='msp')
    token_id = nft['tokenId'].to_bytes(32, 'big')
    calldata = '0x70a08231' + owner[2:].rjust(64, '0')
    if nft['family'] == 'erc1155':
        calldata = '0x00fdd58e' + owner[2:].rjust(64, '0') + token_id.hex()
    count = read_words(contract, calldata, height, 1, node.evm)[0]
    payload = b'mintcandy:nft-binding:v1\0' + bytes([len(CHAIN)]) + CHAIN.encode()
    payload += EVM_ID.to_bytes(8, 'big') + bytes.fromhex(contract[2:]) + token_id
    token = native('/decimal/nft/v1/token/evm1_' + hashlib.sha256(payload).hexdigest(), height, base=node.rest)['token']
    serials = [int(row['id']) for row in token.get('sub_tokens', [])]
    check(len(serials) == count and int(token['minted']) == nft['minted'] and
          int(token.get('burnt', 0)) == nft['minted'] - count and
          token['reserve'] == {'denom': 'msp', 'amount': str(UNIT)} and
          all(bech32_payload(row['owner']) == owner for row in token.get('sub_tokens', [])),
          'native wallet ownership or reserve differs from EVM')
    supply = native('/cosmos/bank/v1beta1/supply/by_denom?denom=msp', height, base=node.rest)['amount']
    check(supply['denom'] == 'msp', 'native supply query returned a different denomination')
    return wallet, custody, serials, int(supply['amount'])


def prove_mint(args, root, env, nodes, report, nft, nonce):
    fixture, created = nft_call(args, root, env, nodes, report, nft, nonce)
    multi = nft['family'] == 'erc1155'
    topics = [fixture['event'], f'0x{int(multi):064x}']
    records = [row['data'] for row in created['logs']
               if row['address'].lower() == fixture['target'] and row['topics'] == topics]
    check(len(records) == 1 and re.fullmatch(r'0x0{24}[0-9a-f]{40}(?:[0-9a-f]{64})+', records[0]),
          'factory did not return one exact creation event')
    nft.update(contract='0x' + records[0][26:66], wallet=fixture['creator'], tokenId=123 if multi else 1,
               minted=2 if multi else 1)
    _, mint = nft_call(args, root, env, nodes, report, nft, nonce + 1)
    height = int(mint['blockNumber'], 16)
    expected = nft['minted'] * UNIT
    result = nft_state(nodes[0], height, nft)
    check(result[1:3] == (expected, list(range(1, nft['minted'] + 1))) and
          all(nft_state(other, height, nft) == result for other in nodes[1:]),
          'minted NFT reserve and serials disagree across replicas')
    return {'height': height, 'reserveAtomic': str(expected)}


def prove_burn(args, root, env, nodes, report, nft, nonce, *, accept):
    signed, receipt = nft_call(args, root, env, nodes, report, nft, nonce, action='burn', status=int(accept))
    height = int(receipt['blockNumber'], 16)
    before, after = (nft_state(nodes[0], at, nft) for at in (height - 1, height))
    refunded = UNIT if accept else 0
    fee = int(receipt['gasUsed'], 16) * int(receipt['effectiveGasPrice'], 16)
    check(after == (before[0] + refunded - fee, before[1] - refunded, before[2][int(accept):], before[3]) and
          all(nft_state(other, height, nft) == after for other in nodes[1:]),
          'refund changed another NFT unit, wallet balance, custody or native supply')
    if accept:
        check(sum(row['address'].lower() == nft['contract'] and row['topics'][0] == signed['event']
                  for row in receipt['logs']) == 1, 'successful refund did not include one burn event')
    else:
        check(not receipt['logs'], 'reverted burn left receipt logs')
    return {'height': height, 'gas': int(receipt['gasUsed'], 16), 'paidAtomic': str(refunded),
            'remainingAtomic': str(after[1]), 'activeSerials': after[2]}


def governance_tx(args, source, root, env, report, action, index, signer, label):
    at = committed_height(source)
    account = native(f'/cosmos/auth/v1beta1/accounts/{signer}', at, base=source.rest)['account']
    check(account['address'] == signer, 'governance fixture queried a different account')
    name = label + '.json'
    action.update(index=index, accountNumber=int(account['account_number']), sequence=int(account['sequence']))
    path = sign_fixture(args, root, env, 'TestExportMainnetNFTUpgradeAction', name,
                        MCND_NFT_GOV_GENESIS=str(source.home / 'config/genesis.json'),
                        MCND_NFT_GOV_OUTPUT=str(root / name), MCND_NFT_GOV_ACTION=json.dumps(action))
    signed = json.loads(private_bytes(path, 4096))
    check(signed['sender'] == signer and signed['genesisSha256'] == report['genesisSha256'] and
          hashlib.sha256(base64.b64decode(signed['tx'], validate=True)).hexdigest() == signed['hash'],
          'governed upgrade signature fixture differs from the private domain')
    return submit_governance_tx(source, signed, label, report)


def rehearse(args, root, env, genesis, expected, keys, owner, report):
    rows = expected['validators']
    powers = tuple(row['initialPower'] for row in rows)
    validators = [create_replica(str(args.predecessor), root, row['name'], i, genesis, key, env,
                                 chain_id=CHAIN, evm_chain_id=EVM_ID)
                  for i, (row, key) in enumerate(zip(rows, keys, strict=True))]
    observer = create_replica(str(args.predecessor), root, 'observer', 4, genesis, None, env,
                              chain_id=CHAIN, evm_chain_id=EVM_ID)
    nodes = [*validators, observer]
    for node in nodes:
        configure_peers(node, [other for other in validators if other is not node])
        write_config(node.home, 'config.toml', {('consensus', 'timeout_commit'): '"2s"'})
    run_command(str(args.predecessor), validators[0].home, ['genesis', 'validate-genesis'], env)
    for node in nodes:
        node.start(str(args.predecessor), env, owner, 'published')
    wait_replicas(nodes, 8, 105, report)
    effective_set(nodes, rows, 4, powers)

    report['phase'] = 'create-backed-nfts-on-predecessor'
    nfts = [{'family': 'erc721'}, {'family': 'erc1155'}]
    for i, nft in enumerate(nfts):
        nft['mint'] = prove_mint(args, root, env, nodes, report, nft, 2 * i)
    for i, nft in enumerate(nfts):
        nft['beforeUpgrade'] = prove_burn(args, root, env, nodes, report, nft, 4 + i, accept=False)

    report['phase'] = 'signed-validator-upgrade-votes'
    source = validators[0]
    validators_by_key = {row['consensus_pubkey']['key']: row['operator_address']
                         for row in json.loads(genesis)['app_state']['validator']['validators']}
    signer_addresses = [native_address_from_evm(bech32_payload(
        validators_by_key[base64.b64encode(decode_pubkey(row['publicKey'])).decode('ascii')], 'd0valoper')) for row in rows]
    target = committed_height(source) + 32
    manifest = SCHEMA + ':' + json.dumps({'name': PLAN, 'height': target}, separators=(',', ':'))
    action = {'passedID': 1, 'planHeight': target}
    submission = governance_tx(args, source, root, env, report, {'height': target}, 0, signer_addresses[0], 'proposal')
    check(query_proposal(source, 1, submission['height'])['metadata'] == manifest, 'different committed upgrade manifest')
    votes = [governance_tx(args, source, root, env, report, {'proposalId': 1}, i, voter, f'vote-{i}')
             for i, voter in enumerate(signer_addresses)]
    height = wait_tally(source, 1, votes[-1]['height'], report)
    wait_replicas(nodes, height + 1, 40, report)
    report['proposal'] = check_proposal(nodes, 1, height, manifest, True, voting_weight=str(sum(powers) * ATOMS))
    check_schedule(nodes, action, height)

    report['phase'] = 'halt-and-reopen-the-same-homes'
    wait_predecessor_halt(validators, action, report)
    checkpoint = committed_header(validators, target - 1)
    boundary = committed_header(validators, target)
    for node in nodes:
        owner.stop(node.child)
        node.start(str(args.successor), env, owner, 'successor')
    wait_replicas(nodes, target + 5, 125, report)
    check(committed_header(nodes, target - 1) == checkpoint and committed_header(nodes, target) == boundary and
          all(applied_height(node, target) == target and current_plan(node, target) is None for node in nodes),
          'successor diverged from the published boundary or applied a different plan')
    report['upgrade'] = {'height': target, 'before': checkpoint, 'boundary': boundary,
                         'appHashAfter': committed_header(nodes, target + 1)['app_hash']}

    report['phase'] = 'refund-and-replay-on-updated-replicas'
    nonce = 6 + sum(voter == native_address_from_evm(nfts[0]['wallet']) for voter in signer_addresses)
    if native_address_from_evm(nfts[0]['wallet']) == signer_addresses[0]:
        nonce += 1
    for nft in nfts:
        nft['refunds'] = []
        for _ in range(nft['minted']):
            nft['refunds'].append(prove_burn(args, root, env, nodes, report, nft, nonce, accept=True))
            nonce += 1
        nft['repeat'] = prove_burn(args, root, env, nodes, report, nft, nonce, accept=False)
        nonce += 1
    report['nfts'] = nfts
    last = nfts[-1]['repeat']['height']
    report['finalCommit'] = find_four_signature_commit(nodes, rows, args.commit_probe, last + 1, last + 10, report)
    report['finalCommit']['appHash'] = committed_header(nodes, report['finalCommit']['height'])['app_hash']


def main(signals):
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ('predecessor', 'successor', 'fixture', 'assembly-report', 'signer', 'commit-probe', 'home-parent', 'report'):
        parser.add_argument('--' + flag, required=True, type=Path)
    for flag in ('predecessor-sha256', 'successor-sha256', 'predecessor-source', 'successor-source'):
        parser.add_argument('--' + flag, required=True)
    args = parser.parse_args()
    report = {'status': 'FAIL', 'phase': 'verify-disposable-domain', 'initialHeight': 1}
    with args.report.open('x', encoding='utf8') as stream:
        try:
            private_namespace()
            original, expected, keys = load_fixture(args.fixture, four_validators=True, empty_user_assets=True,
                                                    assembly_report=args.assembly_report, network_profile='mainnet')
            doc = json.loads(original)
            state = doc['app_state']
            check(state['withdrawal_settlement']['enable_native_upgrade_governance'] and
                  not state['gov'].get('proposals'), 'expected configured governance without proposals')
            # The actual published genesis is never used. Only shorten voting
            # on the cold-verified, disposable CI copy.
            state['gov']['params'].update(voting_period='32s', expedited_voting_period='12s')
            genesis = json.dumps(doc, separators=(',', ':')).encode()
            report.update(loopbackOnlyNamespace=True, chainId=CHAIN, evmChainId=EVM_ID, nativeDenom='msp',
                          genesisSha256=hashlib.sha256(genesis).hexdigest(),
                          originalFixtureSha256=hashlib.sha256(original).hexdigest(), fixtureVotingPeriod='32s')
            with tempfile.TemporaryDirectory(prefix='mcnd-nft-refund-', dir=args.home_parent) as directory:
                root = Path(directory)
                env = private_environment(root)
                for role in ('predecessor', 'successor'):
                    binary = getattr(args, role).resolve(strict=True)
                    setattr(args, role, binary)
                    digest = getattr(args, role + '_sha256')
                    source = getattr(args, role + '_source')
                    check(re.fullmatch(SHA256, digest) and re.fullmatch(r'[0-9a-f]{40}', source), 'invalid provenance')
                    with binary.open('rb') as f:
                        check(hashlib.file_digest(f, 'sha256').hexdigest() == digest, f'{role} executable hash differs')
                    version = json.loads(run_command(str(binary), root, ['version', '--long', '--output', 'json'], env))
                    check(version['server_name'] == 'mcnd' and version['commit'] == source, f'{role} source differs')
                    report[role] = {'sha256': digest, 'source': source}
                check(report['predecessor'] != report['successor'], 'requires separate predecessor and successor revisions')
                owner = ProcessOwner(signals)
                try:
                    rehearse(args, root, env, genesis, expected, keys, owner, report)
                finally:
                    owner.close()
            report.update(status='PASS', phase='complete')
        except Exception as error:
            report['error'] = safe_diagnostic(str(error))
        finally:
            json.dump(report, stream, indent=2)
            stream.write('\n')
    print(json.dumps(report, indent=2))
    return 0 if report['status'] == 'PASS' else 1


if __name__ == '__main__':
    with SignalGuard() as signals:
        raise SystemExit(main(signals))
