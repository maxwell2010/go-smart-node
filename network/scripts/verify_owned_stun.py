#!/usr/bin/env python3
"""Verify the complete STUN v2 source and its declared upstream guard backport."""
from pathlib import Path
from verify_owned_evm import verify

STUN_IDENTITY = {
    "schemaVersion": 1,
    "upstreamModule": "github.com/pion/stun/v2",
    "upstreamVersion": "v2.0.0",
    "upstreamCommit": "4ff55da8871d9ab252670138c1f670136b958ca4",
    "upstreamModuleSum": "h1:A5+wXKLAypxQri59+tmQKVs7+l6mMM+3d+eER9ifRU0=",
}

if __name__ == "__main__":
    files, changes = verify(Path(__file__).resolve().parent.parent / "third_party/pion-stun-v2", STUN_IDENTITY)
    print(f"Owned STUN source verification PASS: {files} files, {changes} declared changes")
