import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from verify_owned_geth import verify_geth


class OwnedGethIntegrity(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.manifest = json.loads((Path(__file__).resolve().parent.parent / "third_party/cosmos-geth/UPSTREAM.json").read_bytes())

    def verify_manifest(self, manifest):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "UPSTREAM.json").write_bytes(json.dumps(manifest).encode())
            return verify_geth(root)

    def test_partial_fix_omitting_cgo_guard_is_rejected(self):
        manifest = copy.deepcopy(self.manifest)
        del manifest["modifiedFiles"]["crypto/secp256k1/ext.h"]
        with self.assertRaisesRegex(RuntimeError, "patch inventory"):
            self.verify_manifest(manifest)

    def test_modified_canonical_inventory_cannot_be_relabelled(self):
        manifest = copy.deepcopy(self.manifest)
        manifest["canonicalFiles"]["COPYING.LESSER"] = "0" * 64
        manifest["canonicalFilesSha256"] = hashlib.sha256(json.dumps(manifest["canonicalFiles"], sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        with self.assertRaisesRegex(RuntimeError, "canonical file inventory"):
            self.verify_manifest(manifest)

    def test_manifest_does_not_authorize_missing_source_files(self):
        with self.assertRaisesRegex(RuntimeError, "missing="):
            self.verify_manifest(self.manifest)


if __name__ == "__main__":
    unittest.main()
