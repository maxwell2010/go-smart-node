"""Capture a scanner's completed stdout and bind it to an atomic sidecar manifest."""
from __future__ import annotations
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


def atomic_json(path: Path, value: dict) -> None:
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=path.parent, prefix='.capture-', delete=False) as stream:
            temporary = Path(stream.name)
            stream.write((json.dumps(value, sort_keys=True, indent=2) + '\n').encode())
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def capture(command: list[str], raw: Path, manifest: Path, source_commit: str,
            scope: str, go_command: list[str] | None = None) -> dict:
    if not command or any(not isinstance(x, str) or not x for x in command):
        raise ValueError('Scanner argv must contain nonempty strings')
    if not re.fullmatch(r'[0-9a-f]{40}', source_commit):
        raise ValueError('An exact lowercase 40-character source commit is required')
    if not isinstance(scope, str) or not scope.strip():
        raise ValueError('An explicit nonempty scan scope is required')
    raw, manifest = raw.resolve(), manifest.resolve()
    if raw == manifest:
        raise ValueError('Raw output and manifest must be different paths')
    raw.parent.mkdir(parents=True, exist_ok=True)
    manifest.parent.mkdir(parents=True, exist_ok=True)
    # Old completion evidence must not authorize a capture in progress or a failed launch.
    manifest.unlink(missing_ok=True)
    toolchain = subprocess.check_output(go_command or ['go', 'version'], text=True).strip()
    if not toolchain.startswith('go version '):
        raise ValueError('Cannot identify the actual Go command toolchain')
    started = datetime.datetime.now(datetime.timezone.utc).isoformat()
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=raw.parent, prefix='.scanner-', delete=False) as stream:
            temporary = Path(stream.name)
            process = subprocess.run(command, stdout=stream, check=False)
            stream.flush()
            os.fsync(stream.fileno())
        digest = hashlib.sha256()
        with temporary.open('rb') as stream:
            for block in iter(lambda: stream.read(1024 * 1024), b''):
                digest.update(block)
        result = {'schema_version': 1, 'completed': True,
                  'scanner_exit_code': process.returncode,
                  'raw_sha256': digest.hexdigest(), 'raw_bytes': temporary.stat().st_size,
                  'source_commit': source_commit, 'toolchain': toolchain,
                  'argv': command, 'scope': scope, 'started_utc': started,
                  'completed_utc': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        os.replace(temporary, raw)
        # Publication order matters: an interruption before this point cannot pass the gate.
        atomic_json(manifest, result)
        return result
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--raw', required=True, type=Path)
    parser.add_argument('--manifest', required=True, type=Path)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--scope', required=True)
    parser.add_argument('--go', default='go', help='Go executable used by the scan environment')
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ['--'] else args.command
    try:
        result = capture(command, args.raw, args.manifest, args.source_commit, args.scope,
                         [args.go, 'version'])
    except (OSError, ValueError, subprocess.SubprocessError) as exc:
        print(json.dumps({'completed': False, 'error': str(exc)}))
        return 2
    print(json.dumps(result, sort_keys=True))
    return 0 if result['scanner_exit_code'] == 0 else 2


if __name__ == '__main__':
    raise SystemExit(main())
