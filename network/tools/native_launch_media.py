"""Rehearse a signed avatar commitment against the attested disposable daemon."""

import json
from pathlib import Path
import re
import sys
import time

from native_consensus_smoke import committed_header, wait_replicas
from withdrawal_genesis_smoke import ADDRESS, WORD, check, ethereum, private_bytes


def rehearse_media(root, expected, nodes, fixture_path, report):
    check(expected['chainId'] == 'mintcandy_990014-1' and expected['evmChainId'] == 990014 and
          report['loopbackOnlyNamespace'] and report['offlineAssembledGenesis'],
          'media transaction fixture requires the private signing domain')
    sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'media'))
    from mcnd_media.commitment import CommittedReader, LocalRPC
    from mcnd_media.store import Asset, MediaStore, strict

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

    report['phase'] = 'media-publication-from-committed-uri'
    asset = Asset(expected['chainId'], contract, 'erc20')
    def reader(node):
        return CommittedReader(asset.chain_id, LocalRPC(f'http://127.0.0.1:{node.p2p + 1}'), LocalRPC(node.evm))

    check(reader(source)(asset, fixture['uri']) == fixture['creator'], 'validator returned a different media authority')
    store = MediaStore(root / 'committed-media', commitment_reader=reader(witness))
    try:
        publication = store.publish_committed(asset, metadata, image, 'image/png')
        check(publication['metadata_uri'] == fixture['uri'] and publication['owner'] == fixture['creator'],
              'published media does not match signed transaction')
        try:
            store.publish_committed(asset, {'name': 'Different bytes'}, image, 'image/png')
        except ValueError as error:
            check('does not commit to this content' in str(error), 'modified metadata failed for an unexpected reason')
        else:
            raise ValueError('the media store accepted uncommitted metadata')
        check(store.publish_committed(asset, metadata, image, 'image/png') == publication and
              len(store.verify()[0]) == 1, 'media retry created an extra stored version')
    finally:
        store.close()
    report['committedMedia'] = {'status': 'PASS', 'kind': 'erc20-reserveless', 'receiptHeight': height,
                                'txHash': tx_hash, 'alteredMetadataRejected': True, **publication}
