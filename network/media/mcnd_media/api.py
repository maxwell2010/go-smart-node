"""WSGI media upload and immutable reads for a dedicated MintCandy origin."""

import base64
from contextlib import closing
from http import HTTPStatus
import os
from pathlib import Path
import re
import sqlite3
from subprocess import TimeoutExpired

from .commitment import CommittedReader, LocalRPC
from .gateway_export import PNG_HEADER
from .ipfs import CID, cid, matches_cid, object_cid
from .store import MAX_BLOB, Asset, MediaStore, canonical, prepare_media, regular_read, require, strict


BODY_LIMIT = 14 << 20
METADATA_LIMIT = 64 << 10
UPLOAD_ROUTES = {'/v1/prepare', '/v1/publish'}
FILENAME = re.compile(r'/ipfs/(' + CID.pattern + r')')
KINDS = {'erc20', 'erc721', 'erc1155'}


class RequestError(ValueError):
    def __init__(self, status):
        self.status = status


def request_media(env, chain_id, publish):
    if env.get('CONTENT_TYPE') != 'application/json' or env.get('HTTP_CONTENT_ENCODING'):
        raise RequestError(415)
    length = env.get('CONTENT_LENGTH', '')
    if not re.fullmatch(r'0|[1-9][0-9]{0,17}', length):
        raise RequestError(411)
    size = int(length)
    if size > BODY_LIMIT:
        raise RequestError(413)
    body = env['wsgi.input'].read(size)
    require(len(body) == size, 'truncated request')
    document = strict(body)
    fields = {'kind', 'metadata', 'image_base64', 'mime'}
    if publish:
        fields.update({'contract', 'token_id'})
    require(isinstance(document, dict) and set(document) == fields, 'invalid media request fields')
    kind, mime, encoded = (document[key] for key in ('kind', 'mime', 'image_base64'))
    require(isinstance(kind, str) and kind in KINDS and isinstance(encoded, str) and
            mime in ('image/png', 'image/jpeg', 'image/webp'), 'invalid media request types')
    maximum = 1 << 20 if kind == 'erc20' else MAX_BLOB
    if len(encoded) > 4 * ((maximum + 2) // 3):
        raise RequestError(413)
    image = base64.b64decode(encoded, validate=True)
    if len(image) > maximum:
        raise RequestError(413)
    metadata = document['metadata']
    contract, token_id = (document['contract'], document['token_id']) if publish else ('0x' + '01' * 20, '0')
    require(isinstance(contract, str) and isinstance(token_id, str), 'invalid asset address or ID')
    asset = Asset(chain_id, contract.lower(), kind, token_id)
    asset.key()
    return asset, metadata, image, mime


def read_object(root, name):
    database = root / 'index.sqlite3'
    with closing(sqlite3.connect(database.as_uri() + '?mode=ro', uri=True)) as conn:
        rows = conn.execute(
            "SELECT 'image/png' FROM records WHERE image=? UNION "
            "SELECT 'application/json' FROM records WHERE metadata=?", (name, name),
        ).fetchall()
    if not rows:
        raise RequestError(404)
    if len(rows) != 1:
        raise OSError('ambiguous published object type')
    (content_type,), = rows
    is_image = content_type == 'image/png'
    blob = regular_read(root / 'objects' / name, MAX_BLOB if is_image else METADATA_LIMIT)
    if not matches_cid(name, blob) or (is_image and not blob.startswith(PNG_HEADER)):
        raise OSError('published object failed integrity verification')
    return content_type, blob


def create_app(root, chain_id, commitment_reader):
    with closing(MediaStore(root, commitment_reader=commitment_reader)) as store:
        root = store.root
    Asset(chain_id, '0x' + '01' * 20, 'erc20').key()

    def application(env, start_response):
        method, path = env['REQUEST_METHOD'], env['PATH_INFO']
        route = FILENAME.fullmatch(path)
        headers = [('X-Content-Type-Options', 'nosniff'),
                   ('Content-Security-Policy', "default-src 'none'; sandbox"),
                   ('Referrer-Policy', 'no-referrer')]
        status = 200
        content_type = 'application/json'
        cache = 'no-store'
        blob = b''
        try:
            if env.get('QUERY_STRING') or not (route or path in UPLOAD_ROUTES):
                raise RequestError(404)
            allowed = ('GET', 'HEAD') if route else ('POST',)
            if method not in allowed:
                headers.append(('Allow', ', '.join(allowed)))
                raise RequestError(405)
            if route:
                content_type, blob = read_object(root, route[1])
                cache = 'public, max-age=31536000, immutable'
                headers.extend([('Access-Control-Allow-Origin', '*'), ('Cross-Origin-Resource-Policy', 'cross-origin')])
            else:
                publish = path == '/v1/publish'
                args = request_media(env, chain_id, publish)
                if publish:
                    with closing(MediaStore(root, commitment_reader=commitment_reader)) as store:
                        result = store.publish_committed(*args)
                else:
                    clean, payload = prepare_media(*args)
                    result = {'metadata_uri': 'ipfs://' + cid(payload), 'image_uri': 'ipfs://' + object_cid(clean)}
                blob = canonical(result)
        except RequestError as error:
            status = error.status
        except (ValueError, RecursionError):
            status = 422 if path == '/v1/publish' else 400
            if route:
                status = 503
        except (OSError, sqlite3.Error, TimeoutExpired):
            status = 503
        if status != 200:
            blob = canonical({'error': HTTPStatus(status).phrase})
        headers.extend([('Cache-Control', cache), ('Content-Type', content_type), ('Content-Length', str(len(blob)))])
        start_response(f'{status} {HTTPStatus(status).phrase}', headers)
        return [b'' if method == 'HEAD' else blob]

    return application


def from_environment():
    chain_id = os.environ['MCND_MEDIA_CHAIN_ID']
    reader = CommittedReader(chain_id, LocalRPC(os.environ['MCND_MEDIA_COMET_RPC']),
                             LocalRPC(os.environ['MCND_MEDIA_EVM_RPC']))
    return create_app(Path(os.environ['MCND_MEDIA_STORE']), chain_id, reader)
