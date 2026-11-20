import json
import unittest
from unittest.mock import Mock

from verify_hermes_route import check_config, inspect_route, read_result


LEFT = ('mintcandy_9905401-1', 'channel-7')
RIGHT = ('partner-1', 'channel-19')


def channel(peer, connection):
    return {'state': 'Open', 'ordering': 'Unordered', 'version': 'ics20-1',
            'remote': {'port_id': 'transfer', 'channel_id': peer}, 'connection_hops': [connection]}


def state():
    return {
        'channel_end': channel(RIGHT[1], 'connection-3'),
        'counterparty_channel_end': channel(LEFT[1], 'connection-8'),
        'connection_end': {'state': 'Open', 'client_id': '07-tendermint-2',
                           'counterparty': {'connection_id': 'connection-8', 'client_id': '07-tendermint-6'}},
        'counterparty_connection_end': {'state': 'Open', 'client_id': '07-tendermint-6',
                                        'counterparty': {'connection_id': 'connection-3', 'client_id': '07-tendermint-2'}},
        'client_state': {'type': 'Tendermint', 'chain_id': RIGHT[0]},
        'counterparty_client_state': {'type': 'Tendermint', 'chain_id': LEFT[0]},
    }


class HermesRouteTest(unittest.TestCase):
    def test_accepts_different_local_channel_ids_and_checks_both_clients(self):
        query = Mock(side_effect=[state(), 'Active', 'Active'])
        ends = inspect_route(query, LEFT, RIGHT)
        self.assertEqual([row['channelId'] for row in ends], [LEFT[1], RIGHT[1]])
        self.assertEqual([row['connectionId'] for row in ends], ['connection-3', 'connection-8'])
        self.assertEqual([row['clientStatus'] for row in ends], ['Active', 'Active'])
        self.assertEqual([entry.args[:2] for entry in query.call_args_list],
                         [('channel', 'ends'), ('client', 'status'), ('client', 'status')])

    def test_rejects_wrong_chain_asymmetric_channel_connection_and_expired_client(self):
        for path, value, message in (
            (('counterparty_channel_end', 'version'), 'ics20-2', 'use ICS-20 v1'),
            (('counterparty_channel_end', 'remote'), {'port_id': 'transfer', 'channel_id': 'channel-5'}, 'channel or port differs'),
            (('counterparty_client_state', 'chain_id'), 'some-other-chain', 'expected counterparty chain'),
            (('counterparty_connection_end', 'counterparty'), {'connection_id': 'connection-9', 'client_id': '07-tendermint-2'}, 'do not point back'),
        ):
            with self.subTest(path=path):
                result = state()
                result[path[0]][path[1]] = value
                with self.assertRaisesRegex(ValueError, message):
                    inspect_route(Mock(return_value=result), LEFT, RIGHT)
        with self.assertRaisesRegex(ValueError, 'client must be active'):
            inspect_route(Mock(side_effect=[state(), 'Active', 'Expired']), LEFT, RIGHT)

    def test_packet_filter_must_select_only_each_local_channel(self):
        config = b'''[[chains]]
id = "mintcandy_9905401-1"
packet_filter = {policy = "allow", list = [["transfer", "channel-7"]]}
[[chains]]
id = "partner-1"
packet_filter = {policy = "allow", list = [["transfer", "channel-19"]]}
'''
        check_config(config, LEFT, RIGHT)
        with self.assertRaisesRegex(ValueError, 'allow only'):
            check_config(config.replace(b'"channel-19"', b'"channel-*"'), LEFT, RIGHT)
        with self.assertRaisesRegex(ValueError, 'exactly the two'):
            check_config(config + b'\n[[chains]]\nid = "extra-network"\n', LEFT, RIGHT)

    def test_warn_or_missing_result_is_never_accepted_just_because_status_is_success(self):
        output = json.dumps({'status': 'success', 'result': state()})
        self.assertEqual(read_result(output), state())
        with self.assertRaisesRegex(ValueError, 'warning or error'):
            read_result('{"level":"WARN"}\n' + output)
        with self.assertRaisesRegex(ValueError, 'success result'):
            read_result('{"status":"success"}')


if __name__ == '__main__':
    unittest.main()
