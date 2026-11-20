import json
import os
from pathlib import Path
import tempfile
import unittest

from configure_history import validate_fresh_home
from install_validator_genesis import COMET_PATHS, install_operator_genesis, run_offline, validate_prepared_home
from native_launch_allocation import allocation_report
from native_launch_package import sha256


class PreparedHomeTest(unittest.TestCase):
    def test_rejects_external_signer_wrong_domain_and_existing_state(self):
        with tempfile.TemporaryDirectory() as work:
            home = Path(work)
            config = home / 'config'
            data = home / 'data'
            config.mkdir()
            data.mkdir()
            chain_id, evm_id = 'mintcandy_990014-1', '990014'
            for name in ('genesis.json', 'node_key.json', 'priv_validator_key.json'):
                (config / name).write_text('{}')
            (config / 'client.toml').write_text('chain-id = ""')
            app = config / 'app.toml'
            app.write_text(f'[evm]\nevm-chain-id = {evm_id}')
            comet = config / 'config.toml'
            comet.write_text('\n'.join(f'{key} = {json.dumps(value)}' for key, value in COMET_PATHS.items()))
            state = data / 'priv_validator_state.json'
            state.write_text('{"height": "0", "round": 0, "step": 0}')
            validate_prepared_home(home, chain_id, evm_id)
            with self.assertRaisesRegex(ValueError, 'signing domain differs'):
                validate_prepared_home(home, chain_id, '990015')
            original = comet.read_text()
            comet.write_text(original.replace('priv_validator_laddr = ""', 'priv_validator_laddr = "tcp://127.0.0.1:1"'))
            with self.assertRaisesRegex(ValueError, 'local signer'):
                validate_prepared_home(home, chain_id, evm_id)
            comet.write_text(original)
            state.write_text('{"height": "2", "round": 0, "step": 0}')
            with self.assertRaisesRegex(ValueError, 'existing signing state'):
                validate_prepared_home(home, chain_id, evm_id)


@unittest.skipUnless(os.environ.get('MCND_VALIDATOR_INSTALL_BINARY'), 'requires the published Linux binary and disposable fixture')
class PublishedValidatorInstallTest(unittest.TestCase):
    def test_exact_install_preserves_signer_and_rejects_used_or_unrelated_home(self):
        from mcnd_process_smoke import private_namespace
        from public_genesis_manifest import release_for

        private_namespace()
        binary = Path(os.environ['MCND_VALIDATOR_INSTALL_BINARY'])
        fixture = Path(os.environ['MCND_VALIDATOR_INSTALL_FIXTURE'])
        genesis = (fixture / 'genesis.json').read_bytes()
        body = allocation_report(genesis)
        chain_id, evm_id = body['chainId'], body['evmChainId']
        self.assertEqual(chain_id, 'mintcandy_990014-1')
        validator = body['validators'][0]
        operator = validator['operatorAddress']
        verified = {key: body[key] for key in ('chainId', 'evmChainId', 'genesisSha256')}
        verified['software'] = release_for(binary)
        key = next(path.read_bytes() for path in fixture.glob('priv_validator_key_*.json')
                   if json.loads(path.read_bytes())['pub_key']['value'] == validator['consensusPublicKeyBase64'])

        with tempfile.TemporaryDirectory(prefix='validator-install-ci-') as directory:
            root = Path(directory)
            home = root / 'validator'
            run_offline(binary, home, root, 'init', 'validator-install-ci', '--chain-id', chain_id, '--evm.evm-chain-id', evm_id)
            path = home / 'config/genesis.json'
            local_key = home / 'config/priv_validator_key.json'
            state = home / 'data/priv_validator_state.json'
            original_genesis = path.read_bytes()
            with self.assertRaisesRegex(ValueError, 'local consensus key does not match'):
                install_operator_genesis(binary, home, operator, genesis, verified)
            self.assertEqual(path.read_bytes(), original_genesis)

            local_key.write_bytes(key)
            pristine_state = state.read_bytes()
            state.write_text('{"height": "2", "round": 0, "step": 0}')
            with self.assertRaisesRegex(ValueError, 'existing signing state'):
                install_operator_genesis(binary, home, operator, genesis, verified)
            state.write_bytes(pristine_state)
            unknown_data = home / 'data/application.db'
            unknown_data.mkdir()
            with self.assertRaisesRegex(ValueError, 'existing chain data'):
                install_operator_genesis(binary, home, operator, genesis, verified)
            unknown_data.rmdir()

            changed = json.loads(original_genesis)
            changed['initial_height'] = '3'
            unrelated_genesis = json.dumps(changed).encode()
            path.write_bytes(unrelated_genesis)
            with self.assertRaisesRegex(ValueError, 'existing genesis differs'):
                install_operator_genesis(binary, home, operator, genesis, verified)
            self.assertEqual(path.read_bytes(), unrelated_genesis)
            path.write_bytes(original_genesis)

            validate_fresh_home(home)
            preserved = {file: sha256(file) for file in home.rglob('*') if file.is_file() and file != path}
            report = install_operator_genesis(binary, home, operator, genesis, verified)
            self.assertTrue(report['changed'])
            self.assertFalse(report['networkLaunchAuthorized'])
            self.assertEqual(path.read_bytes(), genesis)
            self.assertFalse(install_operator_genesis(binary, home, operator, genesis, verified)['changed'])
            self.assertEqual(preserved, {file: sha256(file) for file in home.rglob('*') if file.is_file() and file != path})
            output = os.environ.get('MCND_VALIDATOR_INSTALL_REPORT')
            if output:
                with Path(output).open('x', encoding='utf8') as stream:
                    json.dump({**report, 'chainId': chain_id, 'preservedHomeFiles': len(preserved),
                               'idempotent': True, 'rejectsForeignKey': True, 'rejectsExistingState': True}, stream)


if __name__ == '__main__':
    unittest.main()
