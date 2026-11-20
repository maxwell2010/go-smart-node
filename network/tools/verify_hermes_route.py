"""Check one Tendermint ICS-20 v1 route using read-only Hermes queries."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tomllib


PORT = 'transfer'
CHANNEL = re.compile(r'channel-[0-9]{1,20}')
CONNECTION = re.compile(r'connection-[0-9]{1,20}')
CLIENT = re.compile(r'07-tendermint-[0-9]{1,20}')


def require(condition, reason):
    if not condition:
        raise ValueError(reason)


def check_config(raw, left, right):
    require(left[0] != right[0], 'route must connect two different chains')
    for chain, channel in (left, right):
        require(re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,49}', chain) and CHANNEL.fullmatch(channel),
                'both ends require a chain ID and a specific channel-N')
    config = tomllib.loads(raw.decode('utf8'))
    chains = config.get('chains', [])
    require(len(chains) == 2 and {row['id'] for row in chains} == {left[0], right[0]},
            'configuration must contain exactly the two route chains')
    expected_channels = dict((left, right))
    for row in chains:
        chain = row['id']
        channel_filter = row.get('packet_filter', {})
        require(channel_filter.get('policy') == 'allow' and channel_filter.get('list') == [[PORT, expected_channels[chain]]],
                f'{chain}: packet filter must allow only its specific transfer channel')
        require(row.get('trusted_node', False) is False, f'{chain}: trusted_node must be false')


def inspect_route(query, left, right):
    state = query('channel', 'ends', '--chain', left[0], '--port', PORT, '--channel', left[1], '--verbose')
    ends = []
    for name, local, peer in (('', left, right), ('counterparty_', right, left)):
        channel = state[name + 'channel_end']
        connection = state[name + 'connection_end']
        client = state[name + 'client_state']
        require(channel['state'] == 'Open' and channel['ordering'] == 'Unordered' and channel['version'] == 'ics20-1',
                f'{local[0]}: channel must be open, unordered and use ICS-20 v1')
        require(channel['remote']['port_id'] == PORT and channel['remote']['channel_id'] == peer[1],
                f'{local[0]}: counterparty channel or port differs from the selected route')
        hops = channel['connection_hops']
        require(len(hops) == 1 and CONNECTION.fullmatch(hops[0]) and connection['state'] == 'Open',
                f'{local[0]}: expected a single open IBC connection')
        require(CLIENT.fullmatch(connection['client_id']) and client['type'] == 'Tendermint' and client['chain_id'] == peer[0],
                f'{local[0]}: connection client does not track the expected counterparty chain')
        ends.append({'chainId': local[0], 'port': PORT, 'channelId': local[1], 'connectionId': hops[0],
                     'clientId': connection['client_id'], 'counterpartyChainId': peer[0], 'counterpartyChannelId': peer[1]})

    for name, local, peer in (('', ends[0], ends[1]), ('counterparty_', ends[1], ends[0])):
        counterparty = state[name + 'connection_end']['counterparty']
        require(counterparty['connection_id'] == peer['connectionId'] and counterparty['client_id'] == peer['clientId'],
                f"{local['chainId']}: IBC connections do not point back to each other")
    for end in ends:
        status = query('client', 'status', '--chain', end['chainId'], '--client', end['clientId'])
        require(status == 'Active', f"{end['chainId']}: IBC client must be active")
        end['clientStatus'] = status
    return ends


def read_result(output):
    docs = [json.loads(line) for line in output.splitlines() if line.strip()]
    require(not any(doc.get('level') in ('WARN', 'ERROR') or doc.get('status') == 'error' for doc in docs),
            'Hermes query returned a warning or error')
    results = [doc['result'] for doc in docs if doc.get('status') == 'success' and 'result' in doc]
    require(len(results) == 1, 'Hermes query did not return one success result')
    return results[0]


def verify_route(binary, expected_sha, config, left, right):
    binary, config = binary.resolve(strict=True), config.resolve(strict=True)
    require(binary.is_file(), 'Hermes must be a regular executable')
    require(config.is_file(), 'Hermes configuration must be a regular file')
    with binary.open('rb') as stream:
        require(hashlib.file_digest(stream, 'sha256').hexdigest() == expected_sha, 'Hermes SHA256 differs')
    with config.open('rb') as stream:
        raw = stream.read(1024**2 + 1)
    require(0 < len(raw) <= 1024**2, 'invalid Hermes configuration size')
    check_config(raw, left, right)

    def query(*args):
        result = subprocess.run([binary, '--config', config, '--json', 'query', *args],
                                capture_output=True, text=True, encoding='utf8', timeout=45)
        require(result.returncode == 0, f'Hermes query {args[0]} {args[1]} failed')
        return read_result(result.stdout)

    ends = inspect_route(query, left, right)
    with config.open('rb') as stream:
        require(stream.read(len(raw) + 1) == raw, 'Hermes configuration changed during verification')
    return {'status': 'IBC_ROUTE_STATE_MATCH', 'protocol': 'ics20-1', 'chains': ends,
            'configSha256': hashlib.sha256(raw).hexdigest(), 'hermesSha256': expected_sha,
            'transactionsSent': False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--hermes', required=True, type=Path)
    parser.add_argument('--hermes-sha256', required=True)
    parser.add_argument('--config', required=True, type=Path)
    parser.add_argument('--left', nargs=2, required=True, metavar=('CHAIN', 'CHANNEL'))
    parser.add_argument('--right', nargs=2, required=True, metavar=('CHAIN', 'CHANNEL'))
    args = parser.parse_args()
    try:
        report = verify_route(args.hermes, args.hermes_sha256, args.config, args.left, args.right)
    except (OSError, ValueError, KeyError, TypeError, AttributeError, subprocess.SubprocessError) as error:
        parser.exit(1, f'IBC route: {error}\n')
    print(json.dumps(report, sort_keys=True, indent=2))


if __name__ == '__main__':
    main()
