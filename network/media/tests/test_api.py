import base64
from contextlib import closing
import io
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import sys
from tempfile import TemporaryDirectory
import time
import unittest
from unittest.mock import patch

from mcnd_media.api import BODY_LIMIT, create_app
from mcnd_media.store import Asset, MediaStore, canonical, cid, prepare_media
from test_commitment import CHAIN, CREATOR, META, TOKEN
from test_gateway import NGINX, gateway, loopback_certificate, request


MEDIA_ROOT = Path(__file__).resolve().parents[1]
TEST_PNG = b'\x89PNG\r\n\x1a\nfixture'


def upload(image):
    return {'image_base64': base64.b64encode(image).decode(), 'metadata': META,
            'mime': 'image/png', 'kind': 'erc20'}


def wsgi_request(app, path, method='GET', body=b'', **overrides):
    response = {}
    env = {'PATH_INFO': path, 'REQUEST_METHOD': method, 'CONTENT_TYPE': 'application/json',
           'CONTENT_LENGTH': str(len(body)), 'wsgi.input': io.BytesIO(body)}
    env.update(overrides)

    def start_response(status, headers):
        response.update(status=int(status.split()[0]), headers=dict(headers))

    return response, b''.join(app(env, start_response))


def build_test_app():
    uri = os.environ['TEST_AUTHORIZED_URI']

    def authority(asset, requested_uri):
        if asset.contract != TOKEN or requested_uri != uri:
            raise ValueError('mismatch')
        return CREATOR

    return create_app(Path(os.environ['TEST_STORE']), CHAIN, authority)


class ApiChecks(unittest.TestCase):
    def test_publish_and_read_require_committed_record(self):
        with TemporaryDirectory() as directory, patch('mcnd_media.store.decode_image', return_value=TEST_PNG):
            root = Path(directory)
            _, prepared = prepare_media(Asset(CHAIN, TOKEN, 'erc20'), META, TEST_PNG, 'image/png')
            name = cid(prepared)

            def reader(asset, uri):
                self.assertEqual(asset.contract, TOKEN)
                if uri != 'ipfs://' + name:
                    raise ValueError('mismatch')
                return CREATOR

            app = create_app(root, CHAIN, reader)
            staged_path = root / 'objects' / name
            staged_path.write_bytes(prepared[:5])
            response, _ = wsgi_request(app, '/ipfs/' + name)
            self.assertEqual(response['status'], 404)
            body = {**upload(TEST_PNG), 'contract': TOKEN, 'token_id': '0'}
            altered = {**body, 'metadata': {'name': 'changed'}}
            response, _ = wsgi_request(app, '/v1/publish', 'POST', canonical(altered))
            self.assertEqual(response['status'], 422)
            self.assertEqual(staged_path.read_bytes(), prepared[:5])
            response, payload = wsgi_request(app, '/v1/publish', 'POST', canonical(body))
            self.assertEqual(response['status'], 200)
            self.assertEqual(json.loads(payload)['version'], 1)
            response, payload = wsgi_request(app, '/ipfs/' + name)
            self.assertEqual(response['status'], 200)
            self.assertEqual(response['headers']['Content-Type'], 'application/json')
            self.assertEqual(payload, prepared)
            staged_path.write_bytes(b'changed')
            response, _ = wsgi_request(app, '/ipfs/' + name)
            self.assertEqual(response['status'], 503)
            self.assertEqual(response['headers']['Cache-Control'], 'no-store')

    def test_rejects_malformed_or_oversized_requests_before_decoding(self):
        with TemporaryDirectory() as directory, patch('mcnd_media.store.decode_image') as decode:
            app = create_app(Path(directory), CHAIN, fail_if_called)
            for status, body, env in (
                (413, b'', {'CONTENT_LENGTH': str(BODY_LIMIT + 1)}),
                (411, b'', {'CONTENT_LENGTH': ''}),
                (415, b'{}', {'HTTP_CONTENT_ENCODING': 'gzip'}),
                (400, b'{"kind":"erc20","kind":"erc721"}', {}),
                (400, b'{}', {'CONTENT_LENGTH': '5'}),
                (413, canonical(upload(b'x' * ((1 << 20) + 1))), {}),
                (400, canonical({**upload(b'x'), 'metadata': {'name': ''}}), {}),
                (400, canonical({**upload(b'x'), 'metadata': {'name': ' \u00a0\u200d'}}), {}),
                (400, canonical({**upload(b'x'), 'metadata': {'name': 'A\u202eB'}}), {}),
            ):
                with self.subTest(status=status, env=env):
                    response, _ = wsgi_request(app, '/v1/prepare', 'POST', body, **env)
                    self.assertEqual(response['status'], status)
            decode.assert_not_called()


def fail_if_called(*_):
    raise AssertionError('malformed requests must never hit RPC')


@unittest.skipUnless(NGINX and os.environ.get('MCND_MEDIA_TEST_FULL') == '1', 'requires real HTTPS stack')
class HttpsApiChecks(unittest.TestCase):
    def test_prepare_publish_and_serve_through_nginx_and_gunicorn(self):
        from PIL import Image

        with TemporaryDirectory() as directory:
            root = Path(directory)
            cert, key = loopback_certificate(root)
            trusted = ssl.create_default_context(cafile=str(cert))
            picture = io.BytesIO()
            Image.new('RGB', (5, 5), '#0055ee').save(picture, format='PNG')
            raw = picture.getvalue()
            normalized, metadata = prepare_media(Asset(CHAIN, TOKEN, 'erc20'), META, raw, 'image/png')
            uri = 'ipfs://' + cid(metadata)
            store_root = root / 'private'
            live = root / 'nginx-live.conf'
            with socket.socket() as sock:
                sock.bind(('127.0.0.1', 0))
                listen = sock.getsockname()[1]
            live.write_text((MEDIA_ROOT / 'nginx-live.conf').read_text().replace(':8910', f':{listen}'))
            env = dict(os.environ, TEST_STORE=str(store_root), TEST_AUTHORIZED_URI=uri,
                       PYTHONPATH=os.pathsep.join((str(MEDIA_ROOT), str(MEDIA_ROOT / 'tests'))))
            argv = [sys.executable, '-B', '-m', 'gunicorn', '--bind', f'127.0.0.1:{listen}',
                    '--workers', '2', '--worker-class', 'sync', '--no-control-socket', 'test_api:build_test_app()']
            with subprocess.Popen(argv, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE) as server:
                try:
                    for _ in range(40):
                        if server.poll() is not None:
                            self.fail(server.stderr.read().decode())
                        try:
                            self.assertEqual(request(listen, '/')[0], 404)
                            break
                        except OSError:
                            time.sleep(0.1)
                    else:
                        self.fail('Gunicorn did not listen')
                    with gateway(root / 'proxy', root, live, MEDIA_ROOT / 'nginx-media-limits.conf',
                                 certificate=(cert, key)) as port:
                        with self.assertRaises(ssl.SSLCertVerificationError):
                            request(port, '/', context=ssl.create_default_context())
                        with socket.create_connection(('127.0.0.1', port), timeout=4) as connection:
                            with self.assertRaises(ssl.SSLCertVerificationError):
                                trusted.wrap_socket(connection, server_hostname='wrong-media-host.invalid')
                        for version in (ssl.TLSVersion.TLSv1_2, ssl.TLSVersion.TLSv1_3):
                            context = ssl.create_default_context(cafile=str(cert))
                            context.minimum_version = context.maximum_version = version
                            self.assertEqual(request(port, '/', context=context)[0], 404)
                        self.assertEqual(request(port, '/v1/publish', method='POST')[0], 400)

                        def secure(path, method='GET', data=None, headers=None):
                            return request(port, path, method, data, headers, context=trusted)

                        headers = {'Content-Type': 'application/json'}
                        body = upload(raw)
                        code, _, result = secure('/v1/prepare', 'POST', canonical(body), headers)
                        self.assertEqual(code, 200)
                        self.assertEqual(json.loads(result)['metadata_uri'], uri)
                        metadata_path = '/ipfs/' + cid(metadata)
                        self.assertEqual(secure(metadata_path)[0], 404)
                        publish = {**body, 'contract': TOKEN, 'token_id': '0'}
                        altered = {**publish, 'metadata': {'name': 'Unauthorized alternative'}}
                        self.assertEqual(secure('/v1/publish', 'POST', canonical(altered), headers)[0], 422)
                        code, _, result = secure('/v1/publish', 'POST', canonical(publish), headers)
                        self.assertEqual(code, 200)
                        self.assertEqual(json.loads(result)['version'], 1)
                        self.assertEqual(secure('/v1/publish', 'POST', canonical(publish), headers)[2], result)
                        for path, expected, mime in ((metadata_path, metadata, 'application/json'),
                                                       ('/ipfs/' + cid(normalized), normalized, 'image/png')):
                            code, response_headers, payload = secure(path)
                            self.assertEqual((code, payload), (200, expected))
                            self.assertEqual(response_headers['Content-Type'], mime)
                            self.assertEqual(response_headers['Access-Control-Allow-Origin'], '*')
                            self.assertIn('immutable', response_headers['Cache-Control'])
                            self.assertEqual(secure(path, 'HEAD')[2], b'')
                        self.assertEqual(secure('/index.sqlite3')[0], 404)
                        self.assertEqual(secure('/key.pem')[0], 404)
                        self.assertEqual(secure('/v1/prepare', 'POST', b'', {
                            **headers, 'Content-Length': str(BODY_LIMIT + 1),
                        })[0], 413)
                        attempts = [secure('/v1/prepare', 'POST', b'{}', headers)[0] for _ in range(7)]
                        self.assertIn(429, attempts)
                    with closing(MediaStore(store_root)) as store:
                        self.assertEqual(len(store.verify()[0]), 1)
                finally:
                    server.terminate()
                    try:
                        server.communicate(timeout=8)
                    except subprocess.TimeoutExpired:
                        server.kill()
                        server.communicate(timeout=5)
