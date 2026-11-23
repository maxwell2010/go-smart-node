import copy
import json
import unittest

from check_public_launch_draft import (
    DEFAULT_DRAFT, DEFAULT_INTAKE, DEFAULT_PLAN, MAINNET_DRAFT, MAINNET_PLAN,
    check_draft, check_intake, check_plan, read_reviewed_inputs, unique_object,
)


class PublicLaunchDraftTests(unittest.TestCase):
    def setUp(self):
        self.draft = json.loads(DEFAULT_DRAFT.read_text(encoding='utf8'))

    def test_current_template_is_unresolved(self):
        self.assertEqual(check_draft(self.draft)['status'], 'DRAFT_NOT_RUNNABLE')

    def test_display_rename_preserves_internal_units_and_scaling(self):
        result = check_draft(self.draft)
        self.assertEqual((result['nativeName'], result['nativeSymbol'], result['baseDenom']),
                         ('MintCandy', 'MSP', 'del'))
        for field, value in (('symbol', 'DEL'), ('base', 'msp'), ('display', 'del')):
            with self.subTest(field=field):
                draft = copy.deepcopy(self.draft)
                draft['app_state']['bank']['denom_metadata'][0][field] = value
                with self.assertRaisesRegex(ValueError, 'MSP metadata'):
                    check_draft(draft)
        self.draft['app_state']['bank']['denom_metadata'][0]['denom_units'][1]['exponent'] = 6
        with self.assertRaisesRegex(ValueError, 'denomination scale'):
            check_draft(self.draft)

    def test_supplied_identity_or_bank_state_fails_draft_check(self):
        for path, value in (
            (('withdrawal_settlement', 'registry'), '0x' + '12' * 20),
            (('bank', 'balances'), []),
            (('validator', 'validators'), []),
        ):
            with self.subTest(path=path):
                draft = copy.deepcopy(self.draft)
                draft['app_state'][path[0]][path[1]] = value
                with self.assertRaises(ValueError):
                    check_draft(draft)

    def test_secret_fields_and_duplicate_json_are_rejected(self):
        self.draft['priv_key'] = None
        with self.assertRaisesRegex(ValueError, 'secret field'):
            check_draft(self.draft)
        with self.assertRaisesRegex(ValueError, 'duplicate JSON'):
            json.loads('{"chain_id": "a", "chain_id": "b"}', object_pairs_hook=unique_object)

    def test_gas_and_signing_domain_cannot_drift(self):
        self.draft['consensus_params']['block']['max_gas'] = '-1'
        with self.assertRaisesRegex(ValueError, '24 million'):
            check_draft(self.draft)
        self.draft['consensus_params']['block']['max_gas'] = '24000000'
        self.draft['app_state']['validator']['binding_domain']['evm_chain_id'] = '75'
        with self.assertRaisesRegex(ValueError, 'binding domain'):
            check_draft(self.draft)

    def test_fixed_supply_and_inflation_require_review(self):
        self.draft['app_state']['coin']['params']['base_volume'] = '100000000000000000000000'
        with self.assertRaisesRegex(ValueError, 'unapproved launch input'):
            check_draft(self.draft)
        self.draft['app_state']['coin']['params']['base_volume'] = None
        self.draft['app_state']['mint']['params']['inflation_max'] = '0.01'
        with self.assertRaisesRegex(ValueError, 'emission enabled'):
            check_draft(self.draft)


class PublicLaunchPlanTests(unittest.TestCase):
    def setUp(self):
        self.draft = json.loads(DEFAULT_DRAFT.read_text(encoding='utf8'))
        self.plan = json.loads(DEFAULT_PLAN.read_text(encoding='utf8'))
        self.intake = json.loads(DEFAULT_INTAKE.read_text(encoding='utf8'))

    def test_current_plan_and_intake_are_unapproved(self):
        self.assertEqual(check_plan(self.plan, self.draft)['status'], 'PLAN_DRAFT_UNAPPROVED')
        self.assertEqual(check_intake(self.intake)['status'], 'INTAKE_DRAFT_UNAPPROVED')

    def test_mainnet_plan_uses_msp_without_changing_economics(self):
        draft, plan = read_reviewed_inputs(MAINNET_DRAFT, MAINNET_PLAN)
        self.assertEqual(plan['denom'], 'msp')
        self.assertEqual(plan['initial_supply_atoms'], self.plan['initial_supply_atoms'])
        self.assertEqual(check_draft(draft, 'msp')['baseDenom'], 'msp')
        with self.assertRaisesRegex(ValueError, 'denomination scale'):
            check_plan(plan, self.draft)
        draft['app_state']['validator']['params']['base_denom'] = 'del'
        with self.assertRaisesRegex(ValueError, 'native denomination'):
            check_draft(draft, 'msp')

    def test_selected_validator_count_cannot_drift_or_approve_genesis(self):
        self.assertEqual(check_plan(self.plan, self.draft)['approvedValidatorCount'], 4)
        self.assertFalse(check_intake(self.intake)['publicGenesisApproved'])
        self.plan['approved_validator_count'] = 5
        with self.assertRaisesRegex(ValueError, 'four-validator start'):
            check_plan(self.plan, self.draft)
        self.intake['network']['approved_validator_count'] = None
        with self.assertRaisesRegex(ValueError, 'selected validator count'):
            check_intake(self.intake)

    def test_asset_identity_cannot_diverge_between_launch_documents(self):
        self.plan['native_token_symbol'] = 'DEL'
        with self.assertRaisesRegex(ValueError, 'MSP identity'):
            check_plan(self.plan, self.draft)
        self.intake['allocation']['atoms_per_native_token'] = '1000000'
        with self.assertRaisesRegex(ValueError, 'MSP identity'):
            check_intake(self.intake)

    def test_allocation_cannot_add_an_atom_to_supply(self):
        self.plan['bank_allocation'][0]['amount_atoms'] = str(
            int(self.plan['bank_allocation'][0]['amount_atoms']) + 1)
        with self.assertRaisesRegex(ValueError, 'allocation total'):
            check_plan(self.plan, self.draft)

    def test_single_account_decision_cannot_duplicate_treasury_or_reduce_requested_balance(self):
        self.plan['treasury_policy']['multisig_required'] = True
        with self.assertRaisesRegex(ValueError, 'single-account treasury'):
            check_plan(self.plan, self.draft)
        self.plan['treasury_policy']['multisig_required'] = False
        self.plan['bank_allocation'][0]['amount_atoms'] = str(4_999_999 * 10**18)
        self.plan['initial_supply_atoms'] = str(
            sum(int(row['amount_atoms']) for row in self.plan['bank_allocation']))
        with self.assertRaisesRegex(ValueError, 'founder requested balance'):
            check_plan(self.plan, self.draft)

    def test_correct_bank_total_cannot_hide_unbacked_delegations(self):
        self.plan['initial_delegations_within_wdel_backing'][0]['amount_atoms'] = str(
            int(self.plan['initial_delegations_within_wdel_backing'][0]['amount_atoms']) + 1)
        with self.assertRaisesRegex(ValueError, 'wrapper backing'):
            check_plan(self.plan, self.draft)

    def test_balanced_supply_cannot_hide_unwanted_initial_assets(self):
        self.plan['bank_allocation'].append({'purpose': 'approved_erc1155_origin_reserve',
                                             'amount_atoms': '1', 'bank_address': None})
        self.plan['initial_supply_atoms'] = str(int(self.plan['initial_supply_atoms']) + 1)
        with self.assertRaisesRegex(ValueError, 'empty user asset allocation'):
            check_plan(self.plan, self.draft)

    def test_unbuilt_creation_and_storage_cannot_be_declared_ready(self):
        self.plan['release_requirements']['public_nft_creation_ci'] = 'passed'
        with self.assertRaisesRegex(ValueError, 'not been demonstrated'):
            check_plan(self.plan, self.draft)

    def test_accidental_financial_approval_is_rejected(self):
        for name in ('launch_approved', 'initial_asset_creation_approved'):
            with self.subTest(decision=name):
                plan = copy.deepcopy(self.plan)
                plan['decisions'][name] = True
                with self.assertRaisesRegex(ValueError, 'approval in a draft'):
                    check_plan(plan, self.draft)

    def test_approved_gas_distribution_cannot_drift_at_the_same_total(self):
        rows = self.plan['bank_allocation']
        rows[2]['amount_atoms'] = str(201 * 10**18)
        rows[-1]['amount_atoms'] = str(99 * 10**18)
        with self.assertRaisesRegex(ValueError, 'gas wallet budgets'):
            check_plan(self.plan, self.draft)

    def test_selected_founder_delegations_cannot_become_operator_gifts(self):
        self.assertEqual(check_plan(self.plan, self.draft)['selectedStakeOwnership'],
                         'founder_owned_delegations')
        self.plan['decisions']['selected_stake_ownership'] = 'gifts_to_approved_validator_operators'
        with self.assertRaisesRegex(ValueError, 'founder delegation ownership'):
            check_plan(self.plan, self.draft)

    def test_intake_secrets_and_genesis_approval_are_rejected(self):
        self.intake['validators'][0]['readiness']['seed_phrase'] = 'not-a-real-secret'
        with self.assertRaisesRegex(ValueError, 'secret field'):
            check_intake(self.intake)
        del self.intake['validators'][0]['readiness']['seed_phrase']
        self.intake['release']['genesis']['sha256'] = 'a' * 64
        with self.assertRaisesRegex(ValueError, 'public genesis'):
            check_intake(self.intake)

    def test_intake_software_hashes_must_match_the_published_record(self):
        for field, length in (('source_revision', 40), ('binary_archive_sha256', 64), ('binary_sha256', 64)):
            with self.subTest(field=field):
                draft = copy.deepcopy(self.intake)
                draft['release']['software'][field] = 'a' * length
                with self.assertRaisesRegex(ValueError, 'differ from the published release'):
                    check_intake(draft)


if __name__ == '__main__':
    unittest.main()
