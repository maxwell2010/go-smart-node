"""Offline filename and identity regression for the isolated fee-daemon gate."""

import base64
import hashlib
import json
import pathlib
import tempfile
import unittest
from unittest.mock import patch

import reserve_fee_smoke as smoke


class FeeDaemonKeyFixtureTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = pathlib.Path(self.temporary.name)
        self.validators = []
        for index, name in enumerate('abc', 1):
            public = bytes([index]) * 32
            encoded = base64.b64encode(public).decode()
            address = hashlib.sha256(public).hexdigest()[:40].upper()
            self.validators.append({'name': name, 'publicKey': encoded, 'consensusAddress': address})
            # Public-only parser fixtures: no key suitable for signing and no
            # daemon is started by these tests.
            self.write(name, {'address': address, 'pub_key': {'type': 'tendermint/PubKeyEd25519', 'value': encoded}})
        # Exercise exact on-disk filenames and bytes on both Windows and Linux.
        # POSIX private-file mode checks have separate utility coverage; this
        # read shim avoids pretending Windows chmod enforces POSIX semantics.
        self.reader = patch.object(smoke, 'private_bytes', side_effect=lambda path, _limit: path.read_bytes())
        self.reader.start()
        self.addCleanup(self.reader.stop)

    def write(self, name, value):
        (self.directory / f'priv_validator_key_{name}.json').write_text(json.dumps(value))

    def test_reads_the_three_exact_exported_key_filenames(self):
        keys = smoke.load_validator_keys(self.directory, self.validators)
        self.assertEqual(len(keys), 3)
        self.assertEqual([json.loads(key)['address'] for key in keys], [row['consensusAddress'] for row in self.validators])

    def test_missing_historical_seed_key_is_not_silently_replaced(self):
        (self.directory / 'priv_validator_key_a.json').unlink()
        with self.assertRaises(FileNotFoundError):
            smoke.load_validator_keys(self.directory, self.validators)

    def test_wrong_public_key_or_address_is_rejected(self):
        self.write('b', json.loads((self.directory / 'priv_validator_key_a.json').read_bytes()))
        with self.assertRaisesRegex(ValueError, 'signing identity differs'):
            smoke.load_validator_keys(self.directory, self.validators)


if __name__ == '__main__':
    unittest.main()
