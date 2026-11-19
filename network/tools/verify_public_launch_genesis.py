"""Compare a candidate MintCandy genesis with the launch ledger and operator intake."""

import argparse
from datetime import datetime
from decimal import Decimal
import hashlib
import json
from pathlib import Path
import re

from check_operator_applications import check_applications, read_intake, strict_native_address
from check_public_launch_draft import DEFAULT_DRAFT, DEFAULT_PLAN, SYSTEM_ROLES, check_plan, read_json, require
from native_launch_allocation import MAX_GENESIS_BYTES, evm_address, summarize_allocation, unique_object, verify_launch_plan
from prepare_founding_ledger import bank_allocation


def verify_cold_launch_profile(doc, draft):
    state = doc['app_state']
    require(not doc.get('validators') and not doc.get('app_hash'),
            'cold launch must derive validator power and AppHash in InitChain')
    genesis_time = doc['genesis_time']
    require(isinstance(genesis_time, str) and re.fullmatch(
        r'[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?Z', genesis_time),
        'candidate requires a UTC genesis timestamp')
    datetime.fromisoformat(genesis_time[:-1] + '+00:00')
    require(doc['consensus_params']['block']['max_gas'] == draft['consensus_params']['block']['max_gas'],
            'candidate block gas cap differs from the launch draft')

    bank, coin = state['bank'], state['coin']
    require(all(len(row['coins']) == 1 and row['coins'][0]['denom'] == 'del' for row in bank['balances'])
            and len(bank['supply']) == 1 and bank['supply'][0]['denom'] == 'del',
            'candidate must declare an MSP-only genesis ledger and supply')
    metadata = bank['denom_metadata']
    require(len(metadata) == 1 and coin['params']['base_title'] == 'MintCandy' and
            [metadata[0][key] for key in ('base', 'display', 'name', 'symbol')] ==
            ['del', 'MSP', 'MintCandy', 'MSP'] and
            [(unit['denom'], unit['exponent']) for unit in metadata[0]['denom_units']] == [('del', 0), ('MSP', 18)],
            'candidate MintCandy MSP metadata differs')

    profile = state['withdrawal_settlement']
    for field, value in draft['app_state']['withdrawal_settlement'].items():
        if value is True:
            require(profile.get(field) is True, f'candidate requires {field}')
    require(profile.get('test_grace_until_height', 0) in (0, '0'), 'test grace is active in the candidate')
    roles = {role: evm_address(profile[role]) for role in SYSTEM_ROLES}
    require(len(set(roles.values())) == len(roles) and all(int(value, 16) for value in roles.values()),
            'candidate requires distinct nonzero system contract addresses')

    nft = state['nft']
    collections = nft.get('collections') or []
    require(not any(coin.get(field) for field in ('coins', 'checks')) and
            not any(nft.get(field) for field in ('external_bindings', 'isolated_positions')) and
            len(collections) == 1 and evm_address(collections[0]['addressDRC']) == roles['position_vault'] and
            not collections[0].get('tokens') and collections[0].get('supply', 0) in (0, '0'),
            'candidate must start with an empty system receipt collection and no user assets')
    require(isinstance(state['ibc'], dict) and isinstance(state['transfer'], dict),
            'candidate must declare IBC and transfer genesis')
    require(state['mint']['params']['mint_denom'] == 'del' and all(
        state['mint'][section][field] == '0.000000000000000000' for section, field in (
            ('minter', 'inflation'), ('minter', 'annual_provisions'),
            ('params', 'inflation_min'), ('params', 'inflation_max'),
        )), 'candidate must disable MSP emission')
    return roles


def verify_recipients(report, operators, plan, roles, relayer_wallets):
    wrapped = [row['address'] for row in report['bankAllocations'] if row['category'] == 'wrapped_del_backing']
    require(len(wrapped) == 1 and strict_native_address(wrapped[0], 'd0') == roles['wrapped_del'],
            'candidate wrapper treasury differs from the system contract')
    bank = bank_allocation(operators, plan, wrapped[0], relayer_wallets)
    expected = {strict_native_address(row['address'], 'd0'): row['coins'][0]['amount'] for row in bank['balances']}
    actual = {strict_native_address(row['address'], 'd0'): row['amountAtomic'] for row in report['bankAllocations']}
    require(actual == expected, 'candidate bank recipients or per-wallet amounts differ from the reviewed ledger')


def verify_validator_roster(report, operators, plan):
    expected = {row['operatorAddress']: row for row in operators}
    actual = {row['operatorAddress']: row for row in report['validators']}
    require(expected.keys() == actual.keys(), 'candidate validator roster differs from the reviewed intake')
    for operator, row in actual.items():
        application = expected[operator]
        require(row['declaredOnline'] is True and row['rewardAddress'] == application['rewardAddress'] and
                row['moniker'] == application['moniker'] and
                row['consensusPublicKeyBase64'] == application['consensusPubkey']['key'] and
                Decimal(row['commission']) == Decimal(application['commission']),
                'candidate validator key, online flag, reward address, moniker or commission differs from the reviewed intake')
    expected_delegations = {(row['operatorAddress'], row['stakeOwnerAddress'], row['stakeAmountAtoms']) for row in operators}
    actual_delegations = {(row['validator'], row['delegator'], row['amountAtomic']) for row in report['genesisDelegations']}
    require(actual_delegations == expected_delegations and len(report['genesisDelegations']) == len(expected_delegations),
            'candidate delegation owner, target validator or amount differs from the reviewed intake')
    require(all(Decimal(row['commission']) == Decimal(plan['validator_commission_fraction']) for row in operators),
            'reviewed operator commission differs from the launch plan')


def verify_candidate(data, applications, plan, draft, relayer_wallets):
    require(applications['status'] == 'PRECHECK_PASSED', 'four complete operator applications are required')
    doc = json.loads(data, object_pairs_hook=unique_object)
    require(doc['chain_id'] == plan['chain_id'] == applications['chainId'] and
            plan['evm_chain_id'] == applications['evmChainId'], 'candidate signing domain differs from the plan')
    roles = verify_cold_launch_profile(doc, draft)
    report = summarize_allocation(doc, hashlib.sha256(data).hexdigest())
    require(report['evmChainId'] == plan['evm_chain_id'], 'candidate EVM chain ID differs from the plan')
    verify_launch_plan(report, plan)
    operators = applications['operators']
    verify_recipients(report, operators, plan, roles, relayer_wallets)
    verify_validator_roster(report, operators, plan)
    return {
        'status': 'CANDIDATE_LEDGER_MATCH',
        'genesisSha256': report['genesisSha256'],
        'genesisTime': report['genesisTime'],
        'chainId': report['chainId'],
        'evmChainId': report['evmChainId'],
        'initialSupplyAtomic': report['initialSupplyAtomic'],
        'checkedValidators': len(operators),
        'checkedBankRecipients': len(report['bankAllocations']),
        'checkedDelegations': len(report['genesisDelegations']),
        'runtimeInitChainVerified': False,
        'ownershipProofsVerified': False,
        'publicGenesisApproved': False,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--genesis', required=True, type=Path)
    parser.add_argument('--intake', required=True, type=Path, help='private copy of the reviewed operator intake')
    parser.add_argument('--relayer-wallet', required=True, action='append', help='reviewed d0 gas wallet, repeat twice')
    args = parser.parse_args()
    try:
        draft = read_json(DEFAULT_DRAFT)
        plan = read_json(DEFAULT_PLAN)
        check_plan(plan, draft)
        applications = check_applications(read_intake(args.intake), plan)
        require(applications['status'] == 'PRECHECK_PASSED', 'four complete operator applications are required')
        with args.genesis.open('rb') as stream:
            data = stream.read(MAX_GENESIS_BYTES + 1)
        require(0 < len(data) <= MAX_GENESIS_BYTES, 'invalid genesis byte count')
        report = verify_candidate(data, applications, plan, draft, args.relayer_wallet)
    except (OSError, AttributeError, KeyError, RecursionError, TypeError, ValueError) as error:
        parser.exit(1, f'candidate genesis: {error}\n')
    print(json.dumps(report, sort_keys=True, indent=2))


if __name__ == '__main__':
    main()
