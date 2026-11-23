"""Compare two Comet RPCs with an independently approved state-sync checkpoint."""

import argparse
from datetime import datetime, timezone
import json
import re
from urllib.parse import urlsplit, urlunsplit
from urllib.request import build_opener, HTTPRedirectHandler, ProxyHandler

from consensus_health import integer, non_nil_hash, utc_time


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


HTTP = build_opener(ProxyHandler({}), NoRedirect())
MAX_REPLY_BYTES = 1024 * 1024


def require(condition, reason):
    if not condition:
        raise ValueError(reason)


def rpc_base(url):
    require(isinstance(url, str) and not any(ch.isspace() or ord(ch) < 32 for ch in url),
            'invalid RPC URL')
    parts = urlsplit(url)
    require(parts.scheme in ('http', 'https') and parts.hostname and parts.username is None
            and not parts.query and not parts.fragment, 'RPC URL requires HTTP(S) without credentials, query or fragment')
    require(parts.port != 0, 'RPC port must be nonzero')
    netloc = parts.netloc.lower()
    return urlunsplit((parts.scheme, netloc, parts.path.rstrip('/'), '', ''))


def read_rpc(base, method):
    with HTTP.open(f'{base}/{method}', timeout=10) as response:
        raw = response.read(MAX_REPLY_BYTES + 1)
    require(len(raw) <= MAX_REPLY_BYTES, 'RPC reply exceeds the size limit')
    body = json.loads(raw)
    require('error' not in body and 'result' in body, 'Comet RPC returned no result')
    return body['result']


def verify_checkpoint(servers, chain_id, height, expected_hash, trust_period_seconds, *, now=None, rpc=read_rpc):
    require(re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,49}', chain_id), 'invalid Comet chain ID')
    height = integer(height, 1)
    expected_hash = non_nil_hash(expected_hash)
    trust_period_seconds = integer(trust_period_seconds, 1)
    servers = tuple(map(rpc_base, servers))
    require(len(servers) == 2 and len(set(servers)) == 2, 'provide two distinct RPC URLs')
    now = now or datetime.now(timezone.utc)
    peers = []
    checkpoint = None
    for base in servers:
        status = rpc(base, 'status')
        info, sync = status['node_info'], status['sync_info']
        require(info['network'] == chain_id, 'RPC is connected to a different chain')
        node_id = info['id']
        require(re.fullmatch(r'[0-9a-f]{40}', node_id), 'invalid RPC node ID')
        require(all(peer['nodeId'] != node_id for peer in peers), 'both RPC URLs identify the same node')
        earliest = integer(sync['earliest_block_height'], 1)
        latest = integer(sync['latest_block_height'], 1)
        require(sync['catching_up'] is False, 'RPC node is catching up')
        require(earliest <= height < latest, 'RPC node does not retain the checkpoint below its tip')

        result = rpc(base, f'commit?height={height}')
        bundle = result['signed_header']
        header, commit = bundle['header'], bundle['commit']
        require(result['canonical'] is True and header['chain_id'] == chain_id and
                integer(header['height'], 1) == integer(commit['height'], 1) == height,
                'RPC did not return the requested canonical commit')
        require(non_nil_hash(commit['block_id']['hash']) == expected_hash, 'RPC block hash differs from approved trust hash')
        timestamp = utc_time(header['time'])
        age = (now - timestamp).total_seconds()
        require(0 <= age < trust_period_seconds, 'checkpoint is in the future or outside its trust period')
        observed = {'appHash': non_nil_hash(header['app_hash']), 'headerTimeUtc': timestamp.isoformat()}
        require(checkpoint is None or checkpoint == observed, 'RPCs disagree on checkpoint state or time')
        checkpoint = observed
        peers.append({'nodeId': node_id, 'earliestBlockHeight': earliest, 'latestBlockHeight': latest})
    return {'status': 'STATE_SYNC_CHECKPOINT_MATCH', 'chainId': chain_id, 'trustHeight': height,
            'trustHash': expected_hash, 'trustPeriodSeconds': trust_period_seconds,
            **checkpoint, 'peers': peers}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--rpc-servers', nargs=2, required=True, metavar=('RPC_A', 'RPC_B'))
    parser.add_argument('--chain-id', required=True)
    parser.add_argument('--trust-height', type=int, required=True)
    parser.add_argument('--trust-hash', required=True)
    parser.add_argument('--trust-period-seconds', type=int, required=True)
    args = parser.parse_args()
    try:
        report = verify_checkpoint(args.rpc_servers, args.chain_id, args.trust_height,
                                   args.trust_hash, args.trust_period_seconds)
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
        parser.exit(1, f'State sync checkpoint: {error}\n')
    print(json.dumps(report, sort_keys=True, indent=2))


if __name__ == '__main__':
    main()
