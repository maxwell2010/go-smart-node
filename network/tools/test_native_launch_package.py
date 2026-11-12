import io
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest

from native_launch_package import BINARY, GENESIS, MANIFEST, build, sha256, verify


@unittest.skipUnless(sys.platform == "linux" and shutil.which("gpg") and shutil.which("gpgv"),
                     "requires Linux and OpenPGP tools")
class NativeLaunchPackageTest(unittest.TestCase):
    def setUp(self):
        work = tempfile.TemporaryDirectory(prefix="mcnd-launch-package-test-")
        self.addCleanup(work.cleanup)
        self.root = Path(work.name)
        signer_home = self.root / "signer"
        signer_home.mkdir(mode=0o700)
        gpgconf = shutil.which("gpgconf")
        if gpgconf:
            self.addCleanup(subprocess.run, [gpgconf, "--homedir", str(signer_home), "--kill", "gpg-agent"],
                            capture_output=True, timeout=10)
        gpg = ["gpg", "--no-options", "--batch", "--homedir", str(signer_home)]
        subprocess.run([*gpg, "--pinentry-mode", "loopback", "--passphrase", "", "--quick-generate-key",
                        "MCND archive test <mcnd-package@example.invalid>", "ed25519", "sign", "1d"],
                       check=True, capture_output=True, timeout=30)
        keys = subprocess.check_output([*gpg, "--with-colons", "--fingerprint"], text=True, timeout=30)
        self.signer = next(line.split(":")[9] for line in keys.splitlines() if line.startswith("fpr:"))
        self.keyring = self.root / "trusted.gpg"
        self.keyring.write_bytes(subprocess.check_output([*gpg, "--export", self.signer], timeout=30))
        self.binary = self.root / "mcnd"
        self.binary.write_bytes(b"\x7fELF\x02\x01" + b"\0" * 12 + b"\x3e\x00")
        self.genesis = self.root / GENESIS
        self.genesis.write_bytes(b'{"chain_id":"mintcandy_990012-1"}\n')
        self.archive = self.root / "mcnd-launch.tar.gz"
        build(self.binary, self.genesis, "a" * 40, self.archive, signer_home, self.signer)

    def test_signature_and_checksums_reject_independent_changes(self):
        extracted = self.root / "verified"
        report = verify(self.archive, self.keyring, self.signer.lower(), extracted)
        self.assertEqual(report["signerFingerprint"], self.signer)
        self.assertEqual(report["archiveSha256"], sha256(self.archive))
        self.assertEqual(report["binarySha256"], sha256(self.binary))
        self.assertEqual(report["genesisSha256"], sha256(self.genesis))
        self.assertEqual(report["manifestSha256"], sha256(extracted / MANIFEST))
        self.assertEqual((extracted / BINARY).read_bytes(), self.binary.read_bytes())
        with self.assertRaisesRegex(ValueError, "signature or signer fingerprint"):
            verify(self.archive, self.keyring, "0" * 40, self.root / "wrong-signer")
        for name, reason in ((GENESIS, "SHA256 payload checksum"), (MANIFEST, "OpenPGP signature")):
            with self.subTest(member=name):
                tampered = self.root / (name + ".tar.gz")
                with tarfile.open(self.archive, "r:gz") as source, \
                        tarfile.open(tampered, "w:gz", format=tarfile.USTAR_FORMAT) as destination:
                    for member in source:
                        with source.extractfile(member) as stream:
                            content = stream.read()
                        if member.name == name:
                            content += b"\n"
                            member.size = len(content)
                        destination.addfile(member, io.BytesIO(content))
                unverified = self.root / (name + ".unverified")
                with self.assertRaisesRegex(ValueError, reason):
                    verify(tampered, self.keyring, self.signer, unverified)
                self.assertFalse(unverified.exists())


if __name__ == "__main__":
    unittest.main()
