from contextlib import contextmanager
import hashlib
import http.client
import io
import json
import os
from pathlib import Path
import random
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import unittest

from mcnd_media.gateway_export import export_backup
from mcnd_media.ipfs import matches_cid
from mcnd_media.store import Asset, MediaStore, cid


NGINX = os.environ.get('MCND_MEDIA_NGINX')
NGINX_RULES = Path(__file__).resolve().parents[1] / 'nginx-ipfs.conf'
OWNER = '0x' + '11' * 20
AVATAR = Asset('mintcandy_9905401-1', '0x' + '22' * 20, 'erc20')
NFT = Asset('mintcandy_9905401-1', '0x' + '33' * 20, 'erc1155', '1024')


def loopback_certificate(root):
    cert, key = root / 'cert.pem', root / 'key.pem'
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                    '-subj', '/CN=media-test.invalid', '-addext', 'subjectAltName=IP:127.0.0.1',
                    '-keyout', str(key), '-out', str(cert)], check=True, capture_output=True, timeout=10)
    return cert, key


def request(port, path, method='GET', data=None, headers=None, *, context=None):
    conn = (http.client.HTTPConnection('127.0.0.1', port, timeout=4) if context is None
            else http.client.HTTPSConnection('127.0.0.1', port, timeout=4, context=context))
    try:
        conn.request(method, path, body=data, headers=headers or {})
        response = conn.getresponse()
        return response.status, response.headers, response.read()
    finally:
        conn.close()


@contextmanager
def gateway(root, public, rules=NGINX_RULES, http_rules=None, *, certificate=None):
    root.mkdir()
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        port = listener.getsockname()[1]
    config = root / 'nginx.conf'
    http_include = f'include "{http_rules}";' if http_rules else ''
    tls_options = ''
    tls_identity = ''
    listen_suffix = ''
    context = None
    if certificate is not None:
        cert, key = certificate
        tls_options = 'ssl_protocols TLSv1.2 TLSv1.3;'
        tls_identity = f'ssl_certificate "{cert}"; ssl_certificate_key "{key}";'
        listen_suffix = ' ssl'
        context = ssl.create_default_context(cafile=str(cert))
    config.write_text(f'''daemon off;
master_process off;
pid "{root}/nginx.pid";
lock_file "{root}/nginx.lock";
error_log stderr;
events {{ worker_connections 32; }}
http {{
    {http_include}
    {tls_options}
    access_log off;
    client_body_temp_path client-body;
    proxy_temp_path proxy;
    fastcgi_temp_path fastcgi;
    uwsgi_temp_path uwsgi;
    scgi_temp_path scgi;
    server {{
        listen 127.0.0.1:{port}{listen_suffix};
        {tls_identity}
        root "{public}";
        include "{rules}";
    }}
}}
''', encoding='utf8')
    argv = [NGINX, '-p', str(root) + '/', '-c', str(config)]
    subprocess.run([*argv, '-t'], check=True, capture_output=True, timeout=10)
    with subprocess.Popen(argv, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE) as process:
        try:
            for _ in range(40):
                if process.poll() is not None:
                    raise RuntimeError(process.stderr.read().decode(errors='replace'))
                try:
                    request(port, '/', context=context)
                    break
                except OSError:
                    time.sleep(0.1)
            else:
                raise RuntimeError('isolated Nginx listener did not start')
            yield port
        finally:
            if process.poll() is None:
                process.terminate()
            try:
                process.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.communicate(timeout=5)


class ExportChecks(unittest.TestCase):
    def test_requires_trusted_hash_and_fresh_destination(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            store = MediaStore(root / 'primary')
            backup = root / 'backup.zip'
            try:
                checksum = store.backup(backup)
            finally:
                store.close()
            target = root / 'gateway'
            with self.assertRaisesRegex(ValueError, 'backup SHA256 mismatch'):
                export_backup(backup, '0' * 64, target)
            self.assertFalse(target.exists())
            report = export_backup(backup, checksum, target)
            self.assertEqual(report['objectCount'], 0)
            self.assertEqual(list((target / 'public/ipfs').iterdir()), [])
            manifest = (target / 'EXPORT.json').read_bytes()
            with self.assertRaisesRegex(ValueError, 'destination must be new'):
                export_backup(backup, checksum, target)
            self.assertEqual((target / 'EXPORT.json').read_bytes(), manifest)


@unittest.skipUnless(NGINX and os.environ.get('MCND_MEDIA_TEST_FULL') == '1', 'requires Nginx and pinned decoder')
class GatewayChecks(unittest.TestCase):
    def test_http_roundtrip_after_loss_of_primary_store_and_gateway(self):
        from PIL import Image

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            backup = root / 'backup.zip'
            original = root / 'original'
            recovered = root / 'recovered'
            source = root / 'primary'
            store = MediaStore(source, lambda asset, actor, action: actor == OWNER)
            try:
                avatar = io.BytesIO()
                Image.new('RGB', (7, 6), 'blue').save(avatar, format='JPEG')
                store.publish(AVATAR, OWNER, {'name': 'Token avatar'}, avatar.getvalue(), 'image/jpeg')
                image = io.BytesIO()
                size = (1100, 900)
                Image.frombytes('RGB', size, random.Random(462).randbytes(size[0] * size[1] * 3)).save(image, 'PNG')
                published_nft = store.publish(NFT, OWNER, {'name': 'NFT metadata'}, image.getvalue(), 'image/png')
                self.assertTrue(published_nft['image_uri'].startswith('ipfs://bafybei'))
                checksum = store.backup(backup)
            finally:
                store.close()

            report = export_backup(backup, checksum, original)
            with gateway(root / 'nginx-original', original / 'public') as port:
                published = self.assert_export(port, report)

            # Make the primary store and first export unavailable at their old paths.
            source.rename(root / 'offline-store')
            original.rename(root / 'offline-export')
            output = subprocess.run([sys.executable, '-B', '-m', 'mcnd_media.gateway_export',
                                     '--backup', str(backup), '--backup-sha256', checksum,
                                     '--destination', str(recovered)], check=True, capture_output=True, text=True, timeout=30)
            self.assertEqual(json.loads(output.stdout)['objects'], 4)
            self.assertEqual(json.loads((recovered / 'EXPORT.json').read_bytes()), report)
            with gateway(root / 'nginx-restored', recovered / 'public') as port:
                self.assertEqual(self.assert_export(port, report), published)

    def assert_export(self, port, report):
        objects = {}
        for row in report['objects']:
            status, headers, body = request(port, row['path'])
            self.assertEqual(status, 200)
            self.assertEqual(headers.get_content_type(), row['contentType'])
            self.assertEqual(headers['X-Content-Type-Options'], 'nosniff')
            self.assertEqual(headers['Access-Control-Allow-Origin'], '*')
            self.assertIn('immutable', headers['Cache-Control'])
            self.assertTrue(matches_cid(row['cid'], body))
            self.assertEqual(hashlib.sha256(body).hexdigest(), row['sha256'])
            self.assertEqual(len(body), row['sizeBytes'])
            head, head_headers, empty = request(port, row['path'], 'HEAD')
            self.assertEqual((head, empty), (200, b''))
            self.assertEqual(int(head_headers['Content-Length']), len(body))
            self.assertEqual(request(port, row['path'], 'POST')[0], 403)
            objects[row['cid']] = (row['contentType'], body)
        for path in ('/', '/EXPORT.json', '/index.sqlite3', f'/ipfs/{cid(b"missing")}', report['objects'][0]['path'] + '.png'):
            self.assertEqual(request(port, path)[0], 404)
        metadata = [json.loads(body) for mime, body in objects.values() if mime == 'application/json']
        self.assertEqual({record['name'] for record in metadata}, {'Token avatar', 'NFT metadata'})
        for record in metadata:
            self.assertEqual(objects[record['image'].removeprefix('ipfs://')][0], 'image/png')
        return objects


if __name__ == '__main__':
    unittest.main()
