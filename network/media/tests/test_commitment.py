import base64
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
import io
import json
import os
from pathlib import Path
import tempfile
from threading import Thread
import unittest
from contextlib import redirect_stdout
from unittest.mock import patch

from mcnd_media.commitment import CommittedReader, LocalRPC, main
from mcnd_media.store import Asset, MediaStore, canonical, cid


CHAIN = 'mintcandy_9905401-1'
TOKEN = '0x' + '22' * 20
NFT = '0x' + '33' * 20
FACTORY = '0x' + '77' * 20
CREATOR = '0x' + 'ab' * 20
HOLDER = '0x' + 'cd' * 20
NOW = datetime(2026, 11, 20, 10, 0, tzinfo=timezone.utc)
HASH = 'ef' * 32
CLEAN = b'normalized test image'
META = {'name': 'A token image'}
URI = 'ipfs://' + cid(canonical({'name': META['name'], 'image': 'ipfs://' + cid(CLEAN)}))


def b64(value):
    return base64.b64encode(value).decode('ascii')


def words(*values):
    return '0x' + ''.join(f'{value:064x}' for value in values)


def string(value):
    data = value.encode()
    return words(32, len(data)) + data.hex().ljust((len(data) + 31) // 32 * 64, '0')


class RPCFixture:
    def __init__(self):
        self.now = NOW
        self.head = 11
        self.catching_up = False
        self.eth_chain = hex(9905401)
        self.block_hash = HASH
        self.snapshot = 10
        self.uri = URI
        self.active = 1
        self.exists = True
        self.nft_kind = 0
        self.token_id = 1
        self.reserveless = True
        self.membership = True
        self.calls = []

    def comet(self, method, params):
        if method == 'status':
            return {'node_info': {'network': CHAIN},
                    'sync_info': {'latest_block_height': str(self.head), 'catching_up': self.catching_up}}
        if method == 'abci_info':
            return {'response': {'last_block_height': str(self.head)}}
        if method == 'commit' and params == {'height': str(self.snapshot)}:
            return {'canonical': True, 'signed_header': {
                'header': {'height': str(self.snapshot), 'chain_id': CHAIN, 'time': self.now.isoformat()},
                'commit': {'height': str(self.snapshot), 'block_id': {'hash': HASH.upper()}}}}
        if method == 'abci_query':
            assert params['path'] == '/store/validator/key' and params['height'] == str(self.snapshot)
            assert params['prove'] is False
            key = bytes.fromhex(params['data'])
            value = b''
            if key == b'\x6f':
                value = canonical({'nft_center': FACTORY})
            elif key == b'\x70' + bytes.fromhex(TOKEN[2:]) and self.membership and self.reserveless:
                value = b'\x01'
            elif key == b'\x80' + bytes.fromhex(TOKEN[2:]) and self.membership and not self.reserveless:
                value = bytes.fromhex(TOKEN[2:]) + b'\xfe' * 32
            return {'response': {'height': str(self.snapshot), 'key': b64(key), 'value': b64(value)}}
        raise AssertionError((method, params))

    def evm(self, method, params):
        if method == 'eth_chainId':
            return self.eth_chain
        if method == 'eth_blockNumber':
            return hex(self.head)
        if method == 'eth_getBlockByNumber' and params == [hex(self.snapshot), False]:
            return {'number': hex(self.snapshot), 'hash': '0x' + self.block_hash}
        if method == 'eth_call':
            target = params[0]['to']
            calldata = params[0]['data']
            self.calls.append((target, calldata))
            assert params[1] == hex(self.snapshot)
            token = f'{self.token_id:064x}'
            if target == FACTORY and calldata == '0x377cf68d' + NFT[2:].rjust(64, '0'):
                return words(self.active, self.nft_kind, 0)
            if (target, calldata) in [(TOKEN, '0x' + ('8da5cb5b' if self.reserveless else '02d05d3f')),
                                      (NFT, '0x02d05d3f')]:
                return words(int(CREATOR, 16))
            if target == NFT and calldata == '0x6352211e' + token:
                return words(int(HOLDER, 16) if self.exists else 0)
            if target == NFT and calldata == '0x4f558e79' + token:
                return words(int(self.exists))
            if (target, calldata) in [(TOKEN, '0x2c159a1a'), (NFT, '0xc87b56dd' + token),
                                      (NFT, '0x0e89341c' + token)]:
                return string(self.uri)
        raise AssertionError((method, params))


class CommittedTests(unittest.TestCase):
    def setUp(self):
        self.rpc = RPCFixture()
        self.reader = CommittedReader(CHAIN, self.rpc.comet, self.rpc.evm, now=lambda: NOW)

    def test_reads_owner_for_reserveless_and_creator_for_backed(self):
        for reserveless in (True, False):
            with self.subTest(reserveless=reserveless):
                self.rpc.reserveless = reserveless
                self.assertEqual(self.reader(Asset(CHAIN, TOKEN, 'erc20'), URI), CREATOR)

    def test_nft_binds_family_existence_and_full_uint256_id(self):
        erc721 = Asset(CHAIN, NFT, 'erc721', '1')
        self.assertEqual(self.reader(erc721, URI), CREATOR)
        self.rpc.nft_kind = 1
        self.rpc.token_id = 2**256 - 1
        erc1155 = Asset(CHAIN, NFT, 'erc1155', str(self.rpc.token_id))
        self.assertEqual(self.reader(erc1155, URI), CREATOR)
        self.assertIn((NFT, '0x0e89341c' + 'f' * 64), self.rpc.calls)
        with self.assertRaisesRegex(ValueError, 'family differs'):
            self.reader(erc721, URI)
        self.rpc.exists = False
        with self.assertRaisesRegex(ValueError, 'does not exist'):
            self.reader(erc1155, URI)
        self.rpc.active = 0
        with self.assertRaisesRegex(ValueError, 'not active'):
            self.reader(erc1155, URI)

    def test_unregistered_contract_is_rejected_before_eth_call(self):
        self.rpc.membership = False
        with self.assertRaisesRegex(ValueError, 'not admitted'):
            self.reader(Asset(CHAIN, TOKEN, 'erc20'), URI)
        self.assertEqual(self.rpc.calls, [])

    def test_mismatched_chain_sync_and_stale_or_different_block(self):
        asset = Asset(CHAIN, TOKEN, 'erc20')
        with self.assertRaisesRegex(ValueError, 'asset chain'):
            self.reader(Asset(CHAIN + '1', TOKEN, 'erc20'), URI)
        for key, value, reason in [
            ('catching_up', True, 'still syncing'),
            ('eth_chain', '0x1', 'EVM chain mismatch'),
            ('block_hash', 'ff' * 32, 'hashes differ'),
            ('now', NOW.replace(hour=9), 'stale'),
        ]:
            with self.subTest(key=key), patch.object(self.rpc, key, value):
                with self.assertRaisesRegex(ValueError, reason):
                    self.reader(asset, URI)
        self.assertEqual(self.rpc.calls, [])

    def test_publication_failure_is_atomic_and_retry_does_not_add_versions(self):
        asset = Asset(CHAIN, TOKEN, 'erc20')
        with tempfile.TemporaryDirectory() as tmp, patch('mcnd_media.store.decode_image', return_value=CLEAN):
            root = Path(tmp)
            store = MediaStore(root, commitment_reader=self.reader)
            try:
                with self.assertRaisesRegex(ValueError, 'does not commit'):
                    store.publish_committed(asset, {'name': 'changed'}, b'x', 'image/png')
                self.assertEqual(store.records(), [])
                self.assertEqual(list((root / 'objects').iterdir()), [])
                result = store.publish_committed(asset, META, b'x', 'image/png')
                self.assertEqual(result['owner'], CREATOR)
                self.assertEqual(result['metadata_uri'], URI)
                self.assertEqual(result['version'], 1)
                self.assertEqual(store.publish_committed(asset, META, b'x', 'image/png'), result)
                self.rpc.uri = URI + '/changed'
                with self.assertRaisesRegex(ValueError, 'does not commit'):
                    store.publish_committed(asset, META, b'x', 'image/png')
                self.assertEqual(len(store.verify()[0]), 1)
            finally:
                store.close()

    def test_client_only_accepts_operator_loopback_endpoints(self):
        for endpoint in ('https://example.com/rpc', 'http://127.0.0.1:8080/path',
                         'http://user@127.0.0.1:8080', 'http://127.0.0.1:70000'):
            with self.subTest(endpoint=endpoint), self.assertRaisesRegex(ValueError, 'loopback'):
                LocalRPC(endpoint)

    @unittest.skipUnless(os.environ.get('MCND_MEDIA_TEST_FULL') == '1', 'pinned image worker in Linux CI')
    def test_cli_prepares_and_publishes_through_local_rpc(self):
        from PIL import Image

        fixture = self.rpc
        fixture.now = datetime.now(timezone.utc)

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                req = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                call = fixture.evm if req['method'].startswith('eth_') else fixture.comet
                result = canonical({'jsonrpc': '2.0', 'id': req['id'], 'result': call(req['method'], req['params'])})
                self.send_response(200)
                self.send_header('Content-Length', str(len(result)))
                self.end_headers()
                self.wfile.write(result)

            def log_message(self, *_):
                pass

        with tempfile.TemporaryDirectory() as tmp, HTTPServer(('127.0.0.1', 0), Handler) as rpc:
            worker = Thread(target=rpc.serve_forever, daemon=True)
            worker.start()
            try:
                root = Path(tmp)
                (root / 'metadata.json').write_bytes(canonical(META))
                Image.new('RGB', (5, 5), '#00dd99').save(root / 'avatar.png')
                inputs = ['--chain-id', CHAIN, '--kind', 'erc20', '--metadata', str(root / 'metadata.json'),
                          '--image', str(root / 'avatar.png'), '--mime', 'image/png']
                prepared = io.StringIO()
                with redirect_stdout(prepared):
                    self.assertEqual(main(['prepare', *inputs]), 0)
                fixture.uri = json.loads(prepared.getvalue())['metadata_uri']
                endpoint = f'http://127.0.0.1:{rpc.server_port}'
                output = io.StringIO()
                with redirect_stdout(output):
                    self.assertEqual(main(['publish', *inputs, '--contract', TOKEN, '--comet-rpc', endpoint,
                                           '--evm-rpc', endpoint, '--store', str(root / 'store')]), 0)
                self.assertEqual(json.loads(output.getvalue())['metadata_uri'], fixture.uri)
                store = MediaStore(root / 'store')
                try:
                    rows, objects = store.verify()
                    self.assertEqual(len(rows), 1)
                    self.assertEqual(len(objects), 2)
                finally:
                    store.close()
            finally:
                rpc.shutdown()
                worker.join(timeout=5)


if __name__ == '__main__':
    unittest.main()
