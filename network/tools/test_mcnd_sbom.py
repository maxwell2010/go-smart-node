import copy
import hashlib
import json
import unittest

from binary_provenance import MAIN_MODULE
from mcnd_sbom import make_sbom


REVISION = "a" * 40
BINARY_SHA256 = "b" * 64


class McndSbomTest(unittest.TestCase):
    def fixture(self):
        comet = "github.com/cometbft/cometbft"
        keyring = "github.com/99designs/keyring"
        dependencies = {
            comet: {"path": comet, "version": "v0.38.26", "sum": "h1:comet"},
            keyring: {"path": keyring, "version": "v1.2.1", "sum": "", "replace": {
                "path": "github.com/cosmos/keyring", "version": "v1.2.0", "sum": "h1:keyring",
            }},
        }
        inventory = {"sourceRevision": REVISION, "files": {}}
        archive = {}
        for name, folder, upstream in [
            ("github.com/cosmos/cosmos-sdk", "cosmos-sdk", "github.com/cosmos/cosmos-sdk"),
            ("github.com/cosmos/evm", "cosmos-evm", "github.com/cosmos/evm"),
            ("github.com/ethereum/go-ethereum", "cosmos-geth", "github.com/cosmos/go-ethereum"),
        ]:
            path = "third_party/" + folder
            dependencies[name] = {"path": name, "version": "v1.0.0", "sum": "", "replace": {
                "path": "./" + path, "version": "(devel)", "sum": "",
            }}
            manifest = {
                "schemaVersion": 1, "upstreamModule": upstream, "upstreamVersion": "v1.0.0",
                "upstreamCommit": "c" * 40, "upstreamModuleSum": "h1:upstream",
                "canonicalFiles": {"go.mod": "1" * 64, "file.go": "2" * 64},
                "modifiedFiles": {"file.go": "3" * 64}, "addedFiles": {"added.go": "4" * 64},
                ("patchVersion" if folder == "cosmos-evm" else "patchLabel"): "v1.0.0-mintcandy.1",
            }
            if name != upstream:
                manifest["moduleDeclaration"] = name
            prefix = "network/" + path + "/"
            archive["provenance/" + prefix + "UPSTREAM.json"] = json.dumps(manifest).encode()
            inventory["files"].update({prefix + name: digest for name, digest in {
                "go.mod": "1" * 64, "file.go": "3" * 64, "added.go": "4" * 64,
            }.items()})
        build = {
            "goVersion": "go1.26.11", "module": {"path": MAIN_MODULE},
            "settings": {"vcs.revision": REVISION}, "dependencies": dependencies,
        }
        return build, inventory, archive

    def test_binary_identity_and_effective_module_provenance(self):
        build, inventory, archive = self.fixture()
        bom = make_sbom(build, inventory, archive, BINARY_SHA256)
        metadata = bom["metadata"]["component"]
        self.assertEqual(("CycloneDX", "1.5"), (bom["bomFormat"], bom["specVersion"]))
        self.assertEqual(("mcnd", REVISION), (metadata["name"], metadata["version"]))
        self.assertEqual([{"alg": "SHA-256", "content": BINARY_SHA256}], metadata["hashes"])
        modules = {component["bom-ref"]: component for component in bom["components"]}
        self.assertEqual(len(build["dependencies"]) + 1, len(modules))
        self.assertEqual("go1.26.11", modules["go-toolchain"]["version"])
        keyring = modules["go-module:github.com/99designs/keyring"]
        self.assertEqual("pkg:golang/github.com/cosmos/keyring@v1.2.0", keyring["purl"])
        self.assertEqual("h1:keyring", {p["name"]: p["value"] for p in keyring["properties"]}["go.module.sum"])
        owned = [component for ref, component in modules.items() if ref.startswith("owned-go-module:")]
        self.assertEqual(3, len(owned))
        for component in owned:
            self.assertNotIn("purl", component)
            self.assertEqual("v1.0.0-mintcandy.1", component["version"])
            values = {p["name"]: p["value"] for p in component["properties"]}
            path = values["go.owned.manifest.path"]
            self.assertEqual(hashlib.sha256(archive[path]).hexdigest(), values["go.owned.manifest.sha256"])
            expected = json.dumps({"added.go": "4" * 64, "file.go": "3" * 64, "go.mod": "1" * 64},
                                  sort_keys=True, separators=(",", ":")).encode()
            self.assertEqual(hashlib.sha256(expected).hexdigest(), values["go.owned.compiled_inputs.sha256"])
        self.assertEqual("github.com/cosmos/go-ethereum", modules["owned-go-module:github.com/ethereum/go-ethereum"]["name"])
        self.assertNotIn("vulnerabilities", bom)

    def test_refuses_source_drift_unknown_replacement_and_missing_manifest(self):
        build, inventory, archive = self.fixture()
        with self.assertRaisesRegex(ValueError, "differs from verified binary revision"):
            make_sbom(build, {**inventory, "sourceRevision": "d" * 40}, archive, BINARY_SHA256)
        changed = copy.deepcopy(inventory)
        changed["files"]["network/third_party/cosmos-geth/file.go"] = "5" * 64
        with self.assertRaisesRegex(ValueError, "compiled inputs differ"):
            make_sbom(build, changed, archive, BINARY_SHA256)
        with self.assertRaisesRegex(ValueError, "missing archived"):
            make_sbom(build, inventory, {}, BINARY_SHA256)
        build["dependencies"]["github.com/cosmos/evm"]["replace"]["path"] = "./unreviewed"
        with self.assertRaisesRegex(ValueError, "unrecognized owned Go module"):
            make_sbom(build, inventory, archive, BINARY_SHA256)
