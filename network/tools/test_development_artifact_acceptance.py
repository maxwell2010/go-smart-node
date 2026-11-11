"""Opt-in checks of a real CI archive, its Git inputs and actual ELF metadata.

Set MCND_ACCEPTANCE_ROOT to a new fixture directory containing source/,
github-artifact.zip, github-artifact-metadata.json and the downloaded tar/sidecar.
MCND_ACCEPTANCE_COMMIT selects the exact published commit; an optional
MCND_ACCEPTANCE_SOURCE may name a repository containing that Git object.
These tests never start a node, install dependencies or send transactions.
"""
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile
import unittest
import zipfile

from install_mcnd import pinned_go_version


COMMIT = os.environ.get("MCND_ACCEPTANCE_COMMIT", "88ed5365340a6cf22bb757b27a9a9e5eef3dbe28")
ARCHIVE = "mcnd-development-linux-amd64.tar.gz"


def digest_stream(stream):
    digest = hashlib.sha256()
    for block in iter(lambda: stream.read(1024 * 1024), b""):
        digest.update(block)
    return digest.hexdigest()


@unittest.skipUnless(os.environ.get("MCND_ACCEPTANCE_ROOT"), "real CI artifact acceptance is opt-in")
class PublishedDevelopmentArtifactAcceptance(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.root = Path(os.environ["MCND_ACCEPTANCE_ROOT"]).resolve(strict=True)
        if not re.fullmatch("[0-9a-f]{40}", COMMIT):
            raise ValueError("exact lowercase Git commit required")
        cls.source = Path(os.environ.get("MCND_ACCEPTANCE_SOURCE", str(cls.root / "source"))).resolve(strict=True)
        metadata = json.loads((cls.root / "github-artifact-metadata.json").read_bytes())
        cls.artifact = metadata["artifacts"][0]
        with (cls.root / "github-artifact.zip").open("rb") as stream:
            if cls.artifact["digest"] != "sha256:" + digest_stream(stream):
                raise ValueError("GitHub artifact ZIP digest mismatch")
        with tarfile.open(cls.root / ARCHIVE, "r:gz") as archive:
            cls.manifest = json.load(archive.extractfile("MANIFEST.json"))
            cls.inventory = json.load(archive.extractfile("SOURCE_INPUTS.json"))
            cls.build_info = archive.extractfile("BUILD_INFO.txt").read().decode()

    def test_github_digest_sidecar_and_complete_archive_manifest(self):
        self.assertEqual(COMMIT, self.artifact["workflow_run"]["head_sha"])
        with (self.root / "github-artifact.zip").open("rb") as stream:
            self.assertEqual("sha256:" + digest_stream(stream), self.artifact["digest"])
        with zipfile.ZipFile(self.root / "github-artifact.zip") as archive:
            self.assertEqual({ARCHIVE, ARCHIVE + ".sha256"}, set(archive.namelist()))
            for name in archive.namelist():
                with archive.open(name) as stream, (self.root / name).open("rb") as local:
                    self.assertEqual(digest_stream(stream), digest_stream(local))
        expected, filename = (self.root / (ARCHIVE + ".sha256")).read_text().split()
        self.assertEqual(ARCHIVE, filename)
        with (self.root / ARCHIVE).open("rb") as stream:
            self.assertEqual(expected, digest_stream(stream))
        self.assertEqual(COMMIT, self.manifest["sourceRevision"])
        self.assertEqual("development", self.manifest["status"])
        with tarfile.open(self.root / ARCHIVE, "r:gz") as archive:
            names = set()
            for member in archive:
                path = PurePosixPath(member.name)
                self.assertTrue(member.isfile())
                self.assertFalse(path.is_absolute() or ".." in path.parts or member.name in names)
                names.add(member.name)
                if member.name != "MANIFEST.json":
                    self.assertEqual(self.manifest["files"][member.name], digest_stream(archive.extractfile(member)))
            self.assertEqual(set(self.manifest["files"]) | {"MANIFEST.json"}, names)
        self.assertEqual(self.manifest["binarySha256"], self.manifest["files"]["bin/mcnd"])

    def test_every_production_input_matches_the_pinned_git_blob(self):
        # Read explicit commit objects, never substitute a mutable branch/HEAD.
        revision = subprocess.check_output(["git", "-C", str(self.source), "rev-parse", COMMIT + "^{commit}"], text=True).strip()
        self.assertEqual(COMMIT, revision)
        self.assertEqual(COMMIT, self.inventory["sourceRevision"])
        self.assertEqual({"CC": "gcc", "CGO_ENABLED": "1", "GOAMD64": "v1", "GOARCH": "amd64", "GOENV": "off", "GOFLAGS": "", "GOOS": "linux", "GOTOOLCHAIN": "local", "GOWORK": "off"}, self.inventory["buildPolicy"])
        names = list(self.inventory["files"])
        for name in names:
            path = PurePosixPath(name)
            self.assertFalse(path.is_absolute() or ".." in path.parts)
        request = "".join(COMMIT + ":" + name + "\n" for name in names).encode()
        result = subprocess.run(["git", "-C", str(self.source), "cat-file", "--batch"], input=request, stdout=subprocess.PIPE, check=True)
        stream = io.BytesIO(result.stdout)
        for name in names:
            header = stream.readline().decode().split()
            self.assertEqual("blob", header[1], name)
            value = stream.read(int(header[2]))
            self.assertEqual(b"\n", stream.read(1))
            self.assertEqual(self.inventory["files"][name], hashlib.sha256(value).hexdigest(), name)

    def test_actual_ci_elf_build_metadata_matches_packaged_metadata(self):
        # Extract only the already hash-verified binary to this new fixture root.
        target = self.root / "ci-bin/mcnd"
        self.assertFalse(target.parent.is_symlink() or target.is_symlink())
        target.parent.mkdir(exist_ok=True)
        expected = self.manifest["binarySha256"]
        if not target.exists():
            with tarfile.open(self.root / ARCHIVE, "r:gz") as archive, target.open("xb") as output:
                stream = archive.extractfile("bin/mcnd")
                for block in iter(lambda: stream.read(1024 * 1024), b""):
                    output.write(block)
            target.chmod(0o755)
        with target.open("rb") as stream:
            self.assertEqual(expected, digest_stream(stream))
        actual = subprocess.check_output(["go", "version", "-m", str(target)], text=True)
        # The leading filename varies; all embedded module/settings lines must match.
        self.assertEqual(self.build_info.splitlines()[1:], actual.splitlines()[1:])
        go_mod = subprocess.check_output(
            ["git", "-C", str(self.source), "show", COMMIT + ":network/go.mod"], text=True)
        expected_go = pinned_go_version(go_mod)
        self.assertTrue(actual.splitlines()[0].endswith(": " + expected_go))
        self.assertTrue(self.build_info.splitlines()[0].endswith(": " + expected_go))
        for setting in ["-buildmode=exe", "-compiler=gc", "-trimpath=true", "CGO_ENABLED=1", "GOARCH=amd64", "GOOS=linux", "GOAMD64=v1", "vcs.revision=" + COMMIT, "vcs.modified=false"]:
            self.assertIn("\tbuild\t" + setting, actual.splitlines())
        self.assertFalse(any(line.startswith("\tbuild\t-tags=") for line in actual.splitlines()))
        for prefix in ("\tbuild\t-race", "\tbuild\t-msan", "\tbuild\t-asan"):
            self.assertFalse(any(line.startswith(prefix) for line in actual.splitlines()))


if __name__ == "__main__":
    unittest.main()
