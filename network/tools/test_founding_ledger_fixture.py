import argparse
import base64
import copy
import json
import os
from pathlib import Path
import unittest

from check_operator_applications import ED25519_TYPE, check_applications
from check_public_launch_draft import ATOMS_PER_DEL, DEFAULT_DRAFT, DEFAULT_INTAKE, DEFAULT_PLAN, read_json
from prepare_founding_ledger import prepare_ledger
from verify_public_launch_genesis import verify_candidate


def prepared_fixture(source, deployment=None):
    expected = read_json(source / 'expectations.json')
    doc = read_json(source / 'public/genesis.json')
    if (doc['chain_id'], expected['evmChainId']) != ('mintcandy_990014-1', 990014):
        raise ValueError('founding ledger rehearsal requires the sponsored CI domain')
    plan = read_json(DEFAULT_PLAN)
    plan.update(chain_id=doc['chain_id'], evm_chain_id=str(expected['evmChainId']),
                founder_evm_address=expected['actor'].lower())
    intake = read_json(DEFAULT_INTAKE)
    intake['network'].update(cosmos_chain_id=plan['chain_id'], evm_chain_id=plan['evm_chain_id'])
    sample = intake['validators'][0]
    relayers = [row['address'] for row in doc['app_state']['bank']['balances']
                if row['coins'] == [{'denom': 'del', 'amount': str(100 * ATOMS_PER_DEL)}]]
    initial_reward_wallets = relayers.copy()
    intake['validators'] = []
    for index, operator in enumerate(expected['validators'], 1):
        row = copy.deepcopy(sample)
        row.update(
            application_id=f'CI-{index}', operator_address=operator['operator'],
            evm_operator_address=operator['evm'],
            consensus_pubkey={'@type': ED25519_TYPE, 'key': operator['publicKey']},
            reward_address=initial_reward_wallets[(index - 1) % len(initial_reward_wallets)],
            moniker=f"Ledger replay {operator['name']}",
            commission_fraction='0.05', stake_owner_address=expected['actorNative'],
            beneficiary_address=expected['actorNative'], stake_ownership_model='founder_owned_delegations',
            stake_amount_atoms=str(10_000 * ATOMS_PER_DEL), gas_wallet_address=operator['rewardNative'],
            gas_budget_atoms=str(200 * ATOMS_PER_DEL), operator_contact='ci.example.invalid',
        )
        node_id = f'{index:040x}'
        row['connectivity'].update(node_id=node_id, p2p_address=f'{node_id}@peer{index}.example.invalid:26656',
                                   role='sentry')
        if index == 1:
            row['gas_wallet_address'], relayers[0] = relayers[0], row['gas_wallet_address']
        intake['validators'].append(row)
    applications = check_applications(intake, plan)
    if deployment is None:
        deployment = {'wrappedDelWallet': expected['wrappedNative'], 'gateway': expected['gateway'],
                      'chainId': plan['chain_id'], 'evmChainId': plan['evm_chain_id']}
    if (deployment['chainId'], deployment['evmChainId']) != (plan['chain_id'], plan['evm_chain_id']):
        raise ValueError('deployment addresses differ from the isolated signing domain')
    ledger = prepare_ledger(applications, plan, deployment['wrappedDelWallet'], deployment['gateway'], relayers)
    return ledger, applications, plan, relayers


class FoundingLedgerFixtureTest(unittest.TestCase):
    @unittest.skipUnless(os.environ.get('MCND_FOUNDING_LEDGER_FIXTURE'), 'requires the verified Go bootstrap fixture')
    def test_prepared_ledger_programmed_cold_genesis(self):
        fixture = Path(os.environ['MCND_FOUNDING_LEDGER_FIXTURE'])
        source = Path(os.environ['MCND_FOUNDING_LEDGER_SOURCE'])
        roles = os.environ.get('MCND_FOUNDING_LEDGER_ROLES')
        ledger, applications, plan, relayers = prepared_fixture(source, read_json(Path(roles)) if roles else None)
        raw = (fixture / 'public/genesis.json').read_bytes()
        doc = json.loads(raw)
        state = doc['app_state']
        native = state['validator']
        self.assertEqual(ledger['bank']['balances'], state['bank']['balances'])
        self.assertEqual(ledger['bank']['supply'], state['bank']['supply'])
        self.assertEqual(
            ledger['validatorDeclarations'],
            [{key: row[key] if key != 'description' else {'moniker': row[key]['moniker']} for key in (
                'operator_address', 'reward_address', 'consensus_pubkey', 'description', 'commission', 'online',
            )} for row in sorted(native['validators'], key=lambda row: row['operator_address'])],
        )
        self.assertEqual({(row['delegator'], row['validator'], row['stake']['stake']['amount'])
                          for row in ledger['delegations']},
                         {(row['delegator'], row['validator'], row['stake']['stake']['amount'])
                          for row in native['delegations']})
        evm_hex = lambda value: '0x' + base64.b64decode(value, validate=True).hex()
        funding = ledger['contractSetup']['gatewayFunding']
        self.assertEqual(funding['gateway'], state['withdrawal_settlement']['gateway'].lower())
        self.assertEqual(funding['wrappedDel'], state['withdrawal_settlement']['wrapped_del'].lower())
        self.assertEqual(
            {(row['beneficiary'], row['validator'], row['valueAtomic']) for row in funding['calls']},
            {(evm_hex(row['beneficiary']), evm_hex(row['validator']), row['raw_amount'])
             for row in native['evm_stake_bindings']},
        )
        report = verify_candidate(raw, applications, plan, read_json(DEFAULT_DRAFT), relayers)
        self.assertEqual(report['status'], 'CANDIDATE_LEDGER_MATCH')
        self.assertEqual((report['checkedBankRecipients'], report['checkedDelegations']), (8, 4))
        self.assertFalse(ledger['publicGenesisApproved'])


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='Prepare a ledger for the isolated founding rehearsal')
    parser.add_argument('--source', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--roles', type=Path)
    args = parser.parse_args()
    ledger, _, _, _ = prepared_fixture(args.source, read_json(args.roles) if args.roles else None)
    with args.output.open('x', encoding='utf-8') as stream:
        json.dump(ledger, stream)
