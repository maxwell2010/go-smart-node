"""Rehearse a signed avatar commitment against the attested disposable daemon."""

import base64
from contextlib import closing
import json
from pathlib import Path
import re
import ssl
import sys
import time

from native_consensus_smoke import committed_header, wait_replicas
from withdrawal_genesis_smoke import ADDRESS, WORD, check, ethereum, private_bytes


MEDIA = Path(__file__).resolve().parents[1] / 'media'


def rehearse_media(root, expected, nodes, fixture_path, report, env, owner):
    check(expected['chainId'] == 'mintcandy_990014-1' and expected['evmChainId'] == 990014 and
          report['loopbackOnlyNamespace'] and report['offlineAssembledGenesis'],
          'media transaction fixture requires the private signing domain')
    sys.path[:0] = [str(MEDIA), str(MEDIA / 'tests')]
    from mcnd_media.commitment import CommittedReader, LocalRPC
    from mcnd_media.store import Asset, strict

    report['phase'] = 'media-signed-token-creation'
    fixture = json.loads(private_bytes(fixture_path, 4096))
    for key in ('creator', 'factory'):
        check(re.fullmatch(ADDRESS, fixture[key]), 'invalid test fixture address')
        fixture[key] = fixture[key].lower()
    for key in ('event', 'hash'):
        check(re.fullmatch(WORD, fixture[key]), 'invalid test fixture hash')
    check(re.fullmatch(r'0x(?:[0-9a-f]{2}){100,1024}', fixture['raw']), 'invalid test fixture transaction')
    metadata = strict(private_bytes(fixture_path.parent / 'metadata.json', 64 << 10))
    image = private_bytes(fixture_path.parent / 'avatar.png', 1 << 20)
    source, witness = nodes[0], nodes[-1]
    check(int(ethereum('eth_getTransactionCount', [fixture['creator'], 'latest'], base=source.evm), 16) == 0,
          'test media creator unexpectedly has an existing nonce')
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
    check(int(receipt['status'], 16) == 1 and receipt['transactionHash'] == tx_hash and
          receipt['from'].lower() == fixture['creator'] and receipt['to'].lower() == fixture['factory'],
          'signed token creation failed or receipt does not match')
    records = [entry['data'] for entry in receipt['logs']
               if entry['address'].lower() == fixture['factory'] and entry['topics'] == [fixture['event']]]
    check(len(records) == 1 and re.fullmatch(r'0x0{24}[0-9a-f]{40}', records[0]),
          'missing exact reserveless creation event')
    contract = '0x' + records[0][-40:]
    height = int(receipt['blockNumber'], 16)
    wait_replicas(nodes, height + 3, 65, report)
    check(committed_header(nodes, height)['block_hash'].lower() == receipt['blockHash'][2:].lower(),
          'created token receipt belongs to a different block')

    report['phase'] = 'media-https-publication-from-committed-uri'
    asset = Asset(expected['chainId'], contract, 'erc20')
    source_reader = CommittedReader(asset.chain_id, LocalRPC(f'http://127.0.0.1:{source.p2p + 1}'), LocalRPC(source.evm))
    check(source_reader(asset, fixture['uri']) == fixture['creator'], 'validator returned a different media authority')
    publication = rehearse_https(root, asset, fixture, metadata, image, witness, env, owner, report)
    report['committedMedia'] = {'status': 'PASS', 'kind': 'erc20-reserveless', 'receiptHeight': height,
                                'txHash': tx_hash, 'alteredMetadataRejected': True, 'httpsVerified': True,
                                'restoredObjects': 2, **publication}


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
            upload = {'kind': 'erc20', 'contract': asset.contract, 'token_id': '0', 'metadata': metadata,
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
