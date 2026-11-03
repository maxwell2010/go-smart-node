import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from verify_pion_transport import PION_MODULES, verify_transport


class CanonicalPionIntegrity(unittest.TestCase):
    def run_fixture(self, change=None, legacy_import=False):
        with tempfile.TemporaryDirectory() as directory:
            module = Path(directory)
            (module / "go.mod").write_bytes(b"module fixture\n")
            (module / "go.sum").write_bytes(b"fixture checksums\n")
            entries = [{"Path": name, **copy.deepcopy(identity)} for name, identity in PION_MODULES.items()]
            if change:
                change(entries)

            def output(argv, **kwargs):
                self.assertNotIn("tidy", argv)
                self.assertNotIn("download", argv)
                if "-m" in argv:
                    self.assertIn("-mod=readonly", argv)
                    return "\n".join(json.dumps(entry) for entry in entries)
                if "-deps" in argv:
                    self.assertIn("-mod=readonly", argv)
                    return "\n".join([*PION_MODULES, *( ["github.com/pion/dtls/v2"] if legacy_import else [])])
                return "all modules verified\n"

            with patch("verify_pion_transport.subprocess.check_output", side_effect=output):
                return verify_transport(module)

    def test_canonical_modules_pass(self):
        self.assertEqual([], self.run_fixture()["compiled_legacy_imports"])

    def test_version_and_checksum_changes_fail(self):
        for field in ("Version", "Sum", "GoModSum"):
            with self.subTest(field=field), self.assertRaisesRegex(RuntimeError, "version/checksum"):
                self.run_fixture(lambda entries: entries[0].update({field: "unexpected"}))

    def test_local_and_alias_replacements_fail(self):
        for path in ("./local", "github.com/pion/stun/v3"):
            with self.subTest(path=path), self.assertRaisesRegex(RuntimeError, "replacements"):
                self.run_fixture(lambda entries: entries[0].update({"Replace": {"Path": path}}))
        with self.assertRaisesRegex(RuntimeError, "replacements"):
            self.run_fixture(lambda entries: entries.append({"Path": "github.com/pion/stun/v2", "Replace": {"Path": "github.com/pion/stun/v3"}}))

    def test_compiled_legacy_import_fails(self):
        with self.assertRaisesRegex(RuntimeError, "Legacy Pion"):
            self.run_fixture(legacy_import=True)

    def test_unused_legacy_metadata_is_reported(self):
        result = self.run_fixture(lambda entries: entries.append({"Path": "github.com/pion/dtls/v2", "Version": "v2.2.7"}))
        self.assertEqual({"github.com/pion/dtls/v2": "v2.2.7"}, result["legacy_mvs_inventory"])


if __name__ == "__main__":
    unittest.main()
