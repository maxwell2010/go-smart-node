import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from verify_owned_evm import verify


class OwnedSourceIntegrity(unittest.TestCase):
    def test_declared_patch_and_unannounced_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            original = b"canonical source"
            patched = b"declared corrected source"
            (root / "source.go").write_bytes(patched)
            manifest = {"schemaVersion": 1, "upstreamModule": "github.com/cosmos/evm", "upstreamVersion": "v0.6.3",
                        "upstreamCommit": "71c78b5072a248bae5273c34ad2f53d349799ccd",
                        "upstreamModuleSum": "h1:1G8+iqyQWslZ2g7873h90Fcr3pL6RV24PgOS1wiK2SY=",
                        "canonicalFiles": {"source.go": hashlib.sha256(original).hexdigest()},
                        "modifiedFiles": {"source.go": hashlib.sha256(patched).hexdigest()}, "addedFiles": {}}
            (root / "UPSTREAM.json").write_text(json.dumps(manifest))
            self.assertEqual((1, 1), verify(root))
            (root / "source.go").write_bytes(b"undeclared change")
            with self.assertRaisesRegex(RuntimeError, "changed"):
                verify(root)
            (root / "source.go").write_bytes(patched)
            (root / "extra.go").write_bytes(b"extra")
            with self.assertRaisesRegex(RuntimeError, "extra"):
                verify(root)

    def test_missing_canonical_file_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifest = {"schemaVersion": 1, "upstreamModule": "github.com/cosmos/evm", "upstreamVersion": "v0.6.3",
                        "upstreamCommit": "71c78b5072a248bae5273c34ad2f53d349799ccd",
                        "upstreamModuleSum": "h1:1G8+iqyQWslZ2g7873h90Fcr3pL6RV24PgOS1wiK2SY=",
                        "canonicalFiles": {"missing.go": hashlib.sha256(b"original").hexdigest()},
                        "modifiedFiles": {}, "addedFiles": {}}
            (root / "UPSTREAM.json").write_text(json.dumps(manifest))
            with self.assertRaisesRegex(RuntimeError, "missing"):
                verify(root)


if __name__ == "__main__":
    unittest.main()
