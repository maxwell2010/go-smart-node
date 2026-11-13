import base64
import hashlib
import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from native_launch_systemd import SystemdValidatorFleet, validator_identity


class SignerReactivationTest(unittest.TestCase):
    def setUp(self):
        work = tempfile.TemporaryDirectory(prefix="mcnd-systemd-signing-test-")
        self.addCleanup(work.cleanup)
        self.root = Path(work.name)
        self.fleet = SystemdValidatorFleet(sys.executable, "fixture-sha")
        self.fleet.root = self.root
        self.fleet.backups = {}
        self.homes = [self.root / name for name in "abc"]
        self.original = {}
        # Exercise the key/state handling without a Linux namespace or systemd.
        # Windows cannot represent POSIX permissions for private fixture files.
        patches = (
            patch("native_launch_systemd.private_namespace"),
            patch("native_launch_systemd.private_bytes", side_effect=lambda path, limit: path.read_bytes()),
        )
        for mock in patches:
            mock.start()
            self.addCleanup(mock.stop)
        for i, home in enumerate(self.homes, 1):
            key = self.key(i)
            state = self.state(49 + i)
            self.original[home] = key, state
            self.install(home, key, state)
            self.install(self.root / "backups" / home.name, key, self.state(31))
            self.fleet.services[home] = SimpleNamespace(submitted=False)
            self.fleet.original_identities[home] = validator_identity(home)

    @staticmethod
    def key(marker):
        public = bytes([marker]) * 32
        private = b"\0" * 32 + public
        return json.dumps({
            "address": hashlib.sha256(public).hexdigest()[:40].upper(),
            "pub_key": {"type": "tendermint/PubKeyEd25519", "value": base64.b64encode(public).decode()},
            "priv_key": {"type": "tendermint/PrivKeyEd25519", "value": base64.b64encode(private).decode()},
        }).encode()

    @staticmethod
    def state(height):
        state = {"height": str(height), "round": 0, "step": 3 if height else 0}
        if height:
            state.update(signature=base64.b64encode(b"\0" * 64).decode(), signbytes="0102")
        return json.dumps(state).encode()

    def install(self, home, key, state):
        for relative, value in ((self.fleet.KEY_FILE, key), (self.fleet.STATE_FILE, state)):
            path = home / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(value)

    def test_signer_reactivation_requires_all_retained_heights_and_keeps_latest_bytes(self):
        retained = self.fleet.capture_signing_watermarks()
        self.assertEqual([retained[label]["height"] for label in "abc"], [50, 51, 52])
        self.fleet.restored = True
        for i, home in enumerate(self.homes, 10):
            self.install(home, self.key(i), self.state(0))
            self.fleet.replay_identities[home] = validator_identity(home)

        verified = dict.fromkeys(self.homes, 52)
        verified[self.homes[-1]] = 51
        with self.assertRaisesRegex(RuntimeError, "not safely replayed"):
            self.fleet.reactivate_signers(verified)
        for home in self.homes:
            self.assertEqual((home / self.fleet.STATE_FILE).read_bytes(), self.state(0))

        self.assertEqual(self.fleet.reactivate_signers(dict.fromkeys(self.homes, 52)), retained)
        for home, (key, state) in self.original.items():
            self.assertEqual((home / self.fleet.KEY_FILE).read_bytes(), key)
            self.assertEqual((home / self.fleet.STATE_FILE).read_bytes(), state)
            self.assertNotEqual(state, self.state(31))

        home = self.homes[0]
        (home / self.fleet.STATE_FILE).write_bytes(self.state(31))
        with self.assertRaisesRegex(RuntimeError, "exact retained key and signing state"):
            self.fleet.spawn(["mcnd", "start", "--home", str(home)], home / "test.log", {})

    def test_capture_rejects_regressed_signing_state(self):
        home = self.homes[-1]
        (home / self.fleet.STATE_FILE).write_bytes(self.state(30))
        with self.assertRaisesRegex(RuntimeError, "regressed relative to its backup"):
            self.fleet.capture_signing_watermarks()
        self.assertIsNone(self.fleet.signing_checkpoints)
