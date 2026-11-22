from contextlib import closing
from datetime import datetime, timezone
import http.client
import io
import json
import os
from pathlib import Path
import socket
import stat
import subprocess
import time
import unittest

from mcnd_media.store import canonical
from test_api import upload
from test_commitment import CHAIN, TOKEN, RPCFixture, serve_rpc


def request(path, body=None):
    with closing(http.client.HTTPConnection('127.0.0.1', 8910, timeout=4)) as connection:
        method = 'GET' if body is None else 'POST'
        connection.request(method, path, body, {'Content-Type': 'application/json'})
        response = connection.getresponse()
        return response.status, response.read()


@unittest.skipUnless(os.environ.get('MCND_MEDIA_SYSTEMD_CI') == '1', 'requires a disposable Linux system service')
class MediaSystemdTest(unittest.TestCase):
    def control(self, *arguments):
        return subprocess.check_output(['systemctl', *arguments, 'mcnd-media.service'], text=True, timeout=30).strip()

    def ready(self):
        for _ in range(60):
            try:
                status, _ = request('/')
            except OSError:
                time.sleep(0.1)
                continue
            self.assertEqual(status, 404)
            return
        self.fail('media service did not start')

    def test_published_bytes_and_version_survive_restart(self):
        import pwd
        from PIL import Image

        self.assertEqual([name for _, name in socket.if_nameindex()], ['lo'])
        fixture = RPCFixture()
        fixture.now = datetime.now(timezone.utc)
        output = io.BytesIO()
        Image.new('RGB', (10, 10), '#4422ee').save(output, 'PNG')
        original = upload(output.getvalue())
        with serve_rpc(fixture) as endpoint:
            config = Path('/etc/mcnd-media.env')
            config.write_text(f'MCND_MEDIA_CHAIN_ID={CHAIN}\nMCND_MEDIA_COMET_RPC={endpoint}\nMCND_MEDIA_EVM_RPC={endpoint}\n')
            config.chmod(0o600)
            self.addCleanup(lambda: self.control('stop'))
            self.control('start')
            self.ready()
            self.assertEqual(self.control('show', '-p', 'User', '--value'), 'mcnd-media')
            pid = int(self.control('show', '-p', 'MainPID', '--value'))
            service_uid = pwd.getpwnam('mcnd-media').pw_uid
            self.assertNotEqual(service_uid, 0)
            self.assertEqual(Path(f'/proc/{pid}').stat().st_uid, service_uid)

            status, response = request('/v1/prepare', canonical(original))
            self.assertEqual(status, 200)
            fixture.uri = json.loads(response)['metadata_uri']
            meta_path = '/ipfs/' + fixture.uri.removeprefix('ipfs://')
            self.assertEqual(request(meta_path)[0], 404)

            publication = {**original, 'contract': TOKEN, 'token_id': '0'}
            self.assertEqual(request('/v1/publish', canonical({**publication, 'metadata': {'name': 'Changed'}}))[0], 422)
            status, response = request('/v1/publish', canonical(publication))
            self.assertEqual(status, 200)
            published = json.loads(response)
            self.assertEqual(published['metadata_uri'], fixture.uri)
            self.assertEqual(published['version'], 1)
            picture_path = '/ipfs/' + published['image_uri'].removeprefix('ipfs://')
            metadata, picture = request(meta_path), request(picture_path)
            self.assertEqual((metadata[0], picture[0]), (200, 200))

            self.control('restart')
            self.ready()
            self.assertNotEqual(int(self.control('show', '-p', 'MainPID', '--value')), pid)
            self.assertEqual((request(meta_path), request(picture_path)), (metadata, picture))
            self.assertEqual(request('/v1/publish', canonical(publication)), (200, response))
            state = Path('/var/lib/mcnd-media')
            self.assertEqual(state.stat().st_uid, service_uid)
            self.assertEqual(stat.S_IMODE(state.stat().st_mode), 0o700)
            self.assertEqual(stat.S_IMODE((state / 'index.sqlite3').stat().st_mode), 0o600)
