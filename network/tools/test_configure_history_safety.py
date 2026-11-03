import json
import os
from pathlib import Path
import tempfile
import unittest

from configure_history import PROFILE, configure


class HistoryFreshHomeSafety(unittest.TestCase):
    def fixture(self, directory):
        home = Path(directory)
        (home / "config").mkdir()
        (home / "data").mkdir()
        for name, values in PROFILE.items():
            text = ""
            previous = None
            for (section, key), value in values.items():
                if section != previous:
                    text += "[" + section + "]\n"
                    previous = section
                text += key + " = " + value + "\n"
            (home / "config" / name).write_text(text)
        return home

    def original(self, home):
        return {path.name: path.read_bytes() for path in (home / "config").iterdir()}

    def test_zeroed_init_signing_state_is_accepted_without_modification(self):
        with tempfile.TemporaryDirectory() as directory:
            home = self.fixture(directory)
            state = home / "data/priv_validator_state.json"
            state.write_text(json.dumps({"height": "0", "round": 0, "step": 0}))
            original = state.read_bytes()
            configure(home, True)
            self.assertEqual(original, state.read_bytes())

    def test_existing_signing_state_is_rejected_before_any_config_write(self):
        for changes in ({"height": "2"}, {"round": 1}, {"step": 1}, {"signature": "ordinary fixture"}, {"height": 0}, {"step": False}, {"unknown": 0}):
            with self.subTest(changes=changes), tempfile.TemporaryDirectory() as directory:
                home = self.fixture(directory)
                state = {"height": "0", "round": 0, "step": 0, **changes}
                (home / "data/priv_validator_state.json").write_text(json.dumps(state))
                original = self.original(home)
                with self.assertRaises(ValueError):
                    configure(home, True)
                self.assertEqual(original, self.original(home))

    def test_unknown_data_artifact_is_rejected_without_config_changes(self):
        for name in ("application.db-wal", "snapshot.tmp", "unexpected-directory"):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                home = self.fixture(directory)
                path = home / "data" / name
                path.mkdir() if name.endswith("directory") else path.write_bytes(b"ordinary fixture")
                original = self.original(home)
                with self.assertRaises(ValueError):
                    configure(home, True)
                self.assertEqual(original, self.original(home))

    def test_preexisting_staging_file_creates_no_backups(self):
        with tempfile.TemporaryDirectory() as directory:
            home = self.fixture(directory)
            (home / "config/config.toml.new").write_bytes(b"operator-owned staging content")
            original = self.original(home)
            with self.assertRaises(ValueError):
                configure(home, True)
            self.assertEqual(original, self.original(home))

    @unittest.skipIf(os.name == "nt", "Linux symlink and permission checks")
    def test_staging_symlink_and_modes(self):
        with tempfile.TemporaryDirectory() as directory:
            home = self.fixture(directory)
            target = home / "outside"
            link = home / "config/config.toml.new"
            link.symlink_to(target)
            original = self.original(home) if target.exists() else {p.name: p.read_bytes() for p in (home / "config").iterdir() if not p.is_symlink()}
            with self.assertRaises(ValueError):
                configure(home, True)
            self.assertFalse(target.exists())
            self.assertEqual(original, {p.name: p.read_bytes() for p in (home / "config").iterdir() if not p.is_symlink()})
            link.unlink()
            for path in (home / "config").iterdir():
                path.chmod(0o600)
            configure(home, True)
            for path in (home / "config").iterdir():
                self.assertEqual(0o600, path.stat().st_mode & 0o777)


if __name__ == "__main__":
    unittest.main()
