import base64
import unittest
from urllib.parse import parse_qs

from native_reward_checkpoint import compare_reward_migration, decode_reward_sweep, native_reward_checkpoint


class Node:
    rest = 'http://isolated.invalid'

    def __init__(self, *, owed=50, modern=True, cursor=True):
        self.rows = {}
        self.calls = []
        if modern:
            self.rows[b'\x7e\x00'] = b'\x01'
            self.rows[b'\x7d'] = owed.to_bytes(32, 'big')
            if cursor:
                first = b'\x7b\x00' + bytes([1]) * 40
                last = b'\x7b\x01' + bytes([2]) * 40
                self.rows[b'\x7e\x01'] = (120).to_bytes(8, 'big') + len(first).to_bytes(2, 'big') + first + len(last).to_bytes(2, 'big') + last

    def rpc(self, method):
        self.calls.append(method)
        assert method.startswith('abci_query?'), 'checkpoint attempted a non-query RPC'
        params = parse_qs(method.split('?', 1)[1])
        assert params['path'] == ['"/store/validator/key"']
        key = bytes.fromhex(params['data'][0][2:])
        return {'response': {'height': params['height'][0], 'key': base64.b64encode(key).decode(),
                             'value': base64.b64encode(self.rows.get(key, b'')).decode()}}


def bank(balance, collector=0):
    def read(path, height, *, base):
        assert height > 0 and base == Node.rest
        if path.startswith('/cosmos/auth/v1beta1/module_accounts/'):
            module = path.rsplit('/', 1)[1]
            assert module in ('native_fee_rewards', 'fee_collector')
            return {'account': {'name': module, 'base_account': {'address': 'd01' + ('a' if module == 'native_fee_rewards' else 'b') * 38}}}
        assert path.startswith('/cosmos/bank/v1beta1/balances/') and path.endswith('/by_denom?denom=del')
        amount = collector if 'd01' + 'b' * 38 in path else balance
        return {'balance': {'denom': 'del', 'amount': str(amount)}}
    return read


class NativeRewardCheckpointTests(unittest.TestCase):
    def test_pinned_pending_liability_and_partial_cursor_across_replicas(self):
        nodes = [Node(), Node(), Node()]
        state = native_reward_checkpoint(nodes, 123, require_pending=True, read_native=bank(50))
        self.assertEqual(state['owedAtomic'], '50')
        self.assertTrue(state['sweep']['cursorHex'])
        self.assertTrue(all(node.calls for node in nodes))
        after = native_reward_checkpoint(nodes, 124, require_pending=True, read_native=bank(50))
        self.assertEqual(compare_reward_migration(state, after), 'pending-reward-tree-preserved')

    def test_deficit_disagreement_or_wrong_height_fail_closed(self):
        with self.assertRaises(ValueError):
            native_reward_checkpoint([Node()], 123, read_native=bank(49))
        with self.assertRaises(ValueError):
            native_reward_checkpoint([Node(), Node(owed=51)], 123, read_native=bank(100))
        class WrongHeight(Node):
            def rpc(self, method):
                out = super().rpc(method)
                out['response']['height'] = '122'
                return out
        with self.assertRaises(ValueError):
            native_reward_checkpoint([WrongHeight()], 123, read_native=bank(100))

    def test_legacy_explicit_migration_and_no_fabricated_credit(self):
        old = native_reward_checkpoint([Node(modern=False)], 47, read_native=bank(0))
        new = native_reward_checkpoint([Node(owed=0, cursor=False)], 48, read_native=bank(0))
        self.assertEqual(compare_reward_migration(old, new), 'legacy-index-to-reward-tree')
        with self.assertRaises(ValueError):
            native_reward_checkpoint([Node(modern=False)], 47, require_pending=True)
        bad = dict(new, owedAtomic='1')
        with self.assertRaises(ValueError):
            compare_reward_migration(old, bad)

    def test_real_unallocated_collector_fees_become_new_escrow_liabilities(self):
        old = native_reward_checkpoint([Node(modern=False)], 47, read_native=bank(0, collector=7))
        new = native_reward_checkpoint([Node(owed=7, cursor=False)], 48, read_native=bank(7))
        self.assertEqual(compare_reward_migration(old, new), 'legacy-index-to-reward-tree')
        with self.assertRaises(ValueError):
            compare_reward_migration(old, dict(new, owedAtomic='8'))
        before = native_reward_checkpoint([Node()], 123, read_native=bank(50, collector=7))
        after = native_reward_checkpoint([Node(owed=57)], 124, read_native=bank(57))
        self.assertEqual(compare_reward_migration(before, after), 'pending-reward-tree-preserved')
        with self.assertRaises(ValueError):
            compare_reward_migration(before, dict(after, escrowAtomic='56'))

    def test_changed_credit_or_cursor_is_not_migration_success(self):
        before = native_reward_checkpoint([Node()], 123, read_native=bank(50))
        after = native_reward_checkpoint([Node()], 124, read_native=bank(50))
        for field, value in [('owedAtomic', '0'), ('sweepSha256', 'f' * 64), ('escrowAtomic', '49')]:
            with self.assertRaises(ValueError):
                compare_reward_migration(before, dict(after, **{field: value}))

    def test_malformed_sweep_prefix_and_future_cursor(self):
        with self.assertRaises(ValueError):
            decode_reward_sweep(b'bad')
        node = Node()
        raw = bytearray(node.rows[b'\x7e\x01'])
        raw[10] = 0x73
        with self.assertRaises(ValueError):
            decode_reward_sweep(bytes(raw))
        with self.assertRaises(ValueError):
            native_reward_checkpoint([Node()], 119, read_native=bank(50))


if __name__ == '__main__':
    unittest.main()
