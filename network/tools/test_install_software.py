import hashlib
from contextlib import nullcontext
import io
import json
from pathlib import Path
import subprocess
import stat
from types import SimpleNamespace
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import install_software as installer


class InstallSoftwareTests(unittest.TestCase):
    def package(self, root):
        archive = root / "software.tar.gz"
        binary = b"isolated binary fixture"
        with tarfile.open(archive, "w:gz") as tar:
            for name, data in (("bin/mcnd", binary), ("do-not-extract.txt", b"other package data")):
                info = tarfile.TarInfo(name)
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
        args = dict(source_sha="a" * 40, source_ref="refs/heads/release/1.0.0", release_version="1.0.0",
                    archive_sha=installer.archive_sha256(archive), destination=root / "new-version" / "mcnd", archive=archive)
        verified = dict(sourceRevision=args["source_sha"], releaseVersion="1.0.0", archiveSha256=args["archive_sha"],
                        binarySha256=hashlib.sha256(binary).hexdigest(), includesGenesis=False, networkLaunchApproved=False)
        return args, verified, binary

    def supported(self):
        return patch.multiple(installer.platform, system=lambda: "Linux", machine=lambda: "x86_64")

    def test_only_new_binary_is_installed_after_strict_oidc_and_package_verification(self):
        with tempfile.TemporaryDirectory() as temporary, self.supported(), patch.object(installer.shutil, "which", return_value="gh"):
            root = Path(temporary)
            args, verified, binary = self.package(root)
            calls, workspaces = [], []
            def verify(workspace, archive, sha, version):
                self.assertEqual(len(calls), 1, "OIDC must succeed before package parsing")
                workspaces.append(workspace)
                return verified
            def run(command, **options):
                self.assertEqual(command[:3], ["gh", "attestation", "verify"])
                self.assertEqual(command[command.index("--source-digest") + 1], "a" * 40)
                self.assertEqual(command[command.index("--signer-digest") + 1], "a" * 40)
                self.assertEqual(command[command.index("--signer-workflow") + 1], installer.SIGNER_WORKFLOW)
                self.assertIn("--deny-self-hosted-runners", command)
                self.assertTrue(options["check"])
                self.assertEqual(options["timeout"], 180)
                calls.append(command)
            requested_modes = []
            def windows_test_fchmod(fd, mode):
                requested_modes.append(mode)
            permissions = nullcontext() if hasattr(installer.os, "fchmod") else patch.object(installer.os, "fchmod", side_effect=windows_test_fchmod, create=True)
            with permissions, patch.object(installer.subprocess, "run", side_effect=run), patch.object(installer, "verify_contents", side_effect=verify):
                result = installer.install(**args)
            self.assertEqual(args["destination"].read_bytes(), binary)
            if installer.os.name == "posix":
                self.assertEqual(args["destination"].stat().st_mode & 0o777, 0o755)
            else:
                self.assertEqual(requested_modes, [0o755], "Windows fixture records the requested Linux permission change")
            self.assertFalse((args["destination"].parent / "do-not-extract.txt").exists())
            self.assertFalse((root / ".mcnd").exists())
            self.assertFalse(result["networkLaunchApproved"])
            self.assertTrue(all(not workspace.exists() for workspace in workspaces))

    def test_bad_archive_or_oidc_never_creates_destination(self):
        with tempfile.TemporaryDirectory() as temporary, self.supported(), patch.object(installer.shutil, "which", return_value="gh"):
            args, verified, _ = self.package(Path(temporary))
            with patch.object(installer.subprocess, "run") as run, patch.object(installer, "verify_contents") as verify:
                with self.assertRaisesRegex(ValueError, "archive SHA256"):
                    installer.install(**dict(args, archive_sha="0" * 64))
                run.assert_not_called()
                verify.assert_not_called()
            with patch.object(installer.subprocess, "run", side_effect=subprocess.CalledProcessError(1, ["gh"])), patch.object(installer, "verify_contents") as verify:
                with self.assertRaises(subprocess.CalledProcessError):
                    installer.install(**args)
                verify.assert_not_called()
            self.assertFalse(args["destination"].parent.exists())

    def test_mainnet_install_rejects_another_native_denom(self):
        with tempfile.TemporaryDirectory() as temporary, self.supported(), patch.object(installer.shutil, "which", return_value="gh"):
            args, verified, _ = self.package(Path(temporary))
            with patch.object(installer.subprocess, "run"), patch.object(installer, "verify_contents", return_value=dict(verified, nativeDenom="tmsp")) as verify:
                with self.assertRaisesRegex(ValueError, "native denomination differs"):
                    installer.install(**args, native_denom="msp")
                self.assertEqual(verify.call_args.kwargs["native_denom"], "msp")
            self.assertFalse(args["destination"].exists())

    def test_package_identity_and_existing_target_fail_closed(self):
        with tempfile.TemporaryDirectory() as temporary, self.supported(), patch.object(installer.shutil, "which", return_value="gh"):
            args, verified, _ = self.package(Path(temporary))
            with patch.object(installer.subprocess, "run"), patch.object(installer, "verify_contents", return_value=dict(verified, sourceRevision="b" * 40)):
                with self.assertRaisesRegex(ValueError, "identity differs"):
                    installer.install(**args)
            self.assertFalse(args["destination"].exists())
            args["destination"].parent.mkdir()
            args["destination"].write_bytes(b"existing release")
            with self.assertRaisesRegex(ValueError, "new absolute"):
                installer.install(**args)
            self.assertEqual(args["destination"].read_bytes(), b"existing release")
            with self.assertRaises(FileExistsError):
                installer.copy_binary(args["archive"], args["destination"], verified["binarySha256"])
            self.assertEqual(args["destination"].read_bytes(), b"existing release")

    def test_exclusive_copy_removes_only_its_failed_new_file(self):
        with tempfile.TemporaryDirectory() as temporary:
            args, _, _ = self.package(Path(temporary))
            with self.assertRaisesRegex(ValueError, "binary SHA256"):
                installer.copy_binary(args["archive"], args["destination"], "0" * 64)
            self.assertFalse(args["destination"].exists())

    def test_verifier_downloads_are_source_pinned_and_python_is_isolated(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            args, verified, _ = self.package(root)
            urls = []
            def download(url, target, maximum):
                self.assertIn("/" + "a" * 40 + "/network/", url)
                self.assertEqual(maximum, installer.MAX_SOURCE)
                urls.append(url)
                target.write_bytes(b"pinned test verifier")
            def run(command, **options):
                self.assertEqual(command[1:3], ["-I", "-B"])
                self.assertIn("--revision", command)
                self.assertTrue(options["capture_output"])
                return subprocess.CompletedProcess(command, 0, stdout=json.dumps(verified))
            with patch.object(installer, "download", side_effect=download), patch.object(installer.subprocess, "run", side_effect=run):
                self.assertEqual(installer.verify_contents(root, args["archive"], "a" * 40, "1.0.0"), verified)
            self.assertEqual(len(urls), 5)

    def test_growing_local_archive_cannot_exceed_temporary_disk_bound(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source, destination = root / "source", root / "downloaded"
            source.write_bytes(b"grew after stat")
            initial = SimpleNamespace(st_size=1, st_mode=stat.S_IFREG)
            with patch.object(installer.os, "fstat", return_value=initial):
                with self.assertRaisesRegex(ValueError, "size bound"):
                    installer.copy_local_archive(source, destination, maximum=2)
            self.assertLessEqual(destination.stat().st_size, 2)

    def test_https_and_size_limits(self):
        for url in ("http://example.org/a", "file:///tmp/a", "https://user:secret@example.org/a", "https://example.org/a#fragment"):
            with self.assertRaises(ValueError):
                installer.https_url(url)
        with self.assertRaises(ValueError):
            installer.HTTPSRedirect().redirect_request(None, None, 302, "", {}, "http://example.org/a")
        class Response(io.BytesIO):
            url = "https://example.org/a"
            headers = {}
        class Opener:
            def open(self, url, timeout):
                return Response(b"too large")
        with tempfile.TemporaryDirectory() as temporary, patch.object(installer, "build_opener", return_value=Opener()):
            with self.assertRaisesRegex(ValueError, "size bound"):
                installer.download("https://example.org/a", Path(temporary) / "archive", 2)


if __name__ == "__main__":
    unittest.main()
