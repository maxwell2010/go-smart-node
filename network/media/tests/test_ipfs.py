from contextlib import ExitStack, closing, contextmanager
import hashlib
import io
import json
import os
from pathlib import Path
import random
import socket
import subprocess
from tempfile import TemporaryDirectory
import time
import unittest
from unittest.mock import patch

from mcnd_media.api import create_app
from mcnd_media.gateway_export import export_backup, PNG_HEADER
from mcnd_media.ipfs import RAW_BLOCK_LIMIT, cid, matches_cid, object_cid
from mcnd_media.pin_backup import pin_backup
from mcnd_media.store import Asset, CommitmentMismatch, MediaStore, canonical, prepare_media
from test_api import wsgi_request


NFT = Asset('mintcandy_9905401-1', '0x' + '13' * 20, 'erc721', '1')
LEGACY_NFT = Asset(NFT.chain_id, NFT.contract, NFT.kind, '2')
OWNER = '0x' + '42' * 20
META = {'name': 'Independent collection'}
KUBO = os.environ.get('MCND_MEDIA_KUBO')


def fixture_reader(expected):
    def verify(asset, uri):
        if expected[asset] != uri:
            raise CommitmentMismatch('asset URI does not commit to this content')
        return OWNER
    return verify


@contextmanager
def loopback_peer(root, tag, port):
    repo = root / tag
    env = dict(os.environ, IPFS_PATH=str(repo))
    if not (repo / 'config').exists():
        subprocess.run([KUBO, 'init', '--profile=test'], env=env, capture_output=True, check=True, timeout=30)
    config_path = repo / 'config'
    settings = json.loads(config_path.read_text())
    settings['Bootstrap'] = []
    settings.setdefault('Routing', {})['Type'] = 'none'
    settings.setdefault('Discovery', {}).setdefault('MDNS', {})['Enabled'] = False
    settings['Addresses'].update(Swarm=[f'/ip4/127.0.0.1/tcp/{port}'],
                                 API=f'/ip4/127.0.0.1/tcp/{port + 1}', Gateway='')
    config_path.write_text(json.dumps(settings))
    with (root / f'{tag}.log').open('wb') as log:
        with subprocess.Popen([KUBO, 'daemon'], env=env, stdout=log, stderr=subprocess.STDOUT) as peer:
            try:
                for _ in range(100):
                    if (repo / 'api').is_file():
                        ready = subprocess.run([KUBO, '--api', f'/ip4/127.0.0.1/tcp/{port + 1}', 'id'],
                                               env=env, capture_output=True, timeout=3)
                        if ready.returncode == 0:
                            break
                    if peer.poll() is not None:
                        raise RuntimeError((root / f'{tag}.log').read_text())
                    time.sleep(0.05)
                else:
                    raise RuntimeError('Kubo loopback peer did not start')
                yield env, settings['Identity']['PeerID']
            finally:
                if peer.poll() is None:
                    peer.terminate()
                    try:
                        peer.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        peer.kill()
                        peer.wait(timeout=5)


class IPFSMediaTests(unittest.TestCase):
    def test_chunked_and_legacy_kept_through_commit_restore_and_http(self):
        small = b'x' * RAW_BLOCK_LIMIT
        self.assertEqual(object_cid(small), cid(small))
        image = PNG_HEADER + small
        modern, legacy = object_cid(image), cid(image)
        self.assertTrue(modern.startswith('bafybei'))
        self.assertTrue(matches_cid(legacy, image))
        self.assertFalse(matches_cid(modern, image[:-1] + b'y'))
        names = {asset: 'ipfs://' + cid(canonical({**META, 'image': 'ipfs://' + name}))
                 for asset, name in ((NFT, modern), (LEGACY_NFT, legacy))}
        reader = fixture_reader(names)
        with TemporaryDirectory() as tmp, patch('mcnd_media.store.decode_image', return_value=image):
            root = Path(tmp)
            source = root / 'store'
            archive = root / 'backup.zip'
            with closing(MediaStore(source, commitment_reader=reader)) as store:
                for asset, name in ((NFT, modern), (LEGACY_NFT, legacy)):
                    published = store.publish_committed(asset, META, image, 'image/png')
                    self.assertEqual(published['metadata_uri'], names[asset])
                    self.assertEqual(published['image_uri'], 'ipfs://' + name)
                    self.assertEqual(store.publish_committed(asset, META, image, 'image/png')['version'], 1)
                digest = store.backup(archive)
            report = export_backup(archive, digest, root / 'export')
            self.assertEqual(report['objectCount'], 4)
            app = create_app(source, NFT.chain_id, reader)
            for name in (modern, legacy):
                reply, body = wsgi_request(app, '/ipfs/' + name)
                self.assertEqual(reply['status'], 200)
                self.assertEqual(body, image)
                self.assertEqual((root / 'export' / 'public' / 'ipfs' / (name + '.png')).read_bytes(), image)

    @unittest.skipUnless(KUBO and os.environ.get('MCND_MEDIA_TEST_FULL') == '1', 'requires pinned Linux Kubo')
    def test_kubo_pins_chunked_png_and_raw_metadata(self):
        from PIL import Image

        width, height = 1100, 900
        raw = io.BytesIO()
        Image.frombytes('RGB', (width, height), random.Random(8238).randbytes(width * height * 3)).save(raw, 'PNG')
        image, metadata = prepare_media(NFT, META, raw.getvalue(), 'image/png')
        self.assertGreater(len(image), RAW_BLOCK_LIMIT)
        expected = object_cid(image)
        self.assertTrue(expected.startswith('bafybei'))
        meta_name = cid(metadata)
        old_image_name = cid(image)
        old_meta_name = cid(canonical({**META, 'image': 'ipfs://' + old_image_name}))
        reader = fixture_reader({NFT: 'ipfs://' + meta_name, LEGACY_NFT: 'ipfs://' + old_meta_name})
        with TemporaryDirectory() as tmp:
            root = Path(tmp)
            repo, archive = root / 'ipfs-private', root / 'backup.zip'
            with closing(MediaStore(root / 'media-private', commitment_reader=reader)) as store:
                store.publish_committed(NFT, META, raw.getvalue(), 'image/png')
                store.publish_committed(LEGACY_NFT, META, raw.getvalue(), 'image/png')
                digest = store.backup(archive)
            env = dict(os.environ, IPFS_PATH=str(repo))
            subprocess.run([KUBO, 'init', '--profile=test'], env=env, capture_output=True, check=True, timeout=30)
            report = pin_backup(archive, digest, Path(KUBO), repo)
            self.assertCountEqual(report['pinned'], [expected, meta_name, old_meta_name])
            self.assertEqual(report['legacyRawHttpOnly'], [old_image_name])
            pulled = subprocess.check_output([KUBO, '--offline', 'cat', expected], env=env, timeout=30)
            self.assertEqual(hashlib.sha256(pulled).digest(), hashlib.sha256(image).digest())
            self.assertEqual([name for _, name in socket.if_nameindex()], ['lo'])
            with ExitStack() as peers:
                _, provider_id = peers.enter_context(loopback_peer(root, 'ipfs-private', 14801))
                receiver, _ = peers.enter_context(loopback_peer(root, 'ipfs-receiver', 14811))
                endpoint = f'/ip4/127.0.0.1/tcp/14801/p2p/{provider_id}'
                subprocess.run([KUBO, 'swarm', 'connect', endpoint], env=receiver,
                               capture_output=True, check=True, timeout=15)
                received_metadata = subprocess.check_output([KUBO, 'block', 'get', meta_name], env=receiver, timeout=15)
                received_image = subprocess.check_output([KUBO, 'cat', expected], env=receiver, timeout=20)
                self.assertEqual(received_metadata, metadata)
                self.assertEqual(received_image, image)
