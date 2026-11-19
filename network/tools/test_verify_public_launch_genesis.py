import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from check_operator_applications import BECH32_GENERATORS, ED25519_TYPE, check_applications, strict_native_address
from check_public_launch_draft import DEFAULT_DRAFT, DEFAULT_INTAKE, DEFAULT_PLAN, SYSTEM_ROLES, read_json
from native_launch_allocation import allocation_report, verify_launch_plan
from prepare_founding_ledger import prepare_ledger
from test_check_operator_applications import TEST_IDENTITIES
from verify_public_launch_genesis import verify_candidate
from withdrawal_genesis_smoke import ALPHABET


def account(evm_address):
    value = int(evm_address, 16)
    prefix = 'd0'
    digits = [(value >> shift) & 31 for shift in range(155, -1, -5)]
    expanded_prefix = [ord(c) >> 5 for c in prefix] + [0] + [ord(c) & 31 for c in prefix]
    checksum = 1
    for part in [*expanded_prefix, *digits, *([0] * 6)]:
        top = checksum >> 25
        checksum = ((checksum & 0x1ffffff) << 5) ^ part
        for index, generator in enumerate(BECH32_GENERATORS):
            if (top >> index) & 1:
                checksum ^= generator
    checksum ^= 1
    digits.extend((checksum >> shift) & 31 for shift in range(25, -1, -5))
    return prefix + '1' + ''.join(ALPHABET[digit] for digit in digits)


def evm(index):
    return f'0x{index:040x}'


def coin(amount):
    return {'denom': 'del', 'amount': amount}


class PublicGenesisLedgerTest(unittest.TestCase):
    def setUp(self):
        self.draft = read_json(DEFAULT_DRAFT)
        self.plan = read_json(DEFAULT_PLAN)
        self.intake = read_json(DEFAULT_INTAKE)
        # This in-memory document exercises ledger comparison without any keys
        # capable of signing for public operators or any runnable app state.
        document = self.document = copy.deepcopy(self.draft)
        document.update(genesis_time='2026-12-01T00:00:00Z')
        state = document['app_state']
        profile = state['withdrawal_settlement']
        profile.update({role: evm(i) for i, role in enumerate(SYSTEM_ROLES, 1)})
        state['auth'] = {'accounts': []}
        state['ibc'] = {}
        state['transfer'] = {}
        state['nft'].update(collections=[{'addressDRC': profile['position_vault'], 'supply': '0', 'tokens': []}],
                            isolated_positions=[], external_bindings=[])
        state['coin']['params']['base_volume'] = self.plan['initial_supply_atoms']
        state['bank']['supply'] = [coin(self.plan['initial_supply_atoms'])]
        state['validator'].update(validators=[], delegations=[])
        founder = account(self.plan['founder_evm_address'])
        self.relayer_wallets = [account(evm(51)), account(evm(52))]
        wallets = [founder, account(profile['wrapped_del'])]
        template = self.intake['validators'][0]
        self.intake['validators'] = []
        for index, (operator, evm_operator, reward, key, _) in enumerate(TEST_IDENTITIES, 1):
            amount = self.plan['approved_initial_stake_per_validator_atoms']
            wallet = account(evm(40 + index))
            pubkey = {'@type': ED25519_TYPE, 'key': key}
            node_id = f'{index:040x}'
            row = copy.deepcopy(template)
            row.update(application_id=f'V{index}', operator_address=operator, evm_operator_address=evm_operator,
                       consensus_pubkey=pubkey, reward_address=reward, stake_owner_address=founder,
                       beneficiary_address=founder, stake_ownership_model='founder_owned_delegations',
                       stake_amount_atoms=amount, gas_wallet_address=wallet, gas_budget_atoms='200000000000000000000',
                       moniker=f'Test {index}', operator_contact='contact.example.invalid', commission_fraction='0.05')
            row['connectivity'].update(node_id=node_id, p2p_address=f'{node_id}@p{index}.example.invalid:26656',
                                       role='sentry')
            self.intake['validators'].append(row)
            state['validator']['validators'].append({
                'operator_address': operator, 'reward_address': reward, 'online': True,
                'commission': self.plan['validator_commission_fraction'], 'consensus_pubkey': pubkey.copy(),
                'description': {'moniker': row['moniker']},
            })
            state['validator']['delegations'].append({
                'delegator': founder, 'validator': operator, 'stake': {'stake': coin(amount)},
            })
            wallets.append(wallet)
        wallets.extend(self.relayer_wallets)
        state['bank']['balances'] = [{'address': wallet, 'coins': [coin(row['amount_atoms'])]}
                                     for wallet, row in zip(wallets, self.plan['bank_allocation'], strict=True)]
        self.applications = check_applications(self.intake, self.plan)

    def data(self):
        return json.dumps(self.document).encode('utf-8')

    def verify(self):
        return verify_candidate(self.data(), self.applications, self.plan, self.draft, self.relayer_wallets)

    def test_prepares_deterministic_ledger_and_separate_contract_funding(self):
        profile = self.document['app_state']['withdrawal_settlement']
        wrapped = account(profile['wrapped_del'])
        result = prepare_ledger(self.applications, self.plan, wrapped, profile['gateway'], self.relayer_wallets)
        self.assertEqual(result['status'], 'FOUNDING_LEDGER_PREPARED')
        self.assertFalse(result['publicGenesisApproved'])
        self.assertFalse(result['runtimeInitChainVerified'])
        self.assertEqual(len(result['bank']['balances']), 8)
        self.assertEqual(result['bank']['supply'], [coin(self.plan['initial_supply_atoms'])])
        self.assertEqual(len(result['delegations']), 4)
        for delegation in result['delegations']:
            self.assertEqual(delegation['stake'], {
                'type': 1, 'id': self.plan['denom'],
                'stake': coin(self.plan['approved_initial_stake_per_validator_atoms']),
            })
        funding = result['contractSetup']['gatewayFunding']
        self.assertEqual(funding['method'], 'delegateDELTo')
        self.assertEqual({row['beneficiary'] for row in funding['calls']}, {self.plan['founder_evm_address']})
        self.assertNotIn('example.invalid', json.dumps(result))
        self.applications['operators'].reverse()
        self.assertEqual(result, prepare_ledger(self.applications, self.plan, wrapped, profile['gateway'],
                                               self.relayer_wallets[::-1]))
        with self.assertRaisesRegex(ValueError, 'Gateway must differ'):
            prepare_ledger(self.applications, self.plan, wrapped, profile['wrapped_del'], self.relayer_wallets)

    def test_preparation_cli_requires_four_and_preserves_existing_output(self):
        tool = Path(__file__).with_name('prepare_founding_ledger.py')
        profile = self.document['app_state']['withdrawal_settlement']
        with tempfile.TemporaryDirectory() as directory:
            intake = Path(directory, 'intake.json')
            output = Path(directory, 'prepared-ledger.json')
            cmd = [sys.executable, '-B', str(tool), '--intake', str(intake),
                   '--gateway', profile['gateway'], '--wrapped-del-wallet', account(profile['wrapped_del']),
                   '--output', str(output)]
            for wallet in self.relayer_wallets:
                cmd.extend(('--relayer-wallet', wallet))
            intake.write_bytes(DEFAULT_INTAKE.read_bytes())
            rejected = subprocess.run(cmd, capture_output=True, text=True, check=False)
            self.assertNotEqual(rejected.returncode, 0)
            self.assertIn('four complete operator applications', rejected.stderr)
            self.assertFalse(output.exists())
            intake.write_text(json.dumps(self.intake), encoding='utf-8')
            result = subprocess.run(cmd, capture_output=True, text=True, check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            expected = output.read_bytes()
            rejected = subprocess.run(cmd, capture_output=True, text=True, check=False)
            self.assertNotEqual(rejected.returncode, 0)
            self.assertEqual(output.read_bytes(), expected)

    def test_checks_every_allocation_and_reports_exact_candidate_hash(self):
        report = self.verify()
        self.assertEqual(report['status'], 'CANDIDATE_LEDGER_MATCH')
        self.assertEqual((report['checkedValidators'], report['checkedDelegations'], report['checkedBankRecipients']),
                         (4, 4, 8))
        self.assertEqual(report['genesisSha256'], hashlib.sha256(self.data()).hexdigest())
        self.assertFalse(report['publicGenesisApproved'])
        self.assertFalse(report['runtimeInitChainVerified'])
        self.assertFalse(report['ownershipProofsVerified'])
        self.assertNotIn('example.invalid', json.dumps(report))
        self.assertEqual(strict_native_address(account(TEST_IDENTITIES[0][1]), 'd0'), TEST_IDENTITIES[0][1])

    def test_aggregate_audit_cannot_detect_substituted_founder_or_gas_wallet(self):
        balances = self.document['app_state']['bank']['balances']
        for index in (0, 3, 6):
            with self.subTest(index=index):
                original, balances[index]['address'] = balances[index]['address'], account(evm(99))
                verify_launch_plan(allocation_report(self.data()), self.plan)
                with self.assertRaisesRegex(ValueError, 'bank recipients'):
                    self.verify()
                balances[index]['address'] = original

    def test_cannot_move_relayer_and_validator_amounts_between_wallets(self):
        balances = self.document['app_state']['bank']['balances']
        balances[2]['coins'], balances[6]['coins'] = balances[6]['coins'], balances[2]['coins']
        verify_launch_plan(allocation_report(self.data()), self.plan)
        with self.assertRaisesRegex(ValueError, 'per-wallet amounts'):
            self.verify()

    def test_requires_distinct_relayer_wallets(self):
        self.relayer_wallets[1] = self.relayer_wallets[0]
        with self.assertRaisesRegex(ValueError, 'addresses must be distinct'):
            self.verify()

    def test_cannot_redirect_delegation_at_unchanged_power(self):
        self.document['app_state']['validator']['delegations'][0]['delegator'] = TEST_IDENTITIES[0][2]
        verify_launch_plan(allocation_report(self.data()), self.plan)
        with self.assertRaisesRegex(ValueError, 'delegation owner'):
            self.verify()

    def test_cannot_swap_commission_beneficiaries_or_consensus_keys(self):
        validators = self.document['app_state']['validator']['validators']
        for field in ('reward_address', 'consensus_pubkey'):
            with self.subTest(field=field):
                validators[0][field], validators[1][field] = validators[1][field], validators[0][field]
                verify_launch_plan(allocation_report(self.data()), self.plan)
                with self.assertRaisesRegex(ValueError, 'reviewed intake'):
                    self.verify()
                validators[0][field], validators[1][field] = validators[1][field], validators[0][field]

    def test_rejects_downgraded_launch_profile(self):
        for section, field, value, diagnostic in (
            ('withdrawal_settlement', 'enable_native_ibc_v1', False, 'enable_native_ibc_v1'),
            ('withdrawal_settlement', 'test_grace_until_height', 10, 'test grace'),
            ('coin', 'coins', [{'test': 'asset'}], 'no user assets'),
            ('nft', 'collections', [{'addressDRC': evm(900), 'tokens': []}], 'no user assets'),
        ):
            with self.subTest(field=field):
                row = self.document['app_state'][section]
                original = row.get(field)
                row[field] = value
                with self.assertRaisesRegex(ValueError, diagnostic):
                    self.verify()
                if original is None:
                    del row[field]
                else:
                    row[field] = original

    def test_rejects_another_signing_domain_before_reusing_public_allocations(self):
        self.document['chain_id'] = 'mintcandy_990013-1'
        with self.assertRaisesRegex(ValueError, 'signing domain'):
            self.verify()

    def test_rejects_emission_and_a_changed_block_gas_cap(self):
        self.document['app_state']['mint']['params']['inflation_max'] = '0.010000000000000000'
        with self.assertRaisesRegex(ValueError, 'disable MSP emission'):
            self.verify()
        self.document['consensus_params']['block']['max_gas'] = '25000000'
        with self.assertRaisesRegex(ValueError, 'block gas cap'):
            self.verify()

    def test_cli_checks_candidate_bytes_against_supplied_intake(self):
        tool = Path(__file__).with_name('verify_public_launch_genesis.py')
        with tempfile.TemporaryDirectory() as directory:
            genesis = Path(directory, 'candidate.json')
            intake = Path(directory, 'operator-intake.json')
            genesis.write_bytes(self.data())
            intake.write_text(json.dumps(self.intake), encoding='utf-8')
            cmd = [sys.executable, '-B', str(tool), '--genesis', str(genesis), '--intake', str(intake)]
            for wallet in self.relayer_wallets:
                cmd.extend(('--relayer-wallet', wallet))
            result = subprocess.run(cmd, capture_output=True, text=True, check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(result.stdout), self.verify())
            intake.write_bytes(DEFAULT_INTAKE.read_bytes())
            result = subprocess.run(cmd, capture_output=True, text=True, check=False)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn('four complete operator applications', result.stderr)
