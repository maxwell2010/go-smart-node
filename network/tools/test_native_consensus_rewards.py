"""Offline checks for the daemon gate's delayed, bounded reward accounting."""

import unittest
from types import SimpleNamespace
from unittest.mock import patch

import native_consensus_smoke as smoke


class DelayedRewardGateTests(unittest.TestCase):
    def setUp(self):
        self.expected = {
            'actor': 'actor', 'actorNative': 'actor-native',
            'feeCollector': 'collector', 'feeCollectorNative': 'collector-native',
            'validators': [
                {'name': name, 'evm': '0x' + f'{index:040x}', 'reward': name, 'rewardNative': name + '-native'}
                for index, name in enumerate('abc', 1)
            ],
        }
        self.collected = {'height': 1, 'walletAtomic': '100', 'feeCollectorAtomic': '11',
                          'bankSupplyAtomic': '1000', 'rewardAtomic': {name: '0' for name in 'abc'}}

    def test_nondivisible_fee_and_raw_address_tie_preserve_every_atom(self):
        commissions, owner = smoke.exact_fee_shares(self.expected, (2, 1, 0), 11)
        self.assertEqual(commissions, {'a': 1, 'b': 1, 'c': 0})
        self.assertEqual(owner, 9)
        commissions, owner = smoke.exact_fee_shares(self.expected, (1, 1, 1), 11)
        self.assertEqual(commissions, {'a': 1, 'b': 1, 'c': 0})
        self.assertEqual(owner + sum(commissions.values()), 11)

    def drain(self, *, overpay=False, escrow_loss=False, unrelated_transaction=False):
        payments = {120: ('a-native', 1), 121: ('actor-native', 10 if overpay else 9), 122: ('b-native', 1)}

        def cumulative(height):
            totals = {address: 0 for address in ('a-native', 'b-native', 'c-native', 'actor-native')}
            for at, (address, amount) in payments.items():
                if at <= height:
                    totals[address] += amount
            return totals

        def rpc(route):
            height = int(route.split('=')[1])
            event = []
            if height in payments:
                recipient, amount = payments[height]
                event = [{'type': 'decimal_native_fee_reward', 'attributes': [
                    {'key': key, 'value': value} for key, value in
                    {'height': str(height), 'recipient': recipient, 'amount': str(amount) + 'del'}.items()
                ]}]
            return {'finalize_block_events': event, 'txs_results': [{}] if unrelated_transaction else []}

        node = SimpleNamespace(rpc=rpc, evm='evm', rest='rest')

        def balance(_account, _native, _height, _evm, _rest):
            return 0

        def ledger(_replicas, _expected, height, _reserve, **values):
            totals = cumulative(height)
            self.assertEqual(values['wallet'], 100 + totals['actor-native'])
            self.assertEqual(values['rewards'], {name: totals[name + '-native'] for name in 'abc'})
            return {'height': height, 'walletAtomic': str(values['wallet']), 'feeCollectorAtomic': '0',
                    'rewardAtomic': {key: str(value) for key, value in values['rewards'].items()}}

        with patch.object(smoke, 'wait_replicas', side_effect=lambda _r, minimum, _s, _report: minimum), \
                patch.object(smoke, 'balance', side_effect=balance), \
                patch.object(smoke, 'reward_escrow_balance', side_effect=lambda _n, h: 11 - sum(cumulative(h).values()) - int(escrow_loss)), \
                patch.object(smoke, 'check_ledger', side_effect=ledger):
            return smoke.settle_fees([node], self.expected, self.collected, (2, 1, 0), 3, 11, {})

    def test_waits_for_120_tick_and_every_payout_page(self):
        result = self.drain()
        self.assertEqual(result['height'], 122)
        self.assertEqual(result['walletAtomic'], '109')
        self.assertEqual(result['rewardEscrowAtomic'], '0')
        self.assertEqual(result['commissionPaidAtomic'], {'a': '1', 'b': '1', 'c': '0'})

    def test_overpayment_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'overpaid'):
            self.drain(overpay=True)

    def test_lost_escrow_atom_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'conserve'):
            self.drain(escrow_loss=True)

    def test_untracked_transaction_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'untracked transaction'):
            self.drain(unrelated_transaction=True)


if __name__ == '__main__':
    unittest.main()
