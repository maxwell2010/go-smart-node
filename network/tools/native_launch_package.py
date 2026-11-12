"""Build and verify a signed Linux amd64 package for an isolated launch rehearsal."""

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile


BINARY = "bin/mcnd"
GENESIS = "genesis.json"
PROVENANCE = "provenance.json"
PUBLIC_KEY = "release-key.gpg"
MANIFEST = "launch-SHA256SUMS"
SIGNATURE = MANIFEST + ".asc"
PAYLOAD = (BINARY, GENESIS, PROVENANCE, PUBLIC_KEY)
LIMITS = {
    BINARY: 384 * 1024 * 1024,
    GENESIS: 64 * 1024 * 1024,
    PROVENANCE: 8 * 1024,
    PUBLIC_KEY: 64 * 1024,
    MANIFEST: 8 * 1024,
    SIGNATURE: 64 * 1024,
}
REVISION = re.compile(r"[0-9a-f]{40}")
FINGERPRINT = re.compile(r"[0-9A-F]{40}|[0-9A-F]{64}")
CHAIN_ID = re.compile(r"mintcandy_[1-9][0-9]*-[1-9][0-9]*")


def fingerprint(value):
    normalized = value.upper()
    if not FINGERPRINT.fullmatch(normalized):
        raise ValueError("an exact OpenPGP signer fingerprint is required")
    return normalized


def sha256(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def manifest_bytes(directory):
    return "".join(f"{sha256(directory / name)}  {name}\n" for name in sorted(PAYLOAD)).encode("ascii")


def provenance(directory, revision, signer):
    if not REVISION.fullmatch(revision):
        raise ValueError("an exact source commit is required")
    with (directory / BINARY).open("rb") as stream:
        header = stream.read(20)
    if header[:6] != b"\x7fELF\x02\x01" or header[18:20] != b"\x3e\x00":
        raise ValueError("launch package requires a Linux amd64 ELF binary")
    genesis = json.loads((directory / GENESIS).read_bytes())
    chain_id = genesis.get("chain_id") if isinstance(genesis, dict) else None
    if not isinstance(chain_id, str) or not CHAIN_ID.fullmatch(chain_id) or len(chain_id) > 50:
        raise ValueError("launch package requires a MintCandy chain ID")
    return {"schemaVersion": 1, "status": "test-launch-rehearsal", "platform": "linux/amd64",
            "sourceCommit": revision, "chainId": chain_id, "signerFingerprint": signer}


def build(binary, genesis, source_commit, output, signer_home, signer_fingerprint):
    signer = fingerprint(signer_fingerprint)
    output = output.absolute()
    gpg = ["gpg", "--no-options", "--batch", "--no-tty", "--homedir", str(signer_home.resolve(strict=True))]
    with tempfile.TemporaryDirectory(prefix="mcnd-launch-package-") as work:
        directory = Path(work)
        (directory / "bin").mkdir()
        for name, source in ((BINARY, binary), (GENESIS, genesis)):
            if not source.is_file() or not 0 < source.stat().st_size <= LIMITS[name]:
                raise ValueError(f"invalid launch package input {name}")
            shutil.copyfile(source, directory / name)
        info = provenance(directory, source_commit, signer)
        (directory / PROVENANCE).write_bytes((json.dumps(info, sort_keys=True, indent=2) + "\n").encode())
        public_key = subprocess.check_output([*gpg, "--export", signer], timeout=30)
        if not 0 < len(public_key) <= LIMITS[PUBLIC_KEY]:
            raise ValueError("invalid OpenPGP public key export")
        (directory / PUBLIC_KEY).write_bytes(public_key)
        (directory / MANIFEST).write_bytes(manifest_bytes(directory))
        subprocess.run([*gpg, "--local-user", signer + "!", "--armor", "--detach-sign", "--output",
                        str(directory / SIGNATURE), str(directory / MANIFEST)], check=True, timeout=30)
        with output.open("xb") as stream, tarfile.open(fileobj=stream, mode="w:gz", format=tarfile.USTAR_FORMAT) as archive:
            for name in sorted(LIMITS):
                path = directory / name
                member = tarfile.TarInfo(name)
                member.size = path.stat().st_size
                member.mode = 0o700 if name == BINARY else 0o600
                with path.open("rb") as source:
                    archive.addfile(member, source)


def extract(archive, directory):
    """Read only the expected USTAR files, with bounded sizes and no links."""
    directory.mkdir(mode=0o700)
    (directory / "bin").mkdir(mode=0o700)
    remaining = set(LIMITS)
    with gzip.open(archive, "rb") as stream:
        while remaining:
            header = stream.read(tarfile.BLOCKSIZE)
            if len(header) != tarfile.BLOCKSIZE or header[257:263] != b"ustar\0":
                raise ValueError("invalid launch archive header")
            member = tarfile.TarInfo.frombuf(header, "ascii", "strict")
            if member.name not in remaining or member.type != tarfile.REGTYPE:
                raise ValueError("unexpected or repeated launch archive member")
            if not 0 < member.size <= LIMITS[member.name]:
                raise ValueError("invalid launch archive member size")
            remaining.remove(member.name)
            path = directory / member.name
            with path.open("xb") as destination:
                path.chmod(0o600)
                left = member.size
                while left:
                    chunk = stream.read(min(left, 1024 * 1024))
                    if not chunk:
                        raise ValueError("truncated launch archive member")
                    destination.write(chunk)
                    left -= len(chunk)
            padding = (-member.size) % tarfile.BLOCKSIZE
            if stream.read(padding) != b"\0" * padding:
                raise ValueError("invalid launch archive padding")
        tail = stream.read(tarfile.RECORDSIZE + 1)
        if not 2 * tarfile.BLOCKSIZE <= len(tail) <= tarfile.RECORDSIZE or any(tail):
            raise ValueError("unexpected content after launch archive members")


def verify_signature(directory, keyring, signer, verification_home):
    checked = subprocess.run(["gpgv", "--homedir", str(verification_home), "--keyring", str(keyring),
                              "--status-fd", "1", str(directory / SIGNATURE), str(directory / MANIFEST)],
                             capture_output=True, text=True, timeout=30)
    valid = re.findall(r"^\[GNUPG:\] VALIDSIG (\S+) ", checked.stdout, re.MULTILINE)
    if checked.returncode or valid != [signer]:
        raise ValueError("launch manifest OpenPGP signature or signer fingerprint differs")


def verify(archive, trusted_keyring, signer_fingerprint, output_dir):
    signer = fingerprint(signer_fingerprint)
    trusted_keyring = trusted_keyring.resolve(strict=True)
    output_dir = output_dir.absolute()
    if os.path.lexists(output_dir):
        raise ValueError("launch verification output directory already exists")
    if not archive.is_file() or not 0 < archive.stat().st_size <= sum(LIMITS.values()) + 1024 * 1024:
        raise ValueError("invalid launch archive size")
    with tempfile.TemporaryDirectory(prefix="mcnd-launch-verify-", dir=output_dir.parent) as work:
        verification_home = Path(work)
        directory = verification_home / "package"
        extract(archive, directory)
        verify_signature(directory, trusted_keyring, signer, verification_home)
        if (directory / MANIFEST).read_bytes() != manifest_bytes(directory):
            raise ValueError("launch package SHA256 payload checksum differs")
        verify_signature(directory, directory / PUBLIC_KEY, signer, verification_home)
        info = json.loads((directory / PROVENANCE).read_bytes())
        if not isinstance(info, dict) or info != provenance(directory, info.get("sourceCommit", ""), signer):
            raise ValueError("launch package provenance differs")
        result = {**info, "binarySha256": sha256(directory / BINARY), "genesisSha256": sha256(directory / GENESIS),
                  "manifestSha256": sha256(directory / MANIFEST), "archiveSha256": sha256(archive)}
        (directory / BINARY).chmod(0o700)
        directory.rename(output_dir)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    builder = commands.add_parser("build")
    for name in ("binary", "genesis", "output", "signer-home"):
        builder.add_argument("--" + name, required=True, type=Path)
    builder.add_argument("--source-commit", required=True)
    builder.add_argument("--signer-fingerprint", required=True)
    verifier = commands.add_parser("verify")
    for name in ("archive", "trusted-keyring", "output-dir"):
        verifier.add_argument("--" + name, required=True, type=Path)
    verifier.add_argument("--signer-fingerprint", required=True)
    args = parser.parse_args()
    try:
        if args.command == "build":
            build(args.binary, args.genesis, args.source_commit, args.output, args.signer_home, args.signer_fingerprint)
        else:
            print(json.dumps(verify(args.archive, args.trusted_keyring, args.signer_fingerprint, args.output_dir), sort_keys=True))
    except (ValueError, TypeError, OSError, EOFError, tarfile.TarError, subprocess.SubprocessError) as error:
        parser.exit(1, f"launch package: {error}\n")


if __name__ == "__main__":
    main()
