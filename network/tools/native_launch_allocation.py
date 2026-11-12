"""Report the public DEL allocation and validator declarations of a launch genesis."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import re

from withdrawal_genesis_smoke import ADDRESS, bech32_payload, check


ATOMS_PER_DEL = 10**18
MAX_GENESIS_BYTES = 64 * 1024 * 1024
RESERVE_ROLES = {
    'wrapped_del': 'wrapped_del_backing',
    'position_vault': 'isolated_nft_vault',
}
SYSTEM_ROLES = ('registry', 'gateway', 'library', 'nft_center', 'master_validator', 'token_center', 'curve_converter')


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        check(key not in result, 'genesis JSON contains a duplicate field')
        result[key] = value
    return result


def uint(value, label):
    check((type(value) is int or isinstance(value, str)) and re.fullmatch(r'0|[1-9][0-9]{0,77}', str(value)),
          f'invalid integer in {label}')
    return int(value)


def evm_address(value):
    check(isinstance(value, str) and re.fullmatch(ADDRESS, value), 'invalid EVM address in genesis allocation')
    return value.lower()


def del_amount(coins):
    amounts = [uint(coin['amount'], 'bank DEL amount') for coin in coins if coin['denom'] == 'del']
    check(len(amounts) <= 1, 'bank coin list contains duplicate DEL')
    return sum(amounts)


def allocation_report(data):
    doc = json.loads(data, object_pairs_hook=unique_object)
    chain_id = doc['chain_id']
    domain = re.fullmatch(r'mintcandy_([1-9][0-9]{0,19})-([1-9][0-9]{0,19})', chain_id)
    check(domain is not None and len(chain_id) <= 50, 'launch audit requires a separate MintCandy chain ID')
    evm_id, revision = map(int, domain.groups())
    check(max(evm_id, revision) < 2**64 and evm_id not in {75, 202020, 20202020}, 'invalid launch signing domains')
    check(uint(doc['initial_height'], 'initial_height') == 1, 'launch audit requires genesis height one')
    check(isinstance(doc['genesis_time'], str) and doc['genesis_time'], 'launch genesis must declare its timestamp')
    state = doc['app_state']
    profile, bank, coin, validator = (state[key] for key in ('withdrawal_settlement', 'bank', 'coin', 'validator'))
    check(profile.get('enable_native_consensus') is True and profile.get('enable_del_delegation') is True,
          'launch audit requires the native DEL consensus profile')
    binding = validator['binding_domain']
    check(binding['chain_id'] == chain_id and uint(binding['evm_chain_id'], 'binding EVM ID') == evm_id,
          'validator binding domain differs from the genesis signing domains')

    role_addresses = {key: evm_address(profile[key]) for key in (*RESERVE_ROLES, *SYSTEM_ROLES) if key in profile}
    role_addresses = {key: value for key, value in role_addresses.items() if int(value, 16) != 0}
    categories = {value: RESERVE_ROLES.get(key, 'system_contracts') for key, value in role_addresses.items()}
    categories['0x' + '0' * 40] = 'zero_address_balance'
    for account in state.get('auth', {}).get('accounts') or []:
        if account.get('@type') == '/cosmos.auth.v1beta1.ModuleAccount':
            categories.setdefault(bech32_payload(account['base_account']['address']), 'module_accounts')
    for collection in state['nft'].get('collections') or []:
        if collection.get('addressDRC'):
            categories.setdefault(evm_address(collection['addressDRC']), 'nft_origin_backing')
    drc20 = {}
    for asset in coin.get('coins') or []:
        address = evm_address(asset['drc20_address'])
        check(address not in categories, 'DRC20 backing overlaps another allocation role')
        categories[address] = 'drc20_backing'
        drc20[address] = uint(asset['reserve'], 'DRC20 reserve')

    balances, allocations, totals = {}, [], {}
    for account in bank['balances']:
        address = bech32_payload(account['address'])
        check(address not in balances, 'genesis repeats a bank account')
        amount = balances[address] = del_amount(account['coins'])
        if not amount:
            continue
        category = categories.get(address, 'other_bank_balances')
        totals[category] = totals.get(category, 0) + amount
        allocations.append({'address': account['address'], 'amountAtomic': str(amount), 'category': category})
    total = sum(totals.values())
    check(0 < total <= 10**33 and coin['params']['base_denom'] == 'del' and
          total == uint(coin['params']['base_volume'], 'native base volume'), 'native DEL total differs from bank allocation')
    if bank.get('supply'):
        check(total == del_amount(bank['supply']), 'declared bank DEL supply differs from bank allocation')
    check(all(balances.get(address, 0) == amount for address, amount in drc20.items()),
          'DRC20 declared reserves differ from bank DEL allocation')

    operators, seen_keys = {}, set()
    for row in validator['validators']:
        operator = row['operator_address']
        key = row['consensus_pubkey']
        key_bytes = base64.b64decode(key['key'], validate=True)
        check(key['@type'] == '/cosmos.crypto.ed25519.PubKey' and len(key_bytes) == 32 and key_bytes not in seen_keys,
              'invalid or duplicate Ed25519 consensus public key')
        check(operator not in operators, 'duplicate validator operator')
        seen_keys.add(key_bytes)
        operators[operator] = {
            'operatorAddress': operator,
            'operatorEvmAddress': bech32_payload(operator, 'd0valoper'),
            'rewardAddress': row['reward_address'],
            'consensusPublicKeyBase64': key['key'],
            'consensusAddressHex': hashlib.sha256(key_bytes).hexdigest()[:40].upper(),
            'moniker': row.get('description', {}).get('moniker', ''),
            'commission': row['commission'],
            'declaredOnline': row.get('online', False),
            'initialStakeAtomic': 0,
        }
        bech32_payload(row['reward_address'])

    delegations = []
    for row in validator['delegations']:
        amount = row['stake']['stake']
        check(row['validator'] in operators and amount['denom'] == 'del', 'invalid initial native DEL delegation')
        value = uint(amount['amount'], 'initial delegation')
        bech32_payload(row['delegator'])
        operators[row['validator']]['initialStakeAtomic'] += value
        delegations.append({'delegator': row['delegator'], 'validator': row['validator'], 'amountAtomic': str(value)})
    total_stake = sum(row['initialStakeAtomic'] for row in operators.values())
    check(0 < total_stake <= balances.get(role_addresses['wrapped_del'], 0), 'initial delegated DEL lacks bank backing')
    for row in operators.values():
        row['initialStakeAtomic'] = str(row['initialStakeAtomic'])

    return {
        'schemaVersion': 1,
        'chainId': chain_id,
        'evmChainId': str(evm_id),
        'genesisHeight': 1,
        'genesisTime': doc['genesis_time'],
        'genesisSha256': hashlib.sha256(data).hexdigest(),
        'denom': 'del',
        'atomsPerDel': str(ATOMS_PER_DEL),
        'initialSupplyAtomic': str(total),
        'allocationTotalsAtomic': {key: str(value) for key, value in totals.items()},
        'bankAllocations': sorted(allocations, key=lambda row: row['address']),
        'initialStakingAtomicAlreadyInBankSupply': str(total_stake),
        'genesisDelegations': sorted(delegations, key=lambda row: (row['validator'], row['delegator'])),
        'validators': [operators[key] for key in sorted(operators)],
        'systemContractAddresses': role_addresses,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--genesis', required=True, type=Path, help='Public genesis previously validated by mcnd')
    parser.add_argument('--output', required=True, type=Path, help='Create a new public allocation report')
    args = parser.parse_args()
    try:
        with args.genesis.open('rb') as source:
            data = source.read(MAX_GENESIS_BYTES + 1)
        check(0 < len(data) <= MAX_GENESIS_BYTES, 'invalid genesis byte count')
        report = allocation_report(data)
        with args.output.open('x', encoding='utf8') as destination:
            json.dump(report, destination, indent=2, sort_keys=True)
            destination.write('\n')
    except (KeyError, OSError, TypeError, ValueError) as error:
        parser.exit(1, f'genesis allocation audit: {error}\n')


if __name__ == '__main__':
    main()
