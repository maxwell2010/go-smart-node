import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from verify_owned_sdk import PROTON_IDENTITY, verify_compiled_armor, verify_sdk


class OwnedSDKIntegrity(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.manifest = json.loads((Path(__file__).resolve().parent.parent / "third_party/cosmos-sdk/UPSTREAM.json").read_bytes())

    def verify_manifest(self, manifest):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "UPSTREAM.json").write_bytes(json.dumps(manifest).encode())
            return verify_sdk(root)

    def test_missing_crc_adapter_is_rejected(self):
        manifest = copy.deepcopy(self.manifest)
        del manifest["addedFiles"]["crypto/internal/armorcompat/armor.go"]
        with self.assertRaisesRegex(RuntimeError, "patch inventory"):
            self.verify_manifest(manifest)

    def test_altered_canonical_inventory_is_rejected(self):
        manifest = copy.deepcopy(self.manifest)
        manifest["canonicalFiles"]["LICENSE"] = "0" * 64
        manifest["canonicalFilesSha256"] = hashlib.sha256(json.dumps(manifest["canonicalFiles"], sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        with self.assertRaisesRegex(RuntimeError, "canonical file inventory"):
            self.verify_manifest(manifest)

    def test_manifest_does_not_authorize_absent_source(self):
        with self.assertRaisesRegex(RuntimeError, "missing="):
            self.verify_manifest(self.manifest)

    def test_replaced_or_wrong_maintained_source_is_rejected(self):
        for alteration in ({"Replace": {"Path": "../alias"}}, {"Version": "v1.1.5"}, {"Sum": "h1:wrong"}):
            with self.subTest(alteration=alteration), tempfile.TemporaryDirectory() as directory:
                module = Path(directory)
                (module / "go.mod").write_bytes(b"module test\n")
                (module / "go.sum").write_bytes(b"")
                entry = {"Path": "github.com/ProtonMail/go-crypto", **PROTON_IDENTITY, **alteration}
                with patch("verify_owned_sdk.subprocess.check_output", return_value=json.dumps(entry)):
                    with self.assertRaisesRegex(RuntimeError, "maintained armor source identity"):
                        verify_compiled_armor(module)


if __name__ == "__main__":
    unittest.main()
