"""Rehearse a signed avatar commitment against the attested disposable daemon."""

import base64
from contextlib import closing
import json
import os
from pathlib import Path
import re
import ssl
import subprocess
import sys
import time

from native_consensus_smoke import committed_header, wait_replicas
from del_delegation_smoke import read_words
from withdrawal_genesis_smoke import ADDRESS, WORD, check, ethereum, native, private_bytes


MEDIA = Path(__file__).resolve().parents[1] / 'media'


def rehearse_media(root, expected, nodes, fixture_path, report, env, owner):
    check((expected['chainId'], expected['evmChainId']) in {('mintcandy_990014-1', 990014), ('mintcandy_9905401-1', 9905401)} and
          report['loopbackOnlyNamespace'] and report['offlineAssembledGenesis'],
          'media transaction fixture requires the private signing domain')
    sys.path[:0] = [str(MEDIA), str(MEDIA / 'tests')]
    from mcnd_media.commitment import CommittedReader, LocalRPC
    from mcnd_media.store import Asset, strict

    report['phase'] = 'media-signed-token-creation'
    fixture = read_media_fixture(fixture_path, report)
    kind = fixture['kind'].removesuffix('-backed')
    check(kind in ('erc20', 'erc721', 'erc1155'), 'unknown media asset kind')
    backed = fixture['kind'] != kind
    check(not backed or (kind != 'erc20' and expected['chainId'] == 'mintcandy_9905401-1' and
                         expected['nativeDenom'] == 'msp'), 'invalid backing domain')
    if kind != 'erc20':
        report['phase'] = 'media-signed-nft-collection'
    metadata = strict(private_bytes(fixture_path.parent / 'metadata.json', 64 << 10))
    image = private_bytes(fixture_path.parent / 'avatar.png', 1 << 20)
    source, witness = nodes[0], nodes[-1]
    receipt = commit_media_transaction(source, nodes, fixture, 0, report)
    topics = [fixture['event']]
    address_data = r'0x0{24}[0-9a-f]{40}'
    if kind != 'erc20':
        topics.append(f'0x{int(kind == "erc1155"):064x}')
        address_data += r'(?:[0-9a-f]{64})+'
    records = [entry['data'] for entry in receipt['logs']
               if entry['address'].lower() == fixture['target'] and entry['topics'] == topics]
    check(len(records) == 1 and re.fullmatch(address_data, records[0]), 'missing exact asset creation event')
    contract = '0x' + records[0][26:66]
    asset = Asset(expected['chainId'], contract, kind, {'erc20': '0', 'erc721': '1', 'erc1155': '123'}[kind])
    extra = {}
    if kind != 'erc20':
        report['phase'] = 'media-signed-nft-mint'
        mint = sign_media_action(root, fixture_path, fixture, asset, source, env, report, refund='')
        extra['collectionHeight'] = int(receipt['blockNumber'], 16)
        receipt = commit_media_transaction(source, nodes, mint, 1, report)
        recipient = '0x' + fixture['creator'][2:].rjust(64, '0')
        zero = '0x' + '0' * 64
        token_word = f'{int(asset.token_id):064x}'
        expected_topics = [mint['event'], zero, recipient, '0x' + token_word]
        expected_data = '0x'
        if kind == 'erc1155':
            expected_topics = [mint['event'], recipient, zero, recipient]
            expected_data = f'0x{token_word}{2:064x}'
        check(sum(entry['address'].lower() == contract and entry['topics'] == expected_topics and
                  entry['data'] == expected_data for entry in receipt['logs']) == 1,
              'mint receipt does not credit the expected NFT to its creator')

    report['phase'] = 'media-https-publication-from-committed-uri'
    source_reader = CommittedReader(asset.chain_id, LocalRPC(f'http://127.0.0.1:{source.p2p + 1}'), LocalRPC(source.evm))
    check(source_reader(asset, fixture['uri']) == fixture['creator'], 'validator returned a different media authority')
    publication = rehearse_https(root, asset, fixture, metadata, image, witness, env, owner, report)
    if backed:
        extra['reserveRefund'] = rehearse_refund_guard(root, fixture_path, fixture, asset, nodes, env, report, receipt)
    report['committedMedia'] = {'status': 'PASS', 'kind': kind + ('-backed' if backed else '-reserveless'),
                                'assetTokenId': asset.token_id,
                                'receiptHeight': int(receipt['blockNumber'], 16), 'txHash': receipt['transactionHash'],
                                'alteredMetadataRejected': True, 'httpsVerified': True, 'restoredObjects': 2,
                                **extra, **publication}


def sign_media_action(root, fixture_path, fixture, asset, source, env, report, *, refund):
    output = fixture_path.with_name(f'refund-{refund}.json' if refund else 'mint.json')
    signer_env = dict(env, MCND_MEDIA_KIND=fixture['kind'], MCND_MEDIA_URI=fixture['uri'],
                      MCND_MEDIA_COLLECTION=asset.contract, MCND_MEDIA_REFUND=refund,
                      MCND_MEDIA_GENESIS=str(source.home / 'config/genesis.json'), MCND_MEDIA_FIXTURE=str(output))
    signer = str(Path(os.environ['MCND_MEDIA_SIGNER']).resolve(strict=True))
    subprocess.run([signer, '-test.run', '^TestExportLaunchMediaCreation$', '-test.timeout', '30s'],
                   cwd=root, env=signer_env, check=True, timeout=35)
    signed = read_media_fixture(output, report)
    check(tuple(signed[key] for key in ('kind', 'creator', 'uri', 'target')) ==
          (fixture['kind'], fixture['creator'], fixture['uri'], asset.contract),
          'signed action differs from the committed collection')
    return signed


def rehearse_refund_guard(root, fixture_path, fixture, asset, nodes, env, report, mint):
    report['phase'] = 'media-refund-backed-nft'
    source, witness = nodes[0], nodes[-1]
    creator, contract = fixture['creator'], asset.contract
    actor_word = creator[2:].rjust(64, '0')
    token_word = f'{int(asset.token_id):064x}'
    multi = asset.kind == 'erc1155'
    minted = 2 if multi else 1
    unit = 2 * 10**18

    def snapshot(node, height):
        owner_balance, custody = (int(ethereum('eth_getBalance', [address, hex(height)], base=node.evm), 16)
                                  for address in (creator, contract))
        calldata = ('0x00fdd58e' + actor_word + token_word) if multi else ('0x70a08231' + actor_word)
        units = read_words(contract, calldata, height, 1, node.evm)[0]
        supply = native('/cosmos/bank/v1beta1/supply/by_denom?denom=msp', height, base=node.rest)['amount']
        check(supply['denom'] == 'msp', 'refundable NFT supply query returned the wrong denom')
        return owner_balance, custody, units, int(supply['amount'])

    mint_height = int(mint['blockNumber'], 16)
    before, after = (snapshot(source, height) for height in (mint_height - 1, mint_height))
    fee = int(mint['gasUsed'], 16) * int(mint['effectiveGasPrice'], 16)
    check(before[1:3] == (0, 0) and after == (before[0] - minted * unit - fee, minted * unit, minted, before[3])
          and after == snapshot(witness, mint_height), 'NFT mint did not lock the exact MSP reserve')
    signed = sign_media_action(root, fixture_path, fixture, asset, source, env, report, refund='burn')
    receipt = commit_media_transaction(source, nodes, signed, 2, report, status=0)
    height = int(receipt['blockNumber'], 16)
    before, after = (snapshot(source, at) for at in (height - 1, height))
    fee = int(receipt['gasUsed'], 16) * int(receipt['effectiveGasPrice'], 16)
    check(not receipt['logs'] and before[1:3] == (minted * unit, minted) and
          after == (before[0] - fee, *before[1:]) and after == snapshot(witness, height),
          'published burn rejection changed custody, NFT ownership, native supply or a non-fee balance')
    return {'denom': 'msp', 'mintedUnits': minted, 'lockedAtomic': str(minted * unit),
            'bankSupplyUnchanged': True, 'observerMatched': True, 'status': 'REFUND_REJECTED',
            'height': height, 'refundedAtomic': '0', 'custodyAtomic': str(after[1]), 'remainingUnits': after[2]}


def read_media_fixture(path, report):
    fixture = json.loads(private_bytes(path, 4096))
    check((fixture['chainId'], fixture['genesisSha256']) == (report['chainId'], report['genesisSha256']),
          'media fixture is bound to a different isolated genesis')
    for key in ('creator', 'target'):
        check(re.fullmatch(ADDRESS, fixture[key]), 'invalid test fixture address')
        fixture[key] = fixture[key].lower()
    for key in ('event', 'hash'):
        check(re.fullmatch(WORD, fixture[key]), 'invalid test fixture hash')
    check(re.fullmatch(r'0x(?:[0-9a-f]{2}){100,1024}', fixture['raw']), 'invalid test fixture transaction')
    return fixture


def commit_media_transaction(source, nodes, fixture, nonce, report, *, status=1):
    check(int(ethereum('eth_getTransactionCount', [fixture['creator'], 'latest'], base=source.evm), 16) == nonce,
          'test media creator has an unexpected nonce')
    tx_hash = ethereum('eth_sendRawTransaction', [fixture['raw']], base=source.evm)
    check(tx_hash == fixture['hash'], 'media transaction hash differs from signed test fixture')
    deadline = time.monotonic() + 40
    receipt = None
    while time.monotonic() < deadline and receipt is None:
        check(source.child.poll() is None, 'validator stopped while waiting for media transaction')
        try:
            receipt = ethereum('eth_getTransactionReceipt', [tx_hash], base=source.evm)
        except (OSError, ValueError):
            receipt = None
        if receipt is None:
            time.sleep(0.25)
    check(receipt is not None, 'test token creation was not indexed')
    check(int(receipt['status'], 16) == status and receipt['transactionHash'] == tx_hash and
          receipt['from'].lower() == fixture['creator'] and receipt['to'].lower() == fixture['target'],
          'signed media transaction failed or receipt does not match')
    height = int(receipt['blockNumber'], 16)
    wait_replicas(nodes, height + 3, 65, report)
    check(committed_header(nodes, height)['block_hash'].lower() == receipt['blockHash'][2:].lower(),
          'media transaction receipt belongs to a different block')
    return receipt


def rehearse_https(root, asset, fixture, metadata, image, witness, env, owner, report):
    from mcnd_media.gateway_export import export_backup
    from mcnd_media.store import MediaStore, canonical, prepare_media
    from test_gateway import NGINX, gateway, loopback_certificate, request

    check(NGINX is not None, 'media rehearsal requires the isolated Nginx binary')
    store_root = root / 'committed-media'
    upstream = 8910
    media_env = dict(env, PYTHONPATH=str(MEDIA), MCND_MEDIA_STORE=str(store_root), MCND_MEDIA_CHAIN_ID=asset.chain_id,
                     MCND_MEDIA_COMET_RPC=f'http://127.0.0.1:{witness.p2p + 1}', MCND_MEDIA_EVM_RPC=witness.evm)
    argv = [sys.executable, '-B', '-m', 'gunicorn', '--bind', f'127.0.0.1:{upstream}',
            '--workers', '2', '--worker-class', 'sync', '--timeout', '60',
            '--no-control-socket', 'mcnd_media.api:from_environment()']
    certificate = loopback_certificate(root)
    context = ssl.create_default_context(cafile=str(certificate[0]))
    gunicorn = owner.spawn(argv, root / 'gunicorn-media.log', media_env)
    try:
        for _ in range(60):
            check(gunicorn.poll() is None, 'media Gunicorn exited before becoming ready')
            try:
                check(request(upstream, '/')[0] == 404, 'media Gunicorn returned an unexpected status')
                break
            except OSError:
                time.sleep(0.1)
        else:
            raise TimeoutError('media Gunicorn did not become ready')

        with gateway(root / 'nginx-media-live', root, MEDIA / 'nginx-live.conf',
                     MEDIA / 'nginx-media-limits.conf', certificate=certificate) as port:
            headers = {'Content-Type': 'application/json'}
            upload = {'kind': asset.kind, 'contract': asset.contract, 'token_id': asset.token_id, 'metadata': metadata,
                      'image_base64': base64.b64encode(image).decode('ascii'), 'mime': 'image/png'}
            altered = {**upload, 'metadata': {'name': 'Different bytes'}}
            metadata_path = '/ipfs/' + fixture['uri'].removeprefix('ipfs://')
            check(request(port, metadata_path, context=context)[0] == 404, 'media was published before upload')
            check(request(port, '/v1/publish', 'POST', canonical(altered), headers, context=context)[0] == 422,
                  'HTTPS gateway accepted metadata absent from the token')
            status, _, data = request(port, '/v1/publish', 'POST', canonical(upload), headers, context=context)
            check(status == 200, f'committed media publication returned HTTP {status}')
            publication = json.loads(data)
            check(publication['metadata_uri'] == fixture['uri'] and publication['owner'] == fixture['creator'] and
                  publication['version'] == 1, 'published media does not match signed transaction')
            status, _, retry = request(port, '/v1/publish', 'POST', canonical(upload), headers, context=context)
            check(status == 200 and retry == data, 'media retry did not preserve the original publication')
            picture, metadata_bytes = prepare_media(asset, metadata, image, 'image/png')
            objects = (
                (fixture['uri'].removeprefix('ipfs://'), 'application/json', metadata_bytes),
                (publication['image_uri'].removeprefix('ipfs://'), 'image/png', picture),
            )
            verify_https_objects(port, context, objects)
    finally:
        owner.stop(gunicorn)

    report['phase'] = 'media-https-restore-from-verified-backup'
    backup = root / 'committed-media.zip'
    with closing(MediaStore(store_root)) as store:
        check(len(store.verify()[0]) == 1, 'media retry left an extra stored version')
        backup_hash = store.backup(backup)
    store_root.rename(root / 'offline-media-store')
    restored = root / 'restored-media'
    check(export_backup(backup, backup_hash, restored)['objectCount'] == len(objects),
          'restored media object count differs')
    with gateway(root / 'nginx-media-restored', restored / 'public', certificate=certificate) as port:
        verify_https_objects(port, context, objects)
    return publication


def verify_https_objects(port, context, objects):
    from mcnd_media.ipfs import matches_cid
    from test_gateway import request

    for cid, mime, expected in objects:
        path = '/ipfs/' + cid
        status, headers, body = request(port, path, context=context)
        check(status == 200 and headers.get_content_type() == mime and body == expected and matches_cid(cid, body),
              'HTTPS gateway returned different bytes, CID or content type')
        status, headers, body = request(port, path, 'HEAD', context=context)
        check(status == 200 and not body and int(headers['Content-Length']) == len(expected),
              'HTTPS gateway returned incorrect HEAD metadata')
