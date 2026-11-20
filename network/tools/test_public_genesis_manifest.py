import copy
import hashlib
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from public_genesis_manifest import encode, prepare_manifest, release_for, verify_manifest
import test_verify_public_launch_genesis as ledger_fixture


class PublicGenesisManifestTest(unittest.TestCase):
    def setUp(self):
        self.fixture = ledger_fixture.PublicGenesisLedgerTest()
        self.fixture.setUp()
        self.genesis = self.fixture.data()
        self.software = {'releaseVersion': '1.1.1', 'sourceRevision': '3' * 40,
                         'archiveSha256': '4' * 64, 'binarySha256': '5' * 64}
        report = self.fixture.verify()
        self.cold = {'status': 'CANDIDATE_INIT_CHAIN_VERIFIED', 'firstBlockAppHash': '6' * 64,
                     'genesisSha256': report['genesisSha256'], 'chainId': report['chainId'],
                     'evmChainId': str(report['evmChainId']), 'initialSupplyAtoms': report['initialSupplyAtomic'],
                     'bankBalances': report['checkedBankRecipients'], 'validators': report['checkedValidators']}

    def prepare(self):
        fixture = self.fixture
        return prepare_manifest(fixture.data(), fixture.applications, fixture.plan, fixture.draft,
                                fixture.relayer_wallets, self.software, self.cold)

    def test_repeatable_manifest_binds_release_genesis_and_allocation_without_private_intake(self):
        result = self.prepare()
        self.assertEqual(result['genesisSha256'], hashlib.sha256(self.genesis).hexdigest())
        self.assertEqual(result['coldTestFirstBlockAppHash'], self.cold['firstBlockAppHash'])
        self.assertEqual(result['software'], self.software)
        self.assertEqual(result['validators'], 4)
        self.assertEqual(result['initialSupplyAtoms'], '5041000000000000000000000')
        self.assertNotIn(b'example.invalid', encode(result))
        self.assertEqual(encode(result), encode(self.prepare()))

    def test_cannot_prepare_manifest_for_stale_cold_report_or_mismatched_ledger(self):
        self.fixture.document['genesis_time'] = '2026-12-01T01:00:00Z'
        with self.assertRaisesRegex(ValueError, 'cold InitChain report differs'):
            self.prepare()
        self.fixture.document['app_state']['bank']['balances'][0]['address'] = ledger_fixture.account(ledger_fixture.evm(99))
        with self.assertRaisesRegex(ValueError, 'bank recipients'):
            self.prepare()

    def test_release_check_reads_the_binary_instead_of_trusting_its_filename(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory, 'mcnd')
            binary.write_bytes(b'fixture binary')
            software = {**self.software, 'binarySha256': hashlib.sha256(binary.read_bytes()).hexdigest()}
            with patch('public_genesis_manifest.read_json', return_value=software):
                self.assertEqual(release_for(binary), software)
                binary.write_bytes(b'different binary')
                with self.assertRaisesRegex(ValueError, 'binary SHA256 differs'):
                    release_for(binary)

    @unittest.skipUnless(sys.platform == 'linux' and shutil.which('gpg') and shutil.which('gpgv'), 'requires Linux and OpenPGP')
    def test_openpgp_requires_exact_signer_approval_genesis_and_release(self):
        with tempfile.TemporaryDirectory(prefix='mcnd-public-manifest-test-') as directory:
            root = Path(directory)
            home = root / 'gpg'
            home.mkdir(mode=0o700)
            gpg = ['gpg', '--no-options', '--batch', '--homedir', str(home)]
            try:
                subprocess.run([*gpg, '--pinentry-mode', 'loopback', '--passphrase', '', '--quick-generate-key',
                                'CI fixture <public-genesis@example.invalid>', 'ed25519', 'sign', '1d'],
                               check=True, capture_output=True, timeout=30)
                keys = subprocess.check_output([*gpg, '--with-colons', '--fingerprint'], text=True, timeout=30)
                signer = next(line.split(':')[9] for line in keys.splitlines() if line.startswith('fpr:'))
                args = SimpleNamespace(manifest=root / 'manifest.json', signature=root / 'manifest.asc',
                                       trusted_keyring=root / 'trusted.gpg', signer_fingerprint=signer)
                args.trusted_keyring.write_bytes(subprocess.check_output([*gpg, '--export', signer], timeout=30))
                expected = self.prepare()
                args.manifest.write_bytes(encode(expected))
                args.approved_manifest_sha256 = hashlib.sha256(args.manifest.read_bytes()).hexdigest()
                subprocess.run([*gpg, '--local-user', signer + '!', '--armor', '--detach-sign', '--output',
                                str(args.signature), str(args.manifest)], check=True, capture_output=True, timeout=30)

                fixture = self.fixture
                result = verify_manifest(args, self.genesis, fixture.plan, fixture.draft, self.software)
                self.assertEqual(result['status'], 'PUBLIC_GENESIS_SIGNATURE_VERIFIED')
                self.assertFalse(result['networkLaunchAuthorized'])
                for key, value, error in (
                    ('approved_manifest_sha256', '0' * 64, 'independently approved value'),
                    ('signer_fingerprint', '0' * 40, 'signer fingerprint'),
                ):
                    with self.subTest(key=key):
                        wrong = copy.copy(args)
                        setattr(wrong, key, value)
                        with self.assertRaisesRegex(ValueError, error):
                            verify_manifest(wrong, self.genesis, fixture.plan, fixture.draft, self.software)
                with self.assertRaisesRegex(ValueError, 'genesis SHA256 differs'):
                    verify_manifest(args, self.genesis + b'\n', fixture.plan, fixture.draft, self.software)
                with self.assertRaisesRegex(ValueError, 'signed manifest metadata differs'):
                    verify_manifest(args, self.genesis, fixture.plan, fixture.draft, {**self.software, 'releaseVersion': '1.0.0'})
                args.manifest.write_bytes(encode({**expected, 'genesisTimeUtc': '2026-12-01T01:00:00Z'}))
                args.approved_manifest_sha256 = hashlib.sha256(args.manifest.read_bytes()).hexdigest()
                with self.assertRaisesRegex(ValueError, 'OpenPGP signature'):
                    verify_manifest(args, self.genesis, fixture.plan, fixture.draft, self.software)
            finally:
                subprocess.run(['gpgconf', '--homedir', str(home), '--kill', 'gpg-agent'], capture_output=True, timeout=10)


if __name__ == '__main__':
    unittest.main()
