"""Install only an attested, pinned Linux amd64 software binary at a new path."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, build_opener

REPOSITORY = "maxwell2010/go-smart-node"
SIGNER_WORKFLOW = REPOSITORY + "/.github/workflows/software-release.yml"
MAX_ARCHIVE = 512 << 20
MAX_SOURCE = 2 << 20
VERIFIERS = ("verify_software_package.py", "binary_provenance.py", "install_mcnd.py", "source_inventory.py")


def https_url(value):
    parsed = urlsplit(value)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username is not None or parsed.password is not None or parsed.fragment:
        raise ValueError("a direct HTTPS URL without credentials or fragment is required")
    return value


class HTTPSRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        https_url(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def download(url, destination, maximum):
    https_url(url)
    with build_opener(HTTPSRedirect()).open(url, timeout=45) as source, Path(destination).open("xb") as output:
        https_url(source.url)
        declared = source.headers.get("Content-Length")
        if declared is not None and (not declared.isdecimal() or int(declared) > maximum):
            raise ValueError("download exceeds its size bound")
        total = 0
        while chunk := source.read(1 << 20):
            total += len(chunk)
            if total > maximum:
                raise ValueError("download exceeds its size bound")
            output.write(chunk)
        if total == 0:
            raise ValueError("empty download")


def copy_local_archive(source, destination, maximum=MAX_ARCHIVE):
    # A local source can change after stat. Bound the actual read/write stream,
    # and reject nonregular descriptors without blocking on a replaced FIFO.
    fd = os.open(source, os.O_RDONLY | getattr(os, "O_NONBLOCK", 0))
    with os.fdopen(fd, "rb") as stream, Path(destination).open("xb") as output:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or not 0 < info.st_size <= maximum:
            raise ValueError("local archive exceeds its size bound or is not regular")
        total = 0
        while chunk := stream.read(min(1 << 20, maximum - total + 1)):
            total += len(chunk)
            if total > maximum:
                raise ValueError("local archive exceeds its size bound")
            output.write(chunk)


def archive_sha256(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def verify_contents(root, archive, source_sha, release_version):
    tools = root / "source" / "network" / "tools"
    tools.mkdir(parents=True)
    source_url = "https://raw.githubusercontent.com/" + REPOSITORY + "/" + source_sha + "/network/"
    for name in VERIFIERS:
        download(source_url + "tools/" + name, tools / name, MAX_SOURCE)
    download(source_url + "go.mod", tools.parent / "go.mod", MAX_SOURCE)
    # Isolated Python ignores user site packages, PYTHONPATH and the working
    # directory. Only the exact pinned verifier directory is added explicitly.
    runner = 'import runpy,sys; p=sys.argv.pop(1); sys.path.insert(0,p); sys.argv[0]=p+"/verify_software_package.py"; runpy.run_path(sys.argv[0],run_name="__main__")'
    result = subprocess.run([sys.executable, "-I", "-B", "-c", runner, str(tools),
                             "--archive", str(archive), "--revision", source_sha,
                             "--release-version", release_version], check=True, capture_output=True, text=True, timeout=180)
    return json.loads(result.stdout)


def copy_binary(archive, destination, expected_sha):
    # The caller has already checked the entire bounded archive and publisher.
    # Extract only this one regular member; never call extract()/extractall().
    with tarfile.open(archive, "r:gz") as package:
        member = package.getmember("bin/mcnd")
        if not member.isfile() or not 0 < member.size <= 384 << 20:
            raise ValueError("bin/mcnd must be a bounded regular file")
        destination.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        created = os.fstat(fd)
        try:
            with os.fdopen(fd, "wb") as output, package.extractfile(member) as source:
                digest = hashlib.sha256()
                while chunk := source.read(1 << 20):
                    digest.update(chunk)
                    output.write(chunk)
                if digest.hexdigest() != expected_sha:
                    raise ValueError("installed binary SHA256 differs")
                output.flush()
                os.fsync(output.fileno())
                os.fchmod(output.fileno(), 0o755)
        except BaseException:
            # Delete only the new inode created by this installer. Never
            # remove a file another process substituted at the same name.
            try:
                current = destination.lstat()
                if (current.st_dev, current.st_ino) == (created.st_dev, created.st_ino):
                    destination.unlink()
            except FileNotFoundError:
                pass
            raise


def install(*, source_sha, source_ref, release_version, archive_sha, destination, archive_url=None, archive=None, bundle=None):
    if sys.version_info < (3, 11):
        raise ValueError("Python 3.11 or later is required")
    if platform.system() != "Linux" or platform.machine() != "x86_64":
        raise ValueError("only Linux amd64 is validated")
    if not re.fullmatch(r"[0-9a-f]{40}", source_sha) or not re.fullmatch(r"[0-9a-f]{64}", archive_sha):
        raise ValueError("full lowercase source SHA and archive SHA256 are required")
    if len(release_version) > 64 or not re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", release_version):
        raise ValueError("release version must be canonical X.Y.Z")
    if not re.fullmatch(r"refs/heads/[A-Za-z0-9._/-]{1,240}", source_ref) or any(value in source_ref for value in ("..", "//")):
        raise ValueError("an exact refs/heads/... source ref is required")
    destination = Path(destination)
    if not destination.is_absolute() or destination.exists() or destination.is_symlink():
        raise ValueError("choose a new absolute versioned binary path")
    if bool(archive_url) == bool(archive):
        raise ValueError("provide either a direct HTTPS archive URL or one local archive")
    if archive_url:
        https_url(archive_url)
    if not shutil.which("gh"):
        raise ValueError("GitHub CLI with attestation verification is required")
    if bundle is not None:
        bundle = Path(bundle)
        if not bundle.is_absolute() or not bundle.is_file() or not 0 < bundle.stat().st_size <= 16 << 20:
            raise ValueError("bundle must be an existing absolute file up to 16 MiB")
    with tempfile.TemporaryDirectory(prefix="mcnd-software-") as directory:
        root = Path(directory)
        downloaded = root / "package.tar.gz"
        if archive_url:
            download(archive_url, downloaded, MAX_ARCHIVE)
        else:
            archive = Path(archive)
            if not archive.is_absolute() or not archive.is_file() or not 0 < archive.stat().st_size <= MAX_ARCHIVE:
                raise ValueError("local archive must be an absolute file up to 512 MiB")
            copy_local_archive(archive, downloaded)
        if archive_sha256(downloaded) != archive_sha:
            raise ValueError("archive SHA256 differs from the trusted release value")
        command = ["gh", "attestation", "verify", str(downloaded), "--repo", REPOSITORY,
                   "--signer-workflow", SIGNER_WORKFLOW, "--source-ref", source_ref,
                   "--source-digest", source_sha, "--signer-digest", source_sha, "--deny-self-hosted-runners"]
        if bundle is not None:
            command.extend(["--bundle", str(bundle)])
        subprocess.run(command, check=True, timeout=180, stdout=sys.stderr)
        verified = verify_contents(root, downloaded, source_sha, release_version)
        if verified.get("sourceRevision") != source_sha or verified.get("releaseVersion") != release_version or verified.get("archiveSha256") != archive_sha or verified.get("includesGenesis") is not False or verified.get("networkLaunchApproved") is not False:
            raise ValueError("verified software package identity differs")
        copy_binary(downloaded, destination, verified["binarySha256"])
    return {"binary": str(destination), "sourceRevision": source_sha, "releaseVersion": release_version,
            "binarySha256": verified["binarySha256"], "includesGenesis": False, "networkLaunchApproved": False}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("source-sha", "source-ref", "release-version", "archive-sha", "destination"):
        parser.add_argument("--" + name, required=True)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--archive-url")
    source.add_argument("--archive", type=Path)
    parser.add_argument("--bundle", type=Path)
    args = parser.parse_args()
    try:
        result = install(source_sha=args.source_sha, source_ref=args.source_ref, release_version=args.release_version,
                         archive_sha=args.archive_sha, destination=args.destination, archive_url=args.archive_url,
                         archive=args.archive, bundle=args.bundle)
        print(json.dumps(result, sort_keys=True))
    except (ValueError, OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired, tarfile.TarError, KeyError) as error:
        parser.exit(1, "software install failed: " + str(error) + "\n")
