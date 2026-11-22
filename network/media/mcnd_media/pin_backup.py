"""Pin verified media in a dedicated offline Kubo repository."""

import argparse
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import zipfile

from .ipfs import CHUNK_SIZE, RAW_BLOCK_LIMIT, matches_cid
from .store import MAX_BLOB, canonical, regular_read, require, restore


def pin_backup(archive, expected_sha256, binary, repository):
    require(binary.is_absolute() and binary.is_file(), 'absolute Kubo binary path required')
    require(repository.is_absolute() and not repository.is_symlink() and
            (repository / 'config').is_file(), 'absolute initialized Kubo repository required')
    environment = dict(os.environ, IPFS_PATH=str(repository))

    def ipfs(*arguments, data=None):
        result = subprocess.run([str(binary), '--offline', *arguments], input=data, env=environment,
                                capture_output=True, timeout=45)
        if result.returncode != 0:
            raise ValueError(f'Kubo {arguments[0]} failed: {result.stderr.decode(errors="replace")[:400]}')
        return result.stdout

    file_options = ('--quieter', '--cid-version=1', '--hash=sha2-256', '--raw-leaves',
                    f'--chunker=size-{CHUNK_SIZE}', '--max-file-links=174', '--trickle=false',
                    '--inline=false', '--wrap-with-directory=false', '--preserve-mode=false', '--preserve-mtime=false')
    entries = []
    legacy_http_only = []
    with tempfile.TemporaryDirectory(prefix='pin-media-') as scratch:
        store = restore(archive, Path(scratch) / 'verified', expected_sha256)
        try:
            _, names = store.verify()
            for name in sorted(names):
                path = store.root / 'objects' / name
                blob = regular_read(path, MAX_BLOB)
                if name.startswith('bafkrei') and len(blob) > RAW_BLOCK_LIMIT:
                    legacy_http_only.append(name)
                    continue
                if name.startswith('bafybei'):
                    calculated = ipfs('add', *file_options, '--only-hash', '--pin=false', str(path)).decode().strip()
                    require(calculated == name, 'Kubo UnixFS root differs from the committed CID')
                entries.append((name, path))
            for name, path in entries:
                data = regular_read(path, MAX_BLOB)
                require(matches_cid(name, data), 'media changed after verification')
                if name.startswith('bafybei'):
                    added = ipfs('add', *file_options, '--pin=true', str(path))
                    received = ipfs('cat', name)
                else:
                    added = ipfs('block', 'put', '--cid-codec=raw', '--mhtype=sha2-256', '--pin', data=data)
                    received = ipfs('block', 'get', name)
                require(added.decode().strip() == name and received == data and
                        ipfs('pin', 'ls', '--type=recursive', '--quiet', name).decode().strip() == name,
                        'Kubo pin or local byte verification differs')
        finally:
            store.close()
    return {'backupSha256': expected_sha256, 'pinned': [name for name, _ in entries],
            'legacyRawHttpOnly': legacy_http_only}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--backup', required=True, type=Path)
    parser.add_argument('--backup-sha256', required=True)
    parser.add_argument('--ipfs', required=True, type=Path)
    parser.add_argument('--repo', required=True, type=Path)
    args = parser.parse_args()
    try:
        report = pin_backup(args.backup, args.backup_sha256, args.ipfs, args.repo)
    except (OSError, ValueError, KeyError, TypeError, sqlite3.Error, zipfile.BadZipFile, subprocess.TimeoutExpired) as error:
        parser.exit(1, f'IPFS media: {error}\n')
    print(canonical(report).decode())


if __name__ == '__main__':
    main()
