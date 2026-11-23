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
PUBLISHED_SOFTWARE_RECORD = DEFAULT_DRAFT.parent / 'evidence/software-release-1.1.5.json'
ATOMS_PER_DEL = 10**18
APPROVED_CHAIN_ID = 'mintcandy_9905401-1'
APPROVED_EVM_CHAIN_ID = '9905401'
APPROVED_SUPPLY = 5_041_000 * ATOMS_PER_DEL
SYSTEM_ROLES = ('registry', 'gateway', 'library', 'nft_center', 'position_vault',
                'master_validator', 'wrapped_del', 'token_center', 'curve_converter')
UNRESOLVED_PATHS = (
    ('genesis_time',), ('app_state', 'auth'),
    ('app_state', 'bank', 'balances'), ('app_state', 'bank', 'supply', 0, 'amount'),
    ('app_state', 'coin', 'params', 'base_volume'),
    ('app_state', 'evm', 'accounts'), ('app_state', 'evm', 'preinstalls'),
    ('app_state', 'evm', 'params', 'active_static_precompiles'),
    ('app_state', 'nft', 'collections'),
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
            'binding domain differs from genesis chain ID')
    require(doc['chain_id'] == APPROVED_CHAIN_ID and str(evm_id) == APPROVED_EVM_CHAIN_ID,
            'genesis differs from the approved signing domain')
    require(doc['consensus_params']['block']['max_gas'] == '24000000',
            'draft must preserve the proposed 24 million block gas cap')
    state = doc['app_state']
    require(state['coin']['coins'] == [] and state['nft']['external_bindings'] == [],
            'selected genesis cannot contain initial user assets')
    require(state['genutil']['gen_txs'] == [] and state['staking']['validators'] == [] and
            state['staking']['delegations'] == [] and state['staking']['last_total_power'] == '0',
            'native profile cannot import SDK consensus stake or genesis transactions')
    require(state['coin']['params']['base_denom'] == state['evm']['params']['evm_denom'] ==
            state['staking']['params']['bond_denom'] == state['bank']['supply'][0]['denom'] == 'del',
            'native denomination differs')
    metadata = state['bank']['denom_metadata']
    require(len(metadata) == 1 and state['coin']['params']['base_title'] == 'MintCandy' and
            metadata[0]['name'] == 'MintCandy' and metadata[0]['symbol'] == 'MSP' and
            metadata[0]['base'] == 'del' and metadata[0]['display'] == 'MSP' and
            metadata[0]['denom_units'] == [
                {'denom': 'del', 'exponent': 0, 'aliases': []},
                {'denom': 'MSP', 'exponent': 18, 'aliases': []}],
            'MintCandy MSP metadata or denomination scale differs')
    mint = state['mint']
    require(mint['params']['mint_denom'] == 'del' and all(value == '0.000000000000000000' for value in (
        mint['minter']['inflation'], mint['minter']['annual_provisions'],
        mint['params']['inflation_max'], mint['params']['inflation_min'])), 'emission enabled in draft')
    require(state['withdrawal_settlement']['enable_native_consensus'] is True and
            state['withdrawal_settlement']['enable_native_fee_rewards'] is True,
            'draft requires the native consensus and fee reward profile')
    return {'status': 'DRAFT_NOT_RUNNABLE', 'nativeName': 'MintCandy', 'nativeSymbol': 'MSP',
            'baseDenom': 'del', 'displayExponent': 18, 'approvedChainId': doc['chain_id'],
            'blockGasProposal': '24000000', 'unresolvedInputs': len(UNRESOLVED_PATHS),
            'checks': ['unapproved inputs remain null', 'no concrete keys or addresses',
                       'no declared supply or concrete validator roster', 'zero inflation',
                       'matching approved signing domains']}


def atomic(value):
    require(isinstance(value, str) and re.fullmatch(r'0|[1-9][0-9]{0,33}', value),
            'amount must be a canonical nonnegative integer string')
    return int(value)


def check_plan(plan, draft):
    check_contents(plan, forbid_public_material=False)
    require(plan['native_token_name'] == 'MintCandy' and plan['native_token_symbol'] == 'MSP' and
            plan['display_denom'] == 'MSP' and atomic(plan['atoms_per_native_token']) == ATOMS_PER_DEL,
            'allocation MintCandy MSP identity differs')
    require(plan['status'] == 'draft_unapproved' and type(plan['approved_validator_count']) is int and
            plan['approved_validator_count'] == 4 and
            atomic(plan['approved_initial_stake_per_validator_atoms']) == 10_000 * ATOMS_PER_DEL,
            'selected four-validator start or stake size differs')
    decisions = plan['decisions']
    require(decisions['allocation_approved'] is True and decisions['chain_id_approved'] is True,
            'record the approved allocation and signing domain')
    require(decisions['initial_asset_creation_approved'] is False and
            all(value is None or value is False for name, value in decisions.items()
                if name.endswith('_approved') and name not in
                {'allocation_approved', 'chain_id_approved', 'treasury_control_approved',
                 'stake_ownership_approved', 'initial_asset_profile_approved'}),
            'unexpected public launch or asset approval in a draft')
    require(decisions['stake_ownership_approved'] is True and
            decisions['selected_stake_ownership'] == 'founder_owned_delegations' and
            decisions['selected_stake_owner_evm_address'] == plan['founder_evm_address'],
            'selected founder delegation ownership differs')
    require(plan['founder_address_ownership_confirmed'] is False,
            'public founder address control has not been confirmed')
    require(plan['chain_id'] == draft['chain_id'] and
            plan['evm_chain_id'] == draft['app_state']['validator']['binding_domain']['evm_chain_id'],
            'plan signing domains differ from the genesis proposal')
    require(plan['denom'] == 'del' and type(plan['decimals']) is int and plan['decimals'] == 18 and
            atomic(plan['atoms_per_del']) == ATOMS_PER_DEL, 'invalid MSP denomination scale')
    total = atomic(plan['initial_supply_atoms'])
    require(plan['initial_supply_is_proposal'] is False, 'record the approved supply')
    allocations = plan['bank_allocation']
    require(sum(atomic(row['amount_atoms']) for row in allocations) == total,
            'bank allocation total differs from approved supply')
    free = [row for row in allocations if row['purpose'] == 'founder_free_balance_for_distribution_or_sale']
    treasury = plan['treasury_policy']
    require(len(free) == 1 and atomic(free[0]['amount_atoms']) ==
            atomic(plan['founder_requested_free_balance_atoms']) == 5_000_000 * ATOMS_PER_DEL and
            not any(row['purpose'] == 'operating_treasury' for row in allocations) and
            decisions['treasury_control_approved'] is True and
            treasury['selection'] == 'founder_single_account' and treasury['selected_by_founder'] is True and
            treasury['evm_address'] == plan['founder_evm_address'] == free[0]['evm_address'] and
            treasury['multisig_required'] is False and atomic(treasury['separate_allocation_atoms']) == 0,
            'founder requested balance or selected single-account treasury differs')
    require(all(row['bank_address'] is None for row in allocations),
            'bank recipients remain unresolved in the proposal')
    founder = plan['founder_evm_address']
    require(re.fullmatch(r'0x[0-9a-fA-F]{40}', founder) is not None, 'invalid proposed founder address')
    delegations = plan['initial_delegations_within_wdel_backing']
    count = plan['proposed_rehearsal_validator_count']
    require(type(count) is int and count == plan['approved_validator_count'] and len(delegations) == count and
            len({row['validator_slot'] for row in delegations}) == count,
            'rehearsal slots differ from proposed delegation count')
    for row in delegations:
        amount = atomic(row['amount_atoms'])
        require(amount > 0 and row['operator_address'] is None and row['beneficiary_address'] is None and
                row['proposed_owner_evm_address'] == founder,
                'initial delegation recipient or proposed owner differs')
        require(atomic(row['expected_voting_power']) == amount // ATOMS_PER_DEL,
                'delegation voting power differs from whole MSP principal')
    backing = [row for row in allocations if row['purpose'] == 'initial_wdel_backing']
    require(len(backing) == 1 and atomic(backing[0]['amount_atoms']) ==
            sum(atomic(row['amount_atoms']) for row in delegations),
            'WDEL backing differs from proposed delegation total')
    require(all(atomic(row['amount_atoms']) == atomic(plan['approved_initial_stake_per_validator_atoms'])
                for row in delegations), 'selected equal validator stake size differs')
    require(plan['allocation_is_additional_to_delegations'] is False,
            'delegations cannot be added to the bank allocation twice')
    require(decisions['initial_asset_profile_approved'] is True and
            decisions['selected_initial_asset_profile'] == 'empty_user_assets_with_later_user_creation' and
            atomic(plan['initial_nft_reserve_atoms']) == atomic(plan['initial_drc20_reserve_atoms']) == 0 and
            not any(row['purpose'] in ('approved_drc20_reserve', 'approved_erc1155_origin_reserve')
                    for row in allocations), 'selected empty user asset allocation differs')
    require(total == APPROVED_SUPPLY, 'initial supply differs from approved allocation')
    for purpose, slot_field, prefix, wallet_count, amount in (
        ('validator_fee_wallet', 'validator_slot', 'V', 4, 200),
        ('ibc_relayer_fee_wallet', 'relayer_slot', 'R', 2, 100),
    ):
        gas_rows = [row for row in allocations if row['purpose'] == purpose]
        require(len(gas_rows) == wallet_count and {row[slot_field] for row in gas_rows} ==
                {f'{prefix}{i}' for i in range(1, wallet_count + 1)} and
                all(atomic(row['amount_atoms']) == amount * ATOMS_PER_DEL for row in gas_rows),
                'gas wallet budgets differ from approved allocation')
    require(len(allocations) == 8, 'unexpected bank allocation entry')
    require(plan['release_requirements'] and all(value == 'pending' for value in plan['release_requirements'].values()),
            'new user asset release requirements have not been demonstrated yet')
    require(plan['reward_emission'] is False and plan['reward_source'] == 'collected_del_transaction_fees' and
            atomic(plan['fee_collector_atoms']) == atomic(plan['isolated_position_vault_atoms']) == 0,
            'fee rewards require zero initial collector and vault balances and no emission')
    return {'status': 'PLAN_DRAFT_UNAPPROVED', 'initialSupplyAtoms': str(total),
            'selectedStakeOwnership': 'founder_owned_delegations',
            'rehearsalValidatorCount': count, 'approvedValidatorCount': 4,
            'allocationApproved': True, 'chainIdApproved': True,
            'initialAssetCreationApproved': False, 'userCreationAndMediaReleaseGates': 'pending'}


def check_intake(intake):
    check_contents(intake, forbid_public_material=False)
    asset = intake['allocation']
    require(asset['native_token_name'] == 'MintCandy' and asset['native_token_symbol'] == 'MSP' and
            asset['display_denom'] == 'MSP' and asset['denom'] == 'del' and asset['decimals'] == 18 and
            atomic(asset['atoms_per_native_token']) == ATOMS_PER_DEL,
            'validator intake MintCandy MSP identity differs')
    require(intake['status'] == 'draft_unapproved' and intake['schema_version'] == 2,
            'intake must remain an unapproved version two template')
    network = intake['network']
    require(type(network['approved_validator_count']) is int and network['approved_validator_count'] == 4,
            'intake selected validator count differs')
    require(network['cosmos_chain_id'] == APPROVED_CHAIN_ID and network['evm_chain_id'] == APPROVED_EVM_CHAIN_ID,
            'intake differs from the approved signing domain')
    require(all(network[name] is None for name in ('genesis_time_utc',
                'approved_by', 'approval_reference')),
            'network launch approval inputs remain unresolved')
    release = intake['release']
    software = release['software']
    published = read_json(PUBLISHED_SOFTWARE_RECORD)
    require(published['status'] == 'software-release' and published['includesGenesis'] is False and
            published['networkLaunchApproved'] is False, 'invalid published software record')
    require(all(software[field] == published[record] for field, record in (
        ('version', 'releaseVersion'), ('source_revision', 'sourceRevision'),
        ('binary_archive_sha256', 'archiveSha256'), ('binary_sha256', 'binarySha256'),
    )), 'intake software version or hashes differ from the published release')
    github = 'https://github.com/maxwell2010/go-smart-node/releases'
    version = published['releaseVersion']
    download = f'{github}/download/v{version}'
    require(software['release_url'] == f'{github}/tag/v{version}' and
            software['binary_archive_url'] == f'{download}/mcnd-{version}-linux-amd64.tar.gz' and
            software['installation_url'] == f'{download}/INSTALL_RU.md' and
            software['source_ref'] == published['sourceRef'] and software['signature_scheme'] == 'github_oidc_sigstore' and
            software['signer_workflow'] == 'maxwell2010/go-smart-node/.github/workflows/software-release.yml',
            'software release identity differs')
    require(release['genesis'] and all(value is None for value in release['genesis'].values()),
            'software publication cannot approve or sign an unresolved public genesis')
    require(atomic(intake['allocation']['expected_total_atoms']) == APPROVED_SUPPLY and
            all(value is None for row in intake['allocation']['bank_balances'] for value in row.values()),
            'intake must preserve approved total and unresolved wallet recipients')
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
