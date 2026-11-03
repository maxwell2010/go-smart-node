#!/usr/bin/env python3
"""Verify the exact Cosmos geth base and complete upstream coordinate fix."""
import hashlib
import json
from pathlib import Path

from verify_owned_evm import verify


GETH_IDENTITY = {
    "schemaVersion": 1,
    "upstreamModule": "github.com/cosmos/go-ethereum",
    "upstreamVersion": "v1.16.2-cosmos-1",
    "upstreamCommit": "9a3ffad54df1d51db495036a29bf5799be36b4e2",
    "upstreamModuleSum": "h1:QIaIS6HIdPSBdTvpFhxswhMLUJgcr4irbd2o9ZKldAI=",
    "upstreamZipSha256": "70a65b51ae33adf55f6a14de112caebdde2e4d2c69a42472359392ef02797713",
    "patchLabel": "v1.16.2-cosmos-1-mintcandy.2",
    "upstreamFixCommit": "895a8597cb16c02203e38707ed2d1da5c500fe60",
    "upstreamFixPatchSha256": "bfcb148b6dbab007f3a583019590ce43e84d76ee253fc9fdb89b20121e9800e1",
}
CANONICAL_FILES_SHA256 = "3de83ab96326b44a98bf1830862736b0a92c9a3202b42e19a57fb098768c1886"
PATCHED_FILES = {
    "crypto/secp256k1/curve.go": "72bdd468bf33e12f4bd18a23b08d4a8f9f06aea68ac6a8448a0eb7476e26b3cc",
    "crypto/secp256k1/ext.h": "af707db9be7d3d5082f98ee28595dbac1d3d3a4f3c492b6598f5a3f8b8706d89",
    "crypto/signature_nocgo.go": "e8f2e2f6c5ba62fd8d6d7dff4cb450a2af1b83bbf33cba44668db4e8cbf47679",
    "go.mod": "eb014cfde48202dcd8df27ba2d7ee81b2dbcf3ece956bf521a0d21ec2d817135",
    "p2p/nat/stun.go": "e4624d06e556ce6393667925e166d6cebefd03a731dd14d3008ef80b2fe158ac"
}


def verify_geth(root):
    root = Path(root).resolve(strict=True)
    manifest = json.loads((root / "UPSTREAM.json").read_bytes())
    canonical = manifest.get("canonicalFiles", {})
    digest = hashlib.sha256(json.dumps(canonical, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    if len(canonical) != 2261 or digest != CANONICAL_FILES_SHA256 or manifest.get("canonicalFilesSha256") != digest:
        raise RuntimeError("Unexpected geth canonical file inventory")
    if manifest.get("modifiedFiles") != PATCHED_FILES or manifest.get("addedFiles") != {}:
        raise RuntimeError("Unexpected geth patch inventory")
    return verify(root, GETH_IDENTITY)


if __name__ == "__main__":
    files, changes = verify_geth(Path(__file__).resolve().parent.parent / "third_party/cosmos-geth")
    print(f"Owned geth source verification PASS: {files} files, {changes} declared changes")
