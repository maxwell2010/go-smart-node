"""Check that the public launch template retains unresolved launch inputs.

This checks draft hygiene only. The final builder must use fresh mcnd defaults,
authenticated contract state, genesis validation and isolated InitChain checks.
"""

import argparse
import json
from pathlib import Path
import re


DEFAULT_DRAFT = Path(__file__).resolve().parents[1] / 'docs/ru/public-genesis.draft.json'
DEFAULT_PLAN = DEFAULT_DRAFT.with_name('public-launch-allocation.draft.json')
DEFAULT_INTAKE = DEFAULT_DRAFT.with_name('operator-launch-intake.example.json')
ATOMS_PER_DEL = 10**18
SYSTEM_ROLES = ('registry', 'gateway', 'library', 'nft_center', 'position_vault',
                'master_validator', 'wrapped_del', 'token_center', 'curve_converter')
UNRESOLVED_PATHS = (
    ('genesis_time',), ('app_state', 'auth'),
    ('app_state', 'bank', 'balances'), ('app_state', 'bank', 'supply', 0, 'amount'),
    ('app_state', 'coin', 'params', 'base_volume'), ('app_state', 'coin', 'coins'),
    ('app_state', 'evm', 'accounts'), ('app_state', 'evm', 'preinstalls'),
    ('app_state', 'evm', 'params', 'active_static_precompiles'),
    ('app_state', 'nft', 'collections'), ('app_state', 'nft', 'external_bindings'),
    ('app_state', 'nft', 'isolated_vaults'),
    ('app_state', 'validator', 'validators'), ('app_state', 'validator', 'delegations'),
    ('app_state', 'validator', 'params', 'max_validators'),
    ('app_state', 'validator', 'evm_stake_bindings'),
    ('app_state', 'validator', 'binding_revision'),
) + tuple(('app_state', 'withdrawal_settlement', role) for role in SYSTEM_ROLES)
SECRET_FIELDS = {'priv_key', 'private_key', 'mnemonic', 'seed_phrase', 'node_key', 'secret',
                 'privatekey', 'privkey', 'seed', 'password', 'api_token', 'access_token'}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def unique_object(pairs):
    result = {}
    for name, value in pairs:
        require(name not in result, f'duplicate JSON field {name}')
        result[name] = value
    return result


def check_contents(value, *, forbid_public_material=True):
    if isinstance(value, dict):
        require(not (SECRET_FIELDS & {key.lower() for key in value}), 'secret field in draft')
        for child in value.values():
            check_contents(child, forbid_public_material=forbid_public_material)
    elif isinstance(value, list):
        for child in value:
            check_contents(child, forbid_public_material=forbid_public_material)
    elif isinstance(value, str) and forbid_public_material:
        require(not re.search(r'0x[0-9a-fA-F]{40}|\bd0(?:valoper|valcons)?1[a-z0-9]+', value),
                'concrete account or contract address in unresolved draft')
        require(not re.fullmatch(r'[0-9a-fA-F]{64,}|[A-Za-z0-9+/]{43}=', value),
                'key or code material in draft')


def check_draft(doc):
    check_contents(doc)
    for path in UNRESOLVED_PATHS:
        value = doc
        for part in path:
            value = value[part]
        require(value is None, 'unapproved launch input at ' + '.'.join(map(str, path)))
    require(doc['initial_height'] == '1' and doc['validators'] == [] and doc['app_hash'] == '',
            'draft requires height one and application-derived validator power')
    domain = doc['app_state']['validator']['binding_domain']
    match = re.fullmatch(r'mintcandy_([1-9][0-9]*)-([1-9][0-9]*)', doc['chain_id'])
    require(match is not None, 'invalid proposed chain ID format')
    evm_id, revision = map(int, match.groups())
    require(max(evm_id, revision) < 2**64 and evm_id not in {75, 202020, 20202020},
            'invalid proposed signing domain')
    require(domain == {'chain_id': doc['chain_id'], 'evm_chain_id': str(evm_id)},
            'binding domain differs from proposed chain ID')
    require(doc['consensus_params']['block']['max_gas'] == '24000000',
            'draft must preserve the proposed 24 million block gas cap')
    state = doc['app_state']
    require(state['genutil']['gen_txs'] == [] and state['staking']['validators'] == [] and
            state['staking']['delegations'] == [] and state['staking']['last_total_power'] == '0',
            'native profile cannot import SDK consensus stake or genesis transactions')
    require(state['coin']['params']['base_denom'] == state['evm']['params']['evm_denom'] ==
            state['staking']['params']['bond_denom'] == state['bank']['supply'][0]['denom'] == 'del',
            'native denomination differs')
    mint = state['mint']
    require(mint['params']['mint_denom'] == 'del' and all(value == '0.000000000000000000' for value in (
        mint['minter']['inflation'], mint['minter']['annual_provisions'],
        mint['params']['inflation_max'], mint['params']['inflation_min'])), 'emission enabled in draft')
    require(state['withdrawal_settlement']['enable_native_consensus'] is True and
            state['withdrawal_settlement']['enable_native_fee_rewards'] is True,
            'draft requires the native consensus and fee reward profile')
    return {'status': 'DRAFT_NOT_RUNNABLE', 'chainIdProposal': doc['chain_id'],
            'blockGasProposal': '24000000', 'unresolvedInputs': len(UNRESOLVED_PATHS),
            'checks': ['unapproved inputs remain null', 'no concrete keys or addresses',
                       'no declared supply or validator count', 'zero inflation',
                       'matching proposed signing domains']}


def atomic(value):
    require(isinstance(value, str) and re.fullmatch(r'0|[1-9][0-9]{0,33}', value),
            'amount must be a canonical nonnegative integer string')
    return int(value)


def check_plan(plan, draft):
    check_contents(plan, forbid_public_material=False)
    require(plan['status'] == 'draft_unapproved' and plan['approved_validator_count'] is None,
            'allocation must remain unapproved with unresolved validator count')
    decisions = plan['decisions']
    require(decisions['allocation_approved'] is False and
            decisions['initial_asset_creation_approved'] is None and
            all(value is None or value is False for name, value in decisions.items() if name.endswith('_approved')),
            'allocation or asset decision was approved in a draft')
    require(plan['founder_address_ownership_confirmed'] is False,
            'public founder address control has not been confirmed')
    require(plan['chain_id'] == draft['chain_id'] and
            plan['evm_chain_id'] == draft['app_state']['validator']['binding_domain']['evm_chain_id'],
            'plan signing domains differ from the genesis proposal')
    require(plan['denom'] == 'del' and type(plan['decimals']) is int and plan['decimals'] == 18 and
            atomic(plan['atoms_per_del']) == ATOMS_PER_DEL, 'invalid DEL denomination scale')
    total = atomic(plan['initial_supply_atoms'])
    require(total == 100_000 * ATOMS_PER_DEL, 'supply proposal must total exactly 100000 DEL')
    allocations = plan['bank_allocation']
    require(sum(atomic(row['amount_atoms']) for row in allocations) == total,
            'bank allocation total differs from proposed supply')
    require(all(row['bank_address'] is None for row in allocations),
            'bank recipients remain unresolved in the proposal')
    founder = plan['founder_evm_address']
    require(re.fullmatch(r'0x[0-9a-fA-F]{40}', founder) is not None, 'invalid proposed founder address')
    delegations = plan['initial_delegations_within_wdel_backing']
    count = plan['proposed_rehearsal_validator_count']
    require(type(count) is int and 1 <= count <= 16 and len(delegations) == count and
            len({row['validator_slot'] for row in delegations}) == count,
            'rehearsal slots differ from proposed delegation count')
    for row in delegations:
        amount = atomic(row['amount_atoms'])
        require(amount > 0 and row['operator_address'] is None and row['beneficiary_address'] is None and
                row['proposed_owner_evm_address'] == founder,
                'initial delegation recipient or proposed owner differs')
        require(atomic(row['expected_voting_power']) == amount // ATOMS_PER_DEL,
                'delegation voting power differs from whole DEL principal')
    backing = [row for row in allocations if row['purpose'] == 'initial_wdel_backing']
    require(len(backing) == 1 and atomic(backing[0]['amount_atoms']) ==
            sum(atomic(row['amount_atoms']) for row in delegations),
            'WDEL backing differs from proposed delegation total')
    require(plan['allocation_is_additional_to_delegations'] is False,
            'delegations cannot be added to the bank allocation twice')
    for purpose, summary in (('approved_drc20_reserve', 'initial_drc20_reserve_atoms'),
                             ('approved_erc1155_origin_reserve', 'initial_nft_reserve_atoms')):
        rows = [row for row in allocations if row['purpose'] == purpose]
        require(len(rows) == 1 and atomic(rows[0]['amount_atoms']) == atomic(plan[summary]) > 0,
                'asset reserve differs from proposed bank allocation')
        row = rows[0]
        require(row['contract_address'] is None and row['approved_owners'] is None and
                row['asset_name'] is None, 'asset creation inputs remain unresolved')
        if purpose == 'approved_erc1155_origin_reserve':
            require(type(row['units']) is int and row['units'] > 0 and
                    row['units'] * atomic(row['reserve_per_unit_atoms']) == atomic(row['amount_atoms']),
                    'NFT units and reserve rate differ from backing')
            require(row['token_id'] is None, 'NFT identity remains unresolved')
        else:
            require(all(row[name] is None for name in ('asset_parameters', 'token_supply_atoms', 'curve_parameters')),
                    'DRC20 curve and issuance inputs remain unresolved')
    require(plan['reward_emission'] is False and plan['reward_source'] == 'collected_del_transaction_fees' and
            atomic(plan['fee_collector_atoms']) == atomic(plan['isolated_position_vault_atoms']) == 0,
            'fee rewards require zero initial collector and vault balances and no emission')
    return {'status': 'PLAN_DRAFT_UNAPPROVED', 'initialSupplyAtoms': str(total),
            'rehearsalValidatorCount': count, 'approvedValidatorCount': None,
            'initialAssetCreationApproved': None}


def check_intake(intake):
    check_contents(intake, forbid_public_material=False)
    require(intake['status'] == 'draft_unapproved' and intake['schema_version'] == 2,
            'intake must remain an unapproved version two template')
    network = intake['network']
    require(all(network[name] is None for name in ('cosmos_chain_id', 'evm_chain_id', 'genesis_time_utc',
                'approved_validator_count', 'approved_by', 'approval_reference')),
            'network launch approval inputs remain unresolved')
    release = intake['release']
    software = release['software']
    require(software['version'] == '1.0.0' and
            re.fullmatch(r'[0-9a-f]{40}', software['source_revision']) is not None and
            all(re.fullmatch(r'[0-9a-f]{64}', software[name]) is not None
                for name in ('binary_archive_sha256', 'binary_sha256')),
            'released software metadata requires version and source/archive/binary hashes')
    require(software['release_url'] == 'https://github.com/maxwell2010/go-smart-node/releases/tag/v1.0.0' and
            software['signature_scheme'] == 'github_oidc_sigstore', 'software release identity differs')
    require(release['genesis'] and all(value is None for value in release['genesis'].values()),
            'software publication cannot approve or sign an unresolved public genesis')
    require(intake['allocation']['expected_total_atoms'] is None and
            all(value is None for row in intake['allocation']['bank_balances'] for value in row.values()),
            'intake cannot preapprove proposed allocation amounts or recipients')
    require(intake['profile']['max_validators'] is None and intake['profile']['block_max_gas'] is None,
            'profile parameters await launch approval')
    for validator in intake['validators']:
        require(validator['consensus_pubkey']['key'] is None and validator['operator_address'] is None and
                validator['stake_owner_address'] is None and validator['stake_amount_atoms'] is None and
                all(value is None for value in validator['readiness'].values()),
                'validator identity, ownership and approval remain unresolved')
    return {'status': 'INTAKE_DRAFT_UNAPPROVED', 'softwareVersion': software['version'],
            'publicGenesisApproved': False}


def read_json(path):
    return json.loads(path.read_text(encoding='utf8'), object_pairs_hook=unique_object)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--draft', type=Path, default=DEFAULT_DRAFT)
    parser.add_argument('--plan', type=Path, default=DEFAULT_PLAN)
    parser.add_argument('--intake', type=Path, default=DEFAULT_INTAKE)
    args = parser.parse_args()
    try:
        doc = read_json(args.draft)
        report = {'validationScope': 'draft_structure_and_integer_arithmetic_only',
                  'genesis': check_draft(doc), 'allocation': check_plan(read_json(args.plan), doc),
                  'intake': check_intake(read_json(args.intake))}
        print(json.dumps(report, indent=2))
    except (OSError, KeyError, IndexError, TypeError, ValueError) as error:
        parser.exit(1, f'public launch draft: {error}\n')


if __name__ == '__main__':
    main()
