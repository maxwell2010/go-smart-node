#!/usr/bin/env python3
"""Verify canonical Pion v3 identities and the actual default mcnd imports."""
import hashlib
import json
from pathlib import Path
import subprocess


PION_MODULES = {
    "github.com/pion/stun/v3": {
        "Version": "v3.1.7",
        "Sum": "h1:uRXMTlGLf89WgItGNyZ6aR5jMTX0NBbybXADpQCzn+E=",
        "GoModSum": "h1:Nq77RW4aRrSNrltf2ksUJLjxWeipj4lnlgdsYIxC8g8=",
    },
    "github.com/pion/dtls/v3": {
        "Version": "v3.1.15",
        "Sum": "h1:okWk5LNwU21DfhvG8z4wm45knHVSPEdkihwp6BxfQcc=",
        "GoModSum": "h1:iKFQNYrjsN2TiA2YKKMqB9MOZaFpjFULBI/A4sW0eyc=",
    },
}
LEGACY_PREFIXES = ("github.com/pion/stun/v2", "github.com/pion/dtls/v2")


def records(text):
    decoder = json.JSONDecoder()
    offset = 0
    while offset < len(text):
        while offset < len(text) and text[offset].isspace():
            offset += 1
        if offset == len(text):
            break
        value, offset = decoder.raw_decode(text, offset)
        yield value


def verify_transport(module):
    module = Path(module).resolve(strict=True)
    inputs = {name: (module / name).read_bytes() for name in ("go.mod", "go.sum")}
    try:
        output = subprocess.check_output(["go", "list", "-mod=readonly", "-m", "-json", "all"], cwd=module, text=True)
        modules = {entry["Path"]: entry for entry in records(output)}
        for name, entry in modules.items():
            if name.startswith(("github.com/pion/stun/", "github.com/pion/dtls/")) and entry.get("Replace"):
                raise RuntimeError("Pion alias/local replacements are not allowed: " + name)
        for name, identity in PION_MODULES.items():
            entry = modules.get(name, {})
            if any(entry.get(key) != value for key, value in identity.items()):
                raise RuntimeError("Unexpected canonical Pion version/checksum: " + name)
        # h1 metadata alone does not check the downloaded directory contents.
        integrity = subprocess.check_output(["go", "mod", "verify"], cwd=module, text=True).strip()
        dependencies = set(subprocess.check_output(["go", "list", "-mod=readonly", "-tags=", "-deps", "./cmd/mcnd"], cwd=module, text=True).splitlines())
        legacy = sorted(name for name in dependencies if name.startswith(LEGACY_PREFIXES))
        if legacy:
            raise RuntimeError("Legacy Pion packages remain in mcnd imports: " + ", ".join(legacy))
        if not set(PION_MODULES).issubset(dependencies):
            raise RuntimeError("Actual mcnd imports must include both canonical Pion v3 packages")
        return {
            "modules": PION_MODULES,
            "module_integrity": integrity,
            "compiled_legacy_imports": legacy,
            "legacy_mvs_inventory": {name: entry.get("Version") for name, entry in modules.items() if name.startswith(LEGACY_PREFIXES)},
            "scope": "Default mcnd package imports and canonical source integrity; no advisory suppression or runtime exposure assertion.",
            "input_sha256": {name: hashlib.sha256(value).hexdigest() for name, value in inputs.items()},
        }
    finally:
        if any((module / name).read_bytes() != value for name, value in inputs.items()):
            raise RuntimeError("Module files changed during readonly Pion verification")


if __name__ == "__main__":
    print(json.dumps(verify_transport(Path(__file__).resolve().parent.parent), sort_keys=True))
