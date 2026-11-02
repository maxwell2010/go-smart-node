#!/usr/bin/env python3
"""Verify exact canonical files and the declared owned EVM patch bytes."""
import hashlib
import json
from pathlib import Path


def verify(root):
    root = root.resolve(strict=True)
    manifest = json.loads((root / "UPSTREAM.json").read_bytes())
    if (manifest["schemaVersion"] != 1 or manifest["upstreamModule"] != "github.com/cosmos/evm"
            or manifest["upstreamVersion"] != "v0.6.3"
            or manifest["upstreamCommit"] != "71c78b5072a248bae5273c34ad2f53d349799ccd"
            or manifest["upstreamModuleSum"] != "h1:1G8+iqyQWslZ2g7873h90Fcr3pL6RV24PgOS1wiK2SY="):
        raise RuntimeError("Unexpected owned EVM source identity")
    expected = dict(manifest["canonicalFiles"])
    for name, digest in manifest["modifiedFiles"].items():
        if name not in expected:
            raise RuntimeError("Modified file lacks canonical provenance: " + name)
        expected[name] = digest
    for name, digest in manifest["addedFiles"].items():
        if name in expected:
            raise RuntimeError("Added file overlaps canonical source: " + name)
        expected[name] = digest
    actual = {}
    for path in root.rglob("*"):
        if path.is_symlink():
            raise RuntimeError("Owned EVM source cannot contain symbolic links")
        if path.is_file() and path != root / "UPSTREAM.json":
            actual[path.relative_to(root).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    if actual != expected:
        missing = sorted(expected.keys() - actual.keys())
        extra = sorted(actual.keys() - expected.keys())
        changed = sorted(name for name in expected.keys() & actual.keys() if expected[name] != actual[name])
        raise RuntimeError(f"Owned EVM source differs: missing={missing}, extra={extra}, changed={changed}")
    return len(expected), len(manifest["modifiedFiles"])


if __name__ == "__main__":
    files, changes = verify(Path(__file__).resolve().parent.parent / "third_party/cosmos-evm")
    print(f"Owned EVM source verification PASS: {files} files, {changes} declared changes")
