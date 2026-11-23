"""Compare a candidate MintCandy genesis with the launch ledger and operator intake."""

import argparse
from datetime import datetime
from decimal import Decimal
import hashlib
import json
from pathlib import Path
import re

from check_operator_applications import check_applications, read_intake, strict_native_address
from check_public_launch_draft import DEFAULT_DRAFT, DEFAULT_PLAN, SYSTEM_ROLES, read_reviewed_inputs, require
from native_launch_allocation import (
    MAX_GENESIS_BYTES, evm_address, summarize_allocation, unique_object, verify_launch_plan, wrapper_category,
)
from prepare_founding_ledger import bank_allocation, sdk_relayer_wallets


# The pinned ERC20Upgradeable _name and _symbol slots used by
# network/launchgenesis/wrapped_metadata.go. Both MSP values fit one word.
WRAPPER_METADATA = {
    '0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace03': b'MintCandy Wrapped MSP',
    '0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace04': b'WMSP',
}


def verify_wrapper_metadata(evm, wrapper_address, denom):
    accounts = [account for account in evm.get('accounts', [])
                if evm_address(account['address']) == wrapper_address]
    require(len(accounts) == 1, 'candidate requires one native wrapper account')
    storage = {}
    for entry in accounts[0]['storage']:
        key = entry['key'].lower()
        if key in WRAPPER_METADATA:
            require(key not in storage, 'candidate has duplicate wrapper metadata slots')
            storage[key] = entry['value'].lower()
    metadata = dict(zip(WRAPPER_METADATA, (b'MintCandy Wrapped Test MSP', b'WtMSP'))) if denom == 'tmsp' else WRAPPER_METADATA
    expected = {key: '0x' + (value.ljust(31, b'\0') + bytes([2 * len(value)])).hex()
                for key, value in metadata.items()}
    require(storage == expected, 'candidate wrapper name or symbol differs from the launch profile')


def verify_cold_launch_profile(doc, draft, denom='del'):
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
    require(all(len(row['coins']) == 1 and row['coins'][0]['denom'] == denom for row in bank['balances'])
            and len(bank['supply']) == 1 and bank['supply'][0]['denom'] == denom,
            'candidate must declare a native-only genesis ledger and supply')
    metadata = bank['denom_metadata']
    name, symbol = ('MintCandy Test MSP', 'tMSP') if denom == 'tmsp' else ('MintCandy', 'MSP')
    require(len(metadata) == 1 and coin['params']['base_title'] == name and
            [metadata[0][key] for key in ('base', 'display', 'name', 'symbol')] ==
            [denom, symbol, name, symbol] and
            [(unit['denom'], unit['exponent']) for unit in metadata[0]['denom_units']] == [(denom, 0), (symbol, 18)],
            'candidate MintCandy MSP metadata differs')
    if denom != 'del':
        vm = state['evm']['params']
        require(coin['params']['base_denom'] == vm['evm_denom'] == vm['extended_denom_options']['extended_denom'] ==
                state['staking']['params']['bond_denom'] == state['validator']['params']['base_denom'] == denom,
                'candidate application denominations differ from the launch profile')
        for field in ('min_deposit', 'expedited_min_deposit'):
            deposit = state['gov']['params'][field]
            require(len(deposit) == 1 and deposit[0]['denom'] == denom,
                    'candidate governance deposit denomination differs from the launch profile')

    profile = state['withdrawal_settlement']
    for field, value in draft['app_state']['withdrawal_settlement'].items():
        if value is True:
            require(profile.get(field) is True, f'candidate requires {field}')
    require(profile.get('test_grace_until_height', 0) in (0, '0'), 'test grace is active in the candidate')
    roles = {role: evm_address(profile[role]) for role in SYSTEM_ROLES}
    require(len(set(roles.values())) == len(roles) and all(int(value, 16) for value in roles.values()),
            'candidate requires distinct nonzero system contract addresses')
    verify_wrapper_metadata(state['evm'], roles['wrapped_del'], denom)

    nft = state['nft']
    collections = nft.get('collections') or []
    require(not any(coin.get(field) for field in ('coins', 'checks')) and
            not any(nft.get(field) for field in ('external_bindings', 'isolated_positions')) and
            len(collections) == 1 and evm_address(collections[0]['addressDRC']) == roles['position_vault'] and
            not collections[0].get('tokens') and collections[0].get('supply', 0) in (0, '0'),
            'candidate must start with an empty system receipt collection and no user assets')
    require(isinstance(state['ibc'], dict) and isinstance(state['transfer'], dict),
            'candidate must declare IBC and transfer genesis')
    require(state['mint']['params']['mint_denom'] == denom and all(
        state['mint'][section][field] == '0.000000000000000000' for section, field in (
            ('minter', 'inflation'), ('minter', 'annual_provisions'),
            ('params', 'inflation_min'), ('params', 'inflation_max'),
        )), 'candidate must disable MSP emission')
    return roles


def verify_recipients(report, operators, plan, roles, relayer_wallets):
    wrapped = [row['address'] for row in report['bankAllocations'] if row['category'] == wrapper_category(report['denom'])]
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
    roles = verify_cold_launch_profile(doc, draft, plan['denom'])
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
        'nativeDenom': report['denom'],
        'initialSupplyAtomic': report['initialSupplyAtomic'],
        'checkedValidators': len(operators),
        'checkedBankRecipients': len(report['bankAllocations']),
        'checkedDelegations': len(report['genesisDelegations']),
        'wrappedTokenSymbol': 'WtMSP' if plan['denom'] == 'tmsp' else 'WMSP',
        'runtimeInitChainVerified': False,
        'ownershipProofsVerified': False,
        'publicGenesisApproved': False,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--genesis', required=True, type=Path)
    parser.add_argument('--draft', type=Path, default=DEFAULT_DRAFT)
    parser.add_argument('--plan', type=Path, default=DEFAULT_PLAN)
    parser.add_argument('--intake', required=True, type=Path, help='private copy of the reviewed operator intake')
    parser.add_argument('--relayer-wallet', required=True, action='append', help='reviewed relayer gas wallet, repeat twice')
    args = parser.parse_args()
    try:
        draft, plan = read_reviewed_inputs(args.draft, args.plan)
        applications = check_applications(read_intake(args.intake), plan)
        require(applications['status'] == 'PRECHECK_PASSED', 'four complete operator applications are required')
        with args.genesis.open('rb') as stream:
            data = stream.read(MAX_GENESIS_BYTES + 1)
        require(0 < len(data) <= MAX_GENESIS_BYTES, 'invalid genesis byte count')
        report = verify_candidate(data, applications, plan, draft, sdk_relayer_wallets(args.relayer_wallet, plan))
    except (OSError, AttributeError, KeyError, RecursionError, TypeError, ValueError) as error:
        parser.exit(1, f'candidate genesis: {error}\n')
    print(json.dumps(report, sort_keys=True, indent=2))


if __name__ == '__main__':
    main()
