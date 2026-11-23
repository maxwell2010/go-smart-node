import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from check_operator_applications import strict_native_address
from configure_history import validate_fresh_home
from install_validator_genesis import COMET_PATHS, install_genesis, install_operator_genesis, run_offline, validate_prepared_home
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
        import install_validator_genesis
        import public_genesis_manifest
        from check_public_launch_draft import DEFAULT_DRAFT, read_json
        from mcnd_process_smoke import private_namespace
        from public_genesis_manifest import prepare_manifest, release_for, verify_manifest
        from test_founding_ledger_fixture import prepared_fixture
        from test_public_genesis_manifest import signed_fixture

        private_namespace()
        installed_tools = os.environ.get('MCND_VALIDATOR_INSTALL_TOOLS')
        if installed_tools:
            for module in (install_validator_genesis, public_genesis_manifest):
                self.assertEqual(Path(module.__file__).resolve().parent, Path(installed_tools).resolve())
        binary = Path(os.environ['MCND_VALIDATOR_INSTALL_BINARY'])
        fixture = Path(os.environ['MCND_VALIDATOR_INSTALL_FIXTURE'])
        genesis = (fixture / 'genesis.json').read_bytes()
        body = allocation_report(genesis)
        chain_id, evm_id = body['chainId'], body['evmChainId']
        self.assertEqual(chain_id, 'mintcandy_990014-1')
        validator = body['validators'][0]
        operator = validator['operatorAddress']
        verified_release = os.environ.get('MCND_VALIDATOR_INSTALL_CANDIDATE_RECORD')
        if verified_release:
            # The isolated rehearsal already verified the exact archive and
            # signer before supplying this record. The public CLI stays pinned.
            with patch('public_genesis_manifest.RELEASE', Path(verified_release)):
                software = release_for(binary)
        else:
            software = release_for(binary)
        artifacts = fixture.parent
        _, applications, plan, relayers = prepared_fixture(artifacts / 'fixture', read_json(artifacts / 'roles.json'))
        applications['operators'][0]['endpointPublicationAllowed'] = True
        draft = read_json(DEFAULT_DRAFT)
        cold = read_json(artifacts / 'evidence/assembly.json')
        manifest = prepare_manifest(genesis, applications, plan, draft, relayers, software, cold, include_public_p2p=True)
        key = next(path.read_bytes() for path in fixture.glob('priv_validator_key_*.json')
                   if json.loads(path.read_bytes())['pub_key']['value'] == validator['consensusPublicKeyBase64'])

        with tempfile.TemporaryDirectory(prefix='validator-install-ci-') as directory:
            root = Path(directory)
            with signed_fixture(root, manifest) as args:
                with self.assertRaisesRegex(ValueError, 'genesis SHA256 differs'):
                    verify_manifest(args, genesis + b'\n', plan, draft, software)
                verified = verify_manifest(args, genesis, plan, draft, software)
                manifest_output = os.environ.get('MCND_VERIFIED_CONSENT_MANIFEST')
                if manifest_output:
                    with Path(manifest_output).open('xb') as stream:
                        stream.write(args.manifest.read_bytes())
                assembler = os.environ['MCND_VALIDATOR_INSTALL_ASSEMBLER']
                output_file = root / 'operator-message.txt'
                commonly = ['--manifest', str(args.manifest), '--genesis', str(fixture / 'genesis.json'),
                            '--approved-manifest-sha256', args.approved_manifest_sha256]
                evm_operator = strict_native_address(operator, 'd0valoper')
                subprocess.run([assembler, 'launch-consent', 'payload', *commonly,
                                '--operator', evm_operator, '--output', str(output_file)], check=True, timeout=30)
                message = output_file.read_text(encoding='utf8')
                self.assertIn(f'Manifest SHA256: {args.approved_manifest_sha256}\n', message)
                self.assertIn(f'Operator: {evm_operator}\n', message)
                self.assertTrue(message.endswith(validator['consensusPublicKeyBase64']))
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
            with self.assertRaisesRegex(ValueError, 'observer home uses a consensus key declared in'):
                install_genesis(binary, home, genesis, verified)
            self.assertEqual(path.read_bytes(), original_genesis)
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
            report = install_operator_genesis(binary, home, evm_operator, genesis, verified)
            self.assertTrue(report['changed'])
            self.assertEqual(report['operatorAddress'], evm_operator)
            self.assertFalse(report['networkLaunchAuthorized'])
            self.assertEqual(report['manifestSha256'], verified['manifestSha256'])
            self.assertEqual(report['signerFingerprint'], verified['signerFingerprint'])
            self.assertEqual(path.read_bytes(), genesis)
            self.assertFalse(install_operator_genesis(binary, home, evm_operator, genesis, verified)['changed'])
            self.assertEqual(preserved, {file: sha256(file) for file in home.rglob('*') if file.is_file() and file != path})

            observer = root / 'observer'
            run_offline(binary, observer, root, 'init', 'observer-install-ci', '--chain-id', chain_id, '--evm.evm-chain-id', evm_id)
            observer_genesis = observer / 'config/genesis.json'
            observer_files = {file: sha256(file) for file in observer.rglob('*')
                              if file.is_file() and file != observer_genesis}
            observer_report = install_genesis(binary, observer, genesis, verified)
            self.assertEqual(observer_report['status'], 'OBSERVER_GENESIS_INSTALLED')
            self.assertEqual(observer_report['genesisSha256'], report['genesisSha256'])
            self.assertEqual(observer_report['manifestSha256'], report['manifestSha256'])
            self.assertEqual(observer_report['publicP2P'], manifest['publicP2P'])
            self.assertTrue(observer_report['changed'])
            self.assertFalse(observer_report['networkLaunchAuthorized'])
            self.assertEqual(observer_genesis.read_bytes(), genesis)
            self.assertFalse(install_genesis(binary, observer, genesis, verified)['changed'])
            self.assertEqual(observer_files, {file: sha256(file) for file in observer.rglob('*')
                                              if file.is_file() and file != observer_genesis})
            output = os.environ.get('MCND_VALIDATOR_INSTALL_REPORT')
            if output:
                with Path(output).open('x', encoding='utf8') as stream:
                    json.dump({**report, 'chainId': chain_id, 'preservedHomeFiles': len(preserved),
                               'signatureVerified': True, 'consentPayloadVerified': True, 'idempotent': True, 'rejectsForeignKey': True,
                               'installerFromArchive': bool(installed_tools),
                               'rejectsExistingState': True, 'observer': {**observer_report, 'idempotent': True,
                                   'rejectsFoundingKey': True, 'preservedHomeFiles': len(observer_files)}}, stream)


if __name__ == '__main__':
    unittest.main()
