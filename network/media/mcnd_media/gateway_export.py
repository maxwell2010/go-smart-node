"""Prepare a static, content-addressed gateway directory from a trusted backup."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import zipfile

from .store import MAX_BLOB, canonical, cid, regular_read, require, restore


PNG_HEADER = b'\x89PNG\r\n\x1a\n'


def stage_objects(store, public):
    rows, names = store.verify()
    images = {row['image'] for row in rows}
    metadata = {row['metadata'] for row in rows}
    require(not images & metadata, 'one CID cannot be exported with two media types')
    folder = public / 'ipfs'
    folder.mkdir(parents=True)
    objects = []
    for name in sorted(names):
        is_image = name in images
        raw = regular_read(store.root / 'objects' / name, MAX_BLOB if is_image else 64 << 10)
        require(cid(raw) == name, 'object changed after store verification')
        content_type = 'image/png' if is_image else 'application/json'
        if is_image:
            require(raw.startswith(PNG_HEADER), 'image must be a normalized PNG')
        extension = 'png' if is_image else 'json'
        (folder / f'{name}.{extension}').write_bytes(raw)
        objects.append({'cid': name, 'path': f'/ipfs/{name}', 'contentType': content_type,
                        'sha256': hashlib.sha256(raw).hexdigest(), 'sizeBytes': len(raw)})
    return objects


def export_backup(archive, expected_sha256, destination):
    destination = Path(destination)
    require(destination.is_absolute() and '..' not in destination.parts and
            all(not path.is_symlink() for path in destination.parents), 'absolute, non-symlink gateway path required')
    require(not os.path.lexists(destination), 'gateway destination must be new')
    with tempfile.TemporaryDirectory(prefix='gateway-export-', dir=destination.parent) as scratch:
        root = Path(scratch)
        store = restore(archive, root / 'restored', expected_sha256)
        try:
            public = root / 'public'
            objects = stage_objects(store, public)
        finally:
            store.close()
        report = {'schemaVersion': 1, 'scope': 'public-media-gateway', 'backupSha256': expected_sha256,
                  'objectCount': len(objects), 'objects': objects}
        manifest = root / 'EXPORT.json'
        manifest.write_bytes(canonical(report) + b'\n')
        # The public root becomes available only after all object checks succeed.
        # mkdir reserves the fresh destination without replacing an existing path.
        destination.mkdir()
        manifest.rename(destination / 'EXPORT.json')
        public.rename(destination / 'public')
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--backup', required=True, type=Path)
    parser.add_argument('--backup-sha256', required=True)
    parser.add_argument('--destination', required=True, type=Path)
    args = parser.parse_args()
    try:
        report = export_backup(args.backup, args.backup_sha256, args.destination)
    except (OSError, ValueError, KeyError, TypeError, sqlite3.Error, zipfile.BadZipFile) as error:
        parser.exit(1, f'media gateway: {error}\n')
    print(json.dumps({'objects': report['objectCount'], 'documentRoot': str(args.destination / 'public')}, sort_keys=True))


if __name__ == '__main__':
    main()
