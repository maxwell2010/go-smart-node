import json
import pathlib
import unittest
from unittest.mock import call, patch

import withdrawal_genesis_smoke as smoke


def fixture():
    return {
        'chainId': smoke.CHAIN, 'evmChainId': smoke.EVM_CHAIN, 'dueAt': 1075,
        'gateway': '0x' + '01' * 20, 'invalidError': 'frozen deadline does not match',
        'delegateCallData': '0x' + '20' * 36,
        'nft': {
            'id': '0x' + '00' * 32, 'tokenId': '0x' + '03' * 32,
            'vault': '0x' + '02' * 20, 'beneficiary': '0x' + '04' * 20,
            'nativeTokenId': 'receipt_1', 'nativeSubTokenId': 7,
            'originQuantity': 1024, 'reserveAtomic': '2000000000000000000',
            'positionCallData': '0x' + '10' * 4 + '03' * 32,
        },
    }


class WithdrawalGenesisSmokeTests(unittest.TestCase):
    def test_fixture_retains_full_word_and_requires_bounded_deadline(self):
        expected = fixture()
        path = pathlib.Path('expectations.json')
        with patch.object(smoke, 'private_bytes', side_effect=lambda *_: json.dumps(expected).encode()), patch.object(smoke.time, 'time', return_value=1000):
            self.assertEqual(smoke.expectations(path), expected)
            for key, value in [('dueAt', 1014), ('dueAt', 1151), ('evmChainId', 990002)]:
                with self.subTest(key=key, value=value):
                    original = expected[key]
                    expected[key] = value
                    with self.assertRaises(ValueError):
                        smoke.expectations(path)
                    expected[key] = original
            expected['nft']['id'] = '0x00'
            with self.assertRaises(ValueError):
                smoke.expectations(path)

    def test_checksums_and_exported_initial_height(self):
        expected = fixture()
        expected['gateway'] = '0x' + 'aB' * 20
        expected['nft']['vault'] = '0x' + 'Bc' * 20
        with patch.object(smoke, 'private_bytes', return_value=json.dumps(expected).encode()), patch.object(smoke.time, 'time', return_value=1000):
            parsed = smoke.expectations(pathlib.Path('expectations.json'))
        self.assertEqual(parsed['gateway'], expected['gateway'].lower())
        self.assertEqual(parsed['nft']['vault'], expected['nft']['vault'].lower())
        genesis = json.dumps({'chain_id': smoke.CHAIN, 'initial_height': '350'}).encode()
        self.assertEqual(smoke.initial_height(genesis, genesis), 350)
        changed = genesis.replace(b'350', b'351')
        with self.assertRaisesRegex(ValueError, 'negative control changed'):
            smoke.initial_height(genesis, changed)
        with patch.object(smoke, 'comet', return_value={'height': '350', 'finalize_block_events': []}):
            self.assertEqual(smoke.scan_blocks(350, 350, parsed), [])

    def test_completion_requires_exact_committed_endblock_marker(self):
        expected = fixture()
        event = {'type': smoke.COMPLETION, 'attributes': [
            {'key': 'gateway', 'value': expected['gateway']},
            {'key': 'frozen_index', 'value': expected['nft']['id']},
            {'key': 'mode', 'value': 'EndBlock'},
        ]}
        result = {'height': '4', 'finalize_block_events': [event]}
        self.assertEqual(smoke.completion_count(result, 4, expected['gateway'], expected['nft']['id']), 1)
        with self.assertRaisesRegex(ValueError, 'height mismatch'):
            smoke.completion_count(result, 5, expected['gateway'], expected['nft']['id'])
        event['attributes'][-1]['value'] = 'BeginBlock'
        with self.assertRaisesRegex(ValueError, 'execution mode'):
            smoke.completion_count(result, 4, expected['gateway'], expected['nft']['id'])
        event['attributes'].append(event['attributes'][0])
        with self.assertRaisesRegex(ValueError, 'duplicate withdrawal'):
            smoke.completion_count(result, 4, expected['gateway'], expected['nft']['id'])

    def test_readonly_checks_actual_owner_on_both_apis_and_backing(self):
        nft = fixture()['nft']
        owner = nft['beneficiary']
        payload = int(owner, 16)
        # The daemon returns a valid address from its SDK query. This stub
        # substitutes only the payload and an unused checksum for parsing.
        bech32 = 'd01' + ''.join(smoke.ALPHABET[(payload >> shift) & 31] for shift in range(155, -1, -5)) + 'qqqqqq'
        unit = {'id': 7, 'owner': bech32, 'reserve': None}
        token = {'id': nft['nativeTokenId'], 'isolated_receipt': True, 'reserve': {'denom': 'del', 'amount': nft['reserveAtomic']}}

        def eth(method, params):
            self.assertEqual(params[-1], '0x8')
            if method == 'eth_getBalance':
                return hex(int(nft['reserveAtomic']))
            if params[0]['data'] == nft['positionCallData']:
                words = [1, 1, 1, 1024, 0, int(nft['reserveAtomic']), 1, 0, int(owner, 16), 1, 1, 1]
                return '0x' + ''.join(f'{word:064x}' for word in words)
            self.assertEqual(params[0]['data'], '0x6352211e' + nft['tokenId'][2:])
            return '0x' + '0' * 24 + owner[2:]

        with patch.object(smoke, 'native', side_effect=lambda path, _: {'sub_token': unit} if '/tokens/' in path else {'token': token}) as native, patch.object(smoke, 'ethereum', side_effect=eth):
            result = smoke.check_nft(8, nft, owner)
            self.assertEqual(result['vaultBackingAtomic'], nft['reserveAtomic'])
            self.assertEqual(native.call_args_list, [call('/decimal/nft/v1/tokens/receipt_1/7', 8), call('/decimal/nft/v1/token/receipt_1', 8)])
            token['reserve']['amount'] = '1'
            with self.assertRaisesRegex(ValueError, 'native receipt reserve'):
                smoke.check_nft(8, nft, owner)

    def test_only_expected_admission_failure_passes(self):
        expected = fixture()
        response = {'error': {'message': 'failed: withdrawal genesis Gateway admits only completion'}}
        with patch.object(smoke, 'request', return_value=(response, {})) as request:
            smoke.check_admission(8, expected)
            payload = request.call_args.args[1]
            self.assertEqual(payload['method'], 'eth_estimateGas')
            self.assertEqual(payload['params'][1], '0x8')
            self.assertEqual(payload['params'][0]['data'], expected['delegateCallData'])
            response['error']['message'] = 'connection refused'
            with self.assertRaisesRegex(ValueError, 'expected admission reason'):
                smoke.check_admission(8, expected)


if __name__ == '__main__':
    unittest.main()
