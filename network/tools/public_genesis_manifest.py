"""Prepare or verify the offline public MintCandy genesis manifest."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile

from check_operator_applications import check_applications, read_intake
from check_public_launch_draft import (
    DEFAULT_DRAFT, DEFAULT_PLAN, PUBLISHED_SOFTWARE_RECORD, check_contents, check_plan, read_json, require, unique_object,
)
from install_validator_genesis import install_genesis, install_operator_genesis
from native_launch_allocation import MAX_GENESIS_BYTES, summarize_allocation, verify_launch_plan
from native_launch_package import BINARY, LIMITS, fingerprint, sha256, verify_detached_signature
from verify_public_launch_genesis import verify_candidate, verify_cold_launch_profile


RELEASE = PUBLISHED_SOFTWARE_RECORD
MANIFEST_LIMIT = 8192
PGP_LIMIT = 65536
HEX256 = re.compile(r'[0-9a-f]{64}')


def read_bounded(path, limit):
    with path.open('rb') as stream:
        data = stream.read(limit + 1)
    require(0 < len(data) <= limit, f'invalid {path.name} byte count')
    return data


def release_for(binary):
    published = read_json(RELEASE)
    require(binary.is_file() and 0 < binary.stat().st_size <= LIMITS[BINARY], 'invalid mcnd binary byte count')
    require(sha256(binary) == published['binarySha256'], 'mcnd binary SHA256 differs from the published release')
    return {key: published[key] for key in ('releaseVersion', 'sourceRevision', 'archiveSha256', 'binarySha256')}


def manifest_for(genesis, plan, draft, software, first_block_app_hash):
    require(isinstance(first_block_app_hash, str) and HEX256.fullmatch(first_block_app_hash),
            'invalid cold InitChain AppHash')
    document = json.loads(genesis, object_pairs_hook=unique_object)
    check_contents(document, forbid_public_material=False)
    require(document['chain_id'] == plan['chain_id'], 'genesis must use the approved public chain ID')
    verify_cold_launch_profile(document, draft)
    report = summarize_allocation(document, hashlib.sha256(genesis).hexdigest())
    verify_launch_plan(report, plan)
    return {
        'schemaVersion': 1,
        'scope': 'mintcandy-public-genesis',
        'chainId': report['chainId'],
        'evmChainId': str(report['evmChainId']),
        'genesisTimeUtc': report['genesisTime'],
        'genesisSha256': report['genesisSha256'],
        'coldTestFirstBlockAppHash': first_block_app_hash,
        'initialSupplyAtoms': report['initialSupplyAtomic'],
        'allocationTotalsAtoms': report['allocationTotalsAtomic'],
        'validators': len(report['validators']),
        'bankRecipients': len(report['bankAllocations']),
        'software': software,
    }


def prepare_manifest(genesis, applications, plan, draft, relayers, software, cold):
    reviewed = verify_candidate(genesis, applications, plan, draft, relayers)
    expected = {
        'status': 'CANDIDATE_INIT_CHAIN_VERIFIED',
        'genesisSha256': reviewed['genesisSha256'],
        'chainId': reviewed['chainId'],
        'evmChainId': str(reviewed['evmChainId']),
        'validators': reviewed['checkedValidators'],
        'bankBalances': reviewed['checkedBankRecipients'],
        'initialSupplyAtoms': reviewed['initialSupplyAtomic'],
        'wrappedTokenSymbol': reviewed['wrappedTokenSymbol'],
    }
    require(all(cold.get(key) == value for key, value in expected.items()),
            'cold InitChain report differs from the reviewed genesis')
    return manifest_for(genesis, plan, draft, software, cold.get('firstBlockAppHash'))


def encode(manifest):
    return (json.dumps(manifest, sort_keys=True, indent=2) + '\n').encode('utf8')


def verify_manifest(args, genesis, plan, draft, software):
    signed = read_bounded(args.manifest, MANIFEST_LIMIT)
    approved_hash = args.approved_manifest_sha256.lower()
    require(HEX256.fullmatch(approved_hash) and hashlib.sha256(signed).hexdigest() == approved_hash,
            'manifest SHA256 differs from the independently approved value')
    signer = fingerprint(args.signer_fingerprint)
    with tempfile.TemporaryDirectory(prefix='mcnd-genesis-signature-') as directory:
        home = Path(directory)
        manifest_path, signature_path, keyring_path = (home / name for name in ('manifest.json', 'manifest.asc', 'trusted.gpg'))
        manifest_path.write_bytes(signed)
        signature_path.write_bytes(read_bounded(args.signature, PGP_LIMIT))
        keyring_path.write_bytes(read_bounded(args.trusted_keyring, PGP_LIMIT))
        verify_detached_signature(manifest_path, signature_path, keyring_path, signer, home)
    document = json.loads(signed, object_pairs_hook=unique_object)
    require(document['genesisSha256'] == hashlib.sha256(genesis).hexdigest(), 'genesis SHA256 differs from the signed manifest')
    expected = manifest_for(genesis, plan, draft, software, document['coldTestFirstBlockAppHash'])
    require(signed == encode(expected), 'signed manifest metadata differs from the release or genesis')
    return {**expected, 'status': 'PUBLIC_GENESIS_SIGNATURE_VERIFIED', 'signerFingerprint': signer,
            'manifestSha256': approved_hash, 'networkLaunchAuthorized': False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    for name in ('prepare', 'verify', 'install', 'install-observer'):
        command = commands.add_parser(name)
        command.add_argument('--genesis', required=True, type=Path)
        command.add_argument('--binary', required=True, type=Path)
    for name in ('intake', 'init-chain-report', 'output'):
        commands.choices['prepare'].add_argument('--' + name, required=True, type=Path)
    commands.choices['prepare'].add_argument('--relayer-wallet', required=True, action='append')
    for command in (commands.choices[name] for name in ('verify', 'install', 'install-observer')):
        for name in ('manifest', 'signature', 'trusted-keyring'):
            command.add_argument('--' + name, required=True, type=Path)
        command.add_argument('--approved-manifest-sha256', required=True)
        command.add_argument('--signer-fingerprint', required=True)
    commands.choices['install'].add_argument('--home', required=True, type=Path)
    commands.choices['install'].add_argument('--operator', required=True, help='reviewed d0valoper address')
    commands.choices['install-observer'].add_argument('--home', required=True, type=Path)

    args = parser.parse_args()
    try:
        draft, plan = read_json(DEFAULT_DRAFT), read_json(DEFAULT_PLAN)
        check_plan(plan, draft)
        genesis = read_bounded(args.genesis, MAX_GENESIS_BYTES)
        software = release_for(args.binary)
        if args.command == 'prepare':
            applications = check_applications(read_intake(args.intake), plan)
            cold = json.loads(read_bounded(args.init_chain_report, MANIFEST_LIMIT), object_pairs_hook=unique_object)
            manifest = prepare_manifest(genesis, applications, plan, draft, args.relayer_wallet, software, cold)
            with args.output.open('xb') as stream:
                stream.write(encode(manifest))
        else:
            result = verify_manifest(args, genesis, plan, draft, software)
            if args.command == 'install':
                result = install_operator_genesis(args.binary, args.home, args.operator, genesis, result)
            elif args.command == 'install-observer':
                result = install_genesis(args.binary, args.home, genesis, result)
            print(json.dumps(result, sort_keys=True))
    except (AttributeError, KeyError, OSError, RecursionError, TypeError, ValueError, subprocess.SubprocessError) as error:
        parser.exit(1, f'public genesis manifest: {error}\n')


if __name__ == '__main__':
    main()
