"""Prepare the founding ledger and contract inputs for an offline MintCandy bootstrap."""

import argparse
from decimal import Decimal
import json
from pathlib import Path

from check_operator_applications import check_applications, read_intake, strict_native_address
from check_public_launch_draft import DEFAULT_DRAFT, DEFAULT_PLAN, check_plan, read_json, require
from native_launch_allocation import evm_address


def bank_allocation(operators, plan, wrapped_wallet, relayer_wallets):
    require(len(relayer_wallets) == 2, 'two IBC relayer gas wallets are required')
    amounts = {row['purpose']: row['amount_atoms'] for row in plan['bank_allocation']}
    founder = operators[0]['stakeOwnerAddress']
    require(strict_native_address(founder, 'd0') == plan['founder_evm_address'].lower(),
            'founder wallet differs from the approved plan')
    wallets = [
        (founder, amounts['founder_free_balance_for_distribution_or_sale']),
        (wrapped_wallet, amounts['initial_wdel_backing']),
    ]
    wallets.extend((row['gasWalletAddress'], row['gasBudgetAtoms']) for row in operators)
    wallets.extend((wallet, amounts['ibc_relayer_fee_wallet']) for wallet in relayer_wallets)
    evm_wallets = [strict_native_address(wallet, 'd0') for wallet, _ in wallets]
    require(len(set(evm_wallets)) == len(wallets), 'planned founder, backing and gas recipient addresses must be distinct')
    require(sum(int(amount) for _, amount in wallets) == int(plan['initial_supply_atoms']),
            'founding bank ledger differs from the approved supply')
    return {
        'supply': [{'denom': plan['denom'], 'amount': plan['initial_supply_atoms']}],
        'balances': [{'address': wallet, 'coins': [{'denom': plan['denom'], 'amount': amount}]}
                     for wallet, amount in sorted(wallets)],
    }


def prepare_ledger(applications, plan, wrapped_wallet, gateway, relayer_wallets):
    require(applications['status'] == 'PRECHECK_PASSED', 'four complete operator applications are required')
    require(applications['chainId'] == plan['chain_id'] and applications['evmChainId'] == plan['evm_chain_id'],
            'operator roster differs from the launch signing domain')
    operators = sorted(applications['operators'], key=lambda row: row['operatorAddress'])
    require(len(operators) == plan['approved_validator_count'], 'operator roster differs from the launch plan')
    bank = bank_allocation(operators, plan, wrapped_wallet, relayer_wallets)
    wrapped = strict_native_address(wrapped_wallet, 'd0')
    gateway = evm_address(gateway)
    require(int(gateway, 16) != 0 and gateway not in {
        strict_native_address(balance['address'], 'd0') for balance in bank['balances']
    }, 'Gateway must differ from the funded accounts and wrapper')

    commission = plan['validator_commission_fraction']
    founder = plan['founder_evm_address'].lower()
    delegation_total = 0
    validators, delegations, registrations, funding = [], [], [], []
    for row in operators:
        require(Decimal(row['commission']) == Decimal(commission), 'operator commission differs from the launch plan')
        require(row['stakeOwnerAddress'] == row['beneficiaryAddress'] and
                strict_native_address(row['stakeOwnerAddress'], 'd0') == founder,
                'genesis position must belong to the approved founder')
        amount = row['stakeAmountAtoms']
        require(amount == plan['approved_initial_stake_per_validator_atoms'], 'operator stake differs from the launch plan')
        delegation_total += int(amount)
        operator = row['operatorAddress']
        operator_evm = row['operatorEvmAddress']
        pubkey = row['consensusPubkey']
        description = {'moniker': row['moniker']}
        validators.append({
            'operator_address': operator, 'reward_address': row['rewardAddress'],
            'consensus_pubkey': pubkey.copy(), 'description': description, 'commission': commission, 'online': True,
        })
        delegations.append({
            'delegator': row['stakeOwnerAddress'], 'validator': operator,
            'stake': {'stake': {'denom': plan['denom'], 'amount': amount}},
        })
        meta = {
            'operator_address': operator, 'reward_address': strict_native_address(row['rewardAddress'], 'd0'),
            'consensus_pubkey': pubkey['key'], 'description': description, 'commission': commission,
        }
        registrations.append({'validator': operator_evm, 'meta': json.dumps(meta, ensure_ascii=False, separators=(',', ':'))})
        funding.append({'beneficiary': founder, 'validator': operator_evm, 'valueAtomic': amount})
    wrapper_balance = next(row['coins'][0]['amount'] for row in bank['balances'] if row['address'] == wrapped_wallet)
    require(delegation_total == int(wrapper_balance), 'founding delegations must equal the wrapper backing')
    return {
        'status': 'FOUNDING_LEDGER_PREPARED',
        'chainId': plan['chain_id'], 'evmChainId': plan['evm_chain_id'], 'initialHeight': 1,
        'bank': bank, 'validatorDeclarations': validators, 'delegations': delegations,
        'contractSetup': {
            'masterValidatorRegistration': {'method': 'addValidators', 'rows': registrations},
            'gatewayFunding': {'gateway': gateway, 'wrappedDel': wrapped, 'method': 'delegateDELTo', 'calls': funding},
        },
        'runtimeInitChainVerified': False, 'ownershipProofsVerified': False, 'publicGenesisApproved': False,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--intake', required=True, type=Path, help='private copy of reviewed operator intake')
    parser.add_argument('--wrapped-del-wallet', required=True, help='d0 account address of the deployed wrapper')
    parser.add_argument('--gateway', required=True, help='EVM address of the deployed Gateway')
    parser.add_argument('--relayer-wallet', required=True, action='append', help='reviewed d0 gas wallet, repeat twice')
    parser.add_argument('--output', required=True, type=Path, help='new JSON file for the founding ledger')
    args = parser.parse_args()
    try:
        plan = read_json(DEFAULT_PLAN)
        check_plan(plan, read_json(DEFAULT_DRAFT))
        applications = check_applications(read_intake(args.intake), plan)
        ledger = prepare_ledger(applications, plan, args.wrapped_del_wallet, args.gateway, args.relayer_wallet)
        with args.output.open('x', encoding='utf-8') as stream:
            json.dump(ledger, stream, indent=2, sort_keys=True, ensure_ascii=False)
            stream.write('\n')
    except (OSError, AttributeError, KeyError, RecursionError, TypeError, ValueError) as error:
        parser.exit(1, f'founding ledger: {error}\n')


if __name__ == '__main__':
    main()
