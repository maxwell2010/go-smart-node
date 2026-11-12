import base64
import hashlib
import json
import unittest

from native_launch_allocation import allocation_report
from withdrawal_genesis_smoke import ALPHABET


def address(value, prefix='d0'):
    # This summary receives addresses already validated by mcnd genesis validate.
    return prefix + '1' + ''.join(ALPHABET[(value >> shift) & 31] for shift in range(155, -1, -5)) + 'qqqqqq'


def evm(value):
    return f'0x{value:040x}'


def coin(amount):
    return {'denom': 'del', 'amount': str(amount)}


class LaunchAllocationTest(unittest.TestCase):
    def setUp(self):
        self.operator = address(8, 'd0valoper')
        self.public_key = bytes(range(32))
        self.doc = {
            'chain_id': 'mintcandy_990011-1', 'genesis_time': '2026-11-12T00:00:00Z', 'initial_height': '1',
            'app_state': {
                'withdrawal_settlement': {
                    'enable_native_consensus': True, 'enable_del_delegation': True,
                    'wrapped_del': evm(1), 'position_vault': evm(2),
                },
                'bank': {
                    'balances': [{'address': address(i), 'coins': [coin(value)]}
                                 for i, value in ((0, 2), (1, 20), (3, 8), (4, 25), (7, 3), (8, 90))],
                    'supply': [coin(148)],
                },
                'auth': {'accounts': [{'@type': '/cosmos.auth.v1beta1.ModuleAccount',
                                      'base_account': {'address': address(7)}, 'name': 'fixture'}]},
                'coin': {'params': {'base_denom': 'del', 'base_volume': '148'},
                         'coins': [{'drc20_address': evm(4), 'reserve': '25'}]},
                'nft': {'collections': [{'addressDRC': evm(3)}]},
                'validator': {
                    'binding_domain': {'chain_id': 'mintcandy_990011-1', 'evm_chain_id': '990011'},
                    'validators': [{'operator_address': self.operator, 'reward_address': address(8),
                                    'online': True, 'commission': '0.000000000000000000',
                                    'consensus_pubkey': {'@type': '/cosmos.crypto.ed25519.PubKey',
                                                         'key': base64.b64encode(self.public_key).decode()}}],
                    'delegations': [{'delegator': address(8), 'validator': self.operator,
                                     'stake': {'stake': coin(10)}}],
                },
            },
        }

    def encode(self):
        return json.dumps(self.doc, separators=(',', ':')).encode()

    def test_stake_is_in_backing_not_a_second_allocation(self):
        data = self.encode()
        report = allocation_report(data)
        self.assertEqual(report['genesisSha256'], hashlib.sha256(data).hexdigest())
        self.assertEqual(report['initialSupplyAtomic'], '148')
        self.assertEqual(report['allocationTotalsAtomic'], {
            'wrapped_del_backing': '20', 'nft_origin_backing': '8', 'drc20_backing': '25', 'other_bank_balances': '90',
            'zero_address_balance': '2', 'module_accounts': '3',
        })
        self.assertEqual(report['initialStakingAtomicAlreadyInBankSupply'], '10')
        self.assertEqual(report['validators'][0]['consensusAddressHex'], hashlib.sha256(self.public_key).hexdigest()[:40].upper())
        self.assertEqual(report['validators'][0]['operatorEvmAddress'], evm(8))
        self.assertEqual(report['validators'][0]['initialStakeAtomic'], '10')

    def test_rejects_mismatched_supply(self):
        self.doc['app_state']['bank']['supply'] = [coin(142)]
        with self.assertRaisesRegex(ValueError, 'bank DEL supply'):
            allocation_report(self.encode())

    def test_rejects_inconsistent_reserve_and_signing_domain(self):
        state = self.doc['app_state']
        state['coin']['coins'][0]['reserve'] = '24'
        with self.assertRaisesRegex(ValueError, 'declared reserves'):
            allocation_report(self.encode())
        state['validator']['binding_domain']['evm_chain_id'] = '990012'
        with self.assertRaisesRegex(ValueError, 'signing domains'):
            allocation_report(self.encode())

    def test_rejects_ambiguous_json(self):
        with self.assertRaisesRegex(ValueError, 'duplicate field'):
            allocation_report(b'{"chain_id":"a","chain_id":"b"}')


if __name__ == '__main__':
    unittest.main()
