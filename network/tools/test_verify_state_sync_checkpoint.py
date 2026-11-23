from datetime import datetime, timezone
import unittest

from verify_state_sync_checkpoint import verify_checkpoint


class CheckpointTest(unittest.TestCase):
    def setUp(self):
        self.servers = ['https://first.example.invalid', 'https://second.example.invalid']
        self.chain_id = 'mintcandy_990014-1'
        self.now = datetime(2026, 11, 23, 10, 0, 10, tzinfo=timezone.utc)
        self.block_hash = 'A' * 64
        self.replies = {}
        for index, server in enumerate(self.servers, 1):
            self.replies[server, 'status'] = {
                'node_info': {'network': self.chain_id, 'id': f'{index:040x}'},
                'sync_info': {'earliest_block_height': '2', 'latest_block_height': '43', 'catching_up': False},
            }
            self.replies[server, 'commit?height=42'] = {'canonical': True, 'signed_header': {
                'header': {'chain_id': self.chain_id, 'height': '42', 'time': '2026-11-23T10:00:00Z', 'app_hash': 'B' * 64},
                'commit': {'height': '42', 'block_id': {'hash': self.block_hash}},
            }}

    def check(self, period=60):
        return verify_checkpoint(self.servers, self.chain_id, 42, self.block_hash, period,
                                 now=self.now, rpc=lambda base, method: self.replies[base, method])

    def test_matches_checkpoint(self):
        report = self.check()
        self.assertEqual(report['status'], 'STATE_SYNC_CHECKPOINT_MATCH')
        self.assertEqual(report['appHash'], 'B' * 64)
        self.assertEqual(len(report['peers']), 2)

    def test_rejects_expired_checkpoint(self):
        with self.assertRaisesRegex(ValueError, 'outside its trust period'):
            self.check(period=10)

    def test_rejects_duplicate_node(self):
        self.replies[self.servers[1], 'status']['node_info']['id'] = f'{1:040x}'
        with self.assertRaisesRegex(ValueError, 'same node'):
            self.check()

    def test_rejects_other_chain_or_pruned_history(self):
        status = self.replies[self.servers[1], 'status']
        status['node_info']['network'] = 'another-chain'
        with self.assertRaisesRegex(ValueError, 'different chain'):
            self.check()
        status['node_info']['network'] = self.chain_id
        status['sync_info']['earliest_block_height'] = '43'
        with self.assertRaisesRegex(ValueError, 'does not retain'):
            self.check()

    def test_rejects_hash_mismatch_and_disagreeing_app_hash(self):
        block = self.replies[self.servers[1], 'commit?height=42']['signed_header']
        block['commit']['block_id']['hash'] = 'D' * 64
        with self.assertRaisesRegex(ValueError, 'differs from approved trust hash'):
            self.check()
        block['commit']['block_id']['hash'] = self.block_hash
        block['header']['app_hash'] = 'E' * 64
        with self.assertRaisesRegex(ValueError, 'disagree on checkpoint'):
            self.check()
