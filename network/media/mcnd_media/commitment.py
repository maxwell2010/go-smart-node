"""Authorize exact media bytes using the URI committed on a trusted local node."""

import argparse
import base64
from datetime import datetime, timezone
import json
from pathlib import Path
import re
from urllib.error import URLError
from urllib.request import HTTPRedirectHandler, ProxyHandler, Request, build_opener

from .store import ADDRESS, MAX_BLOB, Asset, MediaStore, canonical, cid, prepare_media, regular_read, require, strict


# ABI selectors from the release contracts.
CREATOR = '02d05d3f'
OWNER = '8da5cb5b'
IDENTITY = '2c159a1a'
NFT_STATE = '377cf68d'
OWNER_OF = '6352211e'
TOKEN_URI = 'c87b56dd'
URI_1155 = '0e89341c'
EXISTS = '4f558e79'
MAX_RPC = 512 << 10
MAX_BLOCK_AGE = 300


class RefuseRedirects(HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, location):
        return None


class LocalRPC:
    def __init__(self, endpoint):
        match = re.fullmatch(r'http://127\.0\.0\.1:([1-9][0-9]{0,4})/?', endpoint)
        require(match and int(match[1]) <= 65535, 'RPC endpoint must be configured on IPv4 loopback')
        self.endpoint = endpoint
        self.http = build_opener(ProxyHandler({}), RefuseRedirects())

    def __call__(self, method, params):
        data = canonical({'jsonrpc': '2.0', 'id': 1, 'method': method, 'params': params})
        request = Request(self.endpoint, data=data, headers={'Content-Type': 'application/json'})
        try:
            with self.http.open(request, timeout=4) as response:
                raw = response.read(MAX_RPC + 1)
        except (URLError, OSError):
            raise ValueError(f'{method} RPC unavailable') from None
        require(len(raw) <= MAX_RPC, 'RPC response exceeds size limit')
        result = strict(raw)
        require(isinstance(result, dict) and result.get('jsonrpc') == '2.0' and
                type(result.get('id')) is int and result['id'] == 1 and
                'error' not in result and 'result' in result, f'{method} RPC rejected the query')
        return result['result']


def evm_bytes(value):
    require(isinstance(value, str) and re.fullmatch(r'0x(?:[0-9a-fA-F]{2})*', value), 'invalid ABI response')
    return bytes.fromhex(value[2:])


def abi_address(raw):
    require(len(raw) == 32 and not any(raw[:12]) and any(raw[12:]), 'missing or invalid authority address')
    return '0x' + raw[12:].hex()


def abi_string(raw):
    require(len(raw) >= 64 and raw[:32] == (32).to_bytes(32, 'big'), 'invalid ABI string offset')
    length = int.from_bytes(raw[32:64], 'big')
    require(0 < length <= 256 and len(raw) == 64 + (length + 31) // 32 * 32 and
            not any(raw[64 + length:]), 'invalid ABI string length or padding')
    return raw[64:64 + length].decode('utf-8')


class CommittedReader:
    def __init__(self, chain_id, comet, evm, *, now=None):
        Asset(chain_id, '0x' + '01' * 20, 'erc20').key()
        self.chain_id = chain_id
        self.evm_id = int(chain_id.split('_', 1)[1].split('-', 1)[0])
        self.comet, self.evm = comet, evm
        self.now = now or (lambda: datetime.now(timezone.utc))

    def _height(self):
        status = self.comet('status', {})
        sync = status['sync_info']
        require(status['node_info']['network'] == self.chain_id and sync['catching_up'] is False,
                'Comet chain mismatch or node is still syncing')
        require(int(self.evm('eth_chainId', []), 16) == self.evm_id, 'EVM chain mismatch')
        latest = int(sync['latest_block_height'])
        committed = int(self.comet('abci_info', {})['response']['last_block_height'])
        evm_latest = int(self.evm('eth_blockNumber', []), 16)
        height = min(latest, committed, evm_latest) - 1
        require(height >= 2 and max(latest, committed, evm_latest) - height <= 3,
                'RPC endpoints do not agree on a recent committed height')
        result = self.comet('commit', {'height': str(height)})
        signed = result['signed_header']
        header = signed['header']
        require(result['canonical'] is True and header['chain_id'] == self.chain_id and
                int(header['height']) == height and int(signed['commit']['height']) == height,
                'RPC did not return the expected canonical commit')
        stamp = datetime.fromisoformat(header['time'].replace('Z', '+00:00'))
        require(stamp.tzinfo is not None and -30 <= (self.now() - stamp).total_seconds() <= MAX_BLOCK_AGE,
                'committed block is stale or has an invalid timestamp')
        block_hash = signed['commit']['block_id']['hash'].lower()
        block = self.evm('eth_getBlockByNumber', [hex(height), False])
        require(re.fullmatch(r'[0-9a-f]{64}', block_hash) and int(block['number'], 16) == height and
                block['hash'].lower() == '0x' + block_hash, 'Comet and EVM block hashes differ')
        return height

    def _native(self, height, key):
        response = self.comet('abci_query', {
            'path': '/store/validator/key', 'data': key.hex(), 'height': str(height), 'prove': False,
        })['response']
        require(int(response.get('code', 0)) == 0 and int(response['height']) == height and
                base64.b64decode(response.get('key') or '', validate=True) == key,
                'native membership query did not read committed state')
        return base64.b64decode(response.get('value') or '', validate=True)

    def _call(self, height, address, selector, argument=''):
        return evm_bytes(self.evm('eth_call', [{'to': address, 'data': '0x' + selector + argument}, hex(height)]))

    def _token(self, height, address):
        suffix = bytes.fromhex(address[2:])
        free = self._native(height, b'\x70' + suffix)
        backed = self._native(height, b'\x80' + suffix)
        free_member = free == b'\x01' and not backed
        backed_member = not free and len(backed) == 52 and backed[:20] == suffix and any(backed[20:])
        require(free_member or backed_member, 'token is not admitted in the native registry')
        return self._call(height, address, OWNER if free else CREATOR), self._call(height, address, IDENTITY)

    def _nft(self, height, asset):
        profile = strict(self._native(height, b'\x6f'))
        center = profile['nft_center'].lower()
        require(ADDRESS.fullmatch(center) and int(center, 16), 'committed NFT factory is missing')
        words = self._call(height, center, NFT_STATE, asset.contract[2:].rjust(64, '0'))
        kind = int(asset.kind == 'erc1155')
        require(len(words) == 96 and int.from_bytes(words[:32], 'big') == 1 and
                int.from_bytes(words[32:64], 'big') == kind and int.from_bytes(words[64:], 'big') in (0, 1),
                'collection is not active in the committed factory or NFT family differs')
        token = f'{int(asset.token_id):064x}'
        if kind:
            require(self._call(height, asset.contract, EXISTS, token) == (1).to_bytes(32, 'big'),
                    'ERC1155 ID does not exist')
        else:
            abi_address(self._call(height, asset.contract, OWNER_OF, token))
        return (self._call(height, asset.contract, CREATOR),
                self._call(height, asset.contract, URI_1155 if kind else TOKEN_URI, token))

    def __call__(self, asset, uri):
        asset.key()
        require(asset.chain_id == self.chain_id, 'asset chain does not match configured RPC network')
        try:
            height = self._height()
            owner, committed_uri = (self._token(height, asset.contract) if asset.kind == 'erc20'
                                    else self._nft(height, asset))
            require(abi_string(committed_uri) == uri, 'asset URI does not commit to this content')
            return abi_address(owner)
        except (KeyError, TypeError, AttributeError, IndexError):
            raise ValueError('invalid committed state response') from None


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    prepare = commands.add_parser('prepare', help='compute the URI to put in the normal asset transaction')
    publish = commands.add_parser('publish', help='publish verified bytes from committed state')
    for command in (prepare, publish):
        command.add_argument('--chain-id', required=True)
        command.add_argument('--kind', choices=('erc20', 'erc721', 'erc1155'), required=True)
        command.add_argument('--metadata', required=True, type=Path)
        command.add_argument('--image', required=True, type=Path)
        command.add_argument('--mime', required=True, choices=('image/png', 'image/jpeg', 'image/webp'))
    publish.add_argument('--contract', required=True)
    publish.add_argument('--token-id', default='0')
    publish.add_argument('--comet-rpc', required=True)
    publish.add_argument('--evm-rpc', required=True)
    publish.add_argument('--store', required=True, type=Path)
    args = parser.parse_args(argv)
    try:
        metadata = strict(regular_read(args.metadata, 64 << 10))
        image = regular_read(args.image, (1 << 20) if args.kind == 'erc20' else MAX_BLOB)
        if args.command == 'prepare':
            asset = Asset(args.chain_id, '0x' + '01' * 20, args.kind)
            clean, payload = prepare_media(asset, metadata, image, args.mime)
            result = {'metadata_uri': 'ipfs://' + cid(payload), 'image_uri': 'ipfs://' + cid(clean)}
        else:
            asset = Asset(args.chain_id, args.contract.lower(), args.kind, args.token_id)
            asset.key()
            reader = CommittedReader(args.chain_id, LocalRPC(args.comet_rpc), LocalRPC(args.evm_rpc))
            store = MediaStore(args.store, commitment_reader=reader)
            try:
                result = store.publish_committed(asset, metadata, image, args.mime)
            finally:
                store.close()
    except (ValueError, OSError) as error:
        parser.exit(1, f'{error}\n')
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
