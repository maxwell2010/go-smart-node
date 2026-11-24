import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from source_inventory import json_records, production_environment, verify_build_inputs, verify_profile_inventory


class ProductionSourceInventory(unittest.TestCase):
    def test_default_build_policy_rejects_unsupported_overrides(self):
        for key, value in (("GOFLAGS", "-tags=test"), ("GOWORK", "workspace-file"), ("CGO_CFLAGS", "custom-flags"), ("GOEXPERIMENT", "custom-experiment")):
            with self.subTest(key=key), self.assertRaisesRegex(ValueError, "unsupported default-build environment override"):
                production_environment({key: value})
        env=production_environment({"GOPROXY":"https://proxy.golang.org", "GOCACHE":"cache", "GOENV":"user-config"})
        self.assertEqual("off",env["GOENV"])
        self.assertEqual("off",env["GOWORK"])
        self.assertEqual("local",env["GOTOOLCHAIN"])
        self.assertEqual(("linux","amd64","v1","1","gcc"),tuple(env[k] for k in ("GOOS","GOARCH","GOAMD64","CGO_ENABLED","CC")))
        self.assertEqual("cache",env["GOCACHE"])
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

    def test_go_list_records_only_the_compiled_native_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            root, package, _ = self.fixture(directory)
            module = root / "network"
            (module / "go.mod").write_text("module fixture\n\ngo 1.21\n")
            (package / "main.go").write_text('package main\nimport _ "fixture/identity"\nfunc main() {}\n')
            identity = module / "identity"
            identity.mkdir()
            for name, condition in (("mainnet", "mintcandy_mainnet"), ("testnet", "mintcandy_testnet"),
                                    ("legacy", "!mintcandy_mainnet && !mintcandy_testnet")):
                (identity / f"profile_{name}.go").write_text(f"//go:build {condition}\n\npackage identity\n")
            subprocess.run(["git", "-C", str(root), "add", "network"], check=True)
            subprocess.run(["git", "-C", str(root), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                            "-c", "commit.gpgsign=false", "commit", "-qm", "profiles"], check=True)
            for denom, name, other in (("msp", "mainnet", "tmsp"), ("tmsp", "testnet", "msp")):
                inv = verify_build_inputs(root, native_denom=denom)
                self.assertIn(f"network/identity/profile_{name}.go", inv["files"])
                with self.assertRaisesRegex(ValueError, "native profile"):
                    verify_profile_inventory(inv, other)
                inv["files"]["network/identity/profile_legacy.go"] = "0" * 64
                with self.assertRaisesRegex(ValueError, "native profile"):
                    verify_profile_inventory(inv, denom)

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
