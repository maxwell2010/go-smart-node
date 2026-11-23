import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from check_operator_applications import ED25519_TYPE, check_applications, public_bootstrap, strict_native_address
from check_public_launch_draft import DEFAULT_INTAKE, DEFAULT_PLAN, read_json


# Public addresses from the isolated 1.1.0 rehearsal, verified by InitChain.
# These are format test vectors and must never be used as public validators.
TEST_IDENTITIES = (
    ('d0valoper12syar76pj245dtnem0ksvrkgq34njkzjtuvknm',
     '0x5409d1fb4192ab46ae79dbed060ec8046b395852', 'd012syar76pj245dtnem0ksvrkgq34njkzjtglk5t',
     's3PAGWxj6GS0To2WX9ipxNUU5G3MOCkG3kFU8BvdOR4=', 'C79A9A870AEBAF36A37A9EBC1DD2C442906CA942'),
    ('d0valoper1cjfwnz0s4hdxgpw9p4puyuc8786lu88526h2c0',
     '0xc492e989f0adda6405c50d43c27307f1f5fe1cf4', 'd01cjfwnz0s4hdxgpw9p4puyuc8786lu8852wy2ll',
     'F9rgdW33MpmxBvNrjU9XL/j/OQ68aAv37myBppETjYk=', 'A05542B2B88A83027A63F1582D0441C9B76516DF'),
    ('d0valoper1llslj826gr8uthtpm2qgvuy4n5xlwcrme63uf7',
     '0xffe1f91d5a40cfc5dd61da808670959d0df7607b', 'd01llslj826gr8uthtpm2qgvuy4n5xlwcrmewzuww',
     'JJUs1vdWbDMZWbow9Az2CNN9tAwCVJi1qVxpoEQ79r8=', '3C71CB72F8EA039B9EFD6240312DFD26AB3EF352'),
    ('d0valoper1sr6zvuan35pgmft4epll07qs04x3f3lw588pel',
     '0x80f42673b38d028da575c87ff7f8107d4d14c7ee', 'd01sr6zvuan35pgmft4epll07qs04x3f3lw5n5p70',
     'Rd5z2sinEebJhhffcfQ1pjj9IchU9p21BQpULh/rsN0=', '623EB089FF818FB0CA68DEC775C93BDDAF77C3BA'),
)


class OperatorApplicationTests(unittest.TestCase):
    def setUp(self):
        self.draft = read_json(DEFAULT_INTAKE)
        self.plan = read_json(DEFAULT_PLAN)
        self.document = copy.deepcopy(self.draft)
        # Use an isolated founder identity in the test plan as well.
        self.plan['founder_evm_address'] = TEST_IDENTITIES[0][1]
        founder_account = TEST_IDENTITIES[0][2]
        rows = []
        for index, (operator, evm, reward, public_key, consensus_addr) in enumerate(TEST_IDENTITIES, 1):
            node_id = f'{index:040x}'
            row = copy.deepcopy(self.draft['validators'][0])
            row.update(application_id=f'V{index}', operator_address=operator, evm_operator_address=evm,
                       consensus_pubkey={'@type': ED25519_TYPE, 'key': public_key},
                       consensus_address_hex=consensus_addr, reward_address=reward,
                       stake_owner_address=founder_account, beneficiary_address=founder_account,
                       stake_ownership_model='founder_owned_delegations', stake_amount_atoms='10000000000000000000000',
                       gas_wallet_address=reward, gas_budget_atoms='200000000000000000000',
                       moniker=f'Test operator {index}', operator_contact='contact.example.invalid',
                       commission_fraction='0.05')
            row['connectivity'].update(node_id=node_id, p2p_address=f'{node_id}@peer{index}.example.invalid:26656',
                                       role='sentry', endpoint_publication_allowed=False)
            rows.append(row)
        self.document['validators'] = rows

    def test_full_roster_preflight_preserves_launch_approval_boundary(self):
        report = check_applications(self.document, self.plan)
        self.assertEqual(report['status'], 'PRECHECK_PASSED')
        self.assertEqual((report['checkedOperators'], report['missingSlots']), (4, 0))
        self.assertFalse(report['publicGenesisApproved'])
        self.assertFalse(report['ownershipProofsVerified'])
        self.assertFalse(report['endpointsReachabilityVerified'])
        self.assertNotIn('contact.example.invalid', json.dumps(report))
        self.assertEqual(report['operators'][0]['consensusAddressHex'], TEST_IDENTITIES[0][4])
        self.document['validators'].reverse()
        self.assertEqual(report, check_applications(self.document, self.plan))

    def test_draft_and_incomplete_rows_report_missing_slots(self):
        report = check_applications(self.draft, self.plan)
        self.assertEqual((report['status'], report['checkedOperators'], report['missingSlots']), ('APPLICATIONS_PENDING', 0, 4))
        self.assertIn('consensus_pubkey.key', report['incompleteRows'][0]['missingFields'])
        self.document['validators'][0]['gas_wallet_address'] = None
        report = check_applications(self.document, self.plan)
        self.assertEqual((report['checkedOperators'], report['missingSlots']), (3, 1))
        self.assertEqual(report['incompleteRows'], [{'row': 1, 'missingFields': ['gas_wallet_address']}])

    def test_mainnet_intake_preflight_accepts_msp_and_blocks_legacy_assembly(self):
        self.document['schema_version'] = 3
        self.document['allocation'].update(denom='msp', expected_total_atoms=self.plan['initial_supply_atoms'])
        common_reward = TEST_IDENTITIES[0][1]
        self.document['launch_coordination'] = {
            'requested_mainnet_native_denom': 'msp',
            'manifest_signer_fingerprint': 'A' * 40,
            'common_operator_reward_evm_address': common_reward,
        }
        for row in self.document['validators']:
            row['operator_address'] = row.pop('evm_operator_address')
            for field in ('stake_owner_address', 'beneficiary_address', 'gas_wallet_address'):
                row[field] = strict_native_address(row[field], 'd0')
            row['reward_address'] = common_reward
        report = check_applications(self.document, self.plan, preflight_only=True)
        self.assertEqual((report['status'], report['checkedOperators']), ('PRECHECK_PASSED', 4))
        self.assertFalse(report['publicGenesisApproved'])
        with self.assertRaisesRegex(ValueError, 'requires an msp allocation plan'):
            check_applications(self.document, self.plan)
        msp_plan = {**self.plan, 'denom': 'msp'}
        assembly = check_applications(self.document, msp_plan)
        self.assertFalse(assembly['publicGenesisApproved'])
        first = next(operator for operator in assembly['operators'] if operator['applicationId'] == 'V1')
        self.assertEqual(first['operatorAddress'], TEST_IDENTITIES[0][0])
        self.assertEqual(first['operatorEvmAddress'], TEST_IDENTITIES[0][1])
        self.assertEqual(first['rewardAddress'], TEST_IDENTITIES[0][2])
        self.assertEqual(first['stakeOwnerAddress'], TEST_IDENTITIES[0][2])
        self.assertEqual(first['beneficiaryAddress'], TEST_IDENTITIES[0][2])
        self.assertEqual(first['gasWalletAddress'], TEST_IDENTITIES[0][2])
        with self.assertRaisesRegex(ValueError, 'requires schema 3'):
            check_applications(self.draft, msp_plan)

        for section, field, value, reason in (
            ('launch_coordination', 'manifest_signer_fingerprint', 'A' * 8, 'fingerprint'),
            ('allocation', 'denom', 'del', 'requires the msp native denomination'),
            ('launch_coordination', 'common_operator_reward_evm_address', TEST_IDENTITIES[1][1], 'common reward wallet'),
        ):
            with self.subTest(field=field):
                document = copy.deepcopy(self.document)
                document[section][field] = value
                with self.assertRaisesRegex(ValueError, reason):
                    check_applications(document, self.plan, preflight_only=True)

        operator = self.document['validators'][0]['operator_address']
        self.document['validators'][1]['operator_address'] = '0x' + operator[2:].upper()
        with self.assertRaisesRegex(ValueError, 'duplicate operatorAddress'):
            check_applications(self.document, self.plan, preflight_only=True)

    def test_bech32_checksum_and_role_prefix_share_verified_sdk_address(self):
        operator, evm, account, *_ = TEST_IDENTITIES[0]
        self.assertEqual(strict_native_address(account, 'd0'), evm)
        self.assertEqual(strict_native_address(operator, 'd0valoper'), evm)
        with self.assertRaisesRegex(ValueError, 'checksum'):
            strict_native_address(operator[:-1] + 'q', 'd0valoper')
        with self.assertRaisesRegex(ValueError, 'shape'):
            strict_native_address(operator, 'd0')

    def test_rejects_ownership_and_account_inconsistency(self):
        for field, value, reason in (
            ('evm_operator_address', TEST_IDENTITIES[1][1], 'operator Bech32 and EVM addresses differ'),
            ('consensus_address_hex', TEST_IDENTITIES[1][4], 'consensus address differs'),
            ('beneficiary_address', TEST_IDENTITIES[1][2], 'stake ownership or beneficiary'),
            ('stake_amount_atoms', '10000000000000000000001', 'initial stake differs'),
            ('gas_budget_atoms', '200', 'operator gas budget'),
            ('commission_fraction', '1.01', 'commission must be'),
            ('commission_fraction', '0.049999999999999999', 'commission must be at least 5%'),
            ('moniker', '\u00a0\u3000', 'moniker'),
            ('moniker', '\u2800', 'moniker'),
            ('moniker', 'A\u202eB', 'moniker'),
        ):
            with self.subTest(field=field):
                document = copy.deepcopy(self.document)
                document['validators'][0][field] = value
                with self.assertRaisesRegex(ValueError, reason):
                    check_applications(document, self.plan)

    def test_rejects_reused_consensus_and_peer_identity(self):
        first = self.document['validators'][0]
        for fields, reason in (
            (('consensus_pubkey', 'consensus_address_hex'), 'duplicate consensusAddressHex'),
            (('connectivity',), 'duplicate p2pNodeId'),
        ):
            with self.subTest(reason=reason):
                document = copy.deepcopy(self.document)
                for field in fields:
                    document['validators'][1][field] = first[field]
                with self.assertRaisesRegex(ValueError, reason):
                    check_applications(document, self.plan)

    def test_p2p_validates_host_port_and_public_scope_without_network_access(self):
        connectivity = self.document['validators'][0]['connectivity']
        node_id = connectivity['node_id']
        connectivity['p2p_address'] = f'{node_id}@[fd00::10]:26656'
        report = check_applications(self.document, self.plan)
        self.assertEqual(report['operators'][0]['p2pAddress'], connectivity['p2p_address'])
        connectivity['endpoint_publication_allowed'] = True
        with self.assertRaisesRegex(ValueError, 'globally routable'):
            check_applications(self.document, self.plan)
        for endpoint in (f'{node_id}@127.0.0.1:26656', f'{node_id}@example.invalid:65536',
                         f'{node_id}@[:::]:26656', f'{node_id}@example.invalid:26656?token=secret'):
            with self.subTest(endpoint=endpoint):
                connectivity['p2p_address'] = endpoint
                with self.assertRaisesRegex(ValueError, 'P2P'):
                    check_applications(self.document, self.plan)

    def test_invalid_domain_and_secret_keys_reject_intake(self):
        self.document['network']['evm_chain_id'] = '75'
        with self.assertRaisesRegex(ValueError, 'approved chain domain'):
            check_applications(self.document, self.plan)
        self.document['validators'][0]['priv_key'] = 'test secret'
        with self.assertRaisesRegex(ValueError, 'secret field'):
            check_applications(self.document, self.plan)

    def test_bootstrap_emits_only_explicitly_public_endpoints_and_separates_seeds(self):
        sentry, seed, denied, absent = [row['connectivity'] for row in self.document['validators']]
        sentry['endpoint_publication_allowed'] = True
        seed.update(role='seed', endpoint_publication_allowed=True)
        del absent['endpoint_publication_allowed']
        result = public_bootstrap(check_applications(self.document, self.plan))
        self.assertEqual(result['status'], 'PUBLIC_BOOTSTRAP_CANDIDATE')
        self.assertEqual(result['p2p'], {'persistent_peers': sentry['p2p_address'], 'seeds': seed['p2p_address']})
        self.assertFalse(result['endpointsReachabilityVerified'])
        self.assertFalse(result['publicGenesisApproved'])
        for secret in (denied['p2p_address'], absent['p2p_address'], TEST_IDENTITIES[0][0], TEST_IDENTITIES[0][3]):
            self.assertNotIn(secret, json.dumps(result))
        self.document['validators'].reverse()
        self.assertEqual(public_bootstrap(check_applications(self.document, self.plan)), result)

    def test_bootstrap_requires_a_full_roster_and_an_address_providing_blocks(self):
        seed = self.document['validators'][0]['connectivity']
        seed.update(role='seed', endpoint_publication_allowed=True)
        result = public_bootstrap(check_applications(self.document, self.plan))
        self.assertEqual(result['status'], 'BOOTSTRAP_PEERS_PENDING')
        self.assertNotIn(seed['p2p_address'], json.dumps(result))
        self.document['validators'].pop()
        result = public_bootstrap(check_applications(self.document, self.plan))
        self.assertEqual(result['status'], 'APPLICATIONS_PENDING')
        self.assertNotIn('p2p', result)

    def test_bootstrap_rejects_conflicting_public_ids_on_the_same_endpoint(self):
        first, second = [row['connectivity'] for row in self.document['validators'][:2]]
        first['endpoint_publication_allowed'] = True
        second.update(endpoint_publication_allowed=True,
                      p2p_address=f"{second['node_id']}@PEER1.EXAMPLE.INVALID:26656")
        with self.assertRaisesRegex(ValueError, 'same host and port'):
            public_bootstrap(check_applications(self.document, self.plan))

    def test_cli_pending_gate_and_duplicate_json(self):
        tool = Path(__file__).with_name('check_operator_applications.py')
        result = subprocess.run([sys.executable, '-B', str(tool), str(DEFAULT_INTAKE), '--require-four'],
                                capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertEqual(json.loads(result.stdout)['missingSlots'], 4)
        public = subprocess.run([sys.executable, '-B', str(tool), str(DEFAULT_INTAKE), '--public-bootstrap'],
                                capture_output=True, text=True, check=False)
        self.assertEqual(public.returncode, 2, public.stderr)
        self.assertEqual(json.loads(public.stdout)['status'], 'APPLICATIONS_PENDING')
        self.assertNotIn('operators', json.loads(public.stdout))
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory, 'invalid.json')
            path.write_text('{"duplicate":1,"duplicate":2}', encoding='utf-8')
            result = subprocess.run([sys.executable, '-B', str(tool), str(path)], capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn('duplicate intake fields', result.stderr)
