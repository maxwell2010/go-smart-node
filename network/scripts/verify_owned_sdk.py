#!/usr/bin/env python3
"""Verify the complete declared SDK source and default daemon armor imports."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
from verify_owned_evm import verify
from verify_pion_transport import records

SDK_IDENTITY = {'patchLabel': 'v0.53.8-mintcandy.1', 'schemaVersion': 1, 'upstreamCommit': '102db7cd8c9ade54b770e0f2eea1ca597fa97ed3', 'upstreamGoModSum': 'h1:b/De6uhCfooGEy4kE5G4C0A+MxK9aPr0nZNMyR+PmY0=', 'upstreamModule': 'github.com/cosmos/cosmos-sdk', 'upstreamModuleSum': 'h1:rKQIgqifo3Izv/JQLpPIjReOJ7jK+Qh8Rt8GIttDADw=', 'upstreamVersion': 'v0.53.8', 'upstreamZipSha256': 'b2e19703acfa70db3ae28d7f9a8c13dcb06211b04990ba6cb95abbdb6695e571'}
CANONICAL_FILES_SHA256 = '8e432107ce7936500b5e2098525ccef0daa98e7325d4beeed0d8c253d9a24b85'
PATCHED_FILES = {'go.mod': '6e6d6412e58aa8e85de84f8b6dbacdd31e2fff95eb0f272db576a6f3ae17903b', 'go.sum': 'f07226f63d0f575d713ac9686c369b660fd05f54bef1768f4c765a7305337808', 'crypto/armor.go': '74868016ad52264acf5588d385d9a1fd89f45bd82f99c42141e94ca8987fcb1c'}
ADDED_FILES = {'crypto/internal/armorcompat/armor.go': 'ceb3ac272500328bb16639140fe5d54ac630cba92d3df1320d68a79967c34ca6', 'crypto/internal/armorcompat/armor_test.go': 'c155522721b8345163a361d36dffcae4168b3232c40607dec287687b7b4dd44c', 'crypto/internal/armorcompat/LICENSE': '911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad'}
PROTON_IDENTITY = {"Version": "v1.5.2", "Sum": "h1:cucYnvqcY7UOXVD//mSyjeaPY0SSN3v5cDkYPxumINk=", "GoModSum": "h1:/RaSu30DaKO4RY+XdV/ACcCcZkGr7AhUIduq5sjzzCo="}

def verify_sdk(root):
    root = Path(root).resolve(strict=True)
    manifest = json.loads((root / "UPSTREAM.json").read_bytes())
    canonical = manifest.get("canonicalFiles", {})
    digest = hashlib.sha256(json.dumps(canonical, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    if len(canonical) != 2138 or digest != CANONICAL_FILES_SHA256 or manifest.get("canonicalFilesSha256") != digest:
        raise RuntimeError("Unexpected SDK canonical file inventory")
    if manifest.get("modifiedFiles") != PATCHED_FILES or manifest.get("addedFiles") != ADDED_FILES:
        raise RuntimeError("Unexpected SDK patch inventory")
    return verify(root, SDK_IDENTITY)

def verify_compiled_armor(module):
    module = Path(module).resolve(strict=True)
    inputs = {name: (module / name).read_bytes() for name in ("go.mod", "go.sum")}
    try:
        modules = {entry["Path"]: entry for entry in records(subprocess.check_output(["go", "list", "-mod=readonly", "-m", "-json", "all"], cwd=module, text=True))}
        proton = modules.get("github.com/ProtonMail/go-crypto", {})
        if proton.get("Replace") or any(proton.get(k) != v for k, v in PROTON_IDENTITY.items()):
            raise RuntimeError("Unexpected maintained armor source identity")
        sdk = modules.get("github.com/cosmos/cosmos-sdk", {})
        replacement = sdk.get("Replace", {})
        if sdk.get("Version") != "v0.53.8" or Path(replacement.get("Dir", "")).resolve() != module / "third_party/cosmos-sdk":
            raise RuntimeError("SDK must use the declared owned source")
        integrity = subprocess.check_output(["go", "mod", "verify"], cwd=module, text=True).strip()
        deps = set(subprocess.check_output(["go", "list", "-mod=readonly", "-tags=", "-deps", "./cmd/mcnd"], cwd=module, text=True).splitlines())
        old = sorted(name for name in deps if name.startswith("golang.org/x/crypto/openpgp"))
        if old or "github.com/ProtonMail/go-crypto/openpgp/armor" not in deps or "github.com/cosmos/cosmos-sdk/crypto/internal/armorcompat" not in deps:
            raise RuntimeError("Unexpected default daemon armor imports: " + repr(old))
        return {"maintainedModule": PROTON_IDENTITY, "legacyCompiledImports": old, "moduleIntegrity": integrity, "scannerCoverage": "Local SDK replacement requires explicit canonical SDK advisory review; import verification alone is not an advisory closure or complete audit."}
    finally:
        if any((module / name).read_bytes() != value for name, value in inputs.items()):
            raise RuntimeError("Module files changed during readonly SDK verification")

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compiled", action="store_true")
    args = parser.parse_args()
    module = Path(__file__).resolve().parent.parent
    files, changes = verify_sdk(module / "third_party/cosmos-sdk")
    print(f"Owned SDK integrity PASS: {files} files, {changes} modified canonical files")
    if args.compiled:
        print(json.dumps(verify_compiled_armor(module), sort_keys=True))
