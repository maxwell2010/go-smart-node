import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from source_inventory import json_records, verify_build_inputs


class ProductionSourceInventory(unittest.TestCase):
    def fixture(self, root):
        root = Path(root)
        module = root / "network"
        package = module / "cmd" / "mcnd"
        package.mkdir(parents=True)
        (module / "go.mod").write_text("module fixture\n\ngo 1.26.0\n")
        (module / "go.sum").write_text("")
        (package / "main.go").write_text("package main\nfunc main() {}\n")
        (package / "template.txt").write_text("ordinary embedded data")
        (root / ".gitignore").write_text("hidden.go\n")
        subprocess.run(["git", "init", "-q", str(root)], check=True)
        subprocess.run(["git", "-C", str(root), "add", "."], check=True)
        subprocess.run(["git", "-C", str(root), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture"], check=True)
        descriptor = {"Dir": str(package), "Module": {"Main": True, "GoMod": str(module / "go.mod")},
                      "GoFiles": ["main.go"], "EmbedFiles": ["template.txt"]}
        return root, package, descriptor

    def test_inventory_covers_tracked_production_and_embedded_inputs(self):
        with tempfile.TemporaryDirectory() as directory:
            root, _, descriptor = self.fixture(directory)
            actual = verify_build_inputs(root, package_loader=lambda _: [descriptor])
            self.assertEqual(1, actual["localPackages"])
            self.assertEqual({"network/go.mod", "network/go.sum", "network/cmd/mcnd/main.go", "network/cmd/mcnd/template.txt"}, set(actual["files"]))

    def test_untracked_ignored_and_untracked_embed_inputs_are_rejected(self):
        for name, field in [("extra.go", "GoFiles"), ("hidden.go", "GoFiles"), ("extra.txt", "EmbedFiles")]:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                root, package, descriptor = self.fixture(directory)
                (package / name).write_text("ordinary fixture data")
                descriptor[field].append(name)
                with self.assertRaisesRegex(ValueError, "untracked or ignored production build input"):
                    verify_build_inputs(root, package_loader=lambda _: [descriptor])

    def test_tracked_changes_and_external_local_replacement_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as outside:
            root, package, descriptor = self.fixture(directory)
            original = (package / "main.go").read_bytes()
            (package / "main.go").write_text("package main\nfunc main() { println(1) }\n")
            with self.assertRaisesRegex(ValueError, "tracked source modifications"):
                verify_build_inputs(root, package_loader=lambda _: [descriptor])
            (package / "main.go").write_bytes(original)
            external = {"Dir": outside, "Module": {"Path": "external", "Replace": {"Path": outside}}, "GoFiles": []}
            with self.assertRaisesRegex(ValueError, "outside the pinned repository"):
                verify_build_inputs(root, package_loader=lambda _: [descriptor, external])

    def test_complete_json_stream_and_missing_local_inventory(self):
        self.assertEqual([{"a": 1}, {"b": 2}], list(json_records(' {"a":1}\n{"b":2}\n')))
        with self.assertRaises(json.JSONDecodeError):
            list(json_records('{"a":1}\n{"b":'))
        with tempfile.TemporaryDirectory() as directory:
            root, _, _ = self.fixture(directory)
            with self.assertRaisesRegex(ValueError, "no local packages"):
                verify_build_inputs(root, package_loader=lambda _: [])


if __name__ == "__main__":
    unittest.main()
