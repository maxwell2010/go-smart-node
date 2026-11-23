"""Preflight a private copy of the MintCandy operator intake before assembly."""

import argparse
import base64
from decimal import Decimal
import hashlib
import ipaddress
import json
from pathlib import Path
import re
import unicodedata

from check_public_launch_draft import (
    ATOMS_PER_DEL, DEFAULT_DRAFT, DEFAULT_PLAN, check_contents, check_plan, read_json, require, unique_object,
)
from withdrawal_genesis_smoke import ALPHABET, bech32_payload


MAX_INTAKE_BYTES = 256 * 1024
ED25519_TYPE = '/cosmos.crypto.ed25519.PubKey'
BECH32_GENERATORS = (0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3)
FIELDS = (
    'application_id', 'operator_address', 'evm_operator_address', 'consensus_pubkey.key',
    'moniker', 'operator_contact', 'reward_address', 'commission_fraction',
    'stake_owner_address', 'beneficiary_address', 'stake_ownership_model', 'stake_amount_atoms',
    'gas_wallet_address', 'gas_budget_atoms', 'connectivity.node_id',
    'connectivity.p2p_address', 'connectivity.role',
)


def field_at(row, path):
    for name in path.split('.'):
        if row is None:
            return None
        require(isinstance(row, dict), 'operator nested record must be an object')
        row = row.get(name)
    return row


def text(value, field, limit):
    require(isinstance(value, str) and value == value.strip() and value.isprintable()
            and 0 < len(value.encode('utf-8')) <= limit, f'invalid {field}')
    return value


def strict_native_address(value, prefix):
    require(isinstance(value, str) and value.startswith(prefix + '1') and
            len(value) == len(prefix) + 39, f'invalid {prefix} address shape')
    payload = value[len(prefix) + 1:]
    require(all(character in ALPHABET for character in payload), f'invalid {prefix} address alphabet')
    parts = [ord(c) >> 5 for c in prefix] + [0] + [ord(c) & 31 for c in prefix]
    parts.extend(ALPHABET.index(c) for c in payload)
    checksum = 1
    for part in parts:
        top = checksum >> 25
        checksum = ((checksum & 0x1ffffff) << 5) ^ part
        for index, generator in enumerate(BECH32_GENERATORS):
            if (top >> index) & 1:
                checksum ^= generator
    require(checksum == 1, f'invalid {prefix} address checksum')
    address = bech32_payload(value, prefix)
    require(int(address, 16) != 0, f'{prefix} address must be nonzero')
    return address


def p2p_endpoint(peer, node_id, public):
    require(isinstance(peer, str) and len(peer) <= 320, 'invalid P2P endpoint')
    match = re.fullmatch(r'([0-9a-f]{40})@(\[[0-9A-Fa-f:]+\]|[A-Za-z0-9.-]+):([1-9][0-9]{0,4})', peer)
    require(match is not None, 'P2P endpoint must be NODE_ID@HOST:PORT')
    peer_id, host, port = match.groups()
    require(peer_id == node_id and int(port) <= 65535, 'P2P node ID or port differs')
    bracketed = host.startswith('[')
    try:
        ip = ipaddress.ip_address(host[1:-1] if bracketed else host)
    except ValueError:
        ip = None
    if ip is not None:
        require(bracketed == (ip.version == 6) and not (ip.is_loopback or ip.is_unspecified or
                ip.is_multicast or ip.is_link_local), 'P2P address is not a peer endpoint')
        require(not public or ip.is_global, 'public P2P address must be globally routable')
        host = f'[{ip.compressed}]' if bracketed else ip.compressed
    else:
        host = host.lower()
        label = re.compile(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?')
        require(len(host) <= 253 and all(label.fullmatch(part) for part in host.split('.')) and
                re.search(r'[a-z]', host) and host != 'localhost' and not host.endswith('.localhost'),
                'invalid P2P hostname')
        require(not public or ('.' in host and not host.endswith('.local')), 'invalid public P2P hostname')
    return f'{node_id}@{host}:{port}'


def check_operator(row, plan):
    application = text(row['application_id'], 'application ID', 64)
    require(re.fullmatch(r'[A-Za-z0-9_-]+', application) is not None, 'invalid application ID')
    operator = strict_native_address(row['operator_address'], 'd0valoper')
    evm = row['evm_operator_address']
    require(isinstance(evm, str) and re.fullmatch(r'0x[0-9a-fA-F]{40}', evm) and
            evm.lower() == operator, 'operator Bech32 and EVM addresses differ')
    pub = row['consensus_pubkey']
    require(pub.get('@type') == ED25519_TYPE and isinstance(pub.get('key'), str), 'expected a public Ed25519 key')
    key = pub['key']
    require(re.fullmatch(r'[A-Za-z0-9+/]{43}=', key) is not None, 'invalid Ed25519 base64')
    raw = base64.b64decode(key, validate=True)
    require(len(raw) == 32 and any(raw) and base64.b64encode(raw).decode('ascii') == key,
            'invalid Ed25519 public key encoding')
    consensus = hashlib.sha256(raw).hexdigest()[:40].upper()
    supplied = row.get('consensus_address_hex')
    require(supplied is None or (isinstance(supplied, str) and supplied.upper() == consensus),
            'consensus address differs from public key')

    strict_native_address(row['reward_address'], 'd0')
    strict_native_address(row['gas_wallet_address'], 'd0')
    owner = strict_native_address(row['stake_owner_address'], 'd0')
    beneficiary = strict_native_address(row['beneficiary_address'], 'd0')
    require(owner == beneficiary == plan['founder_evm_address'].lower() and
            row['stake_ownership_model'] == plan['decisions']['selected_stake_ownership'],
            'stake ownership or beneficiary differs from approved founder delegations')
    require(row['stake_amount_atoms'] == plan['approved_initial_stake_per_validator_atoms'],
            'initial stake differs from approved amount')
    require(row['gas_budget_atoms'] == str(200 * ATOMS_PER_DEL), 'operator gas budget must be 200 MSP')
    commission = row['commission_fraction']
    require(isinstance(commission, str) and re.fullmatch(r'(?:0(?:\.[0-9]{1,18})?|1(?:\.0{1,18})?)', commission),
            'commission must be a decimal string from 0 to 1 with at most 18 fractional digits')
    require(Decimal(commission) >= Decimal('0.05'), 'native validator commission must be at least 5%')
    moniker = text(row['moniker'], 'moniker', 70)
    require('\ufffd' not in moniker and
            any(unicodedata.category(ch)[0] in 'LNPS' and ch not in '\u115f\u1160\u2800\u3164\uffa0'
                for ch in moniker), 'moniker must contain a visible character')
    text(row['operator_contact'], 'operator contact', 256)

    connectivity = row['connectivity']
    node_id = connectivity['node_id']
    require(isinstance(node_id, str) and re.fullmatch(r'[0-9a-f]{40}', node_id) and int(node_id, 16) != 0,
            'invalid P2P node ID')
    role = connectivity['role']
    require(role in ('validator', 'sentry', 'peer', 'seed'), 'invalid P2P role')
    public = connectivity.get('endpoint_publication_allowed')
    require(public is None or type(public) is bool, 'invalid P2P publication permission')
    endpoint = p2p_endpoint(connectivity['p2p_address'], node_id, public is True)

    return {
        'applicationId': application,
        'moniker': moniker,
        'operatorAddress': row['operator_address'],
        'operatorEvmAddress': operator,
        'consensusPubkey': {'@type': ED25519_TYPE, 'key': key},
        'consensusAddressHex': consensus,
        'commission': commission,
        'rewardAddress': row['reward_address'],
        'stakeOwnerAddress': row['stake_owner_address'],
        'beneficiaryAddress': row['beneficiary_address'],
        'stakeAmountAtoms': row['stake_amount_atoms'],
        'gasWalletAddress': row['gas_wallet_address'],
        'gasBudgetAtoms': row['gas_budget_atoms'],
        'p2pAddress': endpoint,
        'p2pNodeId': node_id,
        'p2pRole': role,
        'endpointPublicationAllowed': public is True,
    }


def check_applications(intake, plan):
    check_contents(intake, forbid_public_material=False)
    require(isinstance(intake, dict) and type(intake.get('schema_version')) is int
            and intake['schema_version'] == 2, 'expected operator intake schema 2')
    network = intake.get('network')
    count = plan['approved_validator_count']
    require(isinstance(network, dict) and network.get('cosmos_chain_id') == plan['chain_id'] and
            network.get('evm_chain_id') == plan['evm_chain_id'] and
            type(network.get('approved_validator_count')) is int and
            network.get('approved_validator_count') == count, 'intake differs from approved chain domain or roster size')
    rows = intake.get('validators')
    require(isinstance(rows, list) and len(rows) <= count, 'intake contains more rows than the approved roster')
    candidates, incomplete = [], []
    for index, row in enumerate(rows, start=1):
        require(isinstance(row, dict), f'validator row {index} must be an object')
        missing = [name for name in FIELDS if field_at(row, name) is None]
        if missing:
            incomplete.append({'row': index, 'missingFields': missing})
            continue
        try:
            candidates.append(check_operator(row, plan))
        except ValueError as error:
            raise ValueError(f'validator row {index}: {error}') from error
    for field in ('applicationId', 'operatorAddress', 'consensusAddressHex', 'p2pNodeId'):
        require(len({candidate[field] for candidate in candidates}) == len(candidates), f'duplicate {field}')

    return {
        'status': 'PRECHECK_PASSED' if len(candidates) == count else 'APPLICATIONS_PENDING',
        'chainId': plan['chain_id'],
        'evmChainId': plan['evm_chain_id'],
        'checkedOperators': len(candidates),
        'requiredOperators': count,
        'missingSlots': count - len(candidates),
        'incompleteRows': incomplete,
        'operators': sorted(candidates, key=lambda candidate: candidate['operatorAddress']),
        'ownershipProofsVerified': False,
        'endpointsReachabilityVerified': False,
        'publicGenesisApproved': False,
    }


def read_intake(path):
    with path.open('rb') as stream:
        data = stream.read(MAX_INTAKE_BYTES + 1)
    require(0 < len(data) <= MAX_INTAKE_BYTES, 'invalid intake file size')
    try:
        return json.loads(data.decode('utf-8'), object_pairs_hook=unique_object)
    except (ValueError, RecursionError) as error:
        raise ValueError('invalid UTF-8 JSON or duplicate intake fields') from error


def public_bootstrap(applications):
    report = {key: applications[key] for key in (
        'chainId', 'evmChainId', 'checkedOperators', 'requiredOperators',
        'endpointsReachabilityVerified', 'publicGenesisApproved',
    )}
    if applications['status'] != 'PRECHECK_PASSED':
        return {**report, 'status': 'APPLICATIONS_PENDING'}

    peers, seeds, services = [], [], set()
    for operator in applications['operators']:
        if operator['endpointPublicationAllowed'] is not True:
            continue
        endpoint = operator['p2pAddress']
        service = endpoint.split('@', 1)[1]
        require(service not in services, 'multiple public P2P IDs use the same host and port')
        services.add(service)
        group = seeds if operator['p2pRole'] == 'seed' else peers
        group.append(endpoint)

    if not peers:
        return {**report, 'status': 'BOOTSTRAP_PEERS_PENDING'}
    peer_config = {'persistent_peers': ','.join(sorted(peers)), 'seeds': ','.join(sorted(seeds))}
    return {**report, 'status': 'PUBLIC_BOOTSTRAP_CANDIDATE', 'p2p': peer_config}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('intake', type=Path, help='private copy of operator-launch-intake.example.json')
    parser.add_argument('--require-four', action='store_true', help='return a failing exit status until four applications pass preflight')
    parser.add_argument('--public-bootstrap', action='store_true',
                        help='emit only the candidate P2P configuration from explicitly publishable endpoints')
    args = parser.parse_args()
    try:
        plan = read_json(DEFAULT_PLAN)
        check_plan(plan, read_json(DEFAULT_DRAFT))
        report = check_applications(read_intake(args.intake), plan)
        if args.public_bootstrap:
            report = public_bootstrap(report)
    except (OSError, ValueError, RecursionError) as error:
        parser.exit(1, f'operator applications: {error}\n')
    print(json.dumps(report, sort_keys=True, indent=2))
    if args.public_bootstrap:
        return 0 if report['status'] == 'PUBLIC_BOOTSTRAP_CANDIDATE' else 2
    return 2 if args.require_four and report['status'] != 'PRECHECK_PASSED' else 0


if __name__ == '__main__':
    raise SystemExit(main())
